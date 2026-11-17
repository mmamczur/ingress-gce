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

package vipsharing

import (
	"reflect"
	"sort"
	"sync"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"
)

// ReservationResult is the result of the reservation of a VIP.
type ReservationResult struct {
	// Collisions with the other services using the VIP.
	Collisions []Collision
	// Blocked is true if the service can't use the VIP. VIPs already held or
	// reserved by the service are never blocked, even when they collide.
	Blocked bool
	// Err is set if the services holding the VIP could not be listed. The VIP
	// is blocked unless it is already held or reserved by the service.
	Err error
}

// VIPReservations checks VIP collisions and reserves VIPs for services, see the
// package documentation for the terminology and the flow.
//
// It is safe for concurrent use: checking the collisions and reserving the VIPs
// is atomic, so services synced concurrently can't get colliding VIPs. A given
// service must not be reserved or committed concurrently, which work queues
// guarantee.
type VIPReservations struct {
	// serviceIndexer is the Service informer indexer, with the
	// ServicesByLBIngressIPIndex index.
	serviceIndexer cache.Indexer

	mu sync.Mutex
	// reservedPorts maps VIPs to the services reserving them, and the ports the
	// services reserved on them.
	reservedPorts map[string]map[types.NamespacedName][]PortUse
	// reservedVIPs maps services to the VIPs they reserved.
	reservedVIPs map[types.NamespacedName]sets.Set[string]
	// blockedServices maps VIPs to the services blocked on them.
	blockedServices map[string]sets.Set[types.NamespacedName]
	// blockedVIPs maps services to the VIPs they are blocked on.
	blockedVIPs map[types.NamespacedName]sets.Set[string]
}

// NewVIPReservations creates a VIPReservations finding the services holding
// VIPs with the serviceIndexer, which must have the ServicesByLBIngressIPIndex.
func NewVIPReservations(serviceIndexer cache.Indexer) *VIPReservations {
	return &VIPReservations{
		serviceIndexer:  serviceIndexer,
		reservedPorts:   make(map[string]map[types.NamespacedName][]PortUse),
		reservedVIPs:    make(map[types.NamespacedName]sets.Set[string]),
		blockedServices: make(map[string]sets.Set[types.NamespacedName]),
		blockedVIPs:     make(map[types.NamespacedName]sets.Set[string]),
	}
}

// Reserve checks if the ports the service wants to use on each VIP collide with
// the ports of the other services using the VIP, and reserves the VIPs that are
// not blocked. Checking and reserving is atomic.
//
// wantedPortsByVIP maps the VIPs the service wants to use to the ports it wants
// to use on them. The result is keyed by the canonical form of the VIPs.
//
// The previous reservations of the service are kept until Commit, as the
// service status may still use them. The VIPs the service is blocked on replace
// the previous ones.
func (r *VIPReservations) Reserve(svc *v1.Service, wantedPortsByVIP map[string][]PortUse) map[string]ReservationResult {
	r.mu.Lock()
	defer r.mu.Unlock()

	svcName := types.NamespacedName{Namespace: svc.Namespace, Name: svc.Name}
	heldVIPs := StatusVIPs(svc)
	results := make(map[string]ReservationResult, len(wantedPortsByVIP))
	blockedVIPs := sets.New[string]()
	for vip, wantedPorts := range wantedPortsByVIP {
		vip = CanonicalIP(vip)
		alreadyUsedByService := heldVIPs.Has(vip) || r.reservedVIPs[svcName].Has(vip)

		var result ReservationResult
		otherServices, err := r.otherServicesOnVIPLocked(vip, svcName)
		if err != nil {
			result.Err = err
		} else {
			result.Collisions = Detect(wantedPorts, otherServices)
		}
		result.Blocked = !alreadyUsedByService && (result.Err != nil || len(result.Collisions) > 0)
		results[vip] = result

		if result.Blocked {
			blockedVIPs.Insert(vip)
			continue
		}
		previouslyReservedPorts := r.reservedPorts[vip][svcName]
		r.reserveLocked(svcName, vip, append(previouslyReservedPorts, wantedPorts...))
	}
	r.setBlockedVIPsLocked(svcName, blockedVIPs)
	return results
}

// Commit replaces the reservations of the service with the VIPs written to its
// status. usedPortsByVIP maps the VIPs written to the status to the ports the
// service uses on them. It returns the services blocked on the VIPs which
// reservation was released or changed, they should be synced again.
func (r *VIPReservations) Commit(svcName types.NamespacedName, usedPortsByVIP map[string][]PortUse) []types.NamespacedName {
	r.mu.Lock()
	defer r.mu.Unlock()

	usedPortsByCanonicalVIP := make(map[string][]PortUse, len(usedPortsByVIP))
	for vip, ports := range usedPortsByVIP {
		usedPortsByCanonicalVIP[CanonicalIP(vip)] = dedupPorts(ports)
	}
	changedVIPs := sets.New[string]()
	for vip := range r.reservedVIPs[svcName] {
		usedPorts, stillUsed := usedPortsByCanonicalVIP[vip]
		if !stillUsed || !reflect.DeepEqual(usedPorts, r.reservedPorts[vip][svcName]) {
			changedVIPs.Insert(vip)
		}
	}
	r.releaseAllLocked(svcName)
	for vip, ports := range usedPortsByCanonicalVIP {
		r.reserveLocked(svcName, vip, ports)
	}
	return r.servicesBlockedOnLocked(changedVIPs, svcName)
}

