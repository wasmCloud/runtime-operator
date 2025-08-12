/*
Copyright 2024.

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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	HostConditionReporting condition.ConditionType = "Reporting"
	HostConditionHealthy   condition.ConditionType = "Healthy"
)

// HostSpec defines the desired state of Host.
type HostSpec struct {
	HostID string `json:"hostID"`
}

// HostStatus defines the observed state of Host.
type HostStatus struct {
	condition.ConditionedStatus `json:",inline"`

	Version           string `json:"version"`
	HostName          string `json:"hostName"`
	OSName            string `json:"osName"`
	OSArch            string `json:"osArch"`
	OSKernel          string `json:"osKernel"`
	SystemCPUUsage    string `json:"systemCPUUsage"`
	SystemMemoryTotal int64  `json:"systemMemoryTotal"`
	SystemMemoryFree  int64  `json:"systemMemoryFree"`
	ComponentCount    int    `json:"componentCount,omitempty"`
	ProviderCount     int    `json:"providerCount,omitempty"`

	LastSeen metav1.Time `json:"lastSeen,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="HOSTGROUP",type=string,JSONPath=`.metadata.labels.hostgroup`
// +kubebuilder:printcolumn:name="COMPONENTS",type=integer,JSONPath=`.status.componentCount`
// +kubebuilder:printcolumn:name="PROVIDERS",type=integer,JSONPath=`.status.providerCount`
// +kubebuilder:printcolumn:name="READY",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="AGE",type=date,JSONPath=".metadata.creationTimestamp"
// +kubebuilder:printcolumn:name="ID",type=string,JSONPath=".spec.hostID"
// Host is the Schema for the hosts API.
type Host struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HostSpec   `json:"spec,omitempty"`
	Status HostStatus `json:"status,omitempty"`
}

func (h *Host) ConditionedStatus() *condition.ConditionedStatus {
	return &h.Status.ConditionedStatus
}

func (h *Host) InitializeConditionedStatus() {
	h.Status.SetConditions(
		condition.Condition{
			Type:   HostConditionReporting,
			Status: condition.ConditionUnknown,
		},
	)
}

// +kubebuilder:object:root=true

// HostList contains a list of Host.
type HostList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Host `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Host{}, &HostList{})
}
