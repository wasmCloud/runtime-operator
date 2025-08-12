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
	LinkConditionTargetReference condition.ConditionType = "TargetReference"
	LinkConditionConfigReference condition.ConditionType = "ConfigReference"
)

type LinkInterface struct {
	// +kubebuilder:validation:Required
	Namespace string `json:"namespace"`
	// +kubebuilder:validation:Required
	Package string `json:"package"`
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	Interfaces []string `json:"interfaces"`
}

type LinkTarget struct {
	// +kubebuilder:validation:Optional
	Component *corev1.ObjectReference `json:"component,omitempty"`
	// +kubebuilder:validation:Optional
	Provider *corev1.ObjectReference `json:"provider,omitempty"`
	// +kubebuilder:validation:Optional
	ConfigFrom []corev1.ObjectReference `json:"configFrom,omitempty"`
}

// LinkSpec defines the desired state of Link.
type LinkSpec struct {
	// +kubebuilder:validation:Required
	WIT LinkInterface `json:"wit"`
	// +kubebuilder:validation:Required
	Source LinkTarget `json:"source"`
	// +kubebuilder:validation:Required
	Target LinkTarget `json:"target"`
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Default="default"
	// Optional field to specify the name of the link. Defaults to 'default'.
	Name string `json:"name,omitempty"`
}

// LinkStatus defines the observed state of Link.
type LinkStatus struct {
	condition.ConditionedStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// Link is the Schema for the links API.
type Link struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   LinkSpec   `json:"spec,omitempty"`
	Status LinkStatus `json:"status,omitempty"`
}

// Implement the ConfigReferencer interface.
func (l *Link) ConfigRefs() []corev1.ObjectReference {
	var refs []corev1.ObjectReference
	if len(l.Spec.Source.ConfigFrom) > 0 {
		refs = append(refs, l.Spec.Source.ConfigFrom...)
	}
	if len(l.Spec.Target.ConfigFrom) > 0 {
		refs = append(refs, l.Spec.Target.ConfigFrom...)
	}

	return refs
}

func (l *Link) ConditionedStatus() *condition.ConditionedStatus {
	return &l.Status.ConditionedStatus
}

func (l *Link) InitializeConditionedStatus() {
	l.Status.SetConditions(
		condition.Condition{
			Type:   LinkConditionTargetReference,
			Status: condition.ConditionUnknown,
		},
	)
}

// +kubebuilder:object:root=true

// LinkList contains a list of Link.
type LinkList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Link `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Link{}, &LinkList{})
}
