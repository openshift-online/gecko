package authz

import (
	"context"
	"fmt"
	"log"
	"sync/atomic"

	cedar "github.com/cedar-policy/cedar-go"
	privatev1 "github.com/openshift-online/gecko/platform-api/api/private/v1"
	"github.com/openshift-online/gecko/orlop/pkg/apiserver/storage"
)

// Authorizer performs Cedar-based authorization checks.
type Authorizer struct {
	policies     atomic.Pointer[cedar.PolicySet]
	entityGetter *EntityGetter
	cache        *EntityCache
	stores       AuthzStores
}

// NewAuthorizer creates a new Authorizer and loads the initial policy set from the stores.
func NewAuthorizer(ctx context.Context, stores AuthzStores) (*Authorizer, error) {
	a := &Authorizer{
		entityGetter: NewEntityGetter(stores),
		cache:        NewEntityCache(),
		stores:       stores,
	}

	ps, err := a.loadPolicies(ctx)
	if err != nil {
		return nil, fmt.Errorf("load initial policies: %w", err)
	}
	a.policies.Store(ps)

	return a, nil
}

// Authorize checks whether the given user is allowed to perform the action
// on the specified namespace resource with an empty Cedar context.
func (a *Authorizer) Authorize(ctx context.Context, user, action, namespace string) (bool, error) {
	return a.AuthorizeWithContext(ctx, user, action, namespace, cedar.NewRecord(cedar.RecordMap{}))
}

// AuthorizeWithContext checks whether the given user is allowed to perform the
// action on the specified namespace resource, using the provided Cedar context
// record for condition evaluation (resource name, spec fields, etc.).
func (a *Authorizer) AuthorizeWithContext(ctx context.Context, user, action, namespace string, cedarCtx cedar.Record) (bool, error) {
	entities, err := a.getEntities(ctx, user)
	if err != nil {
		return false, fmt.Errorf("build entities: %w", err)
	}

	principal := cedar.NewEntityUID("User", cedar.String(user))
	actionUID := cedar.NewEntityUID("Action", cedar.String(action))
	resource := cedar.NewEntityUID("Namespace", cedar.String(namespace))

	req := cedar.Request{
		Principal: principal,
		Action:    actionUID,
		Resource:  resource,
		Context:   cedarCtx,
	}

	ps := a.policies.Load()
	decision, _ := cedar.Authorize(ps, entities, req)

	return decision == cedar.Allow, nil
}

// AuthorizedNamespaces returns the list of namespaces where the user has
// the given action permission. Delegates to EntityGetter for efficiency.
func (a *Authorizer) AuthorizedNamespaces(ctx context.Context, user, action string) ([]string, error) {
	return a.entityGetter.AuthorizedNamespaces(ctx, user, action)
}

// ReloadPolicies reloads the policy set from the stores.
func (a *Authorizer) ReloadPolicies(ctx context.Context) error {
	ps, err := a.loadPolicies(ctx)
	if err != nil {
		return err
	}
	a.policies.Store(ps)
	log.Printf("authz: reloaded policies")
	return nil
}

// InvalidateCache removes all cached entities, forcing a rebuild on next request.
func (a *Authorizer) InvalidateCache() {
	a.cache.InvalidateAll()
}

// InvalidateUser removes cached entities for a specific user.
func (a *Authorizer) InvalidateUser(user string) {
	a.cache.Invalidate(user)
}

func (a *Authorizer) getEntities(ctx context.Context, user string) (cedar.EntityMap, error) {
	if entities, ok := a.cache.Get(user); ok {
		return entities, nil
	}

	entities, err := a.entityGetter.BuildEntities(ctx, user)
	if err != nil {
		return nil, err
	}

	a.cache.Put(user, entities)
	return entities, nil
}

func (a *Authorizer) loadPolicies(ctx context.Context) (*cedar.PolicySet, error) {
	// Fetch PlatformRoles (cluster-scoped).
	prList, err := a.stores.PlatformRoles.List(ctx, storage.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list platform roles: %w", err)
	}
	prItems, err := extractList(prList)
	if err != nil {
		return nil, fmt.Errorf("extract platform roles: %w", err)
	}
	var platformRoles []privatev1.PlatformRole
	for _, obj := range prItems {
		if pr, ok := obj.(*privatev1.PlatformRole); ok {
			platformRoles = append(platformRoles, *pr)
		}
	}

	// Fetch namespace-scoped Roles (list across all namespaces).
	roleList, err := a.stores.Roles.List(ctx, storage.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list roles: %w", err)
	}
	roleItems, err := extractList(roleList)
	if err != nil {
		return nil, fmt.Errorf("extract roles: %w", err)
	}
	var roles []privatev1.Role
	for _, obj := range roleItems {
		if r, ok := obj.(*privatev1.Role); ok {
			roles = append(roles, *r)
		}
	}

	// Fetch RoleBindings.
	rbList, err := a.stores.RoleBindings.List(ctx, storage.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list role bindings: %w", err)
	}
	rbItems, err := extractList(rbList)
	if err != nil {
		return nil, fmt.Errorf("extract role bindings: %w", err)
	}
	var bindings []privatev1.RoleBinding
	for _, obj := range rbItems {
		if rb, ok := obj.(*privatev1.RoleBinding); ok {
			bindings = append(bindings, *rb)
		}
	}

	return GeneratePolicies(platformRoles, roles, bindings)
}
