package authz

import (
	"testing"

	cedar "github.com/cedar-policy/cedar-go"
)

func TestEntityCache_PutAndGet(t *testing.T) {
	cache := NewEntityCache()

	entities := make(cedar.EntityMap)
	uid := cedar.NewEntityUID("User", cedar.String("alice@example.com"))
	entities[uid] = cedar.Entity{
		UID:        uid,
		Parents:    cedar.NewEntityUIDSet(),
		Attributes: cedar.NewRecord(cedar.RecordMap{}),
	}

	cache.Put("alice@example.com", entities)

	got, ok := cache.Get("alice@example.com")
	if !ok {
		t.Fatal("expected cache hit")
	}
	if len(got) != 1 {
		t.Fatalf("got %d entities, want 1", len(got))
	}
}

func TestEntityCache_Miss(t *testing.T) {
	cache := NewEntityCache()

	_, ok := cache.Get("unknown@example.com")
	if ok {
		t.Fatal("expected cache miss")
	}
}

func TestEntityCache_Invalidate(t *testing.T) {
	cache := NewEntityCache()
	entities := make(cedar.EntityMap)
	cache.Put("alice@example.com", entities)

	cache.Invalidate("alice@example.com")

	_, ok := cache.Get("alice@example.com")
	if ok {
		t.Fatal("expected cache miss after invalidation")
	}
}

func TestEntityCache_InvalidateAll(t *testing.T) {
	cache := NewEntityCache()
	cache.Put("alice@example.com", make(cedar.EntityMap))
	cache.Put("bob@example.com", make(cedar.EntityMap))

	cache.InvalidateAll()

	if _, ok := cache.Get("alice@example.com"); ok {
		t.Fatal("expected cache miss for alice after InvalidateAll")
	}
	if _, ok := cache.Get("bob@example.com"); ok {
		t.Fatal("expected cache miss for bob after InvalidateAll")
	}
}
