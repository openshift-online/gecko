package v1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// Role defines a set of permissions within a namespace.
// User-defined roles are created via the public API by service-admins.
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced
type Role struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +orlop:public
	Spec RoleSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true
type RoleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	// +orlop:public
	Items []Role `json:"items"`
}

type RoleSpec struct {
	// +orlop:public
	Permissions []string `json:"permissions"`
}

func init() { register(&Role{}, &RoleList{}) }
