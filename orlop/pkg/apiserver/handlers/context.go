package handlers

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime"
)

type authorizedNamespacesKey struct{}

// WithAuthorizedNamespaces returns a new context carrying the list of namespaces
// the user is authorized to access. The handler will use these to filter list results.
func WithAuthorizedNamespaces(ctx context.Context, ns []string) context.Context {
	return context.WithValue(ctx, authorizedNamespacesKey{}, ns)
}

// AuthorizedNamespacesFromContext extracts the authorized namespaces from the context.
// Returns nil if no authorized namespaces are set.
func AuthorizedNamespacesFromContext(ctx context.Context) []string {
	ns, _ := ctx.Value(authorizedNamespacesKey{}).([]string)
	return ns
}

type itemFilterKey struct{}

// ItemFilterFunc is called for each item in a list response (on the private object,
// before conversion). Return true to include the item, false to exclude it.
type ItemFilterFunc func(ctx context.Context, obj runtime.Object) bool

// WithItemFilter returns a new context carrying the given item filter function.
// The converting handler applies this filter to each list item before including
// it in the response, enabling per-item authorization checks.
func WithItemFilter(ctx context.Context, fn ItemFilterFunc) context.Context {
	return context.WithValue(ctx, itemFilterKey{}, fn)
}

// ItemFilterFromContext retrieves the ItemFilterFunc from the context.
// Returns nil if no filter is set.
func ItemFilterFromContext(ctx context.Context) ItemFilterFunc {
	fn, _ := ctx.Value(itemFilterKey{}).(ItemFilterFunc)
	return fn
}
