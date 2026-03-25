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

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/GoogleCloudPlatform/k8s-cloud-provider/pkg/cloud"
	"github.com/GoogleCloudPlatform/k8s-cloud-provider/pkg/cloud/meta"
	"google.golang.org/api/compute/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/cloud-provider-gcp/providers/gce"
	ingctx "k8s.io/ingress-gce/pkg/context"
	"k8s.io/ingress-gce/pkg/l4/annotations"
	"k8s.io/ingress-gce/pkg/test"
	"k8s.io/ingress-gce/pkg/utils/namer"
	"k8s.io/klog/v2"
)

func TestCustomForwardingRuleSync(t *testing.T) {
	lbClass := annotations.CustomNegLoadBalancerClass
	frName := "custom-fr"
	frIP := "10.0.0.100"
	hcPort := int64(8080)
	project := "test-project"
	region := "us-central1"

	bsURL := fmt.Sprintf("https://www.googleapis.com/compute/v1/projects/%s/regions/%s/backendServices/bs1", project, region)
	hcURL := fmt.Sprintf("https://www.googleapis.com/compute/v1/projects/%s/regions/%s/healthChecks/hc1", project, region)

	testCases := []struct {
		desc           string
		svc            *v1.Service
		fr             *compute.ForwardingRule
		bs             *compute.BackendService
		hc             *compute.HealthCheck
		expectIP       string
		expectPortAttr string
		expectEvent    bool
	}{
		{
			desc: "Valid HTTP health check",
			svc: &v1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "svc1",
					Namespace: "default",
					Annotations: map[string]string{
						annotations.CustomForwardingRuleKey: frName,
					},
				},
				Spec: v1.ServiceSpec{
					LoadBalancerClass: &lbClass,
				},
			},
			fr: &compute.ForwardingRule{
				Name:           frName,
				IPAddress:      frIP,
				BackendService: bsURL,
			},
			bs: &compute.BackendService{
				Name:         "bs1",
				HealthChecks: []string{hcURL},
			},
			hc: &compute.HealthCheck{
				Name: "hc1",
				Type: "HTTP",
				HttpHealthCheck: &compute.HTTPHealthCheck{
					Port: hcPort,
				},
			},
			expectIP:       frIP,
			expectPortAttr: "8080",
		},
		{
			desc: "Unsupported health check type",
			svc: &v1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "svc2",
					Namespace: "default",
					Annotations: map[string]string{
						annotations.CustomForwardingRuleKey: frName,
					},
				},
				Spec: v1.ServiceSpec{
					LoadBalancerClass: &lbClass,
				},
			},
			fr: &compute.ForwardingRule{
				Name:           frName,
				IPAddress:      frIP,
				BackendService: bsURL,
			},
			bs: &compute.BackendService{
				Name:         "bs1",
				HealthChecks: []string{hcURL},
			},
			hc: &compute.HealthCheck{
				Name: "hc1",
				Type: "TCP",
				TcpHealthCheck: &compute.TCPHealthCheck{
					Port: 80,
				},
			},
			expectIP:       frIP,
			expectPortAttr: "",
			expectEvent:    true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			kubeClient := fake.NewSimpleClientset()
			fakeGCE := gce.NewFakeGCECloud(test.DefaultTestClusterValues())
			namer := namer.NewNamer("cluster-uid", "firewall-name", klog.TODO())

			// Mock GCE resources
			mockGCE := fakeGCE.Compute().(*cloud.MockGCE)
			mockGCE.MockForwardingRules.GetHook = func(ctx context.Context, key *meta.Key, m *cloud.MockForwardingRules, options ...cloud.Option) (bool, *compute.ForwardingRule, error) {
				if key.Name == frName {
					return true, tc.fr, nil
				}
				return false, nil, nil
			}
			mockGCE.MockRegionBackendServices.GetHook = func(ctx context.Context, key *meta.Key, m *cloud.MockRegionBackendServices, options ...cloud.Option) (bool, *compute.BackendService, error) {
				if key.Name == "bs1" {
					return true, tc.bs, nil
				}
				return false, nil, nil
			}
			mockGCE.MockRegionHealthChecks.GetHook = func(ctx context.Context, key *meta.Key, m *cloud.MockRegionHealthChecks, options ...cloud.Option) (bool, *compute.HealthCheck, error) {
				if key.Name == "hc1" {
					return true, tc.hc, nil
				}
				return false, nil, nil
			}

			stopCh := make(chan struct{})
			defer close(stopCh)

			ctxConfig := ingctx.ControllerContextConfig{Namespace: v1.NamespaceAll}
			c, err := ingctx.NewControllerContext(kubeClient, nil, nil, nil, nil, nil, nil, nil, nil, kubeClient, fakeGCE, namer, "", ctxConfig, klog.TODO())
			if err != nil {
				t.Fatalf("Failed to create controller context: %v", err)
			}

			lc := NewCustomNegLBController(c, stopCh, klog.TODO())

			// Add service to informer
			c.ServiceInformer.GetIndexer().Add(tc.svc)
			// Also add to fake kube client so updateStatus works
			kubeClient.CoreV1().Services(tc.svc.Namespace).Create(context.TODO(), tc.svc, metav1.CreateOptions{})

			key := tc.svc.Namespace + "/" + tc.svc.Name
			err = lc.sync(key)
			if err != nil {
				t.Errorf("sync failed: %v", err)
			}

			updatedSvc, _ := kubeClient.CoreV1().Services(tc.svc.Namespace).Get(context.TODO(), tc.svc.Name, metav1.GetOptions{})
			if len(updatedSvc.Status.LoadBalancer.Ingress) == 0 || updatedSvc.Status.LoadBalancer.Ingress[0].IP != tc.expectIP {
				t.Errorf("Expected IP %s, got %v", tc.expectIP, updatedSvc.Status.LoadBalancer.Ingress)
			}

			portAttr := updatedSvc.Annotations[annotations.ExternalHealthCheckPortKey]
			if portAttr != tc.expectPortAttr {
				t.Errorf("Expected port annotation %s, got %s", tc.expectPortAttr, portAttr)
			}

			if tc.expectEvent {
				time.Sleep(100 * time.Millisecond)
				events, _ := kubeClient.CoreV1().Events(tc.svc.Namespace).List(context.TODO(), metav1.ListOptions{})
				found := false
				for _, e := range events.Items {
					if e.Reason == "UnsupportedHealthCheck" {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Expected UnsupportedHealthCheck event, but none found")
				}
			}
		})
	}
}
