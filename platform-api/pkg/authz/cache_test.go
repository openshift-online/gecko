package authz

import (
	"sync"
	"testing"

	cedartypes "github.com/cedar-policy/cedar-go/types"
)

func TestEntityCache_Miss(t *testing.T) {
	c := NewEntityCache()
	em, ok := c.Get("nobody@example.com")
	if ok {
		t.Error("expected cache miss, got hit")
	}
	if em != nil {
		t.Error("expected nil entity map on miss")
	}
}

func TestEntityCache_SetAndGet(t *testing.T) {
	c := NewEntityCache()
	em := make(cedartypes.EntityMap)
	c.Set("user@example.com", em)

	got, ok := c.Get("user@example.com")
	if !ok {
		t.Fatal("expected cache hit after Set")
	}
	if got == nil {
		t.Fatal("expected non-nil entity map")
	}
}

func TestEntityCache_Invalidate(t *testing.T) {
	c := NewEntityCache()
	c.Set("a@example.com", make(cedartypes.EntityMap))
	c.Set("b@example.com", make(cedartypes.EntityMap))

	if c.Len() != 2 {
		t.Fatalf("expected len 2, got %d", c.Len())
	}

	// Invalidate only "a"
	c.Invalidate("a@example.com")

	if _, ok := c.Get("a@example.com"); ok {
		t.Error("expected cache miss for a after Invalidate")
	}
	if _, ok := c.Get("b@example.com"); !ok {
		t.Error("expected cache hit for b after invalidating only a")
	}
	if c.Len() != 1 {
		t.Fatalf("expected len 1 after invalidating one entry, got %d", c.Len())
	}
}

func TestEntityCache_InvalidateAll(t *testing.T) {
	c := NewEntityCache()
	c.Set("a@example.com", make(cedartypes.EntityMap))
	c.Set("b@example.com", make(cedartypes.EntityMap))
	c.Set("c@example.com", make(cedartypes.EntityMap))

	if c.Len() != 3 {
		t.Fatalf("expected len 3, got %d", c.Len())
	}

	c.InvalidateAll()

	if c.Len() != 0 {
		t.Fatalf("expected len 0 after InvalidateAll, got %d", c.Len())
	}
	if _, ok := c.Get("a@example.com"); ok {
		t.Error("expected miss for a after InvalidateAll")
	}
}

func TestEntityCache_Len(t *testing.T) {
	c := NewEntityCache()
	if c.Len() != 0 {
		t.Fatalf("expected len 0 for new cache, got %d", c.Len())
	}

	c.Set("a@example.com", make(cedartypes.EntityMap))
	if c.Len() != 1 {
		t.Fatalf("expected len 1, got %d", c.Len())
	}

	c.Set("b@example.com", make(cedartypes.EntityMap))
	if c.Len() != 2 {
		t.Fatalf("expected len 2, got %d", c.Len())
	}

	// Overwrite existing key — length should stay the same.
	c.Set("a@example.com", make(cedartypes.EntityMap))
	if c.Len() != 2 {
		t.Fatalf("expected len 2 after overwrite, got %d", c.Len())
	}
}

func TestEntityCache_ConcurrentSafety(t *testing.T) {
	c := NewEntityCache()
	const goroutines = 50
	var wg sync.WaitGroup

	// Concurrent writes
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(i int) {
			defer wg.Done()
			email := "user" + string(rune('A'+i)) + "@example.com"
			c.Set(email, make(cedartypes.EntityMap))
		}(i)
	}
	wg.Wait()

	// Concurrent reads
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(i int) {
			defer wg.Done()
			email := "user" + string(rune('A'+i)) + "@example.com"
			c.Get(email)
		}(i)
	}
	wg.Wait()

	// Concurrent mixed operations: read, write, invalidate, len
	wg.Add(goroutines * 4)
	for i := 0; i < goroutines; i++ {
		go func(i int) {
			defer wg.Done()
			email := "mix" + string(rune('A'+i)) + "@example.com"
			c.Set(email, make(cedartypes.EntityMap))
		}(i)
		go func(i int) {
			defer wg.Done()
			email := "mix" + string(rune('A'+i)) + "@example.com"
			c.Get(email)
		}(i)
		go func(i int) {
			defer wg.Done()
			email := "mix" + string(rune('A'+i)) + "@example.com"
			c.Invalidate(email)
		}(i)
		go func(i int) {
			defer wg.Done()
			c.Len()
		}(i)
	}
	wg.Wait()

	// If we got here without a race detector panic, concurrency is safe.
}
