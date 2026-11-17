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
	"sort"
	"strings"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

// PortConflict is a (protocol, port) pair used on the same VIP by the checked
// service and another service.
type PortConflict struct {
	Protocol v1.Protocol
	Port     int32
	// IsHealthCheck is true if the port is a health check port of the checked service.
	IsHealthCheck bool
	// IsOtherServiceHealthCheck is true if the port is a health check port of the other service.
	IsOtherServiceHealthCheck bool
}

func portRole(isHealthCheck bool) string {
	if isHealthCheck {
		return "health check"
	}
	return "service port"
}

// String returns e.g. "TCP:80" for two service ports, or
// "TCP:12345 (health check vs service port)" if a health check port is involved.
func (c PortConflict) String() string {
	if !c.IsHealthCheck && !c.IsOtherServiceHealthCheck {
		return fmt.Sprintf("%s:%d", c.Protocol, c.Port)
	}
	return fmt.Sprintf("%s:%d (%s vs %s)", c.Protocol, c.Port, portRole(c.IsHealthCheck), portRole(c.IsOtherServiceHealthCheck))
}

// Collision describes the ports colliding between the checked service and
// another service using the same VIP.
type Collision struct {
	OtherService types.NamespacedName
	Conflicts    []PortConflict
}

// String returns e.g. "service ns/name on TCP:80, UDP:53".
func (c Collision) String() string {
	conflicts := make([]string, len(c.Conflicts))
	for i, conflict := range c.Conflicts {
		conflicts[i] = conflict.String()
	}
	return fmt.Sprintf("service %s on %s", c.OtherService.String(), strings.Join(conflicts, ", "))
}

// DescribeCollisions returns the descriptions of the collisions, separated with
// semicolons.
func DescribeCollisions(collisions []Collision) string {
	descriptions := make([]string, len(collisions))
	for i, collision := range collisions {
		descriptions[i] = collision.String()
	}
	return strings.Join(descriptions, "; ")
}

// portsByProtocolPort maps the ports by (protocol, port). A port used several
// times is marked as a health check port if any of its uses is a health check.
func portsByProtocolPort(ports []PortUse) map[protocolPort]PortUse {
	result := make(map[protocolPort]PortUse, len(ports))
	for _, port := range ports {
		existing, found := result[port.protocolPort()]
		if found {
			existing.IsHealthCheck = existing.IsHealthCheck || port.IsHealthCheck
			result[port.protocolPort()] = existing
			continue
		}
		result[port.protocolPort()] = port
	}
	return result
}

// Detect returns the collisions between the ports the checked service wants to
// use on a VIP and the ports the other services use on it.
// Ports collide when they have the same protocol and number, whether they are
// service ports or health check ports: traffic to such ports can't be
// distinguished, and health checks would probe the wrong backends.
// The result is sorted by the other service name.
func Detect(wantedPorts []PortUse, otherServices []ServiceOnVIP) []Collision {
	wantedByProtocolPort := portsByProtocolPort(wantedPorts)
	var collisions []Collision
	for _, other := range otherServices {
		var conflicts []PortConflict
		for protocolPort, otherPort := range portsByProtocolPort(other.Ports) {
			wantedPort, found := wantedByProtocolPort[protocolPort]
			if !found {
				continue
			}
			conflicts = append(conflicts, PortConflict{
				Protocol:                  protocolPort.protocol,
				Port:                      protocolPort.port,
				IsHealthCheck:             wantedPort.IsHealthCheck,
				IsOtherServiceHealthCheck: otherPort.IsHealthCheck,
			})
		}
		if len(conflicts) == 0 {
			continue
		}
		sort.Slice(conflicts, func(i, j int) bool {
			if conflicts[i].Port != conflicts[j].Port {
				return conflicts[i].Port < conflicts[j].Port
			}
			return conflicts[i].Protocol < conflicts[j].Protocol
		})
		collisions = append(collisions, Collision{OtherService: other.Service, Conflicts: conflicts})
	}
	sort.Slice(collisions, func(i, j int) bool {
		return collisions[i].OtherService.String() < collisions[j].OtherService.String()
	})
	return collisions
}
