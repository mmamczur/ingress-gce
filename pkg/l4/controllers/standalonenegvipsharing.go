/*
Copyright 2026 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

// This file contains the handling of VIPs shared by multiple services in the
// Standalone NEG LB controller. See the vipsharing package for the terminology.

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"
	"k8s.io/ingress-gce/pkg/l4/annotations"
	l4utils "k8s.io/ingress-gce/pkg/l4/utils"
	"k8s.io/ingress-gce/pkg/l4/vipsharing"
	"k8s.io/klog/v2"
)

// vipUsage describes how a service uses one of the VIPs of its forwarding rules.
type vipUsage struct {
	vip string
	// forwardingRules are the names of the forwarding rules of the service with the VIP.
	forwardingRules []string
	// redirectedHealthCheckPorts are the health check ports redirected on the VIP.
	redirectedHealthCheckPorts sets.Set[int32]
	// blocked is true if the VIP collides with other services, and is not
	// written to the service status.
	blocked bool
}

// portsUsed returns the ports the service uses on the VIP.
func (u *vipUsage) portsUsed(svc *v1.Service) []vipsharing.PortUse {
	ports := vipsharing.PortsUsedOnAllVIPs(svc)
	for _, port := range sets.List(u.redirectedHealthCheckPorts) {
		ports = append(ports, vipsharing.HealthCheckPort(port))
	}
	return ports
}

// serviceVIPs are the VIPs of the forwarding rules of a service, in the order
// of the forwarding rules.
type serviceVIPs struct {
	ordered []*vipUsage
	byVIP   map[string]*vipUsage
}

func newServiceVIPs() *serviceVIPs {
	return &serviceVIPs{byVIP: make(map[string]*vipUsage)}
}

// add records that the forwarding rule uses the vip, and returns the usage of the vip.
func (s *serviceVIPs) add(vip, forwardingRule string) *vipUsage {
	usage, found := s.byVIP[vip]
	if !found {
		usage = &vipUsage{vip: vip, redirectedHealthCheckPorts: sets.New[int32]()}
		s.byVIP[vip] = usage
		s.ordered = append(s.ordered, usage)
	}
	usage.forwardingRules = append(usage.forwardingRules, forwardingRule)
	return usage
}

func (s *serviceVIPs) isBlocked(vip string) bool {
	usage, found := s.byVIP[vip]
	return found && usage.blocked
}

// usedPortsByVIP maps the VIPs which are not blocked to the ports the service uses on them.
func (s *serviceVIPs) usedPortsByVIP(svc *v1.Service) map[string][]vipsharing.PortUse {
	result := make(map[string][]vipsharing.PortUse)
	for _, usage := range s.ordered {
		if !usage.blocked {
			result[usage.vip] = usage.portsUsed(svc)
		}
	}
	return result
}

// reserveVIPs reserves the VIPs which don't collide with other services, and
// marks the colliding ones as blocked. VIPs already held by the service are
// kept even when they collide, the collisions are only reported with an event.
// It returns the errors of the blocked VIPs.
func (lc *StandaloneNEGLBController) reserveVIPs(svc *v1.Service, vips *serviceVIPs, svcLogger klog.Logger) []error {
	wantedPortsByVIP := make(map[string][]vipsharing.PortUse, len(vips.ordered))
	for _, usage := range vips.ordered {
		wantedPortsByVIP[usage.vip] = usage.portsUsed(svc)
	}
	results := lc.vipReservations.Reserve(svc, wantedPortsByVIP)

	var errs []error
	for _, usage := range vips.ordered {
		result := results[usage.vip]
		usage.blocked = result.Blocked
		forwardingRules := strings.Join(usage.forwardingRules, ", ")
		collisions := vipsharing.DescribeCollisions(result.Collisions)

		switch {
		case result.Err != nil:
			svcLogger.Error(result.Err, "Failed to get the services using the VIP", "vip", usage.vip)
			errs = append(errs, fmt.Errorf("failed to check the collisions of VIP %s: %w", usage.vip, result.Err))
		case len(result.Collisions) == 0:
			// The VIP is reserved.
		case result.Blocked:
			svcLogger.Info("VIP collides with other services, not adding it to the service status", "vip", usage.vip, "collisions", collisions)
			lc.ctx.Recorder(svc.Namespace).Eventf(svc, v1.EventTypeWarning, "VIPCollision",
				"VIP %s of forwarding rule(s) %s collides with %s. The VIP is not added to the service status",
				usage.vip, forwardingRules, collisions)
			errs = append(errs, l4utils.NewUserError(l4utils.NewVIPCollisionError(usage.vip, collisions)))
		default:
			svcLogger.Info("VIP already programmed for the service collides with other services, keeping it", "vip", usage.vip, "collisions", collisions)
			lc.ctx.Recorder(svc.Namespace).Eventf(svc, v1.EventTypeWarning, "VIPCollision",
				"VIP %s of forwarding rule(s) %s collides with %s. The VIP was already programmed for the service, it is kept in the service status",
				usage.vip, forwardingRules, collisions)
		}
	}
	return errs
}

// commitVIPs replaces the VIP reservations of the service with its VIPs which
// are not blocked, once they were written to its status. It enqueues the
// services blocked on the released VIPs.
func (lc *StandaloneNEGLBController) commitVIPs(svc *v1.Service, vips *serviceVIPs) {
	svcName := types.NamespacedName{Namespace: svc.Namespace, Name: svc.Name}
	lc.enqueueServices(lc.vipReservations.Commit(svcName, vips.usedPortsByVIP(svc)), "VIP released by another service")
}

// releaseVIPs releases all the VIP reservations of the service, and enqueues
// the services blocked on the released VIPs.
func (lc *StandaloneNEGLBController) releaseVIPs(svcName types.NamespacedName) {
	lc.enqueueServices(lc.vipReservations.Release(svcName), "VIP released by another service")
}

func (lc *StandaloneNEGLBController) enqueueServices(svcNames []types.NamespacedName, reason string) {
	for _, svcName := range svcNames {
		lc.logger.V(3).Info("Enqueuing service", "service", svcName, "reason", reason)
		lc.svcQueue.Enqueue(cache.ExplicitKey(svcName.String()))
	}
}

// affectsVIPSharing checks if the change of the service may affect the VIP
// collisions of the other services using its VIPs. oldSvc is nil for added
// services, newSvc is nil for deleted services.
func affectsVIPSharing(oldSvc, newSvc *v1.Service) bool {
	if oldSvc == nil || newSvc == nil {
		return true
	}
	return oldSvc.Spec.Type != newSvc.Spec.Type ||
		!reflect.DeepEqual(oldSvc.Spec.LoadBalancerClass, newSvc.Spec.LoadBalancerClass) ||
		oldSvc.Annotations[annotations.ExternalHealthCheckKey] != newSvc.Annotations[annotations.ExternalHealthCheckKey] ||
		!vipsharing.StatusVIPs(oldSvc).Equal(vipsharing.StatusVIPs(newSvc)) ||
		!reflect.DeepEqual(vipsharing.PortsUsedOnAllVIPs(oldSvc), vipsharing.PortsUsedOnAllVIPs(newSvc))
}

// enqueueServicesAffectedByVIPChange enqueues the Standalone NEG services which
// VIP collisions may be affected by the change of a service: the services
// holding, or blocked on, the old or new VIPs of the changed service.
// oldSvc is nil for added services, newSvc is nil for deleted services.
func (lc *StandaloneNEGLBController) enqueueServicesAffectedByVIPChange(oldSvc, newSvc *v1.Service) {
	if !affectsVIPSharing(oldSvc, newSvc) {
		return
	}
	changedSvc := newSvc
	if changedSvc == nil {
		changedSvc = oldSvc
	}
	changedSvcName := types.NamespacedName{Namespace: changedSvc.Namespace, Name: changedSvc.Name}
	affectedVIPs := vipsharing.StatusVIPs(oldSvc).Union(vipsharing.StatusVIPs(newSvc))

	affectedSvcNames := sets.New[types.NamespacedName]()
	for _, vip := range sets.List(affectedVIPs) {
		holdingServices, err := vipsharing.ServicesHoldingVIP(lc.ctx.ServiceInformer.GetIndexer(), vip, changedSvcName)
		if err != nil {
			lc.logger.Error(err, "Failed to list the services holding a VIP", "vip", vip)
		}
		for _, holdingSvc := range holdingServices {
			if lc.shouldProcess(holdingSvc) {
				affectedSvcNames.Insert(types.NamespacedName{Namespace: holdingSvc.Namespace, Name: holdingSvc.Name})
			}
		}
		affectedSvcNames.Insert(lc.vipReservations.BlockedServices(vip)...)
	}
	affectedSvcNames.Delete(changedSvcName)
	lc.enqueueServices(sortedServiceNames(affectedSvcNames), fmt.Sprintf("VIP used by changed service %s", changedSvcName))
}

func sortedServiceNames(svcNames sets.Set[types.NamespacedName]) []types.NamespacedName {
	names := svcNames.UnsortedList()
	sort.Slice(names, func(i, j int) bool {
		return names[i].String() < names[j].String()
	})
	return names
}
