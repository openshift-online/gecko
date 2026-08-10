package authz

import (
	"context"
	"fmt"

	cedar "github.com/cedar-policy/cedar-go"
	cedartypes "github.com/cedar-policy/cedar-go/types"
	publicv1 "github.com/openshift-online/gecko/platform-api/api/public/v1"
	"github.com/openshift-online/gecko/orlop/pkg/apiserver/storage"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// PlatformEntity is the singleton platform entity UID used as the parent for
// platform-scoped roles and as the root of the namespace hierarchy.
const PlatformEntity = "gecko"

// Cedar entity type names.
const (
	TypeUser             = "Gecko::User"
	TypeNamespace        = "Gecko::Namespace"
	TypePlatform         = "Gecko::Platform"
	TypeNamespaceRole    = "Gecko::NamespaceRole"
	TypePlatformRole     = "Gecko::PlatformRole"
	TypeCluster          = "Gecko::Cluster"
	TypeNodePool         = "Gecko::NodePool"
)

// EntityGetter builds Cedar entity maps from RoleBinding and PlatformRoleBinding stores.
type EntityGetter struct {
	rbStore  storage.ResourceStore // RoleBinding store
	prbStore storage.ResourceStore // PlatformRoleBinding store
	cache    *EntityCache
}

// NewEntityGetter creates an EntityGetter backed by the given stores and cache.
func NewEntityGetter(rbStore, prbStore storage.ResourceStore, cache *EntityCache) *EntityGetter {
	return &EntityGetter{
		rbStore:  rbStore,
		prbStore: prbStore,
		cache:    cache,
	}
}

// BuildEntityMap constructs a Cedar entity map for the given user.
// It queries RoleBindings and PlatformRoleBindings to determine the user's
// role memberships, building the transitive entity hierarchy:
//
//	User → NamespaceRole → Namespace → Platform
//	User → PlatformRole → Platform
//
// The result is cached per user and invalidated on binding writes.
func (g *EntityGetter) BuildEntityMap(ctx context.Context, email string) (cedartypes.EntityMap, error) {
	// Check cache first
	if em, ok := g.cache.Get(email); ok {
		return em, nil
	}

	em := make(cedartypes.EntityMap)

	// Platform entity (singleton root)
	platformUID := cedar.NewEntityUID(TypePlatform, cedartypes.String(PlatformEntity))
	em[platformUID] = cedartypes.Entity{
		UID:        platformUID,
		Parents:    cedar.NewEntityUIDSet(),
		Attributes: cedartypes.NewRecord(nil),
	}

	// Collect user's parent roles
	var userParents []cedartypes.EntityUID

	// Query RoleBindings across all namespaces for this user
	if g.rbStore != nil {
		rbList, err := g.rbStore.List(ctx, storage.ListOptions{})
		if err != nil {
			return nil, fmt.Errorf("listing RoleBindings: %w", err)
		}
		items := extractItems(rbList)
		for _, item := range items {
			rb, ok := item.(*publicv1.RoleBinding)
			if !ok {
				continue
			}
			if rb.Spec.Subject != email {
				continue
			}

			ns := rb.Namespace
			roleName := rb.Spec.RoleRef

			// Namespace entity
			nsUID := cedar.NewEntityUID(TypeNamespace, cedartypes.String(ns))
			if _, exists := em[nsUID]; !exists {
				em[nsUID] = cedartypes.Entity{
					UID:        nsUID,
					Parents:    cedar.NewEntityUIDSet(platformUID),
					Attributes: cedartypes.NewRecord(nil),
				}
			}

			// NamespaceRole entity (ns/roleName)
			roleID := ns + "/" + roleName
			roleUID := cedar.NewEntityUID(TypeNamespaceRole, cedartypes.String(roleID))
			if _, exists := em[roleUID]; !exists {
				em[roleUID] = cedartypes.Entity{
					UID:        roleUID,
					Parents:    cedar.NewEntityUIDSet(nsUID),
					Attributes: cedartypes.NewRecord(nil),
				}
			}

			userParents = append(userParents, roleUID)
		}
	}

	// Query PlatformRoleBindings for this user
	if g.prbStore != nil {
		prbList, err := g.prbStore.List(ctx, storage.ListOptions{})
		if err != nil {
			return nil, fmt.Errorf("listing PlatformRoleBindings: %w", err)
		}
		items := extractItems(prbList)
		for _, item := range items {
			prb, ok := item.(*publicv1.PlatformRoleBinding)
			if !ok {
				continue
			}
			if prb.Spec.Subject != email {
				continue
			}

			roleName := prb.Spec.RoleRef

			// PlatformRole entity
			roleUID := cedar.NewEntityUID(TypePlatformRole, cedartypes.String(roleName))
			if _, exists := em[roleUID]; !exists {
				em[roleUID] = cedartypes.Entity{
					UID:        roleUID,
					Parents:    cedar.NewEntityUIDSet(platformUID),
					Attributes: cedartypes.NewRecord(nil),
				}
			}

			userParents = append(userParents, roleUID)
		}
	}

	// User entity with all parent roles
	userUID := cedar.NewEntityUID(TypeUser, cedartypes.String(email))
	em[userUID] = cedartypes.Entity{
		UID:        userUID,
		Parents:    cedar.NewEntityUIDSet(userParents...),
		Attributes: cedartypes.NewRecord(nil),
	}

	// Cache the result
	g.cache.Set(email, em)

	return em, nil
}

// AddResourceEntity adds a resource entity to the entity map with the correct
// parent hierarchy for authorization checks.
func AddResourceEntity(em cedartypes.EntityMap, resourceType, resourceID string, parents ...cedartypes.EntityUID) {
	uid := cedar.NewEntityUID(cedartypes.EntityType(resourceType), cedartypes.String(resourceID))
	em[uid] = cedartypes.Entity{
		UID:        uid,
		Parents:    cedar.NewEntityUIDSet(parents...),
		Attributes: cedartypes.NewRecord(nil),
	}
}

// NamespaceEntityUID returns the Cedar EntityUID for a namespace.
func NamespaceEntityUID(ns string) cedartypes.EntityUID {
	return cedar.NewEntityUID(TypeNamespace, cedartypes.String(ns))
}

// PlatformEntityUID returns the Cedar EntityUID for the platform.
func PlatformEntityUID() cedartypes.EntityUID {
	return cedar.NewEntityUID(TypePlatform, cedartypes.String(PlatformEntity))
}

// extractItems extracts individual items from a list object.
func extractItems(list client.ObjectList) []client.Object {
	switch l := list.(type) {
	case *publicv1.RoleBindingList:
		result := make([]client.Object, len(l.Items))
		for i := range l.Items {
			result[i] = &l.Items[i]
		}
		return result
	case *publicv1.PlatformRoleBindingList:
		result := make([]client.Object, len(l.Items))
		for i := range l.Items {
			result[i] = &l.Items[i]
		}
		return result
	default:
		return nil
	}
}

// AuthorizedNamespaces computes the set of namespaces where the user has the
// given Cedar action, based on their cached entity map. This is used for
// cross-namespace list queries.
func (g *EntityGetter) AuthorizedNamespaces(ctx context.Context, email string) ([]string, error) {
	em, err := g.BuildEntityMap(ctx, email)
	if err != nil {
		return nil, err
	}

	// Extract namespaces from NamespaceRole parents
	nsSet := make(map[string]bool)
	userUID := cedar.NewEntityUID(TypeUser, cedartypes.String(email))
	userEntity, ok := em.Get(userUID)
	if !ok {
		return nil, nil
	}

	// Walk the user's parent roles and find their namespace parents
	for parent := range userEntity.Parents.All() {
		if parent.Type == cedartypes.EntityType(TypeNamespaceRole) {
			// NamespaceRole ID format: "ns/roleName"
			roleEntity, ok := em.Get(parent)
			if !ok {
				continue
			}
			for nsParent := range roleEntity.Parents.All() {
				if nsParent.Type == cedartypes.EntityType(TypeNamespace) {
					nsSet[string(nsParent.ID)] = true
				}
			}
		}
	}

	namespaces := make([]string, 0, len(nsSet))
	for ns := range nsSet {
		namespaces = append(namespaces, ns)
	}
	return namespaces, nil
}
