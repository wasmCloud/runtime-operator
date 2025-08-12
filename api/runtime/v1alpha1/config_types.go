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

package v1alpha1

import (
	"github.com/cosmonic-labs/runtime-operator/api/condition"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	ConfigConditionValueReferences condition.ConditionType = "ValueReferences"
)

type ConfigValueFrom struct {
	Secret    *corev1.SecretKeySelector    `json:"secret,omitempty"`
	ConfigMap *corev1.ConfigMapKeySelector `json:"configMap,omitempty"`
}

type ConfigEntries []ConfigEntry

type ConfigEntry struct {
	// +kubebuilder:validation:Required
	Name string `json:"name"`
	// +kubebuilder:validation:Optional
	Value string `json:"value,omitempty"`
	// +kubebuilder:validation:Optional
	ValueFrom *ConfigValueFrom `json:"valueFrom,omitempty"`
}

// ConfigSpec defines the desired state of Config.
type ConfigSpec struct {
	// +kubebuilder:validation:Optional
	Config ConfigEntries `json:"config,omitempty"`
}

// ConfigStatus defines the observed state of Config.
type ConfigStatus struct {
	condition.ConditionedStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// Config is the Schema for the configs API.
type Config struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ConfigSpec   `json:"spec,omitempty"`
	Status ConfigStatus `json:"status,omitempty"`
}

func (c *Config) ConditionedStatus() *condition.ConditionedStatus {
	return &c.Status.ConditionedStatus
}

func (c *Config) InitializeConditionedStatus() {
	c.Status.SetConditions(
		condition.Condition{
			Type:   ConfigConditionValueReferences,
			Status: condition.ConditionUnknown,
		},
	)
}

// +kubebuilder:object:root=true

// ConfigList contains a list of Config.
type ConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Config `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Config{}, &ConfigList{})
}
