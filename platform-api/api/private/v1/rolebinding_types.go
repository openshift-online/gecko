package v1

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// RoleRefValidator is a hook for external validation of roleRef fields.
// It is set at startup by the authz package to validate against known roles.
// If nil, no roleRef validation is performed.
var RoleRefValidator func(roleRef string, scope string) error

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

// RoleBindingSpec binds a user (by email) to a namespace-scoped role.
type RoleBindingSpec struct {
	// Subject is the user email to bind the role to.
	// +orlop:public
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Subject string `json:"subject"`

	// RoleRef is the name of the role to bind (must reference a known namespace-scoped role).
	// +orlop:public
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	RoleRef string `json:"roleRef"`
}

// ValidateCreate validates the RoleBinding on creation.
func (rb *RoleBinding) ValidateCreate(_ context.Context) error {
	if RoleRefValidator != nil {
		return RoleRefValidator(rb.Spec.RoleRef, "namespace")
	}
	return nil
}

// ValidateUpdate validates the RoleBinding on update.
func (rb *RoleBinding) ValidateUpdate(_ context.Context, _ runtime.Object) error {
	if RoleRefValidator != nil {
		return RoleRefValidator(rb.Spec.RoleRef, "namespace")
	}
	return nil
}

// ValidateDelete is a no-op for RoleBinding.
func (rb *RoleBinding) ValidateDelete(_ context.Context) error {
	return nil
}

func init() { register(&RoleBinding{}, &RoleBindingList{}) }
