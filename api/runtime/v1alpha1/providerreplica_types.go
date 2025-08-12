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
	"strconv"

	"github.com/cosmonic-labs/runtime-operator/api/condition"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	ProviderReplicaConditionProviderReference condition.ConditionType = "ProviderReference"
	ProviderReplicaConditionPlacement         condition.ConditionType = "HostPlacement"
	ProviderReplicaConditionSync              condition.ConditionType = "Sync"

	ProviderReplicaGeneration string = "runtime.wasmcloud.dev/provider-replica-generation"
)

// ProviderReplicaSpec defines the desired state of ProviderReplica.
type ProviderReplicaSpec struct {
	// +kubebuilder:validation:Required
	HostID string `json:"hostId"`
	// +kubebuilder:validation:Required
	ProviderRef corev1.ObjectReference `json:"providerRef"`
	// +kubebuilder:validation:Optional
	Links []corev1.ObjectReference `json:"links,omitempty"`
}

// ProviderReplicaStatus defines the observed state of ProviderReplica.
type ProviderReplicaStatus struct {
	condition.ConditionedStatus `json:",inline"`
	LastSeen                    metav1.Time `json:"lastSeen,omitempty"`
	WorkloadID                  string      `json:"workloadId,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="READY",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="AGE",type=date,JSONPath=".metadata.creationTimestamp"
// +kubebuilder:printcolumn:name="HOST",type=string,JSONPath=".spec.hostId"
// ProviderReplica is the Schema for the providerreplicas API.
type ProviderReplica struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ProviderReplicaSpec   `json:"spec,omitempty"`
	Status ProviderReplicaStatus `json:"status,omitempty"`
}

func (p *ProviderReplica) GetParentGeneration() int64 {
	v, ok := p.GetAnnotations()[ProviderReplicaGeneration]
	if !ok {
		return 0
	}
	generation, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0
	}
	return generation
}

func (p *ProviderReplica) ConditionedStatus() *condition.ConditionedStatus {
	return &p.Status.ConditionedStatus
}

func (p *ProviderReplica) InitializeConditionedStatus() {
	p.Status.SetConditions(
		condition.Condition{
			Type:   ProviderReplicaConditionProviderReference,
			Status: condition.ConditionUnknown,
		},
	)
}

// +kubebuilder:object:root=true

// ProviderReplicaList contains a list of ProviderReplica.
type ProviderReplicaList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ProviderReplica `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ProviderReplica{}, &ProviderReplicaList{})
}
