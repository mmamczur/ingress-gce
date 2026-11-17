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
	"net"
	"strconv"
	"strings"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/ingress-gce/pkg/l4/annotations"
)

// PortUse is a (protocol, port) pair used by a service on a VIP.
type PortUse struct {
	Protocol v1.Protocol
	Port     int32
	// IsHealthCheck is true if the port receives the load balancer health check
	// traffic. It does not change how collisions are detected.
	IsHealthCheck bool
}

// protocolPort identifies a port regardless of its use.
type protocolPort struct {
	protocol v1.Protocol
	port     int32
}

func (p PortUse) protocolPort() protocolPort {
	return protocolPort{protocol: p.Protocol, port: p.Port}
}

func (p PortUse) String() string {
	return fmt.Sprintf("%s:%d", p.Protocol, p.Port)
}

// HealthCheckPort returns the PortUse of a health check port. The tracked
// health checks are HTTP, so TCP based.
func HealthCheckPort(port int32) PortUse {
	return PortUse{Protocol: v1.ProtocolTCP, Port: port, IsHealthCheck: true}
}

// PortsUsedOnAllVIPs returns the ports the service uses on all of its VIPs: its
// spec.ports and its health check node port. kube-proxy serves the health check
// node port on all the node addresses, including the VIPs.
func PortsUsedOnAllVIPs(svc *v1.Service) []PortUse {
	var ports []PortUse
	for _, servicePort := range svc.Spec.Ports {
		protocol := servicePort.Protocol
		if protocol == "" {
			protocol = v1.ProtocolTCP
		}
		ports = append(ports, PortUse{Protocol: protocol, Port: servicePort.Port})
	}
	if svc.Spec.HealthCheckNodePort != 0 {
		ports = append(ports, HealthCheckPort(svc.Spec.HealthCheckNodePort))
	}
	return ports
}

// RedirectedHealthCheckPorts returns the health check ports redirected on the
// vip, as listed in the external health check annotation of Standalone NEG
// services. It returns nothing for other services.
func RedirectedHealthCheckPorts(svc *v1.Service, vip string) []PortUse {
	if svc.Spec.LoadBalancerClass == nil || *svc.Spec.LoadBalancerClass != annotations.StandalonePassthroughNegLoadBalancerClass {
		return nil
	}
	annotationValue := svc.Annotations[annotations.ExternalHealthCheckKey]
	if annotationValue == "" {
		return nil
	}
	vip = CanonicalIP(vip)
	var ports []PortUse
	for _, entry := range strings.Split(annotationValue, ",") {
		entryIP, entryPort, err := net.SplitHostPort(strings.TrimSpace(entry))
		if err != nil || CanonicalIP(entryIP) != vip {
			continue
		}
		port, err := strconv.ParseInt(entryPort, 10, 32)
		if err != nil || port <= 0 {
			continue
		}
		ports = append(ports, HealthCheckPort(int32(port)))
	}
	return ports
}

// PortsUsedOnVIP returns all the ports the service uses on the vip.
func PortsUsedOnVIP(svc *v1.Service, vip string) []PortUse {
	return append(PortsUsedOnAllVIPs(svc), RedirectedHealthCheckPorts(svc, vip)...)
}

// ServiceOnVIP is a service using a VIP, with the ports it uses on it.
type ServiceOnVIP struct {
	Service types.NamespacedName
	Ports   []PortUse
}
