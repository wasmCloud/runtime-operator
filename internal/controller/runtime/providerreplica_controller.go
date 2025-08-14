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

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/cosmonic-labs/runtime-operator/api/condition"
	runtimev1alpha1 "github.com/cosmonic-labs/runtime-operator/api/runtime/v1alpha1"
	"github.com/cosmonic-labs/runtime-operator/pkg/crdtools"
	"github.com/cosmonic-labs/runtime-operator/pkg/lattice"
	wasmv1 "github.com/cosmonic-labs/runtime-operator/pkg/rpc/wasmcloud/runtime/v1"
)

const (
	// NOTE(lxf): A reasonable value here is 30 seconds. Lower values will result in more frequent reconciliation, but quicker reaction to changes.
	providerReplicaReconcileInterval   = 1 * time.Minute
	providerReplicaFinalizer           = "runtime.wasmcloud.dev/provider-replica"
	providerReplicaUnresponsiveTimeout = 2 * providerReplicaReconcileInterval
	providerReplicaDeleteTimeout       = providerReplicaUnresponsiveTimeout + providerReplicaReconcileInterval

	providerReplicaPlacementTimeout = 10 * time.Second
)

// ProviderReplicaReconciler reconciles a ProviderReplica object
type ProviderReplicaReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	Dispatch   lattice.Dispatch
	reconciler condition.AnyConditionedReconciler
}

// +kubebuilder:rbac:groups=runtime.wasmcloud.dev,resources=providerreplicas,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=runtime.wasmcloud.dev,resources=providerreplicas/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=runtime.wasmcloud.dev,resources=providerreplicas/finalizers,verbs=update
func (r *ProviderReplicaReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return r.reconciler.Reconcile(ctx, req)
}

// SetupWithManager sets up the controller with the Manager.
func (r *ProviderReplicaReconciler) SetupWithManager(mgr ctrl.Manager) error {
	reconciler := condition.NewConditionedReconciler(
		r.Client,
		r.Scheme,
		&runtimev1alpha1.ProviderReplica{},
		providerReplicaReconcileInterval)
	reconciler.SetFinalizer(providerReplicaFinalizer, r.finalize)

	// check if the provider reference is valid
	reconciler.SetCondition(runtimev1alpha1.ProviderReplicaConditionProviderReference, r.reconcileProviderReference)
	// check if replica is placed correctly on host
	reconciler.SetCondition(runtimev1alpha1.ProviderReplicaConditionPlacement, r.reconcilePlacement)
	// check if the replica is in sync with the provider spec
	reconciler.SetCondition(runtimev1alpha1.ProviderReplicaConditionSync, r.reconcileSync)
	// check if all conditions are met
	reconciler.SetCondition(condition.TypeReady, r.reconcileReady)

	// check if the replica is unresponsive and delete it
	reconciler.AddPostHook(r.maybeDeleteReplica)

	r.reconciler = reconciler

	return ctrl.NewControllerManagedBy(mgr).
		For(&runtimev1alpha1.ProviderReplica{}).
		Named("runtime-providerreplica").
		Complete(r)
}

func (r *ProviderReplicaReconciler) reconcileProviderReference(
	ctx context.Context,
	replica *runtimev1alpha1.ProviderReplica,
) error {
	provider := &runtimev1alpha1.Provider{}
	return r.Get(
		ctx,
		client.ObjectKey{
			Namespace: replica.Spec.ProviderRef.Namespace,
			Name:      replica.Spec.ProviderRef.Name,
		},
		provider)
}

