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
	"fmt"
	"math/rand/v2"
	"reflect"
	"sort"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/uuid"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/cosmonic-labs/runtime-operator/api/condition"
	runtimev1alpha1 "github.com/cosmonic-labs/runtime-operator/api/runtime/v1alpha1"
	"github.com/cosmonic-labs/runtime-operator/pkg/crdtools"
	"github.com/cosmonic-labs/runtime-operator/pkg/lattice"
)

const (
	providerReconcileInterval = 1 * time.Minute
	providerFinalizer         = "runtime.wasmcloud.dev/provider"
	providerPlacementTimeout  = 10 * time.Second
)

// ProviderReconciler reconciles a Provider object
type ProviderReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	Dispatch   lattice.Dispatch
	reconciler condition.AnyConditionedReconciler
}

// +kubebuilder:rbac:groups=runtime.wasmcloud.dev,resources=providers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=runtime.wasmcloud.dev,resources=providers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=runtime.wasmcloud.dev,resources=providers/finalizers,verbs=update

func (r *ProviderReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return r.reconciler.Reconcile(ctx, req)
}

func (r *ProviderReconciler) reconcileConfigReference(
	ctx context.Context,
	provider *runtimev1alpha1.Provider,
) error {
	for _, configName := range provider.Spec.ConfigFrom {
		var config runtimev1alpha1.Config
		if err := r.Get(ctx, client.ObjectKey{Namespace: provider.Namespace, Name: configName.Name}, &config); err != nil {
			return err
		}
		for _, entry := range config.Spec.Config {
			if entry.ValueFrom != nil {
				if _, err := ResolveValue(ctx, r.Client, provider.Namespace, entry.ValueFrom); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (r *ProviderReconciler) reconcileReplicaCount(
	ctx context.Context,
	provider *runtimev1alpha1.Provider,
) error {
	if !provider.Status.AllTrue(runtimev1alpha1.ProviderConditionConfigReference, runtimev1alpha1.ProviderConditionLinkReference) {
		return condition.ErrNoop()
	}

	if len(provider.Status.Links) == 0 {
		// we need to wait for links to be populated
		return condition.ErrNoop()
	}

	replicaList := &runtimev1alpha1.ProviderReplicaList{}
	if err := r.List(
		ctx,
		replicaList,
		client.InNamespace(provider.GetNamespace()),
		client.MatchingFields{
			lattice.ProviderReplicaByProviderIndex: lattice.NamespacedStringForObject(provider),
		}); err != nil {
		return err
	}

	for _, replica := range replicaList.Items {
		if !replica.DeletionTimestamp.IsZero() {
			// mid deletion: need more data to determine if we should scale up/down
			return condition.ErrNoop()
		}
	}

	currentGeneration := provider.GetGeneration()
	// if we not deleting any replicas, and detect replicas out of sync, delete one
	for _, replica := range replicaList.Items {
		if replica.GetParentGeneration() != currentGeneration || !reflect.DeepEqual(replica.Spec.Links, provider.Status.Links) {
			if err := r.Delete(ctx, &replica); err != nil {
				return err
			}
			// we exit here because we only want to delete one replica at a time
			return condition.ErrStatusUnknown(fmt.Errorf("deploy in progress"))
		}
	}

	if len(replicaList.Items) != provider.Spec.Replicas {
		for i := len(replicaList.Items); i < provider.Spec.Replicas; i++ {
			if err := r.createReplica(ctx, provider); err != nil {
				return err
			}
		}

		return condition.ErrStatusUnknown(fmt.Errorf("deploy in progress"))
	}

	return nil
}

func (r *ProviderReconciler) reconcileLinks(
	ctx context.Context,
	provider *runtimev1alpha1.Provider,
) error {
	// TODO(lxf): we are getting all links at once, this should be an indexer.

	var linkList runtimev1alpha1.LinkList
	if err := r.List(ctx, &linkList); err != nil {
		return err
	}

	replicaLinks := make([]corev1.ObjectReference, 0)
	for _, link := range linkList.Items {
		// Provider is the source of the link.
		if link.Spec.Source.Provider != nil {
			source := link.Spec.Source
			sourceNamespace := crdtools.Coalesce(source.Provider.Namespace, link.Namespace)
			if sourceNamespace == provider.Namespace && source.Provider.Name == provider.Name {
				if _, err := ResolveConfigFrom(ctx, r.Client, link.Namespace, link.Spec.Source.ConfigFrom); err != nil {
					// link config is not valid, we need to wait for it to be valid
					continue
				}
				replicaLinks = append(replicaLinks, corev1.ObjectReference{
					Namespace: link.Namespace,
					Name:      link.Name,
				})
			}
		} else if link.Spec.Target.Provider != nil {
			// Provider is the target of the link (component → provider exports).
			target := link.Spec.Target
			targetNamespace := crdtools.Coalesce(target.Provider.Namespace, link.Namespace)
			if targetNamespace == provider.Namespace && target.Provider.Name == provider.Name {
				if _, err := ResolveConfigFrom(ctx, r.Client, link.Namespace, link.Spec.Target.ConfigFrom); err != nil {
					// link config is not valid, we need to wait for it to be valid
					continue
				}
				replicaLinks = append(replicaLinks, corev1.ObjectReference{
					Namespace: link.Namespace,
					Name:      link.Name,
				})
			}
		}
	}

	sort.Slice(replicaLinks, func(i, j int) bool {
		return replicaLinks[i].Name < replicaLinks[j].Name
	})

	provider.Status.Links = replicaLinks

	return nil
}

func (r *ProviderReconciler) reconcileSync(
	ctx context.Context,
	provider *runtimev1alpha1.Provider,
) error {
	// Check if we have replicas with the current generation ready
	replicaList := &runtimev1alpha1.ProviderReplicaList{}
	if err := r.List(
		ctx,
		replicaList,
		client.InNamespace(provider.GetNamespace()),
		client.MatchingFields{
			lattice.ProviderReplicaByProviderIndex: lattice.NamespacedStringForObject(provider),
		}); err != nil {
		return err
	}

	unavailableReplicas := 0
	availableReplicas := 0
	for _, replica := range replicaList.Items {
		if !replica.DeletionTimestamp.IsZero() {
			unavailableReplicas++
			continue
		}
		if replica.GetParentGeneration() != provider.GetGeneration() {
			unavailableReplicas++
			continue
		}
		if !replica.Status.IsAvailable() {
			unavailableReplicas++
			continue
		}
		availableReplicas++
	}

	provider.Status.Replicas = availableReplicas
	provider.Status.UnavailableReplicas = unavailableReplicas

	if availableReplicas != provider.Spec.Replicas {
		return errors.New("not all replicas are available")
	}

	condition.GetReconcilerContext(ctx).ForceUpdate = true

	return nil
}

func (r *ProviderReconciler) reconcileReady(ctx context.Context, provider *runtimev1alpha1.Provider) error {
	if provider.Status.AllTrue(
		runtimev1alpha1.ProviderConditionReplicaCount,
		runtimev1alpha1.ProviderConditionSync) {
		return nil
	}

	return errors.New("not all conditions have passed")
}

func (r *ProviderReconciler) finalize(ctx context.Context, provider *runtimev1alpha1.Provider) error {
	logger := log.FromContext(ctx)
	logger.Info("Removing provider from lattice", "provider", provider.Name)

	return nil
}

func (r *ProviderReconciler) findFreeHost(
	ctx context.Context,
	provider *runtimev1alpha1.Provider,
) (string, error) {
	hostList := runtimev1alpha1.HostList{}
	if err := r.List(ctx, &hostList,
		client.MatchingLabels(provider.Spec.HostSelector.MatchLabels),
		client.MatchingFields{
			lattice.HostReadyIndex: string(condition.ConditionTrue),
		}); err != nil {
		return "", err
	}

	// Shuffle the host list
	rand.Shuffle(len(hostList.Items), func(i, j int) {
		hostList.Items[i], hostList.Items[j] = hostList.Items[j], hostList.Items[i]
	})

	for _, host := range hostList.Items {
		if host.Status.AllTrue(condition.TypeReady) {
			return host.Spec.HostID, nil
		}
	}
	return "", fmt.Errorf("no available host found matching labels: %v", provider.Spec.HostSelector.MatchLabels)
}

func (r *ProviderReconciler) createReplica(
	ctx context.Context,
	provider *runtimev1alpha1.Provider,
) error {
	logger := log.FromContext(ctx)
	freeHost, err := r.findFreeHost(ctx, provider)
	if err != nil {
		logger.Info("Failed to find free host", "error", err)
		return err
	}

	replicaName := string(uuid.NewUUID())
	providerReplica := &runtimev1alpha1.ProviderReplica{
		ObjectMeta: metav1.ObjectMeta{
			Name:      replicaName,
			Namespace: provider.GetNamespace(),
			Annotations: map[string]string{
				runtimev1alpha1.ProviderReplicaGeneration: strconv.FormatInt(provider.GetGeneration(), 10),
			},
		},
		Spec: runtimev1alpha1.ProviderReplicaSpec{
			HostID:      freeHost,
			ProviderRef: corev1.ObjectReference{Namespace: provider.GetNamespace(), Name: provider.GetName()},
			Links:       provider.Status.Links,
		},
	}
	if err := controllerutil.SetControllerReference(provider, providerReplica, r.Scheme); err != nil {
		logger.Error(err, "Failed to set controller reference")
		return err
	}

	if err := r.Create(ctx, providerReplica); err != nil {
		logger.Error(err, "Failed to create provider replica")
		return err
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ProviderReconciler) SetupWithManager(mgr ctrl.Manager) error {
	reconciler := condition.NewConditionedReconciler(
		r.Client,
		r.Scheme,
		&runtimev1alpha1.Provider{},
		providerReconcileInterval)
	reconciler.SetFinalizer(providerFinalizer, r.finalize)

	// check if config references are correct.we don't proceed if they are not correct.
	reconciler.SetCondition(runtimev1alpha1.ProviderConditionConfigReference, r.reconcileConfigReference)
	// populate Status.Links. This will copied over to provider replicas.
	reconciler.SetCondition(runtimev1alpha1.ProviderConditionLinkReference, r.reconcileLinks)

	// check if the replica count is correct, adjusting if necessary
	reconciler.SetCondition(runtimev1alpha1.ProviderConditionReplicaCount, r.reconcileReplicaCount)
	// check if the replicas are in sync with the provider spec
	reconciler.SetCondition(runtimev1alpha1.ProviderConditionSync, r.reconcileSync)
	// check if all previous conditions have passed
	reconciler.SetCondition(condition.TypeReady, r.reconcileReady)

	r.reconciler = reconciler

	return ctrl.NewControllerManagedBy(mgr).
		For(&runtimev1alpha1.Provider{}).
		Owns(&runtimev1alpha1.ProviderReplica{}).
		Watches(&runtimev1alpha1.Link{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
				link := obj.(*runtimev1alpha1.Link)
				if link.Spec.Source.Provider != nil {
					if _, err := ResolveConfigFrom(ctx, r.Client, link.Namespace, link.Spec.Source.ConfigFrom); err != nil {
						// link config is not valid, we need to wait for it to be valid
						return nil
					}
					return []reconcile.Request{
						{NamespacedName: types.NamespacedName{Namespace: link.Spec.Source.Provider.Namespace, Name: link.Spec.Source.Provider.Name}},
					}
				}
				if link.Spec.Target.Provider != nil {
					if _, err := ResolveConfigFrom(ctx, r.Client, link.Namespace, link.Spec.Target.ConfigFrom); err != nil {
						// link config is not valid, we need to wait for it to be valid
						return nil
					}
					return []reconcile.Request{
						{NamespacedName: types.NamespacedName{Namespace: link.Spec.Target.Provider.Namespace, Name: link.Spec.Target.Provider.Name}},
					}
				}
				return nil
			}),
		).
		Named("runtime-provider").
		Complete(r)
}
