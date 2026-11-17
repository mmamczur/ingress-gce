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
	"testing"

	"github.com/google/go-cmp/cmp"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"
	"k8s.io/ingress-gce/pkg/l4/annotations"
)

func lbService(namespace, name string, ips ...string) *v1.Service {
	svc := &v1.Service{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name, Annotations: map[string]string{}},
		Spec:       v1.ServiceSpec{Type: v1.ServiceTypeLoadBalancer},
	}
	for _, ip := range ips {
		svc.Status.LoadBalancer.Ingress = append(svc.Status.LoadBalancer.Ingress, v1.LoadBalancerIngress{IP: ip})
	}
	return svc
}

func standalone(svc *v1.Service, externalHC string) *v1.Service {
	lbClass := annotations.StandalonePassthroughNegLoadBalancerClass
	svc.Spec.LoadBalancerClass = &lbClass
	if externalHC != "" {
		svc.Annotations[annotations.ExternalHealthCheckKey] = externalHC
	}
	return svc
}

func withPorts(svc *v1.Service, ports ...v1.ServicePort) *v1.Service {
	svc.Spec.Ports = ports
	return svc
}

func tcp(port int32) v1.ServicePort {
	return v1.ServicePort{Protocol: v1.ProtocolTCP, Port: port}
}

func udp(port int32) v1.ServicePort {
	return v1.ServicePort{Protocol: v1.ProtocolUDP, Port: port}
}

