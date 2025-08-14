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
	"crypto/sha1"
	"errors"
	"fmt"
	"math/rand/v2"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/cosmonic-labs/runtime-operator/api/condition"
	runtimev1alpha1 "github.com/cosmonic-labs/runtime-operator/api/runtime/v1alpha1"
	"github.com/cosmonic-labs/runtime-operator/pkg/lattice"
)

const (
	componentReconcileInterval = 1 * time.Minute
	componentFinalizer         = "runtime.wasmcloud.dev/component"
	componentPlacementTimeout  = 5 * time.Second
)

// ComponentReconciler reconciles a Component object
type ComponentReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	Dispatch   lattice.Dispatch
	reconciler condition.AnyConditionedReconciler
}

// +kubebuilder:rbac:groups=runtime.wasmcloud.dev,resources=components,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=runtime.wasmcloud.dev,resources=components/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=runtime.wasmcloud.dev,resources=components/finalizers,verbs=update
// +kubebuilder:rbac:groups=runtime.wasmcloud.dev,resources=componentreplicas,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=runtime.wasmcloud.dev,resources=componentreplicas/status,verbs=get;update;patch

func (r *ComponentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return r.reconciler.Reconcile(ctx, req)
}

func (r *ComponentReconciler) reconcileArtifact(
	ctx context.Context,
	component *runtimev1alpha1.Component,
) error {
	artifact := &runtimev1alpha1.Artifact{
		ObjectMeta: metav1.ObjectMeta{
			Name:      component.GetName(),
			Namespace: component.GetNamespace(),
		},
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, artifact, func() error {
		artifact.Spec.Image = component.Spec.Image
		artifact.Spec.ImagePullSecret = component.Spec.ImagePullSecret

		return controllerutil.SetControllerReference(component, artifact, r.Scheme)
	})
	if err != nil {
		return err
	}

	artifactReady := artifact.Status.GetCondition(condition.TypeReady)

	if artifactReady.ObservedGeneration != artifact.GetGeneration() {
		return condition.ErrStatusUnknown(fmt.Errorf("pipeline generation mismatch"))
	}

	if !artifact.Status.IsAvailable() {
		return condition.ErrStatusUnknown(fmt.Errorf("pipeline not ready"))
	}

	return nil
}

