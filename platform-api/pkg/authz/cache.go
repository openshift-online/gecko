package authz

import (
	"sync"

	cedar "github.com/cedar-policy/cedar-go"
	"github.com/cedar-policy/cedar-go/types"
)

// EntityCache caches Cedar entity maps keyed by user email.
// The cache is invalidated per-subject when RoleBindings or PlatformRoleBindings
// are created, updated, or deleted.
type EntityCache struct {
	mu      sync.RWMutex
	entries map[string]types.EntityMap // keyed by user email
}

// NewEntityCache creates a new empty entity cache.
func NewEntityCache() *EntityCache {
	return &EntityCache{
		entries: make(map[string]types.EntityMap),
	}
}

// Get retrieves the cached entity map for a user. Returns nil, false on cache miss.
func (c *EntityCache) Get(email string) (types.EntityMap, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	em, ok := c.entries[email]
	return em, ok
}

// Set stores an entity map for a user in the cache.
func (c *EntityCache) Set(email string, em types.EntityMap) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[email] = em
}

// Invalidate removes the cached entity map for one or more users.
// Called when a RoleBinding/PlatformRoleBinding is written for these subjects.
func (c *EntityCache) Invalidate(emails ...string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, email := range emails {
		delete(c.entries, email)
	}
}

// InvalidateAll clears the entire cache.
func (c *EntityCache) InvalidateAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]types.EntityMap)
}

// Len returns the number of cached entries.
func (c *EntityCache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}

// Ensure EntityMap satisfies EntityGetter at compile time.
var _ types.EntityGetter = (types.EntityMap)(nil)
var _ cedar.PolicyIterator = (*cedar.PolicySet)(nil)
