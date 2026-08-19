package authz

import (
	"container/list"
	"sync"

	cedar "github.com/cedar-policy/cedar-go"
)

const (
	// maxCacheSize is the maximum number of entries in the entity cache.
	// When the cache is full, the least recently used entry is evicted.
	maxCacheSize = 1000
)

// EntityCache is a concurrent LRU cache for Cedar entity maps keyed by user email.
// It bounds memory usage by limiting the number of cached entries.
type EntityCache struct {
	mu    sync.Mutex
	m     map[string]*list.Element
	lru   *list.List
	maxSize int
}

type cacheEntry struct {
	user     string
	entities cedar.EntityMap
}

// NewEntityCache creates a new empty EntityCache with a default size limit.
func NewEntityCache() *EntityCache {
	return &EntityCache{
		m:       make(map[string]*list.Element),
		lru:     list.New(),
		maxSize: maxCacheSize,
	}
}

// Get retrieves the cached entity map for the given user, updating recency.
// Returns the entity map and true if found, or nil and false if not cached.
// The returned entity map is shared and must not be mutated by the caller.
func (c *EntityCache) Get(user string) (cedar.EntityMap, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	elem, ok := c.m[user]
	if !ok {
		return nil, false
	}

	// Move to front (most recently used).
	c.lru.MoveToFront(elem)
	entry := elem.Value.(*cacheEntry)
	return entry.entities, true
}

// Put stores an entity map in the cache for the given user.
// If the cache is at capacity, the least recently used entry is evicted.
func (c *EntityCache) Put(user string, entities cedar.EntityMap) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// If already cached, update and move to front.
	if elem, ok := c.m[user]; ok {
		c.lru.MoveToFront(elem)
		elem.Value.(*cacheEntry).entities = entities
		return
	}

	// Add new entry at front (most recently used).
	entry := &cacheEntry{user: user, entities: entities}
	elem := c.lru.PushFront(entry)
	c.m[user] = elem

	// Evict least recently used if at capacity.
	if c.lru.Len() > c.maxSize {
		back := c.lru.Back()
		c.lru.Remove(back)
		delete(c.m, back.Value.(*cacheEntry).user)
	}
}

// Invalidate removes the cached entity map for the given user.
func (c *EntityCache) Invalidate(user string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if elem, ok := c.m[user]; ok {
		c.lru.Remove(elem)
		delete(c.m, user)
	}
}

// InvalidateAll clears the entire cache.
func (c *EntityCache) InvalidateAll() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.m = make(map[string]*list.Element)
	c.lru = list.New()
}
