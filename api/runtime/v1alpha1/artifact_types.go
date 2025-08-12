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

// ArtifactSpec defines the desired state of Artifact.
type ArtifactSpec struct {
	// +kubebuilder:validation:Required
	Image string `json:"image"`
	// +kubebuilder:validation:Optional
	ImagePullSecret *corev1.LocalObjectReference `json:"imagePullSecret,omitempty"`
}

// ArtifactStatus defines the observed state of Artifact.
type ArtifactStatus struct {
	condition.ConditionedStatus `json:",inline"`
	ArtifactURL                 string `json:"artifactUrl,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// Artifact is the Schema for the artifacts API.
type Artifact struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ArtifactSpec   `json:"spec,omitempty"`
	Status ArtifactStatus `json:"status,omitempty"`
}

// fulfill the ConditionedStatus interface
func (a *Artifact) ConditionedStatus() *condition.ConditionedStatus {
	return &a.Status.ConditionedStatus
}

func (a *Artifact) InitializeConditionedStatus() {
}

// +kubebuilder:object:root=true

// ArtifactList contains a list of Artifact.
type ArtifactList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Artifact `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Artifact{}, &ArtifactList{})
}
