package v1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// PlatformRole defines a set of permissions. Cluster-scoped.
// PlatformRoles are deployed via Helm and applied through the aggregated API via the private
// API server. They are never created directly by end users through the public API.
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
type PlatformRole struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec PlatformRoleSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true
type PlatformRoleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []PlatformRole `json:"items"`
}

type PlatformRoleSpec struct {
	Permissions []string `json:"permissions"`

	System bool `json:"system,omitempty"`
}

func init() { register(&PlatformRole{}, &PlatformRoleList{}) }
