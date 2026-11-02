package v1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// PlatformRoleBinding grants a PlatformRole for platform-scoped resources.
// It is managed through the private API by the platform's GitOps workflow.
//
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
type PlatformRoleBinding struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec identifies the subject, PlatformRole, and optional Cedar condition.
	// A subject of "*" selects all authenticated principals.
	// +required
	Spec PlatformRoleBindingSpec `json:"spec"`
}

// PlatformRoleBindingSpec identifies a subject and PlatformRole granted access
// to platform-scoped resources, with an optional Cedar condition.
type PlatformRoleBindingSpec struct {
	// subject is a normalized email principal or "*" for all authenticated principals.
	// +kubebuilder:validation:MinLength=1
	// +required
	//nolint:geckoconventions // Preserve the RoleBinding subject contract, which has no fixed length limit.
	Subject string `json:"subject"`

	// roleRef identifies a PlatformRole in this API group. Its supported kind
	// is PlatformRole.
	// +required
	RoleRef RoleRef `json:"roleRef"`

	// condition is an optional Cedar expression that further limits this grant.
	// +optional
	// +kubebuilder:validation:MaxLength=4096
	Condition *string `json:"condition,omitempty"`
}

// PlatformRoleBindingList is a list of PlatformRoleBinding resources.
//
// +kubebuilder:object:root=true
type PlatformRoleBindingList struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ListMeta `json:"metadata,omitempty"`

	// items contains the platform-managed bindings.
	// +required
	// +listType=atomic
	//nolint:geckoconventions // Server-generated list responses use pagination rather than a fixed item bound.
	Items []PlatformRoleBinding `json:"items"`
}

func init() {
	register(&PlatformRoleBinding{}, &PlatformRoleBindingList{})
}
