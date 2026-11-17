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

// Package vipsharing detects port collisions between LoadBalancer services
// sharing a VIP.
//
// Terminology:
//   - A service "holds" a VIP when the VIP is in its status.loadBalancer.ingress,
//     as seen in the informer cache.
//   - A service "reserves" a VIP in VIPReservations before writing it to its
//     status. The reservations are kept in memory, they make the collision
//     check and the reservation atomic when services are synced concurrently,
//     and they cover the time until the status update is in the informer cache.
//   - The ports a service uses on a VIP are its spec.ports, its health check
//     node port and, for Standalone NEG services, the health check ports
//     redirected on the VIP (listed in the external health check annotation).
//   - Two services "collide" on a VIP when they use the same protocol and port
//     on it.
//   - A service is "blocked" on a VIP when it can't use the VIP because of
//     collisions.
//
// Flow of a controller syncing a service:
//  1. Reserve the VIPs the service wants to use. The ports are checked against
//     the services holding or reserving the VIPs.
//  2. Write the VIPs which are not blocked to the service status.
//  3. Once the status update succeeded, Commit the VIPs written to the status:
//     the reservations not needed anymore are released. Release all the
//     reservations when the service is deleted or stops using VIPs.
//  4. Requeue the services returned by Commit and Release: they were blocked on
//     the released VIPs and may be able to use them now.
package vipsharing
