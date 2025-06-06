/*
Copyright 2020 The Kubernetes Authors.

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

package metrics

import (
	"fmt"
	"strconv"
	"testing"

	"k8s.io/klog/v2"

	"github.com/google/go-cmp/cmp"
	v1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/ingress-gce/pkg/annotations"
	backendconfigv1 "k8s.io/ingress-gce/pkg/apis/backendconfig/v1"
	frontendconfigv1beta1 "k8s.io/ingress-gce/pkg/apis/frontendconfig/v1beta1"
	pscmetrics "k8s.io/ingress-gce/pkg/psc/metrics"
	"k8s.io/ingress-gce/pkg/utils"
	"k8s.io/utils/ptr"
)

var (
	testTTL          = int64(10)
	defaultNamespace = "default"
	testServicePorts = []utils.ServicePort{
		{
			ID: utils.ServicePortID{
				Service: types.NamespacedName{
					Name:      "dummy-service",
					Namespace: defaultNamespace,
				},
				Port: v1.ServiceBackendPort{Number: 80},
			},
			BackendConfig: &backendconfigv1.BackendConfig{
				Spec: backendconfigv1.BackendConfigSpec{
					Cdn: &backendconfigv1.CDNConfig{
						Enabled:     true,
						CachePolicy: &backendconfigv1.CacheKeyPolicy{},
					},
					SessionAffinity: &backendconfigv1.SessionAffinityConfig{
						AffinityType:         "GENERATED_COOKIE",
						AffinityCookieTtlSec: &testTTL,
					},
					SecurityPolicy: &backendconfigv1.SecurityPolicyConfig{
						Name: "security-policy-1",
					},
					ConnectionDraining: &backendconfigv1.ConnectionDrainingConfig{
						DrainingTimeoutSec: testTTL,
					},
					HealthCheck: &backendconfigv1.HealthCheckConfig{RequestPath: ptr.To("/foo")},
				},
			},
		},
		{
			ID: utils.ServicePortID{
				Service: types.NamespacedName{
					Name:      "foo-service",
					Namespace: defaultNamespace,
				},
				Port: v1.ServiceBackendPort{Number: 80},
			},
			NEGEnabled: true,
			BackendConfig: &backendconfigv1.BackendConfig{
				Spec: backendconfigv1.BackendConfigSpec{
					Iap: &backendconfigv1.IAPConfig{
						Enabled: true,
					},
					SessionAffinity: &backendconfigv1.SessionAffinityConfig{
						AffinityType:         "CLIENT_IP",
						AffinityCookieTtlSec: &testTTL,
					},
					TimeoutSec: &testTTL,
					CustomRequestHeaders: &backendconfigv1.CustomRequestHeadersConfig{
						Headers: []string{},
					},
				},
			},
		},
		// NEG default backend.
		{
			ID: utils.ServicePortID{
				Service: types.NamespacedName{
					Name:      "dummy-service",
					Namespace: defaultNamespace,
				},
				Port: v1.ServiceBackendPort{Number: 80},
			},
			NEGEnabled:   true,
			L7ILBEnabled: true,
		},
		{
			ID: utils.ServicePortID{
				Service: types.NamespacedName{
					Name:      "bar-service",
					Namespace: defaultNamespace,
				},
				Port: v1.ServiceBackendPort{Number: 5000},
			},
			NEGEnabled:   true,
			L7ILBEnabled: true,
			BackendConfig: &backendconfigv1.BackendConfig{
				Spec: backendconfigv1.BackendConfigSpec{
					Iap: &backendconfigv1.IAPConfig{
						Enabled: true,
					},
					SessionAffinity: &backendconfigv1.SessionAffinityConfig{
						AffinityType:         "GENERATED_COOKIE",
						AffinityCookieTtlSec: &testTTL,
					},
					ConnectionDraining: &backendconfigv1.ConnectionDrainingConfig{
						DrainingTimeoutSec: testTTL,
					},
				},
			},
		},
		// ServicePort with THC
		{
			ID: utils.ServicePortID{
				Service: types.NamespacedName{
					Name:      "service-with-thc",
					Namespace: defaultNamespace,
				},
				Port: v1.ServiceBackendPort{Number: 80},
			},
			NEGEnabled:       true,
			THCConfiguration: utils.THCConfiguration{THCOptInOnSvc: true},
		},
		{
			ID: utils.ServicePortID{
				Service: types.NamespacedName{
					Name:      "service-with-empty-security-policy",
					Namespace: defaultNamespace,
				},
				Port: v1.ServiceBackendPort{Number: 80},
			},
			BackendConfig: &backendconfigv1.BackendConfig{
				Spec: backendconfigv1.BackendConfigSpec{
					SecurityPolicy: &backendconfigv1.SecurityPolicyConfig{
						Name: "",
					},
				},
			},
		},
		{
			ID: utils.ServicePortID{
				Service: types.NamespacedName{
					Name:      "service-with-no-security-policy",
					Namespace: defaultNamespace,
				},
				Port: v1.ServiceBackendPort{Number: 80},
			},
			BackendConfig: &backendconfigv1.BackendConfig{
				Spec: backendconfigv1.BackendConfigSpec{},
			},
		},
		// NEG default backend.
		{
			ID: utils.ServicePortID{
				Service: types.NamespacedName{
					Name:      "dummy-service-rxlb",
					Namespace: defaultNamespace,
				},
				Port: v1.ServiceBackendPort{Number: 80},
			},
			NEGEnabled:           true,
			L7XLBRegionalEnabled: true,
		},
	}
	ingressStates = []struct {
		desc             string
		ing              *v1.Ingress
		fc               *frontendconfigv1beta1.FrontendConfig
		frontendFeatures []feature
		svcPorts         []utils.ServicePort
		backendFeatures  []feature
	}{
		{
			"empty spec",
			&v1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: defaultNamespace,
					Name:      "ingress0",
				},
			},
			nil,
			[]feature{ingress, externalIngress, httpEnabled},
			[]utils.ServicePort{},
			nil,
		},
		{
			"http disabled",
			&v1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: defaultNamespace,
					Name:      "ingress1",
					Annotations: map[string]string{
						allowHTTPKey: "false"},
				},
			},
			nil,
			[]feature{ingress, externalIngress},
			[]utils.ServicePort{},
			nil,
		},
		{
			"default backend",
			&v1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: defaultNamespace,
					Name:      "ingress2",
				},
				Spec: v1.IngressSpec{
					DefaultBackend: &v1.IngressBackend{
						Service: &v1.IngressServiceBackend{
							Name: "dummy-service",
							Port: v1.ServiceBackendPort{
								Number: int32(80),
							},
						},
					},
					Rules: []v1.IngressRule{},
				},
			},
			nil,
			[]feature{ingress, externalIngress, httpEnabled},
			[]utils.ServicePort{testServicePorts[0]},
			[]feature{servicePort, externalServicePort, cloudCDN,
				cookieAffinity, cloudArmor, cloudArmorSet, backendConnectionDraining, customHealthChecks},
		},
		{
			"host rule only",
			&v1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: defaultNamespace,
					Name:      "ingress3",
				},
				Spec: v1.IngressSpec{
					Rules: []v1.IngressRule{
						{
							Host: "foo.bar",
						},
					},
				},
			},
			nil,
			[]feature{ingress, externalIngress, httpEnabled, hostBasedRouting},
			[]utils.ServicePort{},
			nil,
		},
		{
			"both host and path rules",
			&v1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: defaultNamespace,
					Name:      "ingress4",
				},
				Spec: v1.IngressSpec{
					Rules: []v1.IngressRule{
						{
							Host: "foo.bar",
							IngressRuleValue: v1.IngressRuleValue{
								HTTP: &v1.HTTPIngressRuleValue{
									Paths: []v1.HTTPIngressPath{
										{
											Path: "/foo",
											Backend: v1.IngressBackend{
												Service: &v1.IngressServiceBackend{
													Name: "foo-service",
													Port: v1.ServiceBackendPort{
														Number: int32(80),
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			nil,
			[]feature{ingress, externalIngress, httpEnabled,
				hostBasedRouting, pathBasedRouting},
			[]utils.ServicePort{testServicePorts[1]},
			[]feature{servicePort, externalServicePort, neg, cloudIAP, cloudIAPEmpty,
				clientIPAffinity, cloudArmorNil, backendTimeout, customRequestHeaders},
		},
		{
			"default backend and host rule",
			&v1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: defaultNamespace,
					Name:      "ingress5",
				},
				Spec: v1.IngressSpec{
					DefaultBackend: &v1.IngressBackend{
						Service: &v1.IngressServiceBackend{
							Name: "dummy-service",
							Port: v1.ServiceBackendPort{
								Number: int32(80),
							},
						},
					},
					Rules: []v1.IngressRule{
						{
							Host: "foo.bar",
							IngressRuleValue: v1.IngressRuleValue{
								HTTP: &v1.HTTPIngressRuleValue{
									Paths: []v1.HTTPIngressPath{
										{
											Path: "/foo",
											Backend: v1.IngressBackend{
												Service: &v1.IngressServiceBackend{
													Name: "foo-service",
													Port: v1.ServiceBackendPort{
														Number: int32(80),
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			nil,
			[]feature{ingress, externalIngress, httpEnabled,
				hostBasedRouting, pathBasedRouting},
			testServicePorts[:2],
			[]feature{servicePort, externalServicePort, cloudCDN,
				cookieAffinity, cloudArmor, cloudArmorSet, backendConnectionDraining, customHealthChecks, neg, cloudIAP, cloudIAPEmpty,
				clientIPAffinity, cloudArmorNil, backendTimeout, customRequestHeaders},
		},
		{
			"tls termination with pre-shared certs",
			&v1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: defaultNamespace,
					Name:      "ingress6",
					Annotations: map[string]string{
						preSharedCertKey: "pre-shared-cert1,pre-shared-cert2",
						SSLCertKey:       "pre-shared-cert1,pre-shared-cert2",
					},
				},
				Spec: v1.IngressSpec{
					DefaultBackend: &v1.IngressBackend{
						Service: &v1.IngressServiceBackend{
							Name: "dummy-service",
							Port: v1.ServiceBackendPort{
								Number: int32(80),
							},
						},
					},
					Rules: []v1.IngressRule{},
				},
			},
			nil,
			[]feature{ingress, externalIngress, httpEnabled,
				tlsTermination, preSharedCertsForTLS},
			[]utils.ServicePort{testServicePorts[0]},
			[]feature{servicePort, externalServicePort, cloudCDN,
				cookieAffinity, cloudArmor, cloudArmorSet, backendConnectionDraining, customHealthChecks},
		},
		{
			"tls termination with google managed certs",
			&v1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: defaultNamespace,
					Name:      "ingress7",
					Annotations: map[string]string{
						managedCertKey: "managed-cert1,managed-cert2",
						SSLCertKey:     "managed-cert1,managed-cert2",
					},
				},
				Spec: v1.IngressSpec{
					DefaultBackend: &v1.IngressBackend{
						Service: &v1.IngressServiceBackend{
							Name: "dummy-service",
							Port: v1.ServiceBackendPort{
								Number: int32(80),
							},
						},
					},
					Rules: []v1.IngressRule{},
				},
			},
			nil,
			[]feature{ingress, externalIngress, httpEnabled,
				tlsTermination, managedCertsForTLS},
			[]utils.ServicePort{testServicePorts[0]},
			[]feature{servicePort, externalServicePort, cloudCDN,
				cookieAffinity, cloudArmor, cloudArmorSet, backendConnectionDraining, customHealthChecks},
		},
		{
			"tls termination with pre-shared and google managed certs",
			&v1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: defaultNamespace,
					Name:      "ingress8",
					Annotations: map[string]string{
						preSharedCertKey: "pre-shared-cert1,pre-shared-cert2",
						managedCertKey:   "managed-cert1,managed-cert2",
						SSLCertKey:       "pre-shared-cert1,pre-shared-cert2,managed-cert1,managed-cert2",
					},
				},
				Spec: v1.IngressSpec{
					DefaultBackend: &v1.IngressBackend{
						Service: &v1.IngressServiceBackend{
							Name: "dummy-service",
							Port: v1.ServiceBackendPort{
								Number: int32(80),
							},
						},
					},
					Rules: []v1.IngressRule{},
				},
			},
			nil,
			[]feature{ingress, externalIngress, httpEnabled,
				tlsTermination, preSharedCertsForTLS, managedCertsForTLS},
			[]utils.ServicePort{testServicePorts[0]},
			[]feature{servicePort, externalServicePort, cloudCDN,
				cookieAffinity, cloudArmor, cloudArmorSet, backendConnectionDraining, customHealthChecks},
		},
		{
			"tls termination with pre-shared and secret based certs",
			&v1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: defaultNamespace,
					Name:      "ingress9",
					Annotations: map[string]string{
						preSharedCertKey: "pre-shared-cert1,pre-shared-cert2",
						SSLCertKey:       "pre-shared-cert1,pre-shared-cert2",
					},
				},
				Spec: v1.IngressSpec{
					Rules: []v1.IngressRule{
						{
							Host: "foo.bar",
							IngressRuleValue: v1.IngressRuleValue{
								HTTP: &v1.HTTPIngressRuleValue{
									Paths: []v1.HTTPIngressPath{
										{
											Path: "/foo",
											Backend: v1.IngressBackend{
												Service: &v1.IngressServiceBackend{
													Name: "foo-service",
													Port: v1.ServiceBackendPort{
														Number: int32(80),
													},
												},
											},
										},
									},
								},
							},
						},
					},
					TLS: []v1.IngressTLS{
						{
							Hosts:      []string{"foo.bar"},
							SecretName: "secret-1",
						},
					},
				},
			},
			nil,
			[]feature{ingress, externalIngress, httpEnabled, hostBasedRouting,
				pathBasedRouting, tlsTermination, preSharedCertsForTLS, secretBasedCertsForTLS},
			[]utils.ServicePort{testServicePorts[1]},
			[]feature{servicePort, externalServicePort, neg, cloudIAP, cloudIAPEmpty,
				clientIPAffinity, cloudArmorNil, backendTimeout, customRequestHeaders},
		},
		{
			"global static ip",
			&v1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: defaultNamespace,
					Name:      "ingress10",
					Annotations: map[string]string{
						preSharedCertKey: "pre-shared-cert1,pre-shared-cert2",
						SSLCertKey:       "pre-shared-cert1,pre-shared-cert2",
						staticIPKey:      "10.0.1.2",
					},
				},
				Spec: v1.IngressSpec{
					DefaultBackend: &v1.IngressBackend{
						Service: &v1.IngressServiceBackend{
							Name: "dummy-service",
							Port: v1.ServiceBackendPort{
								Number: int32(80),
							},
						},
					},
					Rules: []v1.IngressRule{},
				},
			},
			nil,
			[]feature{ingress, externalIngress, httpEnabled,
				tlsTermination, preSharedCertsForTLS, staticGlobalIP, managedStaticGlobalIP},
			[]utils.ServicePort{testServicePorts[0]},
			[]feature{servicePort, externalServicePort, cloudCDN,
				cookieAffinity, cloudArmor, cloudArmorSet, backendConnectionDraining, customHealthChecks},
		},
		{
			"default backend, host rule for internal load-balancer",
			&v1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: defaultNamespace,
					Name:      "ingress11",
					Annotations: map[string]string{
						ingressClassKey: gceL7ILBIngressClass,
					},
				},
				Spec: v1.IngressSpec{
					DefaultBackend: &v1.IngressBackend{
						Service: &v1.IngressServiceBackend{
							Name: "dummy-service",
							Port: v1.ServiceBackendPort{
								Number: int32(80),
							},
						},
					},
					Rules: []v1.IngressRule{
						{
							Host: "bar",
							IngressRuleValue: v1.IngressRuleValue{
								HTTP: &v1.HTTPIngressRuleValue{
									Paths: []v1.HTTPIngressPath{
										{
											Path: "/bar",
											Backend: v1.IngressBackend{
												Service: &v1.IngressServiceBackend{

													Name: "bar-service",
													Port: v1.ServiceBackendPort{
														Number: int32(5000),
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			nil,
			[]feature{ingress, internalIngress, httpEnabled,
				hostBasedRouting, pathBasedRouting},
			[]utils.ServicePort{testServicePorts[2], testServicePorts[3]},
			[]feature{servicePort, internalServicePort, neg, cloudIAP, cloudIAPEmpty,
				cookieAffinity, cloudArmorNil, backendConnectionDraining},
		},
		{
			"non-existent pre-shared cert",
			&v1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: defaultNamespace,
					Name:      "ingress12",
					Annotations: map[string]string{
						preSharedCertKey: "pre-shared-cert1",
					},
				},
				Spec: v1.IngressSpec{
					DefaultBackend: &v1.IngressBackend{
						Service: &v1.IngressServiceBackend{
							Name: "dummy-service",
							Port: v1.ServiceBackendPort{
								Number: int32(80),
							},
						},
					},
					Rules: []v1.IngressRule{},
				},
			},
			nil,
			[]feature{ingress, externalIngress, httpEnabled},
			[]utils.ServicePort{},
			nil,
		},
		{
			"user specified global static IP",
			&v1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: defaultNamespace,
					Name:      "ingress13",
					Annotations: map[string]string{
						StaticGlobalIPNameKey: "user-spec-static-ip",
						staticIPKey:           "user-spec-static-ip",
					},
				},
				Spec: v1.IngressSpec{
					DefaultBackend: &v1.IngressBackend{
						Service: &v1.IngressServiceBackend{
							Name: "dummy-service",
							Port: v1.ServiceBackendPort{
								Number: int32(80),
							},
						},
					},
					Rules: []v1.IngressRule{},
				},
			},
			nil,
			[]feature{ingress, externalIngress, httpEnabled,
				staticGlobalIP, specifiedStaticGlobalIP},
			[]utils.ServicePort{},
			nil,
		},
		{
			"sslpolicy and tls termination with pre-shared certs",
			&v1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: defaultNamespace,
					Name:      "ingress14",
					Annotations: map[string]string{
						preSharedCertKey: "pre-shared-cert1,pre-shared-cert2",
						SSLCertKey:       "pre-shared-cert1,pre-shared-cert2",
					},
				},
				Spec: v1.IngressSpec{
					DefaultBackend: &v1.IngressBackend{
						Service: &v1.IngressServiceBackend{
							Name: "dummy-service",
							Port: v1.ServiceBackendPort{
								Number: int32(80),
							},
						},
					},
					Rules: []v1.IngressRule{},
				},
			},
			&frontendconfigv1beta1.FrontendConfig{
				Spec: frontendconfigv1beta1.FrontendConfigSpec{
					SslPolicy: ptr.To("test-policy"),
				},
			},
			[]feature{ingress, externalIngress, httpEnabled,
				tlsTermination, preSharedCertsForTLS, sslPolicy},
			[]utils.ServicePort{},
			nil,
		},
		{
			"user specified regional static IP",
			&v1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: defaultNamespace,
					Name:      "ingress15",
					Annotations: map[string]string{
						annotations.RegionalStaticIPNameKey: "user-spec-static-ip",
						ingressClassKey:                     "gce-internal",
					},
				},
				Spec: v1.IngressSpec{
					DefaultBackend: &v1.IngressBackend{
						Service: &v1.IngressServiceBackend{
							Name: "dummy-service",
							Port: v1.ServiceBackendPort{
								Number: int32(80),
							},
						},
					},
					Rules: []v1.IngressRule{},
				},
			},
			nil,
			[]feature{ingress, internalIngress, httpEnabled,
				specifiedStaticRegionalIP},
			[]utils.ServicePort{},
			nil,
		},
		{
			"HTTPS Redirects and tls termination with pre-shared certs",
			&v1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: defaultNamespace,
					Name:      "ingress16",
					Annotations: map[string]string{
						preSharedCertKey: "pre-shared-cert1,pre-shared-cert2",
						SSLCertKey:       "pre-shared-cert1,pre-shared-cert2",
					},
				},
				Spec: v1.IngressSpec{
					DefaultBackend: &v1.IngressBackend{
						Service: &v1.IngressServiceBackend{
							Name: "dummy-service",
							Port: v1.ServiceBackendPort{
								Number: int32(80),
							},
						},
					},
					Rules: []v1.IngressRule{},
				},
			},
			&frontendconfigv1beta1.FrontendConfig{
				Spec: frontendconfigv1beta1.FrontendConfigSpec{
					RedirectToHttps: &frontendconfigv1beta1.HttpsRedirectConfig{Enabled: true},
				},
			},
			[]feature{ingress, externalIngress, httpEnabled,
				tlsTermination, preSharedCertsForTLS, httpsRedirects},
			[]utils.ServicePort{},
			nil,
		},
		{
			"HTTPS Redirects Disabled and tls termination with pre-shared certs",
			&v1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: defaultNamespace,
					Name:      "ingress17",
					Annotations: map[string]string{
						preSharedCertKey: "pre-shared-cert1,pre-shared-cert2",
						SSLCertKey:       "pre-shared-cert1,pre-shared-cert2",
					},
				},
				Spec: v1.IngressSpec{
					DefaultBackend: &v1.IngressBackend{
						Service: &v1.IngressServiceBackend{
							Name: "dummy-service",
							Port: v1.ServiceBackendPort{
								Number: int32(80),
							},
						},
					},
					Rules: []v1.IngressRule{},
				},
			},
			&frontendconfigv1beta1.FrontendConfig{
				Spec: frontendconfigv1beta1.FrontendConfigSpec{
					RedirectToHttps: &frontendconfigv1beta1.HttpsRedirectConfig{Enabled: false},
				},
			},
			[]feature{ingress, externalIngress, httpEnabled,
				tlsTermination, preSharedCertsForTLS},
			[]utils.ServicePort{},
			nil,
		},
		{
			"empty sslpolicy and tls termination with pre-shared certs",
			&v1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: defaultNamespace,
					Name:      "ingress18",
					Annotations: map[string]string{
						preSharedCertKey: "pre-shared-cert1,pre-shared-cert2",
						SSLCertKey:       "pre-shared-cert1,pre-shared-cert2",
					},
				},
				Spec: v1.IngressSpec{
					DefaultBackend: &v1.IngressBackend{
						Service: &v1.IngressServiceBackend{
							Name: "dummy-service",
							Port: v1.ServiceBackendPort{
								Number: int32(80),
							},
						},
					},
					Rules: []v1.IngressRule{},
				},
			},
			&frontendconfigv1beta1.FrontendConfig{
				Spec: frontendconfigv1beta1.FrontendConfigSpec{
					SslPolicy: ptr.To(""),
				},
			},
			[]feature{ingress, externalIngress, httpEnabled,
				tlsTermination, preSharedCertsForTLS},
			[]utils.ServicePort{},
			nil,
		},
		{
			"path rule for service port with thc",
			&v1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: defaultNamespace,
					Name:      "ingress19",
				},
				Spec: v1.IngressSpec{
					Rules: []v1.IngressRule{
						{
							IngressRuleValue: v1.IngressRuleValue{
								HTTP: &v1.HTTPIngressRuleValue{
									Paths: []v1.HTTPIngressPath{
										{
											Path: "/foo",
											Backend: v1.IngressBackend{
												Service: &v1.IngressServiceBackend{
													Name: "service-with-thc",
													Port: v1.ServiceBackendPort{
														Number: int32(80),
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			nil,
			[]feature{ingress, externalIngress, httpEnabled, pathBasedRouting},
			[]utils.ServicePort{testServicePorts[4]},
			[]feature{servicePort, externalServicePort, neg, transparentHealthChecks},
		},
		{
			"backend with empty security policy",
			&v1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: defaultNamespace,
					Name:      "ingress20",
				},
				Spec: v1.IngressSpec{
					DefaultBackend: &v1.IngressBackend{
						Service: &v1.IngressServiceBackend{
							Name: "service-with-empty-security-policy",
							Port: v1.ServiceBackendPort{
								Number: int32(80),
							},
						},
					},
					Rules: []v1.IngressRule{},
				},
			},
			nil,
			[]feature{ingress, externalIngress, httpEnabled},
			[]utils.ServicePort{testServicePorts[5]},
			[]feature{servicePort, externalServicePort, cloudArmor, cloudArmorEmpty},
		},
		{
			"backend with no security policy",
			&v1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: defaultNamespace,
					Name:      "ingress21",
				},
				Spec: v1.IngressSpec{
					DefaultBackend: &v1.IngressBackend{
						Service: &v1.IngressServiceBackend{
							Name: "service-with-no-security-policy",
							Port: v1.ServiceBackendPort{
								Number: int32(80),
							},
						},
					},
					Rules: []v1.IngressRule{},
				},
			},
			nil,
			[]feature{ingress, externalIngress, httpEnabled},
			[]utils.ServicePort{testServicePorts[6]},
			[]feature{servicePort, externalServicePort, cloudArmorNil},
		},
		{
			"default backend, host rule for regional external load-balancer",
			&v1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: defaultNamespace,
					Name:      "ingress22",
					Annotations: map[string]string{
						ingressClassKey: gceL7RegionalExternalIngressClass,
					},
				},
				Spec: v1.IngressSpec{
					DefaultBackend: &v1.IngressBackend{
						Service: &v1.IngressServiceBackend{
							Name: "dummy-service",
							Port: v1.ServiceBackendPort{
								Number: int32(80),
							},
						},
					},
					Rules: []v1.IngressRule{
						{
							Host: "bar",
							IngressRuleValue: v1.IngressRuleValue{
								HTTP: &v1.HTTPIngressRuleValue{
									Paths: []v1.HTTPIngressPath{
										{
											Path: "/bar",
											Backend: v1.IngressBackend{
												Service: &v1.IngressServiceBackend{

													Name: "bar-service",
													Port: v1.ServiceBackendPort{
														Number: int32(5000),
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			nil,
			[]feature{ingress, regionalExternalIngress, httpEnabled,
				hostBasedRouting, pathBasedRouting},
			[]utils.ServicePort{testServicePorts[7]},
			[]feature{servicePort, regionalExternalServicePort, neg},
		},
	}
)

func TestFeaturesForIngress(t *testing.T) {
	t.Parallel()
	for _, tc := range ingressStates {
		tc := tc
		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()
			gotFrontendFeatures := featuresForIngress(tc.ing, tc.fc, klog.TODO())
			if diff := cmp.Diff(tc.frontendFeatures, gotFrontendFeatures); diff != "" {
				t.Fatalf("Got diff for frontend features (-want +got):\n%s", diff)
			}
		})
	}
}

func TestFeaturesForServicePort(t *testing.T) {
	t.Parallel()
	for _, tc := range ingressStates {
		tc := tc
		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()
			backendFeatureMap := make(map[feature]bool)
			var gotBackendFeatures []feature
			for _, svcPort := range tc.svcPorts {
				for _, feature := range featuresForServicePort(svcPort, klog.TODO()) {
					if backendFeatureMap[feature] {
						continue
					}
					backendFeatureMap[feature] = true
					gotBackendFeatures = append(gotBackendFeatures, feature)
				}
			}
			if diff := cmp.Diff(tc.backendFeatures, gotBackendFeatures); diff != "" {
				t.Fatalf("Got diff for backend features (-want +got):\n%s", diff)
			}
		})
	}
}

func TestComputeIngressMetrics(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		desc               string
		ingressStates      []IngressState
		expectIngressCount map[feature]int
		expectSvcPortCount map[feature]int
	}{
		{
			"frontend features only",
			[]IngressState{
				NewIngressState(ingressStates[0].ing, nil, ingressStates[0].svcPorts),
				NewIngressState(ingressStates[1].ing, nil, ingressStates[1].svcPorts),
				NewIngressState(ingressStates[3].ing, nil, ingressStates[3].svcPorts),
				NewIngressState(ingressStates[13].ing, nil, ingressStates[13].svcPorts),
				NewIngressState(ingressStates[14].ing, ingressStates[14].fc, ingressStates[13].svcPorts),
				NewIngressState(ingressStates[16].ing, ingressStates[16].fc, ingressStates[16].svcPorts),
			},
			map[feature]int{
				backendConnectionDraining: 0,
				backendTimeout:            0,
				clientIPAffinity:          0,
				cloudArmor:                0,
				cloudCDN:                  0,
				cloudIAP:                  0,
				cookieAffinity:            0,
				customRequestHeaders:      0,
				externalIngress:           6,
				httpEnabled:               5,
				hostBasedRouting:          1,
				ingress:                   6,
				internalIngress:           0,
				regionalExternalIngress:   0,
				managedCertsForTLS:        0,
				managedStaticGlobalIP:     0,
				neg:                       0,
				pathBasedRouting:          0,
				preSharedCertsForTLS:      2,
				secretBasedCertsForTLS:    0,
				specifiedStaticGlobalIP:   1,
				staticGlobalIP:            1,
				tlsTermination:            2,
				sslPolicy:                 1,
				httpsRedirects:            1,
			},
			map[feature]int{
				backendConnectionDraining:   0,
				backendTimeout:              0,
				clientIPAffinity:            0,
				cloudArmor:                  0,
				cloudArmorSet:               0,
				cloudArmorEmpty:             0,
				cloudArmorNil:               0,
				cloudCDN:                    0,
				cloudIAP:                    0,
				cookieAffinity:              0,
				customRequestHeaders:        0,
				internalServicePort:         0,
				regionalExternalServicePort: 0,
				servicePort:                 0,
				externalServicePort:         0,
				neg:                         0,
				transparentHealthChecks:     0,
			},
		},
		{
			"features for internal and external load-balancers",
			[]IngressState{
				NewIngressState(ingressStates[0].ing, nil, ingressStates[0].svcPorts),
				NewIngressState(ingressStates[1].ing, nil, ingressStates[1].svcPorts),
				NewIngressState(ingressStates[3].ing, nil, ingressStates[3].svcPorts),
				NewIngressState(ingressStates[11].ing, nil, ingressStates[11].svcPorts),
				NewIngressState(ingressStates[13].ing, nil, ingressStates[13].svcPorts),
				NewIngressState(ingressStates[22].ing, nil, ingressStates[22].svcPorts),
			},
			map[feature]int{
				backendConnectionDraining: 1,
				backendTimeout:            0,
				clientIPAffinity:          0,
				cloudArmor:                0,
				cloudCDN:                  0,
				cloudIAP:                  1,
				cloudIAPEmpty:             1,
				cookieAffinity:            1,
				customRequestHeaders:      0,
				externalIngress:           4,
				regionalExternalIngress:   1,
				httpEnabled:               5,
				hostBasedRouting:          3,
				ingress:                   6,
				internalIngress:           1,
				managedCertsForTLS:        0,
				managedStaticGlobalIP:     0,
				neg:                       2,
				pathBasedRouting:          2,
				preSharedCertsForTLS:      0,
				secretBasedCertsForTLS:    0,
				specifiedStaticGlobalIP:   1,
				staticGlobalIP:            1,
				tlsTermination:            0,
				sslPolicy:                 0,
			},
			map[feature]int{
				backendConnectionDraining:   1,
				backendTimeout:              0,
				clientIPAffinity:            0,
				cloudArmor:                  0,
				cloudArmorSet:               0,
				cloudArmorEmpty:             0,
				cloudArmorNil:               1,
				cloudCDN:                    0,
				cloudIAP:                    1,
				cloudIAPEmpty:               1,
				cookieAffinity:              1,
				customRequestHeaders:        0,
				internalServicePort:         2,
				regionalExternalServicePort: 1,
				servicePort:                 3,
				externalServicePort:         0,
				neg:                         3,
				transparentHealthChecks:     0,
			},
		},
		{
			"frontend and backend features with thc",
			[]IngressState{
				NewIngressState(ingressStates[2].ing, nil, ingressStates[2].svcPorts),
				NewIngressState(ingressStates[4].ing, nil, ingressStates[4].svcPorts),
				NewIngressState(ingressStates[6].ing, nil, ingressStates[6].svcPorts),
				NewIngressState(ingressStates[8].ing, nil, ingressStates[8].svcPorts),
				NewIngressState(ingressStates[10].ing, nil, ingressStates[10].svcPorts),
				NewIngressState(ingressStates[12].ing, nil, ingressStates[12].svcPorts),
				NewIngressState(ingressStates[19].ing, nil, ingressStates[19].svcPorts),
			},
			map[feature]int{
				backendConnectionDraining: 4,
				backendTimeout:            1,
				clientIPAffinity:          1,
				cloudArmor:                4,
				cloudCDN:                  4,
				cloudIAP:                  1,
				cloudIAPEmpty:             1,
				cookieAffinity:            4,
				customRequestHeaders:      1,
				customHealthChecks:        4,
				externalIngress:           7,
				httpEnabled:               7,
				hostBasedRouting:          1,
				ingress:                   7,
				internalIngress:           0,
				regionalExternalIngress:   0,
				managedCertsForTLS:        1,
				managedStaticGlobalIP:     1,
				neg:                       2,
				pathBasedRouting:          2,
				preSharedCertsForTLS:      3,
				secretBasedCertsForTLS:    0,
				specifiedStaticGlobalIP:   0,
				staticGlobalIP:            1,
				tlsTermination:            3,
				sslPolicy:                 0,
			},
			map[feature]int{
				backendConnectionDraining:   1,
				backendTimeout:              1,
				clientIPAffinity:            1,
				cloudArmor:                  1,
				cloudArmorSet:               1,
				cloudArmorEmpty:             0,
				cloudArmorNil:               1,
				cloudCDN:                    1,
				cloudIAP:                    1,
				cloudIAPEmpty:               1,
				cookieAffinity:              1,
				customRequestHeaders:        1,
				internalServicePort:         0,
				regionalExternalServicePort: 0,
				servicePort:                 3,
				externalServicePort:         3,
				neg:                         2,
				customHealthChecks:          1,
				transparentHealthChecks:     1,
			},
		},
		{
			"all ingress features",
			[]IngressState{
				NewIngressState(ingressStates[0].ing, nil, ingressStates[0].svcPorts),
				NewIngressState(ingressStates[1].ing, nil, ingressStates[1].svcPorts),
				NewIngressState(ingressStates[2].ing, nil, ingressStates[2].svcPorts),
				NewIngressState(ingressStates[3].ing, nil, ingressStates[3].svcPorts),
				NewIngressState(ingressStates[4].ing, nil, ingressStates[4].svcPorts),
				NewIngressState(ingressStates[5].ing, nil, ingressStates[5].svcPorts),
				NewIngressState(ingressStates[6].ing, nil, ingressStates[6].svcPorts),
				NewIngressState(ingressStates[7].ing, nil, ingressStates[7].svcPorts),
				NewIngressState(ingressStates[8].ing, nil, ingressStates[8].svcPorts),
				NewIngressState(ingressStates[9].ing, nil, ingressStates[9].svcPorts),
				NewIngressState(ingressStates[10].ing, nil, ingressStates[10].svcPorts),
				NewIngressState(ingressStates[11].ing, nil, ingressStates[11].svcPorts),
				NewIngressState(ingressStates[12].ing, nil, ingressStates[12].svcPorts),
				NewIngressState(ingressStates[13].ing, nil, ingressStates[13].svcPorts),
				NewIngressState(ingressStates[14].ing, ingressStates[14].fc, ingressStates[14].svcPorts),
				NewIngressState(ingressStates[15].ing, nil, ingressStates[15].svcPorts),
				NewIngressState(ingressStates[16].ing, ingressStates[16].fc, ingressStates[16].svcPorts),
				NewIngressState(ingressStates[20].ing, nil, ingressStates[20].svcPorts),
				NewIngressState(ingressStates[22].ing, nil, ingressStates[22].svcPorts),
			},
			map[feature]int{
				backendConnectionDraining: 7,
				backendTimeout:            3,
				clientIPAffinity:          3,
				cloudArmor:                7,
				cloudCDN:                  6,
				cloudIAP:                  4,
				cloudIAPEmpty:             4,
				cookieAffinity:            7,
				customRequestHeaders:      3,
				customHealthChecks:        6,
				externalIngress:           16,
				regionalExternalIngress:   1,
				httpEnabled:               18,
				hostBasedRouting:          6,
				ingress:                   19,
				internalIngress:           2,
				managedCertsForTLS:        2,
				managedStaticGlobalIP:     1,
				neg:                       5,
				pathBasedRouting:          5,
				preSharedCertsForTLS:      6,
				secretBasedCertsForTLS:    1,
				specifiedStaticGlobalIP:   1,
				specifiedStaticRegionalIP: 1,
				staticGlobalIP:            2,
				tlsTermination:            7,
				sslPolicy:                 1,
				httpsRedirects:            1,
			},
			map[feature]int{
				backendConnectionDraining:   2,
				backendTimeout:              1,
				clientIPAffinity:            1,
				cloudArmor:                  2,
				cloudArmorSet:               1,
				cloudArmorEmpty:             1,
				cloudArmorNil:               2,
				cloudCDN:                    1,
				cloudIAP:                    2,
				cloudIAPEmpty:               2,
				cookieAffinity:              2,
				customRequestHeaders:        1,
				customHealthChecks:          1,
				internalServicePort:         2,
				regionalExternalServicePort: 1,
				servicePort:                 6,
				externalServicePort:         3,
				neg:                         4,
				transparentHealthChecks:     0,
			},
		},
	} {
		tc := tc
		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()
			newMetrics := FakeControllerMetrics()
			for _, ingState := range tc.ingressStates {
				ingKey := fmt.Sprintf("%s/%s", defaultNamespace, ingState.ingress.Name)
				newMetrics.SetIngress(ingKey, ingState)
			}
			gotIngressCount, gotSvcPortCount := newMetrics.computeIngressMetrics()
			if diff := cmp.Diff(tc.expectIngressCount, gotIngressCount); diff != "" {
				t.Errorf("Got diff for ingress features count (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tc.expectSvcPortCount, gotSvcPortCount); diff != "" {
				t.Fatalf("Got diff for service port features count (-want +got):\n%s", diff)
			}
		})
	}
}

func TestComputePSCMetrics(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		desc     string
		saStates []pscmetrics.PSCState
		// service attachments to delete
		deleteStates  []string
		expectSACount map[feature]int
	}{
		{
			desc:     "empty input",
			saStates: []pscmetrics.PSCState{},
			expectSACount: map[feature]int{
				sa:          0,
				saInSuccess: 0,
				saInError:   0,
			},
		},
		{
			desc: "one service attachment",
			saStates: []pscmetrics.PSCState{
				newPSCState(true),
			},
			expectSACount: map[feature]int{
				sa:          1,
				saInSuccess: 1,
				saInError:   0,
			},
		},
		{
			desc: "one service attachment in error",
			saStates: []pscmetrics.PSCState{
				newPSCState(false),
			},
			expectSACount: map[feature]int{
				sa:          1,
				saInSuccess: 0,
				saInError:   1,
			},
		},
		{
			desc: "many service attachments, some in error",
			saStates: []pscmetrics.PSCState{
				newPSCState(true),
				newPSCState(true),
				newPSCState(true),
				newPSCState(false),
				newPSCState(false),
			},
			expectSACount: map[feature]int{
				sa:          5,
				saInSuccess: 3,
				saInError:   2,
			},
		},
		{
			desc: "some additions, and some deletions",
			saStates: []pscmetrics.PSCState{
				newPSCState(true),
				newPSCState(true),
				newPSCState(true),
				newPSCState(false),
				newPSCState(false),
			},
			deleteStates: []string{"0", "3"},
			expectSACount: map[feature]int{
				sa:          3,
				saInSuccess: 2,
				saInError:   1,
			},
		},
	} {
		tc := tc
		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()
			newMetrics := FakeControllerMetrics()
			for i, serviceState := range tc.saStates {
				newMetrics.SetServiceAttachment(strconv.Itoa(i), serviceState)
			}

			for _, key := range tc.deleteStates {
				newMetrics.DeleteServiceAttachment(key)
			}
			got := newMetrics.computePSCMetrics()
			if diff := cmp.Diff(tc.expectSACount, got); diff != "" {
				t.Fatalf("Got diff for service attachment counts (-want +got):\n%s", diff)
			}
		})
	}
}

func newPSCState(inSuccess bool) pscmetrics.PSCState {
	return pscmetrics.PSCState{
		InSuccess: inSuccess,
	}
}

func TestComputeServiceMetrics(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		desc           string
		services       []string
		deleteServices []string
		expectSACount  map[feature]int
	}{
		{
			desc: "empty input",
			expectSACount: map[feature]int{
				services: 0,
			},
		},
		{
			desc:     "one service",
			services: []string{"service-1"},
			expectSACount: map[feature]int{
				services: 1,
			},
		},
		{
			desc:     "many services",
			services: []string{"service-1", "service-2", "service-3", "service-4", "service-5", "service-6"},
			expectSACount: map[feature]int{
				services: 6,
			},
		},
		{
			desc:           "some additions, and some deletions",
			services:       []string{"service-1", "service-2", "service-3", "service-4", "service-5", "service-6"},
			deleteServices: []string{"service-2", "service-5"},
			expectSACount: map[feature]int{
				services: 4,
			},
		},
	} {
		tc := tc
		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()
			newMetrics := FakeControllerMetrics()
			for _, service := range tc.services {
				newMetrics.SetService(service)
			}

			for _, service := range tc.deleteServices {
				newMetrics.DeleteService(service)
			}

			got := newMetrics.computeServiceMetrics()
			if diff := cmp.Diff(tc.expectSACount, got); diff != "" {
				t.Fatalf("Got diff for service counts (-want +got):\n%s", diff)
			}
		})
	}
}
