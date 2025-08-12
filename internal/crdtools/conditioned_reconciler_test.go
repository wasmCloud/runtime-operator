package crdtools

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cosmonic-labs/runtime-operator/api/condition"
	runtimev1alpha1 "github.com/cosmonic-labs/runtime-operator/api/runtime/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestHandleFinalizer(t *testing.T) {
	scheme := runtime.NewScheme()
	utilruntime.Must(runtimev1alpha1.AddToScheme(scheme))
	finalizerName := "test-finalizer"

	tt := []struct {
		name string
		obj  runtimev1alpha1.Config

		emitClientError   bool
		emitCallbackError bool

		hasChanges   bool
		hasFinalizer bool
		hasError     bool
	}{
		{
			name: "acquisition",
			obj: runtimev1alpha1.Config{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "infra",
					Name:      "test-cluster",
				},
			},
			hasChanges:   true,
			hasFinalizer: true,
		},
		{
			name: "acquisition failure",
			obj: runtimev1alpha1.Config{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "infra",
					Name:      "test-cluster",
				},
			},
			emitClientError: true,
			hasError:        true,
		},
		{
			name: "reconciliation",
			obj: runtimev1alpha1.Config{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:  "infra",
					Name:       "test-cluster",
					Finalizers: []string{finalizerName},
				},
			},
			hasChanges:   false,
			hasFinalizer: true,
		},
		{
			name: "reconciliation failure",
			obj: runtimev1alpha1.Config{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:  "infra",
					Name:       "test-cluster",
					Finalizers: []string{finalizerName},
				},
			},
			emitClientError: true,
			hasFinalizer:    true,
		},
		{
			name: "deletion",
			obj: runtimev1alpha1.Config{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:  "infra",
					Name:       "test-cluster",
					Finalizers: []string{finalizerName},
					DeletionTimestamp: &metav1.Time{
						Time: time.Now(),
					},
				},
			},
			hasChanges:   true,
			hasFinalizer: false,
		},
		{
			name: "deletion failure",
			obj: runtimev1alpha1.Config{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:  "infra",
					Name:       "test-cluster",
					Finalizers: []string{finalizerName},
					DeletionTimestamp: &metav1.Time{
						Time: time.Now(),
					},
				},
			},
			emitCallbackError: true,
			hasError:          true,
			hasFinalizer:      true,
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			builder := fake.NewClientBuilder()

			if tc.emitClientError {
				errFunc := interceptor.Funcs{
					Update: func(ctx context.Context, client client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
						return errors.New("boom")
					},
				}
				builder = builder.WithInterceptorFuncs(errFunc)
			}

			kubeClient := builder.
				WithScheme(scheme).
				WithObjects(&tc.obj).Build()

			callback := func(ctx context.Context, obj *runtimev1alpha1.Config) error {
				if tc.emitCallbackError {
					return errors.New("boom")
				}
				return nil
			}

			updated, err := HandleFinalizer(context.Background(), kubeClient, &tc.obj, finalizerName, callback)
			if tc.hasError {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
			} else {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
			}

			if want, got := tc.hasChanges, updated; want != got {
				t.Fatalf("object update: want %v, got %v", want, got)
			}

			freshObj := &runtimev1alpha1.Config{}
			if err := kubeClient.Get(context.Background(), client.ObjectKeyFromObject(&tc.obj), freshObj); err != nil {
				if client.IgnoreNotFound(err) != nil {
					t.Fatalf("failed to get object: %v", err)
				}
				// object deleted, nothing to check
				return
			}

			hasFinalizer := false
			for _, finalizer := range freshObj.GetFinalizers() {
				if finalizer == finalizerName {
					hasFinalizer = true
					break
				}
			}

			if want, got := tc.hasFinalizer, hasFinalizer; want != got {
				t.Fatalf("finalizer presence: want %v, got %v", want, got)
			}
		})
	}
}

