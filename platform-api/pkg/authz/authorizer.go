package authz

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/cedar-policy/cedar-go"
	"github.com/go-logr/logr"
)

// Authorizer evaluates public API requests against an atomically replaced
// Cedar policy set and a per-user entity cache.
type Authorizer struct {
	stores   Stores
	reloadMu sync.Mutex
	policies atomic.Pointer[authorizationPolicySets]
	cache    *EntityCache
	logger   logr.Logger
}

type authorizationPolicySets struct {
	namespace *cedar.PolicySet
	platform  map[string]*cedar.PolicySet
}

// NewAuthorizer performs the initial policy build synchronously. An empty
// policy set is valid and provides the required default-deny behavior.
func NewAuthorizer(ctx context.Context, stores Stores, logger logr.Logger) (*Authorizer, error) {
	if logger.GetSink() == nil {
		logger = logr.Discard()
	}
	a := &Authorizer{
		stores: stores,
		cache:  NewEntityCache(defaultEntityCacheSize),
		logger: logger,
	}
	if err := a.Reload(ctx); err != nil {
		return nil, err
	}
	return a, nil
}

// Reload rebuilds and atomically installs the policy set. On failure, the
// previous set remains active.
func (a *Authorizer) Reload(ctx context.Context) error {
	a.reloadMu.Lock()
	defer a.reloadMu.Unlock()

	namespacePolicies, err := generatePolicySet(ctx, a.stores, a.logger)
	if err != nil {
		return err
	}
	platformPolicies, err := loadPlatformPolicySets()
	if err != nil {
		return err
	}
	a.policies.Store(&authorizationPolicySets{
		namespace: namespacePolicies,
		platform:  platformPolicies,
	})
	a.cache.InvalidateAll()
	return nil
}

// Authorize evaluates one namespace-scoped public API request.
func (a *Authorizer) Authorize(ctx context.Context, email string, action Action, namespace string) (bool, error) {
	if email == "" {
		return false, fmt.Errorf("principal is empty")
	}
	if namespace == "" {
		return false, nil
	}
	entities, err := entitiesForUser(ctx, a.stores, a.cache, email)
	if err != nil {
		return false, err
	}
	policySets := a.policies.Load()
	if policySets == nil || policySets.namespace == nil {
		return false, nil
	}
	decision, _ := cedar.Authorize(policySets.namespace, entities, cedar.Request{
		Principal: cedar.NewEntityUID("User", cedar.String(email)),
		Action:    cedar.NewEntityUID("Action", cedar.String(action)),
		Resource:  cedar.NewEntityUID("Namespace", cedar.String(namespace)),
		Context:   cedar.NewRecord(nil),
	})
	return decision == cedar.Allow, nil
}

// Stores returns the stores used by the authorizer. It is primarily useful
// for wiring validator dependencies and focused tests.
func (a *Authorizer) Stores() Stores { return a.stores }

// Cache returns the entity cache for diagnostics and tests.
func (a *Authorizer) Cache() *EntityCache { return a.cache }
