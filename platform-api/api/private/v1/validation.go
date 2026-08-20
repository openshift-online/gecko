package v1

import (
	"context"
	"sync"
)

// ValidatorDeps provides dependencies for validators without creating circular imports.
// The authz package imports this package, so we cannot import authz here.
// Instead, callers wire these dependencies during server initialization.
type ValidatorDeps struct {
	// RoleExists checks if a namespace-scoped Role with the given name exists in the given namespace.
	RoleExists func(ctx context.Context, namespace, name string) bool
	// PlatformRoleExists checks if a cluster-scoped PlatformRole with the given name exists.
	PlatformRoleExists func(ctx context.Context, name string) bool
}

var (
	validatorDeps   *ValidatorDeps
	validatorDepsMu sync.RWMutex
)

// SetValidatorDeps sets the global validator dependencies.
// Called during server initialization.
func SetValidatorDeps(deps ValidatorDeps) {
	validatorDepsMu.Lock()
	defer validatorDepsMu.Unlock()
	validatorDeps = &deps
}

func getValidatorDeps() *ValidatorDeps {
	validatorDepsMu.RLock()
	defer validatorDepsMu.RUnlock()
	return validatorDeps
}

// validPermissions is the set of all valid permission strings.
// Kept in sync with authz.ValidPermissions. Duplicated here to avoid
// a circular import (authz → v1 → authz).
var validPermissions = map[string]bool{
	"cluster.create":    true,
	"cluster.list":      true,
	"cluster.get":       true,
	"cluster.update":    true,
	"cluster.delete":    true,
	"nodepool.create":   true,
	"nodepool.list":     true,
	"nodepool.get":      true,
	"nodepool.update":   true,
	"nodepool.delete":   true,
	"rolebinding.create": true,
	"rolebinding.list":   true,
	"rolebinding.get":    true,
	"rolebinding.update": true,
	"rolebinding.delete": true,
	"role.create":       true,
	"role.list":         true,
	"role.get":          true,
	"role.update":       true,
	"role.delete":       true,
}

// infraWritePermissions are permissions that modify infrastructure.
// Kept in sync with authz.InfraWritePermissions. User-defined namespace-scoped
// Roles may not include these; they are reserved for PlatformRoles.
var infraWritePermissions = map[string]bool{
	"cluster.create":  true,
	"cluster.update":  true,
	"cluster.delete":  true,
	"nodepool.create": true,
	"nodepool.update": true,
	"nodepool.delete": true,
}
