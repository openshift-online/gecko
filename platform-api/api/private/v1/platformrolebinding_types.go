package v1

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
type PlatformRoleBinding struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +orlop:public
	Spec PlatformRoleBindingSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true
type PlatformRoleBindingList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	// +orlop:public
	Items []PlatformRoleBinding `json:"items"`
}

// PlatformRoleBindingSpec binds a user (by email) to a platform-scoped role.
type PlatformRoleBindingSpec struct {
	// Subject is the user email to bind the role to.
	// +orlop:public
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Subject string `json:"subject"`

	// RoleRef is the name of the role to bind (must reference a known platform-scoped role).
	// +orlop:public
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	RoleRef string `json:"roleRef"`
}

// ValidateCreate validates the PlatformRoleBinding on creation.
func (prb *PlatformRoleBinding) ValidateCreate(_ context.Context) error {
	if RoleRefValidator != nil {
		return RoleRefValidator(prb.Spec.RoleRef, "platform")
	}
	return nil
}

// ValidateUpdate validates the PlatformRoleBinding on update.
func (prb *PlatformRoleBinding) ValidateUpdate(_ context.Context, _ runtime.Object) error {
	if RoleRefValidator != nil {
		return RoleRefValidator(prb.Spec.RoleRef, "platform")
	}
	return nil
}

// ValidateDelete is a no-op for PlatformRoleBinding.
func (prb *PlatformRoleBinding) ValidateDelete(_ context.Context) error {
	return nil
}

func init() { register(&PlatformRoleBinding{}, &PlatformRoleBindingList{}) }