func TestConditionedReconciler(t *testing.T) {
	scheme := runtime.NewScheme()
	utilruntime.Must(runtimev1alpha1.AddToScheme(scheme))
	finalizerName := "test-finalizer"

	okCallback := func(ctx context.Context, obj *runtimev1alpha1.Config) error {
		return nil
	}

	errCallback := func(ctx context.Context, obj *runtimev1alpha1.Config) error {
		return errors.New("boom")
	}

	unknownCallback := func(ctx context.Context, obj *runtimev1alpha1.Config) error {
		return ErrStatusUnknown(errors.New("boom"))
	}

	tt := []struct {
		name string
		obj  runtimev1alpha1.Config

		emitClientError bool

		condType  condition.ConditionType
		condFunc  func(context.Context, *runtimev1alpha1.Config) error
		checkFunc func(*testing.T, *condition.ConditionedStatus)

		hasChanges bool
		hasError   bool

		emitFinalizerError bool
		hasNoFinalizer     bool
	}{
		// finalizer handling
		{
			name: "finalizer setup",
			obj: runtimev1alpha1.Config{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "infra",
					Name:      "test-cluster",
				},
			},
		},
		{
			name: "finalizer setup error",
			obj: runtimev1alpha1.Config{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "infra",
					Name:      "test-cluster",
				},
			},
			emitClientError: true,
			hasError:        true,
		},
		{
			name: "finalizer removal",
			obj: runtimev1alpha1.Config{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:  "infra",
					Name:       "test-cluster",
					Finalizers: []string{finalizerName},
					DeletionTimestamp: &metav1.Time{
						Time: time.Now(),
					},
				},
			},
		},
		{
			name: "finalizer removal error",
			obj: runtimev1alpha1.Config{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:  "infra",
					Name:       "test-cluster",
					Finalizers: []string{finalizerName},
					DeletionTimestamp: &metav1.Time{
						Time: time.Now(),
					},
				},
			},
			emitFinalizerError: true,
			hasError:           true,
		},

		// condition handling
		{
			name: "condition true",
			obj: runtimev1alpha1.Config{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:  "infra",
					Name:       "test-cluster",
					Finalizers: []string{finalizerName},
				},
			},
			condType: "TestReconcile",
			condFunc: okCallback,
			checkFunc: func(t *testing.T, cs *condition.ConditionedStatus) {
				cond := cs.GetCondition("TestReconcile")
				if cond.Status != condition.ConditionTrue {
					t.Fatalf("condition status: want %v, got %v", condition.ConditionTrue, cond.Status)
				}
			},
		},
		{
			name: "condition false",
			obj: runtimev1alpha1.Config{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:  "infra",
					Name:       "test-cluster",
					Finalizers: []string{finalizerName},
				},
			},
			condType: "TestReconcile",
			condFunc: errCallback,
			checkFunc: func(t *testing.T, cs *condition.ConditionedStatus) {
				cond := cs.GetCondition("TestReconcile")
				if cond.Status != condition.ConditionFalse {
					t.Fatalf("condition status: want %v, got %v", condition.ConditionFalse, cond.Status)
				}
			},
		},
		{
			name: "condition unknown",
			obj: runtimev1alpha1.Config{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:  "infra",
					Name:       "test-cluster",
					Finalizers: []string{finalizerName},
				},
			},
			condType: "TestReconcile",
			condFunc: unknownCallback,
			checkFunc: func(t *testing.T, cs *condition.ConditionedStatus) {
				cond := cs.GetCondition("TestReconcile")
				if cond.Status != condition.ConditionUnknown {
					t.Fatalf("condition status: want %v, got %v", condition.ConditionUnknown, cond.Status)
				}
				if cond.LastTransitionTime.IsZero() {
					t.Fatalf("last transition time: want non-zero, got zero")
				}
			},
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			interceptor := interceptor.Funcs{
				SubResourceUpdate: func(
					ctx context.Context,
					client client.Client,
					_ string,
					obj client.Object,
					_ ...client.SubResourceUpdateOption) error {
					//	fmt.Printf("SubResourceUpdate: %+v\n", obj)
					return client.Update(ctx, obj)
				},
			}

			if tc.emitClientError {
				interceptor.Update = func(
					ctx context.Context,
					client client.WithWatch,
					obj client.Object,
					opts ...client.UpdateOption) error {
					return errors.New("boom")
				}
			}

			kubeClient := fake.NewClientBuilder().
				WithInterceptorFuncs(interceptor).
				WithScheme(scheme).
				WithObjects(&tc.obj).Build()

			finalizerFunc := func(ctx context.Context, obj *runtimev1alpha1.Config) error {
				if tc.emitFinalizerError {
					return errors.New("boom")
				}
				return nil
			}

			r := NewConditionedReconciler(kubeClient, scheme, &tc.obj, time.Second)
			r.SetFinalizer(finalizerName, finalizerFunc)

			if tc.condType != "" && tc.condFunc != nil {
				r.SetCondition(tc.condType, tc.condFunc)
			}

			req := reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(&tc.obj),
			}
			_, err := r.Reconcile(context.Background(), req)
			if tc.hasError {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
			} else {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
			}

			freshObj := &runtimev1alpha1.Config{}
			if err := kubeClient.Get(context.Background(), client.ObjectKeyFromObject(&tc.obj), freshObj); err != nil {
				if client.IgnoreNotFound(err) != nil {
					t.Fatalf("failed to get object: %v", err)
				}
				// object deleted, nothing to check
				return
			}

			if tc.checkFunc != nil {
				tc.checkFunc(t, freshObj.ConditionedStatus())
			}

			hasFinalizer := false
			for _, finalizer := range freshObj.GetFinalizers() {
				if finalizer == finalizerName {
					hasFinalizer = true
					break
				}
			}

			if tc.hasNoFinalizer && hasFinalizer {
				t.Fatalf("finalizer presence: want false, got true")
			}

		})
	}
}
