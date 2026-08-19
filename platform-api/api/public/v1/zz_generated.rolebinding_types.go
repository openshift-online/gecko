package v1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// RoleBinding binds a user to a PlatformRole or namespace-scoped Role within a namespace.
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

type RoleBindingSpec struct {
	Subject string `json:"subject"`

	RoleRef RoleRef `json:"roleRef"`
	// Condition is an optional Cedar expression evaluated against the
	// request context (resource name, spec fields, etc.). When set, the
	// binding only grants access when the condition evaluates to true.

	Condition string `json:"condition,omitempty"`
}

// RoleRef references a PlatformRole (cluster-scoped) or a namespace-scoped Role.
// Follows the Kubernetes roleRef convention.
type RoleRef struct {
	// Kind is the type of role being referenced: "PlatformRole" or "Role".

	Kind string `json:"kind"`
	// Name is the name of the role.

	Name string `json:"name"`
	// APIGroup is the API group of the role resource.

	APIGroup string `json:"apiGroup"`
}

func init() { register(&RoleBinding{}, &RoleBindingList{}) }