func (r *ProviderReplicaReconciler) reconcilePlacement(
	ctx context.Context,
	replica *runtimev1alpha1.ProviderReplica,
) error {
	logger := log.FromContext(ctx)
	if !replica.Status.AllTrue(runtimev1alpha1.ProviderReplicaConditionProviderReference) {
		return condition.ErrStatusUnknown(errors.New("provider replica is not associated with a cluster"))
	}

	// already placed on the host, all good.
	if replica.Status.WorkloadID != "" {
		return nil
	}

	// not placed on a host, try to place it.
	logger.Info("ProviderReplica is associated with a cluster. Placing on host.", "provider", replica.Name)

	provider := &runtimev1alpha1.Provider{}
	if err := r.Get(
		ctx,
		client.ObjectKey{
			Namespace: replica.Spec.ProviderRef.Namespace,
			Name:      replica.Spec.ProviderRef.Name,
		},
		provider); err != nil {
		return err
	}

	// resolve provider Config values from config maps and secrets
	providerConfig := make(map[string]string)
	for _, configName := range provider.Spec.ConfigFrom {
		var config runtimev1alpha1.Config
		if err := r.Get(ctx, client.ObjectKey{Namespace: provider.Namespace, Name: configName.Name}, &config); err != nil {
			return err
		}
		for _, entry := range config.Spec.Config {
			value := []byte(entry.Value)
			if entry.ValueFrom != nil {
				var err error
				value, err = ResolveValue(ctx, r.Client, provider.Namespace, entry.ValueFrom)
				if err != nil {
					return err
				}
			}
			providerConfig[entry.Name] = string(value)
		}
	}

	// create links for provider
	linkImports, linkExports, err := r.generateLinks(ctx, provider)
	if err != nil {
		return err
	}

	hostClient, err := r.Dispatch.ClientFor(replica.Spec.HostID)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, providerReplicaPlacementTimeout)
	defer cancel()
	scaleResp, err := hostClient.StartProvider(ctx, &wasmv1.ProviderStartRequest{
		Name:      provider.Name,
		Image:     provider.Spec.Image,
		Namespace: provider.Namespace,
		Config:    providerConfig,
		Imports:   linkImports,
		Exports:   linkExports,
	})
	if err != nil {
		return err
	}

	replica.Status.WorkloadID = scaleResp.Id
	replica.Status.LastSeen = metav1.Now()
	return nil
}

