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
	configReconcileInterval = 5 * time.Minute
	configStoreTimeout      = 5 * time.Second
	configFinalizer         = "runtime.wasmcloud.dev/config"
)

// ConfigReconciler reconciles a Config object
type ConfigReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	reconciler condition.AnyConditionedReconciler
}

// +kubebuilder:rbac:groups=runtime.cosmonic.io,resources=configs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=runtime.cosmonic.io,resources=configs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=runtime.cosmonic.io,resources=configs/finalizers,verbs=update

// +kubebuilder:rbac:groups=core,resources=secrets;configmaps;services,verbs=get;list;watch
func (r *ConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return r.reconciler.Reconcile(ctx, req)
}

func (r *ConfigReconciler) reconcileValueFrom(ctx context.Context, config *runtimev1alpha1.Config) error {
	for _, entry := range config.Spec.Config {
		if entry.ValueFrom != nil {
			var err error
			_, err = ResolveValue(ctx, r.Client, config.Namespace, entry.ValueFrom)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *ConfigReconciler) reconcileReady(ctx context.Context, config *runtimev1alpha1.Config) error {
	if config.Status.AllTrue(
		runtimev1alpha1.ConfigConditionValueReferences) {
		return nil
	}

	return errors.New("not all conditions have passed")
}

func (r *ConfigReconciler) finalize(ctx context.Context, config *runtimev1alpha1.Config) error {
	// nothing to finalize as state is only on api-server.
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	reconciler := condition.NewConditionedReconciler(r.Client, r.Scheme, &runtimev1alpha1.Config{}, configReconcileInterval)
	reconciler.SetFinalizer(configFinalizer, r.finalize)

	reconciler.SetCondition(runtimev1alpha1.ConfigConditionValueReferences, r.reconcileValueFrom)
	reconciler.SetCondition(condition.TypeReady, r.reconcileReady)

	r.reconciler = reconciler
	return ctrl.NewControllerManagedBy(mgr).
		For(&runtimev1alpha1.Config{}).
		Named("runtime-config").
		Complete(r)
}
