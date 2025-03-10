/*
Copyright 2025 The Kubernetes Authors.

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

// Code generated by informer-gen. DO NOT EDIT.

package v1beta1

import (
	"context"
	time "time"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
	watch "k8s.io/apimachinery/pkg/watch"
	cache "k8s.io/client-go/tools/cache"
	serviceattachmentv1beta1 "k8s.io/ingress-gce/pkg/apis/serviceattachment/v1beta1"
	versioned "k8s.io/ingress-gce/pkg/serviceattachment/client/clientset/versioned"
	internalinterfaces "k8s.io/ingress-gce/pkg/serviceattachment/client/informers/externalversions/internalinterfaces"
	v1beta1 "k8s.io/ingress-gce/pkg/serviceattachment/client/listers/serviceattachment/v1beta1"
)

// ServiceAttachmentInformer provides access to a shared informer and lister for
// ServiceAttachments.
type ServiceAttachmentInformer interface {
	Informer() cache.SharedIndexInformer
	Lister() v1beta1.ServiceAttachmentLister
}

type serviceAttachmentInformer struct {
	factory          internalinterfaces.SharedInformerFactory
	tweakListOptions internalinterfaces.TweakListOptionsFunc
	namespace        string
}

// NewServiceAttachmentInformer constructs a new informer for ServiceAttachment type.
// Always prefer using an informer factory to get a shared informer instead of getting an independent
// one. This reduces memory footprint and number of connections to the server.
func NewServiceAttachmentInformer(client versioned.Interface, namespace string, resyncPeriod time.Duration, indexers cache.Indexers) cache.SharedIndexInformer {
	return NewFilteredServiceAttachmentInformer(client, namespace, resyncPeriod, indexers, nil)
}

// NewFilteredServiceAttachmentInformer constructs a new informer for ServiceAttachment type.
// Always prefer using an informer factory to get a shared informer instead of getting an independent
// one. This reduces memory footprint and number of connections to the server.
func NewFilteredServiceAttachmentInformer(client versioned.Interface, namespace string, resyncPeriod time.Duration, indexers cache.Indexers, tweakListOptions internalinterfaces.TweakListOptionsFunc) cache.SharedIndexInformer {
	return cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListFunc: func(options v1.ListOptions) (runtime.Object, error) {
				if tweakListOptions != nil {
					tweakListOptions(&options)
				}
				return client.NetworkingV1beta1().ServiceAttachments(namespace).List(context.TODO(), options)
			},
			WatchFunc: func(options v1.ListOptions) (watch.Interface, error) {
				if tweakListOptions != nil {
					tweakListOptions(&options)
				}
				return client.NetworkingV1beta1().ServiceAttachments(namespace).Watch(context.TODO(), options)
			},
		},
		&serviceattachmentv1beta1.ServiceAttachment{},
		resyncPeriod,
		indexers,
	)
}

func (f *serviceAttachmentInformer) defaultInformer(client versioned.Interface, resyncPeriod time.Duration) cache.SharedIndexInformer {
	return NewFilteredServiceAttachmentInformer(client, f.namespace, resyncPeriod, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc}, f.tweakListOptions)
}

func (f *serviceAttachmentInformer) Informer() cache.SharedIndexInformer {
	return f.factory.InformerFor(&serviceattachmentv1beta1.ServiceAttachment{}, f.defaultInformer)
}

func (f *serviceAttachmentInformer) Lister() v1beta1.ServiceAttachmentLister {
	return v1beta1.NewServiceAttachmentLister(f.Informer().GetIndexer())
}
