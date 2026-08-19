package authz

import (
	"sync"

	cedar "github.com/cedar-policy/cedar-go"
)

// EntityCache is a simple concurrent cache for Cedar entity maps keyed by user email.
type EntityCache struct {
	m sync.Map
}

// NewEntityCache creates a new empty EntityCache.
func NewEntityCache() *EntityCache {
	return &EntityCache{}
}

// Get retrieves the cached entity map for the given user.
// Returns the entity map and true if found, or nil and false if not cached.
func (c *EntityCache) Get(user string) (cedar.EntityMap, bool) {
	v, ok := c.m.Load(user)
	if !ok {
		return nil, false
	}
	em, ok := v.(cedar.EntityMap)
	return em, ok
}

// Put stores an entity map in the cache for the given user.
func (c *EntityCache) Put(user string, entities cedar.EntityMap) {
	c.m.Store(user, entities)
}

// Invalidate removes the cached entity map for the given user.
func (c *EntityCache) Invalidate(user string) {
	c.m.Delete(user)
}

// InvalidateAll clears the entire cache.
func (c *EntityCache) InvalidateAll() {
	c.m.Range(func(key, _ any) bool {
		c.m.Delete(key)
		return true
	})
}
