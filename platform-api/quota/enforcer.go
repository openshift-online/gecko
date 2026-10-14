// Package quota implements quota enforcement for the platform API.
// It provides a StoreEnforcer that reads quota limits from the Quota
// store and counts existing resources to determine whether a creation request
// would exceed the namespace's quota.
package quota

import (
	"context"
	"fmt"

	"github.com/openshift-online/gecko/orlop/pkg/apiserver/storage"
	privatev1 "github.com/openshift-online/gecko/platform-api/api/private/v1"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// QuotaName is the singleton Quota object name per namespace.
	QuotaName = "default"

	// DefaultClusterLimit is the base limit for HostedClusters per namespace.
	DefaultClusterLimit int32 = 50
	// DefaultNodePoolLimit is the base limit for NodePools per namespace.
	DefaultNodePoolLimit int32 = 500

	// DefaultAutoApproveThresholdClusters is the default auto-approve ceiling for clusters (3× base).
	DefaultAutoApproveThresholdClusters int32 = 150
	// DefaultAutoApproveThresholdNodePools is the default auto-approve ceiling for nodepools (3× base).
	DefaultAutoApproveThresholdNodePools int32 = 1500
)

// Enforcer checks whether creating a resource of the given kind in the
// given namespace would exceed the namespace's quota. If the quota would be
// exceeded it returns a non-nil error whose message is suitable for surfacing
// directly to the API caller.
type Enforcer interface {
	CheckQuota(ctx context.Context, namespace, resource string) error
}

// StoreEnforcer implements Enforcer using the platform API stores for Quota
// objects and the resource being created.
type StoreEnforcer struct {
	quotaStore    storage.ResourceStore
	clusterStore  storage.ResourceStore
	nodepoolStore storage.ResourceStore
}

// NewStoreEnforcer creates a StoreEnforcer using the provided stores.
func NewStoreEnforcer(
	quotaStore storage.ResourceStore,
	clusterStore storage.ResourceStore,
	nodepoolStore storage.ResourceStore,
) *StoreEnforcer {
	return &StoreEnforcer{
		quotaStore:    quotaStore,
		clusterStore:  clusterStore,
		nodepoolStore: nodepoolStore,
	}
}

// CheckQuota verifies that creating one more resource of the given kind in the
// given namespace would not exceed the namespace's quota limit.
//
// If the quota store is unavailable the call fails closed: an internal-server
// error is returned and the creation request is rejected. This prevents quota
// bypass during a storage outage.
//
// TOCTOU note: the count read and the subsequent object create in the handler
// are not atomic. Concurrent creation requests in the same namespace can all
// observe current < limit and all succeed, briefly exceeding the limit. The
// window is small at normal request rates. Closing it requires a transactional
// increment-with-compare or a per-namespace advisory lock inside the storage
// backend, which is deferred to a follow-up.
func (e *StoreEnforcer) CheckQuota(ctx context.Context, namespace, resource string) error {
	limit, err := e.effectiveLimit(ctx, namespace, resource)
	if err != nil {
		return apierrors.NewInternalError(fmt.Errorf("quota check failed: unable to read quota for namespace %q: %w", namespace, err))
	}

	current, err := e.countCurrent(ctx, namespace, resource)
	if err != nil {
		return apierrors.NewInternalError(fmt.Errorf("quota check failed: %w", err))
	}

	if current >= limit {
		return QuotaExceededError(namespace, resource, current, limit)
	}
	return nil
}

// effectiveLimit returns the enforced limit for resource in namespace.
// It fails closed: if the Quota object cannot be read, it returns an error
// rather than silently falling back to the default limit.
// A missing Quota object (NotFound) is the only accepted absence — in that
// case the hard-coded default limit is returned.
func (e *StoreEnforcer) effectiveLimit(ctx context.Context, namespace, resource string) (int32, error) {
	obj, err := e.quotaStore.Get(ctx, namespace, QuotaName)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// No Quota object yet — quota controller hasn't bootstrapped it.
			// Apply the hard-coded default; this is not a storage failure.
			return DefaultLimit(resource), nil
		}
		return 0, err
	}

	quota, ok := obj.(*privatev1.Quota)
	if !ok {
		return 0, fmt.Errorf("quota store returned unexpected type %T", obj)
	}

	return EffectiveLimit(quota, resource), nil
}

func (e *StoreEnforcer) countCurrent(ctx context.Context, namespace, resource string) (int32, error) {
	store := e.storeFor(resource)
	if store == nil {
		return 0, fmt.Errorf("no store available for resource %q", resource)
	}

	list, err := store.List(ctx, storage.ListOptions{Namespace: namespace})
	if err != nil {
		return 0, fmt.Errorf("listing %s in namespace %q: %w", resource, namespace, err)
	}

	items, err := meta.ExtractList(list)
	if err != nil {
		return 0, fmt.Errorf("extracting list items for %s: %w", resource, err)
	}
	return int32(len(items)), nil
}

func (e *StoreEnforcer) storeFor(resource string) storage.ResourceStore {
	switch resource {
	case "hostedclusters":
		return e.clusterStore
	case "nodepools":
		return e.nodepoolStore
	default:
		return nil
	}
}

// ─── Pure business logic (package-level, easily unit-tested) ─────────────────

// EffectiveLimit returns the enforced limit for the given resource from the
// Quota spec, falling back to DefaultLimit when the resource entry is absent.
func EffectiveLimit(quota *privatev1.Quota, resource string) int32 {
	for _, r := range quota.Spec.Resources {
		if r.Resource == resource {
			return r.Limit
		}
	}
	return DefaultLimit(resource)
}

// DefaultLimit returns the hard-coded base limit for a resource when no Quota
// object exists or the spec entry is absent.
func DefaultLimit(resource string) int32 {
	if resource == "nodepools" {
		return DefaultNodePoolLimit
	}
	return DefaultClusterLimit
}

// QuotaExceededError returns a *apierrors.StatusError with HTTP 403 Forbidden
// and reason "QuotaExceeded". The Status object carries a structured
// StatusDetails payload so that API clients can extract the current usage and
// enforced limit without parsing the human-readable message string.
//
// The HTTP 403 status follows Google API conventions for quota exhaustion, as
// opposed to HTTP 429 (rate-limit) or HTTP 507 (storage exhaustion).
func QuotaExceededError(namespace, resource string, current, limit int32) error {
	msg := fmt.Sprintf(
		"quota exceeded: namespace %q has %d %s (limit: %d); "+
			"submit a QuotaRequest to request a limit increase",
		namespace, current, resource, limit,
	)

	return &apierrors.StatusError{ErrStatus: metav1.Status{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Status",
		},
		Status:  metav1.StatusFailure,
		Message: msg,
		Reason:  "QuotaExceeded",
		Details: &metav1.StatusDetails{
			// Kind identifies the resource whose quota was exceeded.
			Kind: resource,
			// Causes carries the structured quota state for programmatic clients.
			Causes: []metav1.StatusCause{
				{
					Type:    "QuotaExceeded",
					Message: fmt.Sprintf("current %s count in namespace %q is %d", resource, namespace, current),
					Field:   "quota.current",
				},
				{
					Type:    "QuotaExceeded",
					Message: fmt.Sprintf("enforced %s limit in namespace %q is %d", resource, namespace, limit),
					Field:   "quota.limit",
				},
			},
		},
		Code: 403,
	}}
}
