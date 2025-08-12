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

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/cosmonic-labs/runtime-operator/api/condition"
	runtimev1alpha1 "github.com/cosmonic-labs/runtime-operator/api/runtime/v1alpha1"
)

// ArtifactReconciler reconciles a Artifact object
type ArtifactReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func (r *ArtifactReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	artifact := &runtimev1alpha1.Artifact{}
	if err := r.Get(ctx, req.NamespacedName, artifact); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	cond := artifact.Status.GetCondition(condition.TypeReady)

	if cond.ObservedGeneration != artifact.GetGeneration() {
		logger.Info("UHU", "artifact", artifact.GetGeneration(), "condition", cond.ObservedGeneration)
		cond = condition.Available()
		cond.ObservedGeneration = artifact.GetGeneration()
		artifact.Status.ArtifactURL = artifact.Spec.Image
		artifact.Status.SetConditions(condition.ReconcileSuccess(), cond)

		if err := r.Status().Update(ctx, artifact); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
// +kubebuilder:rbac:groups=runtime.wasmcloud.dev,resources=artifacts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=runtime.wasmcloud.dev,resources=artifacts/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=runtime.wasmcloud.dev,resources=artifacts/finalizers,verbs=update
func (r *ArtifactReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&runtimev1alpha1.Artifact{}).
		Named("runtime-artifact").
		Complete(r)
}
