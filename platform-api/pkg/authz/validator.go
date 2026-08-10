package authz

import (
	"context"
	"fmt"
	"sync"

	"k8s.io/apimachinery/pkg/runtime"
)

// RoleValidator validates roleRef fields against the known role sets.
// It is set globally at startup when the ConfigMap is loaded.
var roleValidator struct {
	mu                  sync.RWMutex
	namespaceRoleLabels map[string]bool
	platformRoleLabels  map[string]bool
}

// SetRoleValidator configures the global role validator with the known role sets.
// Must be called at startup after loading the ConfigMap.
func SetRoleValidator(config *RoleConfig) {
	roleValidator.mu.Lock()
	defer roleValidator.mu.Unlock()
	roleValidator.namespaceRoleLabels = config.NamespaceRoleLabels
	roleValidator.platformRoleLabels = config.PlatformRoleLabels
}

// ValidateNamespaceRoleRef validates that a roleRef references a known namespace-scoped role.
func ValidateNamespaceRoleRef(roleRef string) error {
	roleValidator.mu.RLock()
	defer roleValidator.mu.RUnlock()
	if roleValidator.namespaceRoleLabels == nil {
		return nil // validator not configured (e.g., auth disabled)
	}
	if !roleValidator.namespaceRoleLabels[roleRef] {
		if roleValidator.platformRoleLabels[roleRef] {
			return fmt.Errorf("roleRef %q is a platform-scoped role and cannot be used in a namespace-scoped RoleBinding", roleRef)
		}
		return fmt.Errorf("roleRef %q is not a known role", roleRef)
	}
	return nil
}

// ValidatePlatformRoleRef validates that a roleRef references a known platform-scoped role.
func ValidatePlatformRoleRef(roleRef string) error {
	roleValidator.mu.RLock()
	defer roleValidator.mu.RUnlock()
	if roleValidator.platformRoleLabels == nil {
		return nil // validator not configured (e.g., auth disabled)
	}
	if !roleValidator.platformRoleLabels[roleRef] {
		if roleValidator.namespaceRoleLabels[roleRef] {
			return fmt.Errorf("roleRef %q is a namespace-scoped role and cannot be used in a PlatformRoleBinding", roleRef)
		}
		return fmt.Errorf("roleRef %q is not a known role", roleRef)
	}
	return nil
}

// RoleBindingValidator implements types.CustomValidator for RoleBinding.
// It validates that the roleRef references a known namespace-scoped role.
type RoleBindingValidator struct {
	RoleRef string
}

func (v *RoleBindingValidator) ValidateCreate(_ context.Context) error {
	return ValidateNamespaceRoleRef(v.RoleRef)
}

func (v *RoleBindingValidator) ValidateUpdate(_ context.Context, _ runtime.Object) error {
	return ValidateNamespaceRoleRef(v.RoleRef)
}

func (v *RoleBindingValidator) ValidateDelete(_ context.Context) error {
	return nil
}

// PlatformRoleBindingValidator implements types.CustomValidator for PlatformRoleBinding.
// It validates that the roleRef references a known platform-scoped role.
type PlatformRoleBindingValidator struct {
	RoleRef string
}

func (v *PlatformRoleBindingValidator) ValidateCreate(_ context.Context) error {
	return ValidatePlatformRoleRef(v.RoleRef)
}

func (v *PlatformRoleBindingValidator) ValidateUpdate(_ context.Context, _ runtime.Object) error {
	return ValidatePlatformRoleRef(v.RoleRef)
}

func (v *PlatformRoleBindingValidator) ValidateDelete(_ context.Context) error {
	return nil
}
