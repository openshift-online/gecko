package authz

import (
	"context"
	"fmt"

	cedar "github.com/cedar-policy/cedar-go"
	privatev1 "github.com/openshift-online/gecko/platform-api/api/private/v1"
	"github.com/openshift-online/gecko/orlop/pkg/apiserver/storage"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
)

// AuthzStores holds the storage interfaces needed for authorization.
type AuthzStores struct {
	PlatformRoles storage.ResourceStore
	Roles         storage.ResourceStore
	RoleBindings  storage.ResourceStore
}

// EntityGetter builds Cedar entity graphs from the authorization stores.
type EntityGetter struct {
	stores AuthzStores
}

// NewEntityGetter creates a new EntityGetter with the given stores.
func NewEntityGetter(stores AuthzStores) *EntityGetter {
	return &EntityGetter{stores: stores}
}

// BuildEntities constructs a Cedar EntityMap for the given user.
//
// The entity graph:
//   - User::"email" with parents: NamespaceRole::"ns/roleName/bindingName" for each RoleBinding
//   - NamespaceRole::"ns/roleName/bindingName" with parent Namespace::"ns"
//   - Namespace::"ns" (leaf entity)
//
// NamespaceRole identifiers are three-part keys including the binding name for isolation.
func (eg *EntityGetter) BuildEntities(ctx context.Context, user string) (cedar.EntityMap, error) {
	entities := make(cedar.EntityMap)
	var userParents []cedar.EntityUID

	// Fetch namespace-scoped role bindings for this user.
	rbList, err := eg.stores.RoleBindings.List(ctx, storage.ListOptions{
		FieldFilters: map[string]string{"spec.subject": user},
	})
	if err != nil {
		return nil, fmt.Errorf("list role bindings for user %q: %w", user, err)
	}

	rbItems, err := extractList(rbList)
	if err != nil {
		return nil, fmt.Errorf("extract role bindings: %w", err)
	}

	for _, obj := range rbItems {
		rb, ok := obj.(*privatev1.RoleBinding)
		if !ok {
			continue
		}
		ns := rb.Namespace
		roleName := rb.Spec.RoleRef.Name
		bindingName := rb.Name

		// Each binding gets a unique NamespaceRole entity keyed by
		// "ns/roleName/bindingName", mirroring the policy generation key.
		// This prevents an unconditional binding for one user from granting
		// access to another user who is bound to the same role with a condition.
		nsRoleKey := ns + "/" + roleName + "/" + bindingName
		nsRoleUID := cedar.NewEntityUID("NamespaceRole", cedar.String(nsRoleKey))
		nsUID := cedar.NewEntityUID("Namespace", cedar.String(ns))

		if _, exists := entities[nsRoleUID]; !exists {
			entities[nsRoleUID] = cedar.Entity{
				UID:        nsRoleUID,
				Parents:    cedar.NewEntityUIDSet(nsUID),
				Attributes: cedar.NewRecord(cedar.RecordMap{}),
			}
		}

		// Create Namespace entity if not present.
		if _, exists := entities[nsUID]; !exists {
			entities[nsUID] = cedar.Entity{
				UID:        nsUID,
				Parents:    cedar.NewEntityUIDSet(),
				Attributes: cedar.NewRecord(cedar.RecordMap{}),
			}
		}

		userParents = append(userParents, nsRoleUID)
	}

	// Create User entity.
	userUID := cedar.NewEntityUID("User", cedar.String(user))
	entities[userUID] = cedar.Entity{
		UID:        userUID,
		Parents:    cedar.NewEntityUIDSet(userParents...),
		Attributes: cedar.NewRecord(cedar.RecordMap{}),
	}

	return entities, nil
}

// AuthorizedNamespaces returns the list of namespaces where the user has
// the given action permission.
func (eg *EntityGetter) AuthorizedNamespaces(ctx context.Context, user, action string) ([]string, error) {
	perm, ok := ActionToPermission[action]
	if !ok {
		return nil, fmt.Errorf("unknown action %q", action)
	}

	// Fetch all role bindings for this user.
	rbList, err := eg.stores.RoleBindings.List(ctx, storage.ListOptions{
		FieldFilters: map[string]string{"spec.subject": user},
	})
	if err != nil {
		return nil, fmt.Errorf("list role bindings: %w", err)
	}

	rbItems, err := extractList(rbList)
	if err != nil {
		return nil, fmt.Errorf("extract role bindings: %w", err)
	}

	type nsBinding struct {
		namespace string
		roleName  string
		roleKind  string
	}
	var bindings []nsBinding
	for _, obj := range rbItems {
		rb, ok := obj.(*privatev1.RoleBinding)
		if !ok {
			continue
		}
		bindings = append(bindings, nsBinding{
			namespace: rb.Namespace,
			roleName:  rb.Spec.RoleRef.Name,
			roleKind:  rb.Spec.RoleRef.Kind,
		})
	}

	// For each binding, check if the referenced role grants the permission.
	seen := make(map[string]bool)
	var namespaces []string
	for _, b := range bindings {
		if seen[b.namespace] {
			continue
		}
		var hasPerm bool
		var err error
		switch b.roleKind {
		case privatev1.RoleRefKindPlatformRole:
			hasPerm, err = eg.platformRoleHasPerm(ctx, b.roleName, perm)
			if err != nil {
				return nil, fmt.Errorf("check platform role permission: %w", err)
			}
		case privatev1.RoleRefKindRole:
			hasPerm, err = eg.roleHasPerm(ctx, b.namespace, b.roleName, perm)
			if err != nil {
				return nil, fmt.Errorf("check role permission: %w", err)
			}
		}
		if hasPerm {
			seen[b.namespace] = true
			namespaces = append(namespaces, b.namespace)
		}
	}

	return namespaces, nil
}

func (eg *EntityGetter) platformRoleHasPerm(ctx context.Context, roleName, perm string) (bool, error) {
	obj, err := eg.stores.PlatformRoles.Get(ctx, "", roleName)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("get platform role %q: %w", roleName, err)
	}
	pr, ok := obj.(*privatev1.PlatformRole)
	if !ok {
		return false, fmt.Errorf("unexpected type %T for platform role %q", obj, roleName)
	}
	for _, p := range pr.Spec.Permissions {
		if p == perm {
			return true, nil
		}
	}
	return false, nil
}

func (eg *EntityGetter) roleHasPerm(ctx context.Context, namespace, roleName, perm string) (bool, error) {
	obj, err := eg.stores.Roles.Get(ctx, namespace, roleName)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("get role %s/%s: %w", namespace, roleName, err)
	}
	role, ok := obj.(*privatev1.Role)
	if !ok {
		return false, fmt.Errorf("unexpected type %T for role %s/%s", obj, namespace, roleName)
	}
	for _, p := range role.Spec.Permissions {
		if p == perm {
			return true, nil
		}
	}
	return false, nil
}

// extractList extracts individual runtime.Objects from a list object.
func extractList(list runtime.Object) ([]runtime.Object, error) {
	return meta.ExtractList(list)
}
