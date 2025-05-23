/*
Copyright 2021 The Kubernetes Authors.

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
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/ingress-gce/pkg/metrics"
	"k8s.io/klog/v2"
)

const (
	statusSuccess                                  = "success"
	statusError                                    = "error"
	L4ilbLatencyMetricName                         = "l4_ilb_sync_duration_seconds"
	L4ILBDualStackLatencyMetricName                = "l4_ilb_dualstack_sync_duration_seconds"
	L4ILBMultiNetLatencyMetricName                 = "l4_ilb_multinet_sync_duration_seconds"
	L4ilbErrorMetricName                           = "l4_ilb_sync_error_count"
	L4netlbLatencyMetricName                       = "l4_netlb_sync_duration_seconds"
	L4NetLBDualStackLatencyMetricName              = "l4_netlb_dualstack_sync_duration_seconds"
	L4NetLBMultiNetLatencyMetricName               = "l4_netlb_multinet_sync_duration_seconds"
	L4netlbErrorMetricName                         = "l4_netlb_sync_error_count"
	L4netlbLegacyToRBSMigrationPreventedMetricName = "l4_netlb_legacy_to_rbs_migration_prevented_count"
	l4failedHealthCheckName                        = "l4_failed_healthcheck_count"
	l4ControllerHealthCheckName                    = "l4_controller_healthcheck"
	l4LastSyncTimeName                             = "l4_last_sync_time"
	l4LBRemovedFinalizerMetricName                 = "l4_removed_finalizer_count"
	l4LBControllerPanicsMetricName                 = "l4_controllers_panics_count"
	L4SyncDetailsMetricName                        = "l4_sync_details_count"
	l4WeightedLBPodsPerNodeMetricName              = "l4_weighted_lb_pods_per_node"
)

var (
	l4LBSyncLatencyCommonMetricLabels = []string{
		"sync_result",     // result of the sync
		"sync_type",       // whether this is a new service, update or delete
		"periodic_resync", // whether the sync was periodic resync or a update caused by a resource change
	}

	l4SyncLatencyNetLBSpecificMetricLabels = []string{
		"backend_type", // type of the backends of the LB (IG or NEG)
	}

	l4SyncILBSpecificMetricLabels = []string{
		"zonal_affinity", // whether the service uses zonal affinity
	}

	l4LBSyncLatencyMetricsLabels          = append(l4LBSyncLatencyCommonMetricLabels, l4WeightedLBPodsPerNodeMetricName)
	l4LBDualStackSyncLatencyMetricsLabels = append(l4LBSyncLatencyCommonMetricLabels, "ip_families")
	l4LBSyncErrorMetricLabels             = []string{
		"sync_type",    // whether this is a new service, update or delete
		"gce_resource", // The GCE resource whose update caused the error
		// max number of values for error_type = 18 k8s error reasons + 60 http status errors.
		// In production, we will see much fewer number, since many of the error codes are not applicable.
		"error_type",                      // what type of error it was
		l4WeightedLBPodsPerNodeMetricName, // whether the service uses weighted load balancing by pods-per-node
	}
	l4ILBSyncLatency = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name: L4ilbLatencyMetricName,
			Help: "Latency of an L4 ILB Sync",
			// custom buckets - [0.9375s, 1.875s, 3.75s, 7.5s, 30s, 60s, 120s, 240s(4min), 480s(8min), 960s(16m), 3840s(64min), 7680s(128m) +Inf]
			// using funny starter bucket, 0.9375s will only add buckets to existing metric, this is a safe operation in most time series db
			Buckets: prometheus.ExponentialBuckets(0.9375, 2, 15),
		},
		append(l4LBSyncLatencyMetricsLabels, l4SyncILBSpecificMetricLabels...),
	)
	l4ILBDualStackSyncLatency = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    L4ILBDualStackLatencyMetricName,
			Help:    "Latency of an L4 ILB DualStack Sync",
			Buckets: prometheus.ExponentialBuckets(0.5, 2, 15),
		},
		l4LBDualStackSyncLatencyMetricsLabels,
	)

	l4ILBMultiNetSyncLatency = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    L4ILBMultiNetLatencyMetricName,
			Help:    "Latency of an L4 ILB Multinet Sync",
			Buckets: prometheus.ExponentialBuckets(0.5, 2, 15),
		},
		l4LBSyncLatencyCommonMetricLabels,
	)
	l4NetLBMultiNetSyncLatency = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    L4NetLBMultiNetLatencyMetricName,
			Help:    "Latency of an L4 NetLB Multinet Sync",
			Buckets: prometheus.ExponentialBuckets(0.5, 2, 15),
		},
		l4LBSyncLatencyCommonMetricLabels,
	)
	l4ILBSyncErrorCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: L4ilbErrorMetricName,
			Help: "Count of L4 ILB Sync errors",
		},
		append(l4LBSyncErrorMetricLabels, l4SyncILBSpecificMetricLabels...),
	)
	// l4NetLBSyncLatency is a metric that represents the time spent processing L4NetLB service.
	// The metric is labeled with synchronization type and its result.
	l4NetLBSyncLatency = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name: L4netlbLatencyMetricName,
			Help: "Latency of an L4 NetLB Sync",
			// custom buckets - [0.9375s, 1.875s, 3.75s, 7.5s, 30s, 60s, 120s, 240s(4min), 480s(8min), 960s(16m), 3840s(64min), 7680s(128m) +Inf]
			// using funny starter bucket, 0.9375s will only add buckets to existing metric, this is a safe operation in most time series db
			Buckets: prometheus.ExponentialBuckets(0.9375, 2, 15),
		},
		append(l4LBSyncLatencyMetricsLabels, l4SyncLatencyNetLBSpecificMetricLabels...),
	)
	l4NetLBDualStackSyncLatency = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    L4NetLBDualStackLatencyMetricName,
			Help:    "Latency of an L4 NetB DualStack Sync",
			Buckets: prometheus.ExponentialBuckets(0.5, 2, 15),
		},
		l4LBDualStackSyncLatencyMetricsLabels,
	)
	// l4NetLBSyncErrorCount is a metric that counts number of L4NetLB services in Error state.
	// The metric is labeled with synchronization type, the type of error and the name of gce resource that is in error.
	l4NetLBSyncErrorCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: L4netlbErrorMetricName,
			Help: "Count of L4 NetLB Sync errors",
		},
		l4LBSyncErrorMetricLabels,
	)
	l4FailedHealthCheckCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: l4failedHealthCheckName,
			Help: "Count l4 controller healthcheck failures",
		},
		[]string{"controller_name"},
	)
	l4ControllerHealthCheck = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: l4ControllerHealthCheckName,
			Help: "Count l4 controller healthcheck",
		},
		[]string{"controller_name", "status"},
	)
	l4NetLBLegacyToRBSPrevented = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: L4netlbLegacyToRBSMigrationPreventedMetricName,
			Help: "Count of times legacy to rbs migration was prevented",
		},
		[]string{"type"}, // currently, can be migration or race
	)
	l4LastSyncTime = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: l4LastSyncTimeName,
			Help: "Timestamp of last sync started by controller",
		},
		[]string{"controller_name"},
	)
	l4LBRemovedFinalizers = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: l4LBRemovedFinalizerMetricName,
			Help: "Counter for times when L4 specific finalizers were removed unexpectedly",
		},
		[]string{"finalizer_name"},
	)
	l4LBControllerPanics = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: l4LBControllerPanicsMetricName,
			Help: "Counter for times when L4 controllers panic",
		},
		[]string{"controller_name"},
	)

	l4LBSyncDetails = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: L4SyncDetailsMetricName,
			Help: "Details of updates done during L4 LB ensure operations",
		},
		[]string{"controller_name", "success", "predicted_periodic_resync", "was_update"},
	)
)

// init registers l4 ilb and netlb sync metrics.
func init() {
	klog.V(3).Infof("Registering L4 ILB controller metrics %v, %v", l4ILBSyncLatency, l4ILBSyncErrorCount)
	prometheus.MustRegister(l4ILBSyncLatency, l4ILBSyncErrorCount)
	klog.V(3).Infof("Registering L4 ILB DualStack controller metrics %v", l4ILBDualStackSyncLatency)
	prometheus.MustRegister(l4ILBDualStackSyncLatency)
	klog.V(3).Infof("Registering L4 NetLB controller metrics %v, %v", l4NetLBSyncLatency, l4NetLBSyncErrorCount)
	prometheus.MustRegister(l4NetLBSyncLatency, l4NetLBSyncErrorCount)
	klog.V(3).Infof("Registering L4 NetLB DualStack controller metrics %v", l4NetLBDualStackSyncLatency)
	prometheus.MustRegister(l4NetLBDualStackSyncLatency)
	klog.V(3).Infof("Registering L4 ILB MultiNet controller metrics %v", l4ILBMultiNetSyncLatency)
	prometheus.MustRegister(l4ILBMultiNetSyncLatency)
	klog.V(3).Infof("Registering L4 NetLB MultiNet controller metrics %v", l4NetLBMultiNetSyncLatency)
	prometheus.MustRegister(l4NetLBMultiNetSyncLatency)
	klog.V(3).Infof("Registering L4 healthcheck failures count metric: %v", l4FailedHealthCheckCount)
	prometheus.MustRegister(l4FailedHealthCheckCount)
	klog.V(3).Infof("Registering L4 controller healthcheck metric: %v", l4ControllerHealthCheck)
	prometheus.MustRegister(l4ControllerHealthCheck)
	klog.V(3).Infof("Registering L4 controller last processed item time metric: %v", l4LastSyncTime)
	prometheus.MustRegister(l4LastSyncTime)
	klog.V(3).Infof("Registering L4 Removed Finalizers metric %v", l4LBRemovedFinalizers)
	prometheus.MustRegister(l4LBRemovedFinalizers)
	klog.V(3).Infof("Registering L4 controller panics metric: %v", l4LBControllerPanics)
	prometheus.MustRegister(l4LBControllerPanics)
	klog.V(3).Infof("Registering L4 controller sync details metric: %v", l4LBSyncDetails)
	prometheus.MustRegister(l4LBSyncDetails)
}

// PublishILBSyncMetrics exports metrics related to the L4 ILB sync.
func PublishILBSyncMetrics(success bool, syncType, gceResource, errType string, startTime time.Time, isResync bool, isWeightedLB bool, isZonalAffinityLB bool) {
	publishL4ILBSyncLatency(success, syncType, startTime, isResync, isWeightedLB, isZonalAffinityLB)
	if !success {
		publishL4ILBSyncErrorCount(syncType, gceResource, errType, isWeightedLB, isZonalAffinityLB)
	}
}

// publishL4ILBSyncLatency exports the given sync latency datapoint.
func publishL4ILBSyncLatency(success bool, syncType string, startTime time.Time, isResync bool, isWeightedLB bool, isZonalAffinityLB bool) {
	status := statusSuccess
	if !success {
		status = statusError
	}
	l4ILBSyncLatency.WithLabelValues(status, syncType, strconv.FormatBool(isResync), strconv.FormatBool(isWeightedLB), strconv.FormatBool(isZonalAffinityLB)).Observe(time.Since(startTime).Seconds())
}

// PublishL4ILBDualStackSyncLatency exports the given sync latency datapoint.
func PublishL4ILBDualStackSyncLatency(success bool, syncType, ipFamilies string, startTime time.Time, isResync bool) {
	status := statusSuccess
	if !success {
		status = statusError
	}
	l4ILBDualStackSyncLatency.WithLabelValues(status, syncType, strconv.FormatBool(isResync), ipFamilies).Observe(time.Since(startTime).Seconds())
}

// PublishL4ILBMultiNetSyncLatency exports the given sync latency datapoint.
func PublishL4ILBMultiNetSyncLatency(success bool, syncType string, startTime time.Time, isResync bool) {
	status := statusSuccess
	if !success {
		status = statusError
	}
	l4ILBMultiNetSyncLatency.WithLabelValues(status, syncType, strconv.FormatBool(isResync)).Observe(time.Since(startTime).Seconds())
}

// PublishNetLBSyncMetrics exports metrics related to the L4 NetLB sync.
func PublishNetLBSyncMetrics(success bool, syncType, gceResource, errType string, startTime time.Time, isResync bool, isWeightedLB bool, l4BackendType metrics.L4BackendType) {
	publishL4NetLBSync(success, syncType, startTime, isResync, isWeightedLB, l4BackendType)
	if !success {
		publishL4NetLBSyncErrorCount(syncType, gceResource, errType, isWeightedLB)
	}
}

// PublishL4NetLBMultiNetSyncLatency exports the given sync latency datapoint.
func PublishL4NetLBMultiNetSyncLatency(success bool, syncType string, startTime time.Time, isResync bool) {
	status := statusSuccess
	if !success {
		status = statusError
	}
	l4NetLBMultiNetSyncLatency.WithLabelValues(status, syncType, strconv.FormatBool(isResync)).Observe(time.Since(startTime).Seconds())
}

// publishL4ILBSyncLatency exports the given sync latency datapoint.
func publishL4ILBSyncErrorCount(syncType, gceResource, errorType string, isWeightedLB bool, isZonalAffinityLB bool) {
	l4ILBSyncErrorCount.WithLabelValues(syncType, gceResource, errorType, strconv.FormatBool(isWeightedLB), strconv.FormatBool(isZonalAffinityLB)).Inc()
}

// publishL4NetLBSync exports latency metrics for L4 NetLB service after sync.
func publishL4NetLBSync(success bool, syncType string, startTime time.Time, isResync bool, isWeightedLB bool, backendType metrics.L4BackendType) {
	status := statusSuccess
	if !success {
		status = statusError
	}
	l4NetLBSyncLatency.WithLabelValues(status, syncType, strconv.FormatBool(isResync), strconv.FormatBool(isWeightedLB), string(backendType)).Observe(time.Since(startTime).Seconds())
}

// PublishL4NetLBDualStackSyncLatency exports the given sync latency datapoint.
func PublishL4NetLBDualStackSyncLatency(success bool, syncType, ipFamilies string, startTime time.Time, isResync bool) {
	status := statusSuccess
	if !success {
		status = statusError
	}
	l4NetLBDualStackSyncLatency.WithLabelValues(status, syncType, strconv.FormatBool(isResync), ipFamilies).Observe(time.Since(startTime).Seconds())
}

// publishL4NetLBSyncErrorCount exports error count metrics for L4 NetLB after error sync.
func publishL4NetLBSyncErrorCount(syncType, gceResource, errType string, isWeightedLB bool) {
	l4NetLBSyncErrorCount.WithLabelValues(syncType, gceResource, errType, strconv.FormatBool(isWeightedLB)).Inc()
}

func PublishL4RemovedILBLegacyFinalizer() {
	l4LBRemovedFinalizers.WithLabelValues("ilb_legacy").Inc()
}

func PublishL4RemovedILBFinalizer() {
	l4LBRemovedFinalizers.WithLabelValues("ilb").Inc()
}

func PublishL4RemovedNetLBRBSFinalizer() {
	l4LBRemovedFinalizers.WithLabelValues("netlb_rbs").Inc()
}

func PublishL4ServiceCleanupFinalizer() {
	l4LBRemovedFinalizers.WithLabelValues("service_cleanup").Inc()
}

// PublishL4ControllerPanicCount exports error count metrics for L4 controllers
func PublishL4ControllerPanicCount(controllerName string) {
	l4LBControllerPanics.WithLabelValues(controllerName).Inc()
}

// PublishL4FailedHealthCheckCount observers failed health check from controller.
func PublishL4FailedHealthCheckCount(controllerName string) {
	l4FailedHealthCheckCount.WithLabelValues(controllerName).Inc()
}

type L4ControllerHealthCheckStatus string

const ControllerHealthyStatus = L4ControllerHealthCheckStatus("Healthy")
const ControllerUnhealthyStatus = L4ControllerHealthCheckStatus("Unhealthy")

// PublishL4ControllerHealthCheckStatus stores health state of the controller.
func PublishL4ControllerHealthCheckStatus(controllerName string, status L4ControllerHealthCheckStatus) {
	l4ControllerHealthCheck.WithLabelValues(controllerName, string(status)).Inc()
}

// IncreaseL4NetLBLegacyToRBSMigrationAttempts increases l4NetLBLegacyToRBSPrevented metric for stopped migration
func IncreaseL4NetLBLegacyToRBSMigrationAttempts() {
	l4NetLBLegacyToRBSPrevented.WithLabelValues("migration").Inc()
}

// IncreaseL4NetLBTargetPoolRaceWithRBS increases l4NetLBLegacyToRBSPrevented metric for race condition between controllers
func IncreaseL4NetLBTargetPoolRaceWithRBS() {
	l4NetLBLegacyToRBSPrevented.WithLabelValues("race").Inc()
}

// PublishL4controllerLastSyncTime records timestamp when L4 controller STARTED to sync an item
func PublishL4controllerLastSyncTime(controllerName string) {
	l4LastSyncTime.WithLabelValues(controllerName).SetToCurrentTime()
}

func PublishL4SyncDetails(controllerName string, success, isPredictedResync, wasUpdated bool) {
	l4LBSyncDetails.WithLabelValues(controllerName, strconv.FormatBool(success), strconv.FormatBool(isPredictedResync), strconv.FormatBool(wasUpdated)).Inc()
}