func (r *ComponentReconciler) reconcileConfigReference(
	ctx context.Context,
	component *runtimev1alpha1.Component,
) error {
	for _, configName := range component.Spec.ConfigFrom {
		var config runtimev1alpha1.Config
		if err := r.Get(ctx, client.ObjectKey{Namespace: component.Namespace, Name: configName.Name}, &config); err != nil {
			return err
		}
		for _, entry := range config.Spec.Config {
			if entry.ValueFrom != nil {
				if _, err := ResolveValue(ctx, r.Client, component.Namespace, entry.ValueFrom); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (r *ComponentReconciler) reconcileReplicaCount(
	ctx context.Context,
	component *runtimev1alpha1.Component,
) error {
	if !component.Status.AllTrue(runtimev1alpha1.ComponentConditionArtifact, runtimev1alpha1.ComponentConditionConfigReference) {
		return condition.ErrNoop()
	}

	replicaList := &runtimev1alpha1.ComponentReplicaList{}
	if err := r.List(
		ctx,
		replicaList,
		client.InNamespace(component.GetNamespace()),
		client.MatchingFields{
			lattice.ComponentReplicaByComponentIndex: lattice.NamespacedStringForObject(component),
		}); err != nil {
		return err
	}

	for _, replica := range replicaList.Items {
		if !replica.DeletionTimestamp.IsZero() {
			// mid-deletion wait for it to finish
			return condition.ErrNoop()
		}
	}

	currentGeneration := component.GetGeneration()
	for _, replica := range replicaList.Items {
		// if the replica is out of sync, delete one
		if replica.GetParentGeneration() != currentGeneration {
			if err := r.Delete(ctx, &replica); err != nil {
				return err
			}
			return condition.ErrStatusUnknown(fmt.Errorf("deploy in progress"))
		}
	}

	// NOTE(lxf): Get the artifact then use the resulting image to create the component definition.
	// This is the component image post-processing that hosts will receive.
	var artifact runtimev1alpha1.Artifact
	if err := r.Get(ctx, client.ObjectKey{
		Name:      component.GetName(),
		Namespace: component.GetNamespace(),
	}, &artifact); err != nil {
		return err
	}

	componentDefinition := component.Spec.ComponentDefinition
	componentDefinition.Image = artifact.Status.ArtifactURL

	if len(replicaList.Items) != component.Spec.Replicas {
		// we are more aggressive with scale ups where spec generation doesn't matter
		for i := len(replicaList.Items); i < component.Spec.Replicas; i++ {
			if err := r.createReplica(ctx, component, componentDefinition); err != nil {
				return err
			}
		}

		return condition.ErrStatusUnknown(fmt.Errorf("deploy in progress"))
	}

	return nil
}

func (r *ComponentReconciler) reconcileExportLink(
	ctx context.Context,
	component *runtimev1alpha1.Component,
) error {
	for _, linkSpec := range component.Spec.Exports {
		sha := sha1.New()
		sha.Write([]byte(linkSpec.Name))
		sha.Write([]byte(linkSpec.WIT.Namespace))
		sha.Write([]byte(linkSpec.WIT.Package))
		for _, iface := range linkSpec.WIT.Interfaces {
			sha.Write([]byte(iface))
		}
		linkName := fmt.Sprintf("export-%s-%x", component.GetName(), sha.Sum(nil))
		link := &runtimev1alpha1.Link{
			ObjectMeta: metav1.ObjectMeta{
				Name:      linkName,
				Namespace: component.GetNamespace(),
			},
		}

		source := linkSpec.Target.DeepCopy()
		for i := range source.ConfigFrom {
			source.ConfigFrom[i].Namespace = component.GetNamespace()
		}

		_, err := controllerutil.CreateOrUpdate(ctx, r.Client, link, func() error {
			link.Spec = runtimev1alpha1.LinkSpec{
				Name:   linkSpec.Name,
				WIT:    linkSpec.WIT,
				Source: *source,
				Target: runtimev1alpha1.LinkTarget{
					Component: &corev1.ObjectReference{
						Namespace: component.GetNamespace(),
						Name:      component.GetName(),
					},
				},
			}
			return controllerutil.SetControllerReference(component, link, r.Scheme)
		})
		if err != nil {
			return err
		}
	}

	return nil
}

func (r *ComponentReconciler) reconcileImportLink(
	ctx context.Context,
	component *runtimev1alpha1.Component,
) error {
	for _, linkSpec := range component.Spec.Imports {
		sha := sha1.New()
		sha.Write([]byte(linkSpec.Name))
		sha.Write([]byte(linkSpec.WIT.Namespace))
		sha.Write([]byte(linkSpec.WIT.Package))
		for _, iface := range linkSpec.WIT.Interfaces {
			sha.Write([]byte(iface))
		}
		linkName := fmt.Sprintf("import-%s-%x", component.GetName(), sha.Sum(nil))
		link := &runtimev1alpha1.Link{
			ObjectMeta: metav1.ObjectMeta{
				Name:      linkName,
				Namespace: component.GetNamespace(),
			},
		}

		target := linkSpec.Target.DeepCopy()
		for i := range target.ConfigFrom {
			target.ConfigFrom[i].Namespace = component.GetNamespace()
		}

		_, err := controllerutil.CreateOrUpdate(ctx, r.Client, link, func() error {
			link.Spec = runtimev1alpha1.LinkSpec{
				Name: linkSpec.Name,
				WIT:  linkSpec.WIT,
				Source: runtimev1alpha1.LinkTarget{
					Component: &corev1.ObjectReference{
						Namespace: component.GetNamespace(),
						Name:      component.GetName(),
					},
				},
				Target: *target,
			}
			return controllerutil.SetControllerReference(component, link, r.Scheme)
		})
		if err != nil {
			return err
		}
	}

	return nil
}

func (r *ComponentReconciler) reconcileSync(
	ctx context.Context,
	component *runtimev1alpha1.Component,
) error {
	// Check if we have replicas with the current generation ready
	replicaList := &runtimev1alpha1.ComponentReplicaList{}
	if err := r.List(
		ctx,
		replicaList,
		client.InNamespace(component.GetNamespace()),
		client.MatchingFields{
			lattice.ComponentReplicaByComponentIndex: lattice.NamespacedStringForObject(component),
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
		if replica.GetParentGeneration() != component.GetGeneration() {
			unavailableReplicas++
			continue
		}
		if !replica.Status.IsAvailable() {
			unavailableReplicas++
			continue
		}
		availableReplicas++
	}

	component.Status.Replicas = availableReplicas
	component.Status.UnavailableReplicas = unavailableReplicas

	if availableReplicas != component.Spec.Replicas {
		return errors.New("not all replicas are available")
	}

	condition.GetReconcilerContext(ctx).ForceUpdate = true

	return nil
}

func (r *ComponentReconciler) reconcileReady(ctx context.Context, component *runtimev1alpha1.Component) error {
	if !component.Status.AllTrue(
		runtimev1alpha1.ComponentConditionConfigReference,
		runtimev1alpha1.ComponentConditionExportLink,
		runtimev1alpha1.ComponentConditionImportLink,
		runtimev1alpha1.ComponentConditionSync) {
		return errors.New("not all conditions have passed")
	}

	return nil
}

func (r *ComponentReconciler) finalize(ctx context.Context, component *runtimev1alpha1.Component) error {
	logger := log.FromContext(ctx)
	logger.Info("Removing component from lattice", "component", component.Name)

	return nil
}

func (r *ComponentReconciler) findFreeHost(
	ctx context.Context,
	component *runtimev1alpha1.Component,
) (string, error) {
	hostList := runtimev1alpha1.HostList{}

	if err := r.List(ctx, &hostList,
		client.MatchingLabels(component.Spec.HostSelector.MatchLabels),
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
	return "", errors.New("no free host found")
}

func (r *ComponentReconciler) createReplica(
	ctx context.Context,
	component *runtimev1alpha1.Component,
	componentDefinition runtimev1alpha1.ComponentDefinition,
) error {
	logger := log.FromContext(ctx)
	freeHost, err := r.findFreeHost(ctx, component)
	if err != nil {
		logger.Error(err, "Failed to find free host", "component", component.GetName())
		return err
	}

	replicaName := fmt.Sprintf("%s-%s", component.GetName(), generateRandomString(8))
	componentReplica := &runtimev1alpha1.ComponentReplica{
		ObjectMeta: metav1.ObjectMeta{
			Name:      replicaName,
			Namespace: component.GetNamespace(),
			Annotations: map[string]string{
				runtimev1alpha1.ComponentReplicaGeneration: strconv.FormatInt(component.GetGeneration(), 10),
			},
		},
		Spec: runtimev1alpha1.ComponentReplicaSpec{
			HostID:              freeHost,
			ComponentName:       component.GetName(),
			ComponentDefinition: componentDefinition,
		},
	}
	if err := controllerutil.SetControllerReference(component, componentReplica, r.Scheme); err != nil {
		logger.Error(err, "Failed to set controller reference", "component", component.GetName(), "replica", replicaName)
		return err
	}

	err = r.Create(ctx, componentReplica)
	if err != nil {
		logger.Error(err, "Failed to create component replica", "component", component.GetName(), "replica", replicaName)
		return err
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ComponentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	reconciler := condition.NewConditionedReconciler(
		r.Client,
		r.Scheme,
		&runtimev1alpha1.Component{},
		componentReconcileInterval)
	reconciler.SetFinalizer(componentFinalizer, r.finalize)

	// create artifact if it doesn't exist
	reconciler.SetCondition(runtimev1alpha1.ComponentConditionArtifact, r.reconcileArtifact)

	// check if config references are correct.we don't proceed if they are not correct.
	reconciler.SetCondition(runtimev1alpha1.ComponentConditionConfigReference, r.reconcileConfigReference)

	// check if we can deploy
	reconciler.SetCondition(runtimev1alpha1.ComponentConditionSync, r.reconcileSync)

	// see if we need to adjust the replica count
	reconciler.SetCondition(runtimev1alpha1.ComponentConditionReplicaCount, r.reconcileReplicaCount)

	// check if import links are correct, adjusting if necessary
	reconciler.SetCondition(runtimev1alpha1.ComponentConditionImportLink, r.reconcileImportLink)
	// check if export links are correct, adjusting if necessary
	reconciler.SetCondition(runtimev1alpha1.ComponentConditionExportLink, r.reconcileExportLink)
	// check if all previous conditions have passed
	reconciler.SetCondition(condition.TypeReady, r.reconcileReady)

	r.reconciler = reconciler
	return ctrl.NewControllerManagedBy(mgr).
		For(&runtimev1alpha1.Component{}).
		Owns(&runtimev1alpha1.ComponentReplica{}).
		Owns(&runtimev1alpha1.Artifact{}).
		Named("runtime-component").
		Complete(r)
}

const charset = "abcdefghijklmnopqrstuvwxyz"

func generateRandomString(length int) string {
	seededRand := rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), uint64(time.Now().UnixNano())))
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[seededRand.IntN(len(charset))]
	}
	return string(b)
}
