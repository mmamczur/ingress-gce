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
	"fmt"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"
)

var (
	tcp80  = PortUse{Protocol: v1.ProtocolTCP, Port: 80}
	tcp81  = PortUse{Protocol: v1.ProtocolTCP, Port: 81}
	tcp443 = PortUse{Protocol: v1.ProtocolTCP, Port: 443}
)

// newServiceIndexer returns a Service indexer with the ServicesByLBIngressIPIndex
// index, containing the services.
func newServiceIndexer(t *testing.T, svcs ...*v1.Service) cache.Indexer {
	t.Helper()
	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{ServicesByLBIngressIPIndex: ServicesByLBIngressIPFunc})
	for _, svc := range svcs {
		if err := indexer.Add(svc); err != nil {
			t.Fatalf("indexer.Add() failed: %v", err)
		}
	}
	return indexer
}

func nameOf(svc *v1.Service) types.NamespacedName {
	return types.NamespacedName{Namespace: svc.Namespace, Name: svc.Name}
}

func TestReserve(t *testing.T) {
	svcA := lbService("ns", "a")
	svcB := lbService("ns", "b")
	holdingSvc := withPorts(lbService("ns", "holding", "10.0.0.1"), tcp(443))
	reservations := NewVIPReservations(newServiceIndexer(t, holdingSvc))

	// a reserves the VIP.
	result := reservations.Reserve(svcA, map[string][]PortUse{"10.0.0.1": {tcp80}})["10.0.0.1"]
	if result.Blocked || len(result.Collisions) != 0 {
		t.Fatalf("Reserve(a) = %+v, want not blocked", result)
	}

	// b collides with the reservation of a, which is not in the informer cache.
	result = reservations.Reserve(svcB, map[string][]PortUse{"10.0.0.1": {tcp80}})["10.0.0.1"]
	want := ReservationResult{Blocked: true, Collisions: []Collision{{OtherService: nameOf(svcA), Conflicts: []PortConflict{{Protocol: v1.ProtocolTCP, Port: 80}}}}}
	if diff := cmp.Diff(want, result); diff != "" {
		t.Errorf("Reserve(b) mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]types.NamespacedName{nameOf(svcB)}, reservations.BlockedServices("10.0.0.1")); diff != "" {
		t.Errorf("BlockedServices() mismatch (-want +got):\n%s", diff)
	}

	// b collides with the service holding the VIP in the informer cache.
	result = reservations.Reserve(svcB, map[string][]PortUse{"10.0.0.1": {tcp443}})["10.0.0.1"]
	if !result.Blocked || len(result.Collisions) != 1 || result.Collisions[0].OtherService != nameOf(holdingSvc) {
		t.Errorf("Reserve(b) = %+v, want blocked by %s", result, nameOf(holdingSvc))
	}

	// A VIP held by the service is not blocked, but its collisions are reported.
	svcBHoldingVIP := withPorts(lbService("ns", "b", "10.0.0.1"), tcp(443))
	result = reservations.Reserve(svcBHoldingVIP, map[string][]PortUse{"10.0.0.1": {tcp443}})["10.0.0.1"]
	if result.Blocked || len(result.Collisions) != 1 {
		t.Errorf("Reserve(b) of a held VIP = %+v, want not blocked with 1 collision", result)
	}
	if got := reservations.BlockedServices("10.0.0.1"); len(got) != 0 {
		t.Errorf("BlockedServices() = %v, want none", got)
	}
}

func TestReserveListingError(t *testing.T) {
	// The indexer has no ServicesByLBIngressIPIndex index, listing fails.
	reservations := NewVIPReservations(cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{}))
	results := reservations.Reserve(lbService("ns", "a", "10.0.0.1"), map[string][]PortUse{"10.0.0.1": {tcp80}, "10.0.0.2": {tcp80}})
	if result := results["10.0.0.1"]; result.Blocked || result.Err == nil {
		t.Errorf("Reserve() of a held VIP with a listing error = %+v, want not blocked with an error", result)
	}
	if result := results["10.0.0.2"]; !result.Blocked || result.Err == nil {
		t.Errorf("Reserve() of a new VIP with a listing error = %+v, want blocked with an error", result)
	}
}

