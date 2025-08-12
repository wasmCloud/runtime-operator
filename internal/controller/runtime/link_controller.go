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

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/cosmonic-labs/runtime-operator/api/condition"
	runtimev1alpha1 "github.com/cosmonic-labs/runtime-operator/api/runtime/v1alpha1"
)

const (
	linkReconcileInterval = 1 * time.Minute
	linkPutTimeout        = 5 * time.Second
	linkFinalizer         = "runtime.wasmcloud.dev/link"
)

// LinkReconciler reconciles a Link object
type LinkReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	reconciler condition.AnyConditionedReconciler
}

// +kubebuilder:rbac:groups=runtime.cosmonic.io,resources=links,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=runtime.cosmonic.io,resources=links/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=runtime.cosmonic.io,resources=links/finalizers,verbs=update

func (r *LinkReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return r.reconciler.Reconcile(ctx, req)
}

func (r *LinkReconciler) reconcileTargetReference(ctx context.Context, link *runtimev1alpha1.Link) error {
	return nil
}

func (r *LinkReconciler) reconcileConfigReference(ctx context.Context, link *runtimev1alpha1.Link) error {
	return nil
}

func (r *LinkReconciler) reconcileReady(ctx context.Context, link *runtimev1alpha1.Link) error {
	if link.Status.AllTrue(
		runtimev1alpha1.LinkConditionTargetReference,
		runtimev1alpha1.LinkConditionConfigReference) {
		return nil
	}

	return errors.New("waiting for conditions")
}

func (r *LinkReconciler) finalize(ctx context.Context, link *runtimev1alpha1.Link) error {
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *LinkReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// TODO(lxf): wire up lattice events ( linkdefset linkdefdel, etc )

	reconciler := condition.NewConditionedReconciler(
		r.Client,
		r.Scheme,
		&runtimev1alpha1.Link{},
		linkReconcileInterval)
	reconciler.SetFinalizer(linkFinalizer, r.finalize)

	// check if the target exists
	reconciler.SetCondition(runtimev1alpha1.LinkConditionTargetReference, r.reconcileTargetReference)
	// check if the config is valid
	reconciler.SetCondition(runtimev1alpha1.LinkConditionConfigReference, r.reconcileConfigReference)
	// check if all previous conditions have passed
	reconciler.SetCondition(condition.TypeReady, r.reconcileReady)

	r.reconciler = reconciler
	return ctrl.NewControllerManagedBy(mgr).
		For(&runtimev1alpha1.Link{}).
		Named("runtime-link").
		Complete(r)
}
