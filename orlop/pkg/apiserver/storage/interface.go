package storage

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// authorizedNamespacesKey is the context key for authorized namespaces
// injected by authorization middleware for cross-namespace list queries.
type authorizedNamespacesKey struct{}

// ContextWithAuthorizedNamespaces returns a new context with the authorized
// namespace set. Used by authorization middleware to restrict cross-namespace
// list queries to only the namespaces the user has access to.
func ContextWithAuthorizedNamespaces(ctx context.Context, namespaces []string) context.Context {
	return context.WithValue(ctx, authorizedNamespacesKey{}, namespaces)
}

// AuthorizedNamespacesFromContext retrieves the authorized namespaces from the
// context. Returns nil if not set (meaning no namespace restriction applies).
func AuthorizedNamespacesFromContext(ctx context.Context) []string {
	v, _ := ctx.Value(authorizedNamespacesKey{}).([]string)
	return v
}

// ListOptions extends metav1.ListOptions with storage-specific options.
type ListOptions struct {
	metav1.ListOptions

	// Namespace limits results to a specific namespace.
	// Empty string means all namespaces.
	Namespace string

	// Namespaces limits results to the specified set of namespaces.
	// When non-empty and Namespace is empty, the storage layer filters results
	// to only these namespaces (used for cross-namespace list authorization).
	// Namespace takes precedence over Namespaces if both are set.
	Namespaces []string

	// ShardSelector specifies which shard of results to return.
	// Nil means return all results (no sharding).
	ShardSelector *ShardSelector

	// FieldFilters specifies field-based filtering. Keys are dot-separated
	// JSON paths (e.g., "spec.clusterID"), values are expected string values.
	FieldFilters map[string]string
}

// ShardSelector represents a shard selection for list/watch operations.
type ShardSelector struct {
	// Index is the shard index (0-based)
	Index int
	// Count is the total number of shards
	Count int
}

// ResourceStore defines the interface for storing and retrieving resources.
// Uses client.Object which combines metav1.Object and runtime.Object.
type ResourceStore interface {
	// Create creates a new resource.
	// If obj.GetName() is empty and obj.GetGenerateName() is set,
	// the store must generate a unique name and set it on obj before persisting.
	Create(ctx context.Context, obj client.Object) error

	// Get retrieves a resource by namespace and name.
	Get(ctx context.Context, namespace, name string) (client.Object, error)

	// List lists all resources matching the given options.
	// Returns a properly typed list object with metadata.
	// Storage implementations should handle:
	// - Namespace filtering
	// - Label selector filtering
	// - Shard-based filtering (if ShardSelector provided)
	List(ctx context.Context, opts ListOptions) (client.ObjectList, error)

	// Update updates an existing resource.
	Update(ctx context.Context, obj client.Object) error

	// Delete deletes a resource by namespace and name.
	Delete(ctx context.Context, namespace, name string) error

	// Watch starts watching for changes starting from the given resource version.
	// Returns a channel that receives watch events and a stop function to end the watch.
	// Storage implementations should filter events by:
	// - Namespace (if specified in opts)
	// - Label selector (if specified in opts)
	// - Shard (if ShardSelector provided in opts)
	Watch(ctx context.Context, opts ListOptions, resourceVersion string) (<-chan ResourceEvent, func(), error)
}