func TestCanonicalIP(t *testing.T) {
	testCases := map[string]string{
		"10.0.0.1":              "10.0.0.1",
		"2600:1900:4000:1::/96": "2600:1900:4000:1::",
		"2600:1900:4000:0001:0000:0000:0000:000A": "2600:1900:4000:1::a",
		"not-an-ip/32": "not-an-ip",
		"":             "",
	}
	for in, want := range testCases {
		if got := CanonicalIP(in); got != want {
			t.Errorf("CanonicalIP(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestServicesByLBIngressIPFunc(t *testing.T) {
	hostnameOnly := lbService("ns", "hostname")
	hostnameOnly.Status.LoadBalancer.Ingress = []v1.LoadBalancerIngress{{Hostname: "lb.example.com"}}
	clusterIP := lbService("ns", "cluster-ip", "10.0.0.1")
	clusterIP.Spec.Type = v1.ServiceTypeClusterIP

	testCases := []struct {
		desc string
		svc  *v1.Service
		want []string
	}{
		{desc: "IPv4", svc: lbService("ns", "a", "10.0.0.1"), want: []string{"10.0.0.1"}},
		{desc: "non canonical IPv6", svc: lbService("ns", "a", "2600:1900:4000:0001:0000:0000:0000:000A"), want: []string{"2600:1900:4000:1::a"}},
		{desc: "multiple and duplicate IPs", svc: lbService("ns", "a", "10.0.0.2", "10.0.0.1", "10.0.0.2"), want: []string{"10.0.0.1", "10.0.0.2"}},
		{desc: "hostname only", svc: hostnameOnly, want: []string{}},
		{desc: "not a LoadBalancer service", svc: clusterIP, want: []string{}},
	}
	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			got, err := ServicesByLBIngressIPFunc(tc.svc)
			if err != nil {
				t.Fatalf("ServicesByLBIngressIPFunc() returned error: %v", err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("ServicesByLBIngressIPFunc() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestServicesHoldingVIP(t *testing.T) {
	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{ServicesByLBIngressIPIndex: ServicesByLBIngressIPFunc})
	for _, svc := range []*v1.Service{
		lbService("ns", "self", "10.0.0.1"),
		lbService("ns", "b", "10.0.0.1", "10.0.0.2"),
		lbService("a-ns", "c", "10.0.0.1"),
		lbService("ns", "other", "10.0.0.3"),
	} {
		if err := indexer.Add(svc); err != nil {
			t.Fatalf("indexer.Add() failed: %v", err)
		}
	}

	svcs, err := ServicesHoldingVIP(indexer, "10.0.0.1", types.NamespacedName{Namespace: "ns", Name: "self"})
	if err != nil {
		t.Fatalf("ServicesHoldingVIP() returned error: %v", err)
	}
	var got []string
	for _, svc := range svcs {
		got = append(got, svc.Namespace+"/"+svc.Name)
	}
	if diff := cmp.Diff([]string{"a-ns/c", "ns/b"}, got); diff != "" {
		t.Errorf("ServicesHoldingVIP() mismatch (-want +got):\n%s", diff)
	}
}

func TestPortsUsedOnVIP(t *testing.T) {
	testCases := []struct {
		desc string
		svc  *v1.Service
		vip  string
		want []PortUse
	}{
		{
			desc: "service ports, protocol defaults to TCP",
			svc:  withPorts(lbService("ns", "a"), tcp(80), udp(53), v1.ServicePort{Port: 8080}),
			vip:  "10.0.0.1",
			want: []PortUse{{Protocol: v1.ProtocolTCP, Port: 80}, {Protocol: v1.ProtocolUDP, Port: 53}, {Protocol: v1.ProtocolTCP, Port: 8080}},
		},
		{
			desc: "health check node port",
			svc: func() *v1.Service {
				svc := withPorts(lbService("ns", "a"), tcp(80))
				svc.Spec.HealthCheckNodePort = 30000
				return svc
			}(),
			vip:  "10.0.0.1",
			want: []PortUse{{Protocol: v1.ProtocolTCP, Port: 80}, {Protocol: v1.ProtocolTCP, Port: 30000, IsHealthCheck: true}},
		},
		{
			desc: "external health check entries of the VIP only",
			svc:  standalone(withPorts(lbService("ns", "a"), tcp(80)), "10.0.0.1:12345, 10.0.0.2:23456,[2001:db8::1]:34567,invalid,10.0.0.1:notaport"),
			vip:  "10.0.0.1",
			want: []PortUse{{Protocol: v1.ProtocolTCP, Port: 80}, {Protocol: v1.ProtocolTCP, Port: 12345, IsHealthCheck: true}},
		},
		{
			desc: "external health check IPv6 entries",
			svc:  standalone(lbService("ns", "a"), "10.0.0.1:12345,[2001:db8::1]:34567"),
			vip:  "2001:0db8::0001",
			want: []PortUse{{Protocol: v1.ProtocolTCP, Port: 34567, IsHealthCheck: true}},
		},
		{
			desc: "external health check annotation ignored on non standalone services",
			svc: func() *v1.Service {
				svc := lbService("ns", "a")
				svc.Annotations[annotations.ExternalHealthCheckKey] = "10.0.0.1:12345"
				return svc
			}(),
			vip:  "10.0.0.1",
			want: nil,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			got := PortsUsedOnVIP(tc.svc, tc.vip)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("PortsUsedOnVIP() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestDetect(t *testing.T) {
	other := types.NamespacedName{Namespace: "ns", Name: "other"}
	another := types.NamespacedName{Namespace: "ns", Name: "another"}
	svcPort := func(protocol v1.Protocol, port int32) PortUse { return PortUse{Protocol: protocol, Port: port} }

	testCases := []struct {
		desc          string
		wantedPorts   []PortUse
		otherServices []ServiceOnVIP
		want          []Collision
		wantStr       []string
	}{
		{
			desc:        "no other services",
			wantedPorts: []PortUse{svcPort(v1.ProtocolTCP, 80)},
		},
		{
			desc:          "disjoint ports",
			wantedPorts:   []PortUse{svcPort(v1.ProtocolTCP, 80)},
			otherServices: []ServiceOnVIP{{Service: other, Ports: []PortUse{svcPort(v1.ProtocolTCP, 443)}}},
		},
		{
			desc:          "same port with different protocols",
			wantedPorts:   []PortUse{svcPort(v1.ProtocolTCP, 53)},
			otherServices: []ServiceOnVIP{{Service: other, Ports: []PortUse{svcPort(v1.ProtocolUDP, 53)}}},
		},
		{
			desc:          "same service port",
			wantedPorts:   []PortUse{svcPort(v1.ProtocolTCP, 80), svcPort(v1.ProtocolUDP, 53)},
			otherServices: []ServiceOnVIP{{Service: other, Ports: []PortUse{svcPort(v1.ProtocolUDP, 53), svcPort(v1.ProtocolTCP, 443)}}},
			want:          []Collision{{OtherService: other, Conflicts: []PortConflict{{Protocol: v1.ProtocolUDP, Port: 53}}}},
			wantStr:       []string{"service ns/other on UDP:53"},
		},
		{
			desc:          "health check port vs service port",
			wantedPorts:   []PortUse{svcPort(v1.ProtocolTCP, 80), HealthCheckPort(12345)},
			otherServices: []ServiceOnVIP{{Service: other, Ports: []PortUse{svcPort(v1.ProtocolTCP, 12345)}}},
			want:          []Collision{{OtherService: other, Conflicts: []PortConflict{{Protocol: v1.ProtocolTCP, Port: 12345, IsHealthCheck: true}}}},
			wantStr:       []string{"service ns/other on TCP:12345 (health check vs service port)"},
		},
		{
			desc:          "service port vs health check port",
			wantedPorts:   []PortUse{svcPort(v1.ProtocolTCP, 30000)},
			otherServices: []ServiceOnVIP{{Service: other, Ports: []PortUse{HealthCheckPort(30000)}}},
			want:          []Collision{{OtherService: other, Conflicts: []PortConflict{{Protocol: v1.ProtocolTCP, Port: 30000, IsOtherServiceHealthCheck: true}}}},
			wantStr:       []string{"service ns/other on TCP:30000 (service port vs health check)"},
		},
		{
			desc:          "health check port vs health check port",
			wantedPorts:   []PortUse{HealthCheckPort(12345)},
			otherServices: []ServiceOnVIP{{Service: other, Ports: []PortUse{HealthCheckPort(12345)}}},
			want:          []Collision{{OtherService: other, Conflicts: []PortConflict{{Protocol: v1.ProtocolTCP, Port: 12345, IsHealthCheck: true, IsOtherServiceHealthCheck: true}}}},
			wantStr:       []string{"service ns/other on TCP:12345 (health check vs health check)"},
		},
		{
			desc:        "multiple other services and ports are sorted",
			wantedPorts: []PortUse{svcPort(v1.ProtocolTCP, 443), svcPort(v1.ProtocolTCP, 80), svcPort(v1.ProtocolUDP, 80)},
			otherServices: []ServiceOnVIP{
				{Service: other, Ports: []PortUse{svcPort(v1.ProtocolUDP, 80), svcPort(v1.ProtocolTCP, 443), svcPort(v1.ProtocolTCP, 80)}},
				{Service: another, Ports: []PortUse{svcPort(v1.ProtocolTCP, 443)}},
			},
			want: []Collision{
				{OtherService: another, Conflicts: []PortConflict{{Protocol: v1.ProtocolTCP, Port: 443}}},
				{OtherService: other, Conflicts: []PortConflict{{Protocol: v1.ProtocolTCP, Port: 80}, {Protocol: v1.ProtocolUDP, Port: 80}, {Protocol: v1.ProtocolTCP, Port: 443}}},
			},
			wantStr: []string{"service ns/another on TCP:443", "service ns/other on TCP:80, UDP:80, TCP:443"},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			got := Detect(tc.wantedPorts, tc.otherServices)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("Detect() mismatch (-want +got):\n%s", diff)
			}
			var gotStr []string
			for _, c := range got {
				gotStr = append(gotStr, c.String())
			}
			if diff := cmp.Diff(tc.wantStr, gotStr); diff != "" {
				t.Errorf("Collision.String() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
