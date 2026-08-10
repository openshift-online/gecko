package v1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced
type RoleBinding struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec RoleBindingSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true
type RoleBindingList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []RoleBinding `json:"items"`
}

// RoleBindingSpec binds a user (by email) to a namespace-scoped role.
type RoleBindingSpec struct {
	// Subject is the user email to bind the role to.

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Subject string `json:"subject"`

	// RoleRef is the name of the role to bind (must reference a known namespace-scoped role).

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	RoleRef string `json:"roleRef"`
}

func init() { register(&RoleBinding{}, &RoleBindingList{}) }
