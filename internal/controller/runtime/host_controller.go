/*
Copyright 2024.

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

package runtime

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/cosmonic-labs/runtime-operator/api/condition"
	runtimev1alpha1 "github.com/cosmonic-labs/runtime-operator/api/runtime/v1alpha1"
	"github.com/cosmonic-labs/runtime-operator/pkg/lattice"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	hostFinalizer = "runtime.wasmcloud.dev/host"
	// how often we reconcile the host. Lower values will result in more frequent reconciliation, but quicker reaction to changes.
	hostReconcileInterval = 30 * time.Second
	// how much time to wait before considering a host unresponsive
	hostUnresponsiveTimeout = 2 * hostReconcileInterval
	// how much time to wait before deleting a host
	hostDeleteTimeout = hostUnresponsiveTimeout + (5 * time.Second)
	// how much time to wait for the host to respond to a heartbeat request
	heartbeatRequestTimeout = 1 * time.Second
)

// HostReconciler reconciles a Host object
type HostReconciler struct {
	client.Client
	Scheme         *runtime.Scheme
	Dispatch       lattice.Dispatch
	HostHeartbeats <-chan string
	reconciler     condition.AnyConditionedReconciler
	// scheduling disabled on hosts that have cpu or memory higher than these thresholds ( percentages )
	CPUBackpressureThreshold    float64
	MemoryBackpressureThreshold float64
}

// +kubebuilder:rbac:groups=runtime.wasmcloud.dev,resources=hosts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=runtime.wasmcloud.dev,resources=hosts/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=runtime.wasmcloud.dev,resources=hosts/finalizers,verbs=update
func (r *HostReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return r.reconciler.Reconcile(ctx, req)
}

func (r *HostReconciler) reconcileReady(ctx context.Context, host *runtimev1alpha1.Host) error {
	return host.Status.ErrAllTrue(runtimev1alpha1.HostConditionReporting, runtimev1alpha1.HostConditionHealthy)
}

func (r *HostReconciler) reconcileHostReporting(ctx context.Context, host *runtimev1alpha1.Host) error {
	client, err := r.Dispatch.ClientFor(host.Spec.HostID)
	if err != nil {
		return condition.ErrStatusUnknown(err)
	}

	heartbeat, err := client.Heartbeat(ctx)
	if err != nil {
		return condition.ErrStatusUnknown(err)
	}

	// NOTE(lxf): we force update the status even if the conditions are not changed.
	reconcilerCtx := condition.GetReconcilerContext(ctx)
	reconcilerCtx.ForceUpdate = true

	host.Status.SystemCPUUsage = strconv.FormatFloat(float64(heartbeat.GetSystemCpuUsage()), 'f', -1, 32)
	host.Status.SystemMemoryTotal = int64(heartbeat.GetSystemMemoryTotal())
	host.Status.SystemMemoryFree = int64(heartbeat.GetSystemMemoryFree())
	host.Status.ComponentCount = int(heartbeat.GetComponentCount())
	host.Status.ProviderCount = int(heartbeat.GetProviderCount())
	host.Status.HostName = heartbeat.GetHostname()
	host.Status.OSName = heartbeat.GetOsName()
	host.Status.OSArch = heartbeat.GetOsArch()
	host.Status.OSKernel = heartbeat.GetOsKernel()
	host.Status.Version = heartbeat.GetVersion()

	host.Status.LastSeen = metav1.Now()

	return nil
}

// check if the host is experiencing cpu / memory backpressure
func (r *HostReconciler) reconcileHostHealthy(ctx context.Context, host *runtimev1alpha1.Host) error {
	if !host.Status.AllTrue(runtimev1alpha1.HostConditionReporting) {
		return condition.ErrStatusUnknown(errors.New("host is not reporting"))
	}

	cpuUsage, err := strconv.ParseFloat(host.Status.SystemCPUUsage, 32)
	if err != nil {
		return condition.ErrStatusUnknown(err)
	}

	memoryUsage := 100 * float64(host.Status.SystemMemoryFree) / float64(host.Status.SystemMemoryTotal)

	if cpuUsage > r.CPUBackpressureThreshold {
		return fmt.Errorf("host is experiencing cpu backpressure: %.2f%% (threshold: %.2f%%)", cpuUsage, r.CPUBackpressureThreshold)
	}

	if memoryUsage > r.MemoryBackpressureThreshold {
		return fmt.Errorf("host is experiencing memory backpressure: %.2f%% (threshold: %.2f%%)", memoryUsage, r.MemoryBackpressureThreshold)
	}

	return nil
}

// maybeDeleteHost deletes a host if it is unresponsive and past the delete timeout
// this is a post-reconcile hook, so we don't need to check if the host is still in the cluster.
func (r *HostReconciler) maybeDeleteHost(ctx context.Context, host *runtimev1alpha1.Host) error {
	logger := log.FromContext(ctx)
	// if the host is reporting, don't touch it
	if host.Status.AllTrue(runtimev1alpha1.HostConditionReporting) {
		return nil
	}

	cutoff := metav1.NewTime(time.Now().Add(-hostDeleteTimeout))
	if !host.Status.LastSeen.Before(&cutoff) {
		logger.Info("Host is unresponsive, but still within the delete timeout", "host", host.Name)
		return nil
	}

	logger.Error(nil, "Deleting unresponsive host", "host", host.Name)
	return r.Delete(ctx, host)
}

func (r *HostReconciler) finalize(ctx context.Context, host *runtimev1alpha1.Host) error {
	logger := log.FromContext(ctx)

	// delete all component & provider replicas on this host
	componentReplicaList := &runtimev1alpha1.ComponentReplicaList{}
	if err := r.List(ctx, componentReplicaList, client.MatchingFields{lattice.ComponentReplicaByHostIndex: host.Spec.HostID}); err != nil {
		return err
	}
	providerReplicaList := &runtimev1alpha1.ProviderReplicaList{}
	if err := r.List(ctx, providerReplicaList, client.MatchingFields{lattice.ProviderReplicaByHostIndex: host.Spec.HostID}); err != nil {
		return err
	}

	for _, componentReplica := range componentReplicaList.Items {
		if err := r.Delete(ctx, &componentReplica); err != nil {
			logger.Error(err, "Failed to delete component replica", "componentReplica", componentReplica.Name)
		}
	}

	for _, providerReplica := range providerReplicaList.Items {
		if err := r.Delete(ctx, &providerReplica); err != nil {
			logger.Error(err, "Failed to delete provider replica", "providerReplica", providerReplica.Name)
		}
	}

	return nil
}

// this is the only situation we use the gratuitous heartbeat
// from this point on we communicate with the host via their dedicated subject (control.host.<hostId>.*)
func (r *HostReconciler) handleHeartbeat(hostID string) {
	ctx, cancel := context.WithTimeout(context.Background(), heartbeatRequestTimeout)
	defer cancel()
	logger := log.FromContext(ctx)

	client, err := r.Dispatch.ClientFor(hostID)
	if err != nil {
		logger.Error(err, "Failed to get client for host", "hostID", hostID)
		return
	}

	heartbeat, err := client.Heartbeat(ctx)
	if err != nil {
		logger.Error(err, "Failed to get heartbeat for host", "hostID", hostID)
		return
	}

	// upsert host object in the cluster namespace, the reconciler will acquire it via finalizer
	host := &runtimev1alpha1.Host{
		ObjectMeta: metav1.ObjectMeta{
			Name: heartbeat.GetFriendlyName(),
		},
	}

	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, host, func() error {
		// if the host is new, set the cluster spec
		if host.CreationTimestamp.IsZero() {
			host.Labels = heartbeat.GetLabels()
			host.Spec = runtimev1alpha1.HostSpec{
				HostID: heartbeat.GetId(),
			}
		}
		return nil
	}); err != nil {
		logger.Error(err, "Failed to upsert Host", "host", hostID)
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *HostReconciler) SetupWithManager(mgr ctrl.Manager) error {
	go func() {
		for hostID := range r.HostHeartbeats {
			r.handleHeartbeat(hostID)
		}
	}()

	// Kubernetes
	reconciler := condition.NewConditionedReconciler(r.Client, r.Scheme, &runtimev1alpha1.Host{}, hostReconcileInterval)
	reconciler.SetFinalizer(hostFinalizer, r.finalize)

	reconciler.SetCondition(runtimev1alpha1.HostConditionReporting, r.reconcileHostReporting)
	reconciler.SetCondition(runtimev1alpha1.HostConditionHealthy, r.reconcileHostHealthy)
	reconciler.SetCondition(condition.TypeReady, r.reconcileReady)

	reconciler.AddPostHook(r.maybeDeleteHost)

	r.reconciler = reconciler

	return ctrl.NewControllerManagedBy(mgr).
		For(&runtimev1alpha1.Host{}).
		Named("runtime-host").
		Complete(r)
}
