package v1

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
)

// ValidateCreate validates PlatformRole creation.
func (r *PlatformRole) ValidateCreate(ctx context.Context) error {
	return validatePlatformRoleSpec(r.Spec)
}

// ValidateUpdate validates PlatformRole updates.
func (r *PlatformRole) ValidateUpdate(ctx context.Context, oldObj runtime.Object) error {
	return validatePlatformRoleSpec(r.Spec)
}

// ValidateDelete validates PlatformRole deletion.
func (r *PlatformRole) ValidateDelete(ctx context.Context) error {
	return nil
}

func validatePlatformRoleSpec(spec PlatformRoleSpec) error {
	if len(spec.Permissions) == 0 {
		return fmt.Errorf("platform role must have at least one permission")
	}
	for _, perm := range spec.Permissions {
		if !validPermissions[perm] {
			return fmt.Errorf("invalid permission %q", perm)
		}
	}
	return nil
}