// gemerateLinks generates the imports and exports links for a provider
func (r *ProviderReplicaReconciler) generateLinks(ctx context.Context, provider *runtimev1alpha1.Provider) (linkImports []*wasmv1.Link, linkExports []*wasmv1.Link, err error) {
	linkImports = make([]*wasmv1.Link, 0)
	linkExports = make([]*wasmv1.Link, 0)

	var linkList runtimev1alpha1.LinkList
	if err := r.List(ctx, &linkList); err != nil {
		return nil, nil, err
	}

	for _, link := range linkList.Items {
		source := link.Spec.Source
		target := link.Spec.Target

		// Handle provider-to-* links (imports)
		if source.Provider != nil && source.Provider.Namespace == provider.Namespace && source.Provider.Name == provider.Name {
			linkConfig, err := ResolveConfigFrom(ctx, r.Client, link.Namespace, link.Spec.Source.ConfigFrom)
			if err != nil {
				return nil, nil, err
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

		// Handle component-to-provider links (exports)
		if target.Provider != nil && target.Provider.Namespace == provider.Namespace && target.Provider.Name == provider.Name {
			// Skip provider-to-provider links as requested
			if source.Provider != nil {
				continue
			}

			// Only handle component-to-provider links
			if source.Component == nil {
				continue
			}

			linkConfig, err := ResolveConfigFrom(ctx, r.Client, link.Namespace, link.Spec.Target.ConfigFrom)
			if err != nil {
				return nil, nil, err
			}

			linkExports = append(linkExports, &wasmv1.Link{
				Name: crdtools.Coalesce(link.Name, "default"),
				Wit: &wasmv1.WitInterface{
					Namespace:  link.Spec.WIT.Namespace,
					Package:    link.Spec.WIT.Package,
					Interfaces: link.Spec.WIT.Interfaces,
				},
				Target: &wasmv1.LinkTarget{
					WorkloadType: wasmv1.WorkloadType_WORKLOAD_TYPE_COMPONENT,
					Namespace:    source.Component.Namespace,
					Name:         source.Component.Name,
				},
				Transport: wasmv1.Transport_TRANSPORT_NATS,
				Config:    linkConfig,
			})
		}

		// Handle component-to-provider links (exports)
		if target.Provider != nil && target.Provider.Namespace == provider.Namespace && target.Provider.Name == provider.Name {
			// Skip provider-to-provider links as requested
			if source.Provider != nil {
				continue
			}

			// Only handle component-to-provider links
			if source.Component == nil {
				continue
			}

			linkConfig, err := ResolveConfigFrom(ctx, r.Client, link.Namespace, link.Spec.Target.ConfigFrom)
			if err != nil {
				return nil, nil, err
			}

			linkExports = append(linkExports, &wasmv1.Link{
				Name: crdtools.Coalesce(link.Name, "default"),
				Wit: &wasmv1.WitInterface{
					Namespace:  link.Spec.WIT.Namespace,
					Package:    link.Spec.WIT.Package,
					Interfaces: link.Spec.WIT.Interfaces,
				},
				Target: &wasmv1.LinkTarget{
					WorkloadType: wasmv1.WorkloadType_WORKLOAD_TYPE_COMPONENT,
					Namespace:    source.Component.Namespace,
					Name:         source.Component.Name,
				},
				Transport: wasmv1.Transport_TRANSPORT_NATS,
				Config:    linkConfig,
			})
		}
	}

	return linkImports, linkExports, nil
}

func (r *ProviderReplicaReconciler) reconcileSync(
	ctx context.Context,
	replica *runtimev1alpha1.ProviderReplica,
) error {
	if !replica.Status.AllTrue(runtimev1alpha1.ProviderReplicaConditionPlacement) {
		return condition.ErrStatusUnknown(errors.New("replica has not been placed on a host"))
	}

	hostClient, err := r.Dispatch.ClientFor(replica.Spec.HostID)
	if err != nil {
		return err
	}

	_, err = hostClient.WorkloadStatus(ctx, &wasmv1.WorkloadStatusRequest{
		WorkloadType: wasmv1.WorkloadType_WORKLOAD_TYPE_PROVIDER,
		WorkloadId:   replica.Status.WorkloadID,
	})
	if err != nil {
		return err
	}

	condition.GetReconcilerContext(ctx).ForceUpdate = true
	replica.Status.LastSeen = metav1.Now()
	return nil
}

func (r *ProviderReplicaReconciler) reconcileReady(ctx context.Context, replica *runtimev1alpha1.ProviderReplica) error {
	if !replica.Status.AllTrue(
		runtimev1alpha1.ProviderReplicaConditionProviderReference,
		runtimev1alpha1.ProviderReplicaConditionPlacement,
		runtimev1alpha1.ProviderReplicaConditionSync) {
		return errors.New("not all conditions have passed")
	}

	return nil
}

func (r *ProviderReplicaReconciler) finalize(ctx context.Context, replica *runtimev1alpha1.ProviderReplica) error {
	logger := log.FromContext(ctx)

	if !replica.Status.AllTrue(runtimev1alpha1.ProviderReplicaConditionPlacement) {
		logger.Info("Replica not placed on any hosts. Skipping.", "replica", replica.Name)
		return nil
	}

	hostClient, err := r.Dispatch.ClientFor(replica.Spec.HostID)
	if err != nil {
		logger.Error(err, "Failed to get host client. Ignoring.", "replica", replica.Name)
		return nil
	}

	_, err = hostClient.WorkloadStop(ctx, &wasmv1.WorkloadStopRequest{
		WorkloadType: wasmv1.WorkloadType_WORKLOAD_TYPE_PROVIDER,
		WorkloadId:   replica.Status.WorkloadID,
	})
	if err != nil {
		logger.Error(err, "Failed to stop provider replica. Ignoring.", "replica", replica.Name)
	}

	logger.Info("Removed provider replica from host", "replica", replica.Name)
	return nil
}

// maybeDeleteReplica deletes a replica if it has not been seen for a while.
// this is a post-reconcile hook, so we don't need to check if the replica is still in the cluster.
func (r *ProviderReplicaReconciler) maybeDeleteReplica(ctx context.Context, replica *runtimev1alpha1.ProviderReplica) error {
	logger := log.FromContext(ctx)

	// don't delete valid placements
	if replica.Status.IsAvailable() {
		return nil
	}

	// Replica has been placed, check if within the lastSeen period
	if replica.Status.AllTrue(runtimev1alpha1.ProviderReplicaConditionPlacement) {
		syncCond := replica.Status.GetCondition(runtimev1alpha1.ProviderReplicaConditionSync)
		if syncCond.Status == corev1.ConditionFalse && syncCond.LastTransitionTime.Add(providerReplicaDeleteTimeout).Before(time.Now()) {
			logger.Info("Deleting unresponsive replica", "replica", replica.Name)
			return r.Delete(ctx, replica)
		}
	}

	return nil
}
