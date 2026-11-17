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
	"net/netip"
	"sort"
	"strings"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"
)

// ServicesByLBIngressIPIndex is the name of the Service informer index
// mapping canonical LoadBalancer ingress IPs to the services holding them.
const ServicesByLBIngressIPIndex = "l4ServicesByLBIngressIP"

// CanonicalIP strips an optional /prefix (GCE IPv6 forwarding rules provide
// the IP in CIDR form) and returns the canonical form of the IP address.
// Strings that are not valid IP addresses are returned with the prefix stripped.
func CanonicalIP(ip string) string {
	ipWithoutPrefix, _, _ := strings.Cut(ip, "/")
	if addr, err := netip.ParseAddr(ipWithoutPrefix); err == nil {
		return addr.String()
	}
	return ipWithoutPrefix
}

// StatusVIPs returns the canonical LoadBalancer ingress IPs of the service.
// Only services of type LoadBalancer are considered.
func StatusVIPs(svc *v1.Service) sets.Set[string] {
	vips := sets.New[string]()
	if svc == nil || svc.Spec.Type != v1.ServiceTypeLoadBalancer {
		return vips
	}
	for _, ingress := range svc.Status.LoadBalancer.Ingress {
		if ingress.IP != "" {
			vips.Insert(CanonicalIP(ingress.IP))
		}
	}
	return vips
}

// ServicesByLBIngressIPFunc is a cache.IndexFunc indexing LoadBalancer services
// by the canonical form of each of their status.loadBalancer.ingress[].ip.
// Objects other than services are not indexed.
func ServicesByLBIngressIPFunc(obj interface{}) ([]string, error) {
	svc, ok := obj.(*v1.Service)
	if !ok {
		return nil, nil
	}
	return sets.List(StatusVIPs(svc)), nil
}

// ServicesHoldingVIP returns the services holding the vip in the informer
// cache, except the excluded service. Services are sorted by namespace/name.
// serviceIndexer must have the ServicesByLBIngressIPIndex index.
func ServicesHoldingVIP(serviceIndexer cache.Indexer, vip string, excluded types.NamespacedName) ([]*v1.Service, error) {
	objs, err := serviceIndexer.ByIndex(ServicesByLBIngressIPIndex, CanonicalIP(vip))
	if err != nil {
		return nil, err
	}
	var svcs []*v1.Service
	for _, obj := range objs {
		svc, ok := obj.(*v1.Service)
		if !ok || (svc.Namespace == excluded.Namespace && svc.Name == excluded.Name) {
			continue
		}
		svcs = append(svcs, svc)
	}
	sort.Slice(svcs, func(i, j int) bool {
		if svcs[i].Namespace != svcs[j].Namespace {
			return svcs[i].Namespace < svcs[j].Namespace
		}
		return svcs[i].Name < svcs[j].Name
	})
	return svcs, nil
}
