package authz

import (
	"context"
	"fmt"
	"sync"

	cedar "github.com/cedar-policy/cedar-go"
	cedartypes "github.com/cedar-policy/cedar-go/types"
)

// Decision represents an authorization decision.
type Decision = cedar.Decision

// Allow and Deny are the two possible authorization decisions.
const (
	Allow = cedar.Allow
	Deny  = cedar.Deny
)

// Authorizer evaluates Cedar authorization decisions against policies generated
// from the ConfigMap and a DB-backed entity graph.
type Authorizer struct {
	mu           sync.RWMutex
	policies     *cedar.PolicySet
	entityGetter *EntityGetter
}

// NewAuthorizer creates an Authorizer with the given policy set and entity getter.
func NewAuthorizer(policies *cedar.PolicySet, entityGetter *EntityGetter) *Authorizer {
	return &Authorizer{
		policies:     policies,
		entityGetter: entityGetter,
	}
}

// Authorize checks whether the given user is allowed to perform the specified
// action on the specified resource. It builds the entity map from cached
// RoleBindings/PlatformRoleBindings and evaluates the Cedar policy set.
func (a *Authorizer) Authorize(ctx context.Context, email, action, resourceType, resourceID string) (Decision, error) {
	// Build entity map for this user (cached)
	em, err := a.entityGetter.BuildEntityMap(ctx, email)
	if err != nil {
		return Deny, fmt.Errorf("building entity map: %w", err)
	}

	// Add the resource entity with its parent hierarchy
	addResourceToEntityMap(em, resourceType, resourceID)

	// Build the Cedar request
	req := cedartypes.Request{
		Principal: cedar.NewEntityUID(TypeUser, cedartypes.String(email)),
		Action:    cedar.NewEntityUID("Action", cedartypes.String(action)),
		Resource:  cedar.NewEntityUID(cedartypes.EntityType(resourceType), cedartypes.String(resourceID)),
		Context:   cedartypes.NewRecord(nil),
	}

	// Evaluate
	a.mu.RLock()
	policies := a.policies
	a.mu.RUnlock()

	decision, _ := cedar.Authorize(policies, em, req)
	return decision, nil
}

// SetEntityGetter updates the entity getter used for authorization.
// Called after server creation to wire the actual stores.
func (a *Authorizer) SetEntityGetter(eg *EntityGetter) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.entityGetter = eg
}

// UpdatePolicies atomically swaps the policy set used for authorization.
// Used when custom roles are created/updated/deleted (Phase 6).
func (a *Authorizer) UpdatePolicies(policies *cedar.PolicySet) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.policies = policies
}

// addResourceToEntityMap adds a resource entity to the entity map with the
// correct parent hierarchy based on resource type.
func addResourceToEntityMap(em cedartypes.EntityMap, resourceType, resourceID string) {
	uid := cedar.NewEntityUID(cedartypes.EntityType(resourceType), cedartypes.String(resourceID))
	if _, exists := em.Get(uid); exists {
		return // already added
	}

	switch resourceType {
	case TypeNamespace:
		// Namespace → Platform
		platformUID := PlatformEntityUID()
		ensureEntity(em, platformUID)
		em[uid] = cedartypes.Entity{
			UID:        uid,
			Parents:    cedar.NewEntityUIDSet(platformUID),
			Attributes: cedartypes.NewRecord(nil),
		}

	case TypePlatform:
		// Platform is a root entity
		em[uid] = cedartypes.Entity{
			UID:        uid,
			Parents:    cedar.NewEntityUIDSet(),
			Attributes: cedartypes.NewRecord(nil),
		}

	case TypeCluster:
		// Cluster → Namespace (resourceID format: "ns/name")
		ns := extractNamespace(resourceID)
		nsUID := NamespaceEntityUID(ns)
		addResourceToEntityMap(em, TypeNamespace, ns) // ensure namespace exists
		em[uid] = cedartypes.Entity{
			UID:        uid,
			Parents:    cedar.NewEntityUIDSet(nsUID),
			Attributes: cedartypes.NewRecord(nil),
		}

	case TypeNodePool:
		// NodePool → Namespace (resourceID format: "ns/name")
		ns := extractNamespace(resourceID)
		nsUID := NamespaceEntityUID(ns)
		addResourceToEntityMap(em, TypeNamespace, ns)
		em[uid] = cedartypes.Entity{
			UID:        uid,
			Parents:    cedar.NewEntityUIDSet(nsUID),
			Attributes: cedartypes.NewRecord(nil),
		}

	default:
		// Generic resource, no special parent handling
		em[uid] = cedartypes.Entity{
			UID:        uid,
			Parents:    cedar.NewEntityUIDSet(),
			Attributes: cedartypes.NewRecord(nil),
		}
	}
}

// ensureEntity ensures an entity exists in the map (as a root entity with no parents).
func ensureEntity(em cedartypes.EntityMap, uid cedartypes.EntityUID) {
	if _, exists := em.Get(uid); !exists {
		em[uid] = cedartypes.Entity{
			UID:        uid,
			Parents:    cedar.NewEntityUIDSet(),
			Attributes: cedartypes.NewRecord(nil),
		}
	}
}

// extractNamespace extracts the namespace from a "ns/name" resource ID.
func extractNamespace(resourceID string) string {
	for i, c := range resourceID {
		if c == '/' {
			return resourceID[:i]
		}
	}
	return resourceID
}
