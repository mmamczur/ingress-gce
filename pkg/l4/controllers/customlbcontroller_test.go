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
	"testing"

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

func TestCustomNegLBControllerSync(t *testing.T) {
	lbClass := annotations.CustomNegLoadBalancerClass
	testCases := []struct {
		desc     string
		svc      *v1.Service
		expectIP string
	}{
		{
			desc: "Service with custom lb class and loadBalancerIP",
			svc: &v1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "svc1",
					Namespace: "default",
				},
				Spec: v1.ServiceSpec{
					LoadBalancerClass: &lbClass,
					LoadBalancerIP:    "1.2.3.4",
				},
			},
			expectIP: "1.2.3.4",
		},
		{
			desc: "Service with custom lb class but no loadBalancerIP",
			svc: &v1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "svc2",
					Namespace: "default",
				},
				Spec: v1.ServiceSpec{
					LoadBalancerClass: &lbClass,
				},
			},
			expectIP: "",
		},
		{
			desc: "Service with different lb class",
			svc: &v1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "svc3",
					Namespace: "default",
				},
				Spec: v1.ServiceSpec{
					LoadBalancerClass: ptrTo("other-class"),
					LoadBalancerIP:    "1.2.3.4",
				},
			},
			expectIP: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			kubeClient := fake.NewSimpleClientset()
			fakeGCE := gce.NewFakeGCECloud(test.DefaultTestClusterValues())
			namer := namer.NewNamer("cluster-uid", "firewall-name", klog.TODO())

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
			if tc.expectIP != "" {
				if len(updatedSvc.Status.LoadBalancer.Ingress) == 0 || updatedSvc.Status.LoadBalancer.Ingress[0].IP != tc.expectIP {
					t.Errorf("Expected IP %s, got %v", tc.expectIP, updatedSvc.Status.LoadBalancer.Ingress)
				}
			} else {
				if len(updatedSvc.Status.LoadBalancer.Ingress) > 0 {
					t.Errorf("Expected no ingress IP, got %v", updatedSvc.Status.LoadBalancer.Ingress)
				}
			}
		})
	}
}

func ptrTo(s string) *string {
	return &s
}
