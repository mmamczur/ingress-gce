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
	"fmt"

	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/ingress-gce/pkg/context"
	"k8s.io/ingress-gce/pkg/l4/annotations"
	"k8s.io/ingress-gce/pkg/utils"
	"k8s.io/klog/v2"
)

// CustomLBController manages services with CustomNegLoadBalancerClass.
type CustomLBController struct {
	ctx      *context.ControllerContext
	svcQueue utils.TaskQueue
	logger   klog.Logger
}

// NewCustomNegLBController creates a new instance of CustomLBController.
func NewCustomNegLBController(ctx *context.ControllerContext, stopCh <-chan struct{}, logger klog.Logger) *CustomLBController {
	logger = logger.WithName("CustomLBController")
	lc := &CustomLBController{
		ctx:    ctx,
		logger: logger,
	}
	lc.svcQueue = utils.NewPeriodicTaskQueueWithMultipleWorkers("custom-neg-lb", "services", 1, lc.sync, logger)

	ctx.ServiceInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			svc := obj.(*v1.Service)
			if lc.shouldProcess(svc) {
				lc.enqueue(svc)
			}
		},
		UpdateFunc: func(old, cur interface{}) {
			curSvc := cur.(*v1.Service)
			if lc.shouldProcess(curSvc) {
				lc.enqueue(curSvc)
			}
		},
	})

	return lc
}

func (lc *CustomLBController) shouldProcess(svc *v1.Service) bool {
	return svc.Spec.LoadBalancerClass != nil && *svc.Spec.LoadBalancerClass == annotations.CustomNegLoadBalancerClass
}

func (lc *CustomLBController) enqueue(svc *v1.Service) {
	lc.svcQueue.Enqueue(svc)
}

func (lc *CustomLBController) Run() {
	lc.logger.Info("Starting CustomLBController")
	lc.svcQueue.Run()
}

func (lc *CustomLBController) sync(key string) error {
	obj, exists, err := lc.ctx.ServiceInformer.GetIndexer().GetByKey(key)
	if err != nil {
		return fmt.Errorf("failed to lookup service for key %s: %w", key, err)
	}
	if !exists || obj == nil {
		return nil
	}
	svc := obj.(*v1.Service)
	if !lc.shouldProcess(svc) {
		return nil
	}
	svcLogger := lc.logger.WithValues("service", klog.KObj(svc))

	if svc.Spec.LoadBalancerIP == "" {
		svcLogger.V(4).Info("Service has no loadBalancerIP, skipping")
		return nil
	}

	newStatus := &v1.LoadBalancerStatus{
		Ingress: []v1.LoadBalancerIngress{
			{IP: svc.Spec.LoadBalancerIP},
		},
	}

	return updateServiceStatus(lc.ctx, svc, newStatus, nil, svcLogger)
}
