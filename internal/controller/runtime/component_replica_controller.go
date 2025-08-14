/*
Copyright 2025.

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
	"time"

	"github.com/cosmonic-labs/runtime-operator/api/condition"
	runtimev1alpha1 "github.com/cosmonic-labs/runtime-operator/api/runtime/v1alpha1"
	"github.com/cosmonic-labs/runtime-operator/pkg/crdtools"
	"github.com/cosmonic-labs/runtime-operator/pkg/lattice"
	wasmv1 "github.com/cosmonic-labs/runtime-operator/pkg/rpc/wasmcloud/runtime/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const (
	componentReplicaReconcileInterval = 1 * time.Minute
	componentReplicaFinalizer         = "runtime.wasmcloud.dev/component-replica"

	componentReplicaUnresponsiveTimeout = 2 * componentReplicaReconcileInterval
	componentReplicaDeleteTimeout       = componentReplicaUnresponsiveTimeout + componentReplicaReconcileInterval

	componentReplicaStatusTimeout    = 2 * time.Second
	componentReplicaPlacementTimeout = 10 * time.Second
)

// ComponentReplicaReconciler reconciles a Component object
type ComponentReplicaReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	Dispatch   lattice.Dispatch
	reconciler condition.AnyConditionedReconciler
}

// +kubebuilder:rbac:groups=runtime.wasmcloud.dev,resources=components,verbs=get;list;watch
// +kubebuilder:rbac:groups=runtime.wasmcloud.dev,resources=componentreplicas,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=runtime.wasmcloud.dev,resources=componentreplicas/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=runtime.wasmcloud.dev,resources=componentreplicas/finalizers,verbs=update

func (r *ComponentReplicaReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return r.reconciler.Reconcile(ctx, req)
}

func (r *ComponentReplicaReconciler) reconcileComponentReference(
	ctx context.Context,
	replica *runtimev1alpha1.ComponentReplica,
) error {
	return nil
}

func (r *ComponentReplicaReconciler) reconcilePlacement(
	ctx context.Context,
	replica *runtimev1alpha1.ComponentReplica,
) error {
	logger := log.FromContext(ctx)
	if !replica.Status.AllTrue(runtimev1alpha1.ComponentReplicaConditionValid) {
		return errors.New("component replica is not associated with a cluster")
	}

	// already placed on the host.
	if replica.Status.WorkloadID != "" {
		return nil
	}

	// not placed on a host, try to place it.
	logger.Info("ComponentReplica is associated with a cluster. Placing on host.", "component", replica.Name)

	componentConfig := make(map[string]string)
	for _, configName := range replica.Spec.ComponentDefinition.ConfigFrom {
		var config runtimev1alpha1.Config
		if err := r.Get(ctx, client.ObjectKey{Namespace: replica.Namespace, Name: configName.Name}, &config); err != nil {
			return err
		}
		for _, entry := range config.Spec.Config {
			value := []byte(entry.Value)
			if entry.ValueFrom != nil {
				var err error
				value, err = ResolveValue(ctx, r.Client, replica.Namespace, entry.ValueFrom)
				if err != nil {
					return err
				}
			}
			componentConfig[entry.Name] = string(value)
		}
	}

	linkImports := make([]*wasmv1.Link, 0)

	var linkList runtimev1alpha1.LinkList
	if err := r.List(ctx, &linkList); err != nil {
		return err
	}

	for _, link := range linkList.Items {
		source := link.Spec.Source
		target := link.Spec.Target
		if source.Component != nil && source.Component.Namespace == replica.Namespace && source.Component.Name == replica.Spec.ComponentName {
			linkConfig, err := ResolveConfigFrom(ctx, r.Client, link.Namespace, link.Spec.Source.ConfigFrom)
			if err != nil {
				return err
			}
			var targetType wasmv1.WorkloadType
			var targetNamespace string
			var targetName string
			if target.Component != nil {
				targetType = wasmv1.WorkloadType_WORKLOAD_TYPE_COMPONENT
				targetNamespace = target.Component.Namespace
				targetName = target.Component.Name
			} else if target.Provider != nil {
				targetType = wasmv1.WorkloadType_WORKLOAD_TYPE_PROVIDER
				targetNamespace = target.Provider.Namespace
				targetName = target.Provider.Name
			} else {
				continue
			}

			linkImports = append(linkImports, &wasmv1.Link{
				Name: crdtools.Coalesce(link.Name, "default"),
				Wit: &wasmv1.WitInterface{
					Namespace:  link.Spec.WIT.Namespace,
					Package:    link.Spec.WIT.Package,
					Interfaces: link.Spec.WIT.Interfaces,
				},
				Target: &wasmv1.LinkTarget{
					WorkloadType: targetType,
					Namespace:    targetNamespace,
					Name:         targetName,
				},
				Transport: wasmv1.Transport_TRANSPORT_NATS,
				Config:    linkConfig,
			})
		}
	}

	hostClient, err := r.Dispatch.ClientFor(replica.Spec.HostID)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, componentReplicaPlacementTimeout)
	defer cancel()
	scaleResp, err := hostClient.StartComponent(ctx, &wasmv1.ComponentStartRequest{
		Name:         replica.Spec.ComponentName,
		Image:        replica.Spec.ComponentDefinition.Image,
		Namespace:    replica.Namespace,
		MaxInstances: uint32(replica.Spec.ComponentDefinition.Concurrency),
		Imports:      linkImports,
		Config:       componentConfig,
	})
	if err != nil {
		logger.Error(err, "Failed to start component", "component", replica.Name)
		return err
	}
	logger.Info("ComponentReplica started on host", "component", replica.Name, "workload_id", scaleResp.Id)

	replica.Status.WorkloadID = scaleResp.Id
	replica.Status.LastSeen = metav1.Now()
	return nil
}

func (r *ComponentReplicaReconciler) reconcileSync(
	ctx context.Context,
	replica *runtimev1alpha1.ComponentReplica,
) error {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling sync", "component", replica.Name)
	if !replica.Status.AllTrue(runtimev1alpha1.ComponentReplicaConditionPlacement) {
		return condition.ErrStatusUnknown(errors.New("replica has not been placed on a host"))
	}

	hostClient, err := r.Dispatch.ClientFor(replica.Spec.HostID)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, componentReplicaStatusTimeout)
	defer cancel()
	resp, err := hostClient.WorkloadStatus(ctx, &wasmv1.WorkloadStatusRequest{
		WorkloadType: wasmv1.WorkloadType_WORKLOAD_TYPE_COMPONENT,
		WorkloadId:   replica.Status.WorkloadID,
	})
	if err != nil {
		return err
	}

	if resp.WorkloadStatus == wasmv1.WorkloadStatus_WORKLOAD_STATUS_ERROR {
		return errors.New("component replica is in error state")
	}

	replica.Status.LastSeen = metav1.Now()

	// NOTE(lxf): we force update the status even if the conditions are not changed.
	// This is because we are updating status' fields other than conditions.
	condition.GetReconcilerContext(ctx).ForceUpdate = true

	return nil
}

// maybeDeleteReplica deletes a replica if it has not been seen for a while.
// this is a post-reconcile hook, so we don't need to check if the replica is still in the cluster.
func (r *ComponentReplicaReconciler) maybeDeleteReplica(ctx context.Context, replica *runtimev1alpha1.ComponentReplica) error {
	logger := log.FromContext(ctx)

	// don't delete valid placements
	if !replica.Status.IsAvailable() {
		return nil
	}

	// Replica has been placed, check if within the lastSeen period
	if replica.Status.AllTrue(runtimev1alpha1.ComponentReplicaConditionPlacement) {
		syncCond := replica.Status.GetCondition(runtimev1alpha1.ComponentReplicaConditionSync)
		if syncCond.Status == corev1.ConditionFalse && syncCond.LastTransitionTime.Add(componentReplicaDeleteTimeout).Before(time.Now()) {
			logger.Info("Deleting unresponsive replica", "replica", replica.Name)
			return r.Delete(ctx, replica)
		}
	}

	return nil
}

func (r *ComponentReplicaReconciler) reconcileReady(ctx context.Context, replica *runtimev1alpha1.ComponentReplica) error {
	if replica.Status.AllTrue(
		runtimev1alpha1.ComponentReplicaConditionValid,
		runtimev1alpha1.ComponentReplicaConditionPlacement,
		runtimev1alpha1.ComponentReplicaConditionSync) {
		return nil
	}

	return errors.New("not all conditions have passed")
}

func (r *ComponentReplicaReconciler) finalize(ctx context.Context, replica *runtimev1alpha1.ComponentReplica) error {
	logger := log.FromContext(ctx)

	if !replica.Status.AllTrue(runtimev1alpha1.ComponentReplicaConditionPlacement) {
		logger.Info("ComponentReplica not placed on host. Skipping.", "component", replica.Name)
		return nil
	}

	hostClient, err := r.Dispatch.ClientFor(replica.Spec.HostID)
	if err != nil {
		logger.Error(err, "Failed to get host client. Ignoring.", "component", replica.Name)
		return nil
	}

	_, err = hostClient.WorkloadStop(ctx, &wasmv1.WorkloadStopRequest{
		WorkloadType: wasmv1.WorkloadType_WORKLOAD_TYPE_COMPONENT,
		WorkloadId:   replica.Status.WorkloadID,
	})
	if err != nil {
		logger.Error(err, "Failed to stop component. Ignoring.", "component", replica.Name)
	}

	logger.Info("Removed component from lattice", "component", replica.Name)
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ComponentReplicaReconciler) SetupWithManager(mgr ctrl.Manager) error {
	reconciler := condition.NewConditionedReconciler(
		r.Client,
		r.Scheme,
		&runtimev1alpha1.ComponentReplica{},
		componentReconcileInterval)
	reconciler.SetFinalizer(componentReplicaFinalizer, r.finalize)

	// check if the component reference is valid
	reconciler.SetCondition(runtimev1alpha1.ComponentReplicaConditionValid, r.reconcileComponentReference)
	// check if replica is placed correctly on host
	reconciler.SetCondition(runtimev1alpha1.ComponentReplicaConditionPlacement, r.reconcilePlacement)
	// check if the replica is in sync with the component spec
	reconciler.SetCondition(runtimev1alpha1.ComponentReplicaConditionSync, r.reconcileSync)
	// check if all conditions are met
	reconciler.SetCondition(condition.TypeReady, r.reconcileReady)

	// check if the replica is unresponsive and delete it
	reconciler.AddPostHook(r.maybeDeleteReplica)

	r.reconciler = reconciler
	return ctrl.NewControllerManagedBy(mgr).
		For(&runtimev1alpha1.ComponentReplica{}).
		WithEventFilter(predicate.GenerationChangedPredicate{}).
		Named("runtime-componentreplica").
		Complete(r)
}