func TestCommitAndRelease(t *testing.T) {
	svcA := lbService("ns", "a")
	svcB := lbService("ns", "b")
	reservations := NewVIPReservations(newServiceIndexer(t))

	reservations.Reserve(svcA, map[string][]PortUse{"10.0.0.1": {tcp80}, "2001:0db8::1": {tcp80}})
	if requeue := reservations.Commit(nameOf(svcA), map[string][]PortUse{"10.0.0.1": {tcp80}, "2001:db8::1": {tcp80}}); len(requeue) != 0 {
		t.Errorf("Commit() of unchanged reservations returned %v, want none", requeue)
	}

	// The previously reserved ports are kept until the next Commit.
	reservations.Reserve(svcA, map[string][]PortUse{"10.0.0.1": {tcp81}})
	results := reservations.Reserve(svcB, map[string][]PortUse{"10.0.0.1": {tcp80}, "2001:db8::1": {tcp81}})
	if !results["10.0.0.1"].Blocked {
		t.Errorf("Reserve(b) of a port previously reserved by a = %+v, want blocked", results["10.0.0.1"])
	}
	if results["2001:db8::1"].Blocked {
		t.Errorf("Reserve(b) of a free port = %+v, want not blocked", results["2001:db8::1"])
	}
	reservations.Commit(nameOf(svcB), map[string][]PortUse{"2001:db8::1": {tcp81}})

	// Committing the new ports returns the services blocked on the changed VIP.
	if diff := cmp.Diff([]types.NamespacedName{nameOf(svcB)}, reservations.Commit(nameOf(svcA), map[string][]PortUse{"10.0.0.1": {tcp81}})); diff != "" {
		t.Errorf("Commit() services to requeue mismatch (-want +got):\n%s", diff)
	}
	if result := reservations.Reserve(svcB, map[string][]PortUse{"10.0.0.1": {tcp80}})["10.0.0.1"]; result.Blocked {
		t.Errorf("Reserve(b) after the port was released = %+v, want not blocked", result)
	}

	// Release releases everything.
	reservations.Reserve(svcA, map[string][]PortUse{"10.0.0.2": {tcp80}})
	reservations.Reserve(svcB, map[string][]PortUse{"10.0.0.2": {tcp80}})
	if diff := cmp.Diff([]types.NamespacedName{nameOf(svcB)}, reservations.Release(nameOf(svcA))); diff != "" {
		t.Errorf("Release() services to requeue mismatch (-want +got):\n%s", diff)
	}
	reservations.Release(nameOf(svcB))
	if len(reservations.reservedPorts) != 0 || len(reservations.reservedVIPs) != 0 || len(reservations.blockedServices) != 0 || len(reservations.blockedVIPs) != 0 {
		t.Errorf("Release() left entries behind: reservedPorts=%v blockedServices=%v", reservations.reservedPorts, reservations.blockedServices)
	}
}

// TestConcurrentReserve verifies that only one of the services reserving the
// same VIP and port concurrently gets it.
func TestConcurrentReserve(t *testing.T) {
	const numServices = 50
	reservations := NewVIPReservations(newServiceIndexer(t))
	var wg sync.WaitGroup
	var mu sync.Mutex
	var notBlocked []string
	for i := 0; i < numServices; i++ {
		svc := lbService("ns", fmt.Sprintf("svc-%d", i))
		wg.Add(1)
		go func() {
			defer wg.Done()
			result := reservations.Reserve(svc, map[string][]PortUse{"10.0.0.1": {tcp80}})["10.0.0.1"]
			if !result.Blocked {
				mu.Lock()
				notBlocked = append(notBlocked, svc.Name)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(notBlocked) != 1 {
		t.Fatalf("Got %d services reserving the VIP, want 1: %v", len(notBlocked), notBlocked)
	}
	if got := len(reservations.BlockedServices("10.0.0.1")); got != numServices-1 {
		t.Errorf("Got %d blocked services, want %d", got, numServices-1)
	}
}
