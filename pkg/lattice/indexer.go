package lattice

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/cosmonic-labs/runtime-operator/api/condition"
	runtimev1alpha1 "github.com/cosmonic-labs/runtime-operator/api/runtime/v1alpha1"
)

const LatticeNameIndex = ".latticeName"

const ComponentReplicaByComponentIndex = ".componentReplicaByComponent"
const ComponentReplicaByHostIndex = ".componentReplicaByHost"

const ProviderReplicaByProviderIndex = ".providerReplicaByProvider"
const ProviderReplicaByHostIndex = ".providerReplicaByHost"

const HostReadyIndex = ".hostReady"

type Indexer struct {
	Client       client.Client
	FieldIndexer client.FieldIndexer
}

// Implements manager.Runnable from controller runtime
func (s *Indexer) Start(ctx context.Context) error {
	if err := s.startIndexers(ctx); err != nil {
		return err
	}

	return nil
}

func (s *Indexer) startIndexers(ctx context.Context) error {
	// ***********
	// Hosts
	// ***********
	if err := s.FieldIndexer.IndexField(ctx, &runtimev1alpha1.Host{}, HostReadyIndex,
		func(rawObj client.Object) []string {
			obj := rawObj.(*runtimev1alpha1.Host)
			return []string{string(obj.Status.GetCondition(condition.TypeReady).Status)}
		}); err != nil {
		return err
	}

	// ***********
	// Components
	// ***********

	// all ComponentReplicas for a component, any namespace
	if err := s.FieldIndexer.IndexField(ctx, &runtimev1alpha1.ComponentReplica{}, ComponentReplicaByComponentIndex,
		func(rawObj client.Object) []string {
			obj := rawObj.(*runtimev1alpha1.ComponentReplica)
			return []string{NamespacedStringForReference(corev1.ObjectReference{
				Namespace: obj.Namespace,
				Name:      obj.Spec.ComponentName,
			})}
		}); err != nil {
		return err
	}

	// all ComponentReplicas for a host, any namespace
	if err := s.FieldIndexer.IndexField(ctx, &runtimev1alpha1.ComponentReplica{}, ComponentReplicaByHostIndex,
		func(rawObj client.Object) []string {
			obj := rawObj.(*runtimev1alpha1.ComponentReplica)
			return []string{obj.Spec.HostID}
		}); err != nil {
		return err
	}

	// ***********
	// Providers
	// ***********

	// all ProviderReplicas for a provider, any namespace
	if err := s.FieldIndexer.IndexField(ctx, &runtimev1alpha1.ProviderReplica{}, ProviderReplicaByProviderIndex,
		func(rawObj client.Object) []string {
			obj := rawObj.(*runtimev1alpha1.ProviderReplica)
			return []string{NamespacedStringForReference(obj.Spec.ProviderRef)}
		}); err != nil {
		return err
	}

	// all ProviderReplicas for a host, any namespace
	if err := s.FieldIndexer.IndexField(ctx, &runtimev1alpha1.ProviderReplica{}, ProviderReplicaByHostIndex,
		func(rawObj client.Object) []string {
			obj := rawObj.(*runtimev1alpha1.ProviderReplica)
			return []string{obj.Spec.HostID}
		}); err != nil {
		return err
	}

	return nil
}

func NamespacedStringForObject(ref metav1.Object) string {
	return fmt.Sprintf("%s/%s", ref.GetNamespace(), ref.GetName())
}

func NamespacedStringForReference(ref corev1.ObjectReference) string {
	return fmt.Sprintf("%s/%s", ref.Namespace, ref.Name)
}
