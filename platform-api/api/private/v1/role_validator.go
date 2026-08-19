package v1

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
)

// ValidateCreate validates Role creation.
func (r *Role) ValidateCreate(ctx context.Context) error {
	return validateRoleSpec(r.Spec)
}

// ValidateUpdate validates Role updates.
func (r *Role) ValidateUpdate(ctx context.Context, oldObj runtime.Object) error {
	return validateRoleSpec(r.Spec)
}

// ValidateDelete validates Role deletion.
func (r *Role) ValidateDelete(ctx context.Context) error {
	return nil
}

// validateRoleSpec validates the permissions of a namespace-scoped role spec.
// Namespace-scoped roles are user-defined and may not include infrastructure
// write permissions — those are reserved for PlatformRoles.
func validateRoleSpec(spec RoleSpec) error {
	if len(spec.Permissions) == 0 {
		return fmt.Errorf("role must have at least one permission")
	}
	for _, perm := range spec.Permissions {
		if !validPermissions[perm] {
			return fmt.Errorf("invalid permission %q", perm)
		}
		if infraWritePermissions[perm] {
			return fmt.Errorf("infrastructure write permission %q is not allowed in user-defined roles", perm)
		}
	}
	return nil
}
