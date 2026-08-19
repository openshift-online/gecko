package v1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// GroupVersion is the API group for gecko resources.
const GroupName = "gcp.managed.openshift.io"

// RoleBinding binds a user to a PlatformRole or namespace-scoped Role within a namespace.
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced
type RoleBinding struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +orlop:public
	Spec RoleBindingSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true
type RoleBindingList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	// +orlop:public
	Items []RoleBinding `json:"items"`
}

type RoleBindingSpec struct {
	// +orlop:public
	Subject string `json:"subject"`
	// +orlop:public
	RoleRef RoleRef `json:"roleRef"`
	// Condition is an optional Cedar expression evaluated against the
	// request context (resource name, spec fields, etc.). When set, the
	// binding only grants access when the condition evaluates to true.
	// +orlop:public
	Condition string `json:"condition,omitempty"`
}

// RoleRef references a PlatformRole (cluster-scoped) or a namespace-scoped Role.
// Follows the Kubernetes roleRef convention.
type RoleRef struct {
	// Kind is the type of role being referenced: "PlatformRole" or "Role".
	// +orlop:public
	Kind string `json:"kind"`
	// Name is the name of the role.
	// +orlop:public
	Name string `json:"name"`
	// APIGroup is the API group of the role resource.
	// +orlop:public
	APIGroup string `json:"apiGroup"`
}

func init() { register(&RoleBinding{}, &RoleBindingList{}) }
