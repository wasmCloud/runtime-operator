package runtime

import (
	"context"
	"fmt"

	runtimev1alpha1 "github.com/cosmonic-labs/runtime-operator/api/runtime/v1alpha1"
	"github.com/cosmonic-labs/runtime-operator/internal/crdtools"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ResolveValue resolves a value from a ConfigValueFrom.
// Values can be resolved from a Secret or a ConfigMap.
// If the value cannot be resolved, nil is returned.
func ResolveValue(ctx context.Context, kubeClient client.Client, namespace string, valueFrom *runtimev1alpha1.ConfigValueFrom) ([]byte, error) {
	if valueFrom == nil {
		return nil, fmt.Errorf("valueFrom is nil")
	}

	if valueFrom.Secret != nil {
		return SecretKeyRef(ctx, kubeClient, namespace, valueFrom.Secret)
	}

	if valueFrom.ConfigMap != nil {
		val, err := ConfigMapKeyRef(ctx, kubeClient, namespace, valueFrom.ConfigMap)
		if err != nil {
			return nil, err
		}
		return []byte(val), nil
	}

	return nil, nil
}

func SecretKeyRef(ctx context.Context, kubeClient client.Client, namespace string, ref *corev1.SecretKeySelector) ([]byte, error) {
	if ref == nil {
		return nil, fmt.Errorf("secretKeyRef is nil")
	}

	secret := &corev1.Secret{}
	if err := kubeClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: ref.Name}, secret); err != nil {
		return nil, err
	}

	if value, exists := secret.Data[ref.Key]; exists {
		return value, nil
	}

	return nil, fmt.Errorf("key %s not found in secret %s/%s", ref.Key, namespace, ref.Name)
}

func ConfigMapKeyRef(ctx context.Context, kubeClient client.Client, namespace string, ref *corev1.ConfigMapKeySelector) (string, error) {
	if ref == nil {
		return "", fmt.Errorf("configMapKeyRef is nil")
	}

	configMap := &corev1.ConfigMap{}
	if err := kubeClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: ref.Name}, configMap); err != nil {
		return "", err
	}

	if value, exists := configMap.Data[ref.Key]; exists {
		return value, nil
	}

	return "", fmt.Errorf("key %s not found in configmap %s/%s", ref.Key, namespace, ref.Name)
}

func ResolveConfigFrom(ctx context.Context, kubeClient client.Client, namespace string, configFrom []corev1.ObjectReference) (map[string]string, error) {
	configs := make(map[string]string)
	for _, localRef := range configFrom {
		var config runtimev1alpha1.Config
		if err := kubeClient.Get(ctx, client.ObjectKey{Namespace: crdtools.Coalesce(localRef.Namespace, namespace), Name: localRef.Name}, &config); err != nil {
			return nil, err
		}
		for _, entry := range config.Spec.Config {
			configs[entry.Name] = entry.Value
		}
	}
	return configs, nil
}
