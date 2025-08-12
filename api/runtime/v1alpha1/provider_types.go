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
	// ProviderConditionReplicaCount is true when the provider has the correct number of replicas.
	ProviderConditionReplicaCount condition.ConditionType = "ReplicaCount"
	// ProviderConditionSync is true when the all replicas are on the latest Provider generation.
	ProviderConditionSync condition.ConditionType = "Sync"
	// ProviderConditionConfigReference is true when the provider has the correct config references.
	ProviderConditionConfigReference condition.ConditionType = "ConfigReference"
	// ProviderConditionLinkReference is true when the provider has the correct link references.
	ProviderConditionLinkReference condition.ConditionType = "LinkReference"
)

// ProviderSpec defines the desired state of Provider.
type ProviderSpec struct {
	// +kubebuilder:validation:Required
	Image string `json:"image"`
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	Replicas int `json:"replicas"`
	// +kubebuilder:validation:Optional
	AllHosts bool `json:"allHosts,omitempty"`
	// +kubebuilder:validation:Optional
	HostSelector metav1.LabelSelector `json:"hostSelector,omitempty"`
	// +kubebuilder:validation:Optional
	ConfigFrom []corev1.LocalObjectReference `json:"configFrom,omitempty"`
}

// ProviderStatus defines the observed state of Provider.
type ProviderStatus struct {
	condition.ConditionedStatus `json:",inline"`
	// +kubebuilder:validation:Optional
	Replicas int `json:"replicas,omitempty"`
	// +kubebuilder:validation:Optional
	UnavailableReplicas int `json:"unavailableReplicas"`
	// +kubebuilder:validation:Optional
	Links []corev1.ObjectReference `json:"links,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="REPLICAS",type=integer,JSONPath=`.spec.replicas`
// +kubebuilder:printcolumn:name="READY",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="AGE",type=date,JSONPath=".metadata.creationTimestamp"
// Provider is the Schema for the providers API.
type Provider struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ProviderSpec   `json:"spec,omitempty"`
	Status ProviderStatus `json:"status,omitempty"`
}

// Implement the ConfigReferencer interface.
func (p *Provider) ConfigRefs() []corev1.ObjectReference {
	refs := make([]corev1.ObjectReference, 0, len(p.Spec.ConfigFrom))
	for _, ref := range p.Spec.ConfigFrom {
		refs = append(refs, corev1.ObjectReference{Namespace: p.Namespace, Name: ref.Name})
	}
	return refs
}

func (p *Provider) ConditionedStatus() *condition.ConditionedStatus {
	return &p.Status.ConditionedStatus
}

func (p *Provider) InitializeConditionedStatus() {
	p.Status.SetConditions(
		condition.Condition{
			Type:   ProviderConditionReplicaCount,
			Status: condition.ConditionUnknown,
		},
	)
}

// +kubebuilder:object:root=true

// ProviderList contains a list of Provider.
type ProviderList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Provider `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Provider{}, &ProviderList{})
}