// Release releases all the reservations of the service, and forgets the VIPs it
// is blocked on. It returns the services blocked on the released VIPs, they
// should be synced again.
func (r *VIPReservations) Release(svcName types.NamespacedName) []types.NamespacedName {
	r.mu.Lock()
	defer r.mu.Unlock()
	releasedVIPs := r.reservedVIPs[svcName].Clone()
	r.releaseAllLocked(svcName)
	r.setBlockedVIPsLocked(svcName, nil)
	return r.servicesBlockedOnLocked(releasedVIPs, svcName)
}

// BlockedServices returns the services blocked on the vip, sorted by name.
func (r *VIPReservations) BlockedServices(vip string) []types.NamespacedName {
	r.mu.Lock()
	defer r.mu.Unlock()
	return sortedNames(r.blockedServices[CanonicalIP(vip)])
}

// otherServicesOnVIPLocked returns the services using the vip, except the
// excluded one: the services holding the vip in the informer cache, and the
// services reserving it. For a service both holding and reserving the vip, the
// ports are combined, as the reservation may contain ports which are not in the
// informer cache yet.
func (r *VIPReservations) otherServicesOnVIPLocked(vip string, excluded types.NamespacedName) ([]ServiceOnVIP, error) {
	holdingServices, err := ServicesHoldingVIP(r.serviceIndexer, vip, excluded)
	if err != nil {
		return nil, err
	}
	var result []ServiceOnVIP
	for _, svc := range holdingServices {
		svcName := types.NamespacedName{Namespace: svc.Namespace, Name: svc.Name}
		ports := append(PortsUsedOnVIP(svc, vip), r.reservedPorts[vip][svcName]...)
		result = append(result, ServiceOnVIP{Service: svcName, Ports: ports})
	}
	for svcName, ports := range r.reservedPorts[vip] {
		if svcName == excluded || containsService(result, svcName) {
			continue
		}
		result = append(result, ServiceOnVIP{Service: svcName, Ports: append([]PortUse(nil), ports...)})
	}
	return result, nil
}

func (r *VIPReservations) reserveLocked(svcName types.NamespacedName, vip string, ports []PortUse) {
	if r.reservedPorts[vip] == nil {
		r.reservedPorts[vip] = make(map[types.NamespacedName][]PortUse)
	}
	r.reservedPorts[vip][svcName] = dedupPorts(ports)
	if r.reservedVIPs[svcName] == nil {
		r.reservedVIPs[svcName] = sets.New[string]()
	}
	r.reservedVIPs[svcName].Insert(vip)
}

func (r *VIPReservations) releaseAllLocked(svcName types.NamespacedName) {
	for vip := range r.reservedVIPs[svcName] {
		delete(r.reservedPorts[vip], svcName)
		if len(r.reservedPorts[vip]) == 0 {
			delete(r.reservedPorts, vip)
		}
	}
	delete(r.reservedVIPs, svcName)
}

func (r *VIPReservations) setBlockedVIPsLocked(svcName types.NamespacedName, vips sets.Set[string]) {
	for vip := range r.blockedVIPs[svcName] {
		r.blockedServices[vip].Delete(svcName)
		if r.blockedServices[vip].Len() == 0 {
			delete(r.blockedServices, vip)
		}
	}
	delete(r.blockedVIPs, svcName)
	if vips.Len() == 0 {
		return
	}
	for vip := range vips {
		if r.blockedServices[vip] == nil {
			r.blockedServices[vip] = sets.New[types.NamespacedName]()
		}
		r.blockedServices[vip].Insert(svcName)
	}
	r.blockedVIPs[svcName] = vips.Clone()
}

// servicesBlockedOnLocked returns the services blocked on any of the vips,
// except the excluded one.
func (r *VIPReservations) servicesBlockedOnLocked(vips sets.Set[string], excluded types.NamespacedName) []types.NamespacedName {
	blocked := sets.New[types.NamespacedName]()
	for vip := range vips {
		blocked = blocked.Union(r.blockedServices[vip])
	}
	blocked.Delete(excluded)
	return sortedNames(blocked)
}

func containsService(servicesOnVIP []ServiceOnVIP, svcName types.NamespacedName) bool {
	for _, serviceOnVIP := range servicesOnVIP {
		if serviceOnVIP.Service == svcName {
			return true
		}
	}
	return false
}

// dedupPorts returns the ports without duplicates, keeping the first occurrence.
func dedupPorts(ports []PortUse) []PortUse {
	seen := make(map[PortUse]bool, len(ports))
	var result []PortUse
	for _, port := range ports {
		if seen[port] {
			continue
		}
		seen[port] = true
		result = append(result, port)
	}
	return result
}

func sortedNames(names sets.Set[types.NamespacedName]) []types.NamespacedName {
	list := names.UnsortedList()
	sort.Slice(list, func(i, j int) bool {
		return list[i].String() < list[j].String()
	})
	return list
}
