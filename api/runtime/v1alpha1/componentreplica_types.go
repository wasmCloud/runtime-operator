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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	ComponentReplicaConditionValid     condition.ConditionType = "Valid"
	ComponentReplicaConditionPlacement condition.ConditionType = "HostPlacement"
	ComponentReplicaConditionSync      condition.ConditionType = "Sync"

	ComponentReplicaGeneration string = "runtime.wasmcloud.dev/component-replica-generation"
)

// ComponentReplicaSpec defines the desired state of ComponentReplica.
type ComponentReplicaSpec struct {
	// +kubebuilder:validation:Required
	HostID string `json:"hostId"`
	// +kubebuilder:validation:Required
	ComponentName string `json:"componentName"`
	// +kubebuilder:validation:Required
	ComponentDefinition ComponentDefinition `json:"componentDefinition"`
}

// ComponentReplicaStatus defines the observed state of ComponentReplica.
type ComponentReplicaStatus struct {
	condition.ConditionedStatus `json:",inline"`
	LastSeen                    metav1.Time `json:"lastSeen,omitempty"`
	WorkloadID                  string      `json:"workloadId,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="COMPONENT",type=string,JSONPath=".spec.componentName"
// +kubebuilder:printcolumn:name="VALID",type=string,JSONPath=`.status.conditions[?(@.type=="Valid")].status`
// +kubebuilder:printcolumn:name="PLACED",type=string,JSONPath=`.status.conditions[?(@.type=="HostPlacement")].status`
// +kubebuilder:printcolumn:name="READY",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="AGE",type=date,JSONPath=".metadata.creationTimestamp"
// +kubebuilder:printcolumn:name="HOSTID",type=string,JSONPath=".spec.hostId"
// ComponentReplica is the Schema for the componentreplicas API.
type ComponentReplica struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ComponentReplicaSpec   `json:"spec,omitempty"`
	Status ComponentReplicaStatus `json:"status,omitempty"`
}

func (c *ComponentReplica) GetParentGeneration() int64 {
	generation, ok := c.GetAnnotations()[ComponentReplicaGeneration]
	if !ok {
		return 0
	}
	generationInt, err := strconv.ParseInt(generation, 10, 64)
	if err != nil {
		return 0
	}
	return generationInt
}

func (c *ComponentReplica) ConditionedStatus() *condition.ConditionedStatus {
	return &c.Status.ConditionedStatus
}

func (c *ComponentReplica) InitializeConditionedStatus() {
	c.Status.SetConditions(
		condition.Condition{
			Type:   ComponentReplicaConditionValid,
			Status: condition.ConditionUnknown,
		},
	)
}

// +kubebuilder:object:root=true

// ComponentReplicaList contains a list of ComponentReplica.
type ComponentReplicaList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ComponentReplica `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ComponentReplica{}, &ComponentReplicaList{})
}
