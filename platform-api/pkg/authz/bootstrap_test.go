package authz

import (
	"context"
	"testing"

	privatev1 "github.com/openshift-online/gecko/platform-api/api/private/v1"
	"github.com/openshift-online/gecko/orlop/pkg/apiserver/storage"
	"github.com/openshift-online/gecko/orlop/pkg/apiserver/storage/memory"
	"k8s.io/apimachinery/pkg/runtime"
	runtimeschema "k8s.io/apimachinery/pkg/runtime/schema"
)

func newPRBStore(t *testing.T) *memory.MemoryStore {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := privatev1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	return memory.NewMemoryStore("platformrolebindings", scheme, runtimeschema.GroupVersionKind{
		Group: "gcp.managed.openshift.io", Version: "v1", Kind: "PlatformRoleBinding",
	})
}

func listPRBs(t *testing.T, store *memory.MemoryStore) []privatev1.PlatformRoleBinding {
	t.Helper()
	list, err := store.List(context.Background(), storage.ListOptions{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	prbList, ok := list.(*privatev1.PlatformRoleBindingList)
	if !ok {
		t.Fatalf("expected *PlatformRoleBindingList, got %T", list)
	}
	return prbList.Items
}

func TestRunBootstrap_CreatesBindings(t *testing.T) {
	store := newPRBStore(t)
	bindings := []BootstrapBinding{
		{Name: "admin-binding", Subject: "admin@example.com", RoleRef: "platform-admin"},
		{Name: "ops-binding", Subject: "ops@example.com", RoleRef: "platform-admin"},
	}

	if err := RunBootstrap(context.Background(), store, bindings); err != nil {
		t.Fatalf("RunBootstrap: %v", err)
	}

	items := listPRBs(t, store)
	if len(items) != 2 {
		t.Fatalf("expected 2 PlatformRoleBindings, got %d", len(items))
	}

	// Build a map for easier lookup.
	byName := make(map[string]privatev1.PlatformRoleBinding, len(items))
	for _, item := range items {
		byName[item.Name] = item
	}

	prb, ok := byName["admin-binding"]
	if !ok {
		t.Fatal("expected admin-binding to exist")
	}
	if prb.Spec.Subject != "admin@example.com" {
		t.Errorf("expected subject admin@example.com, got %s", prb.Spec.Subject)
	}
	if prb.Spec.RoleRef != "platform-admin" {
		t.Errorf("expected roleRef platform-admin, got %s", prb.Spec.RoleRef)
	}

	prb2, ok := byName["ops-binding"]
	if !ok {
		t.Fatal("expected ops-binding to exist")
	}
	if prb2.Spec.Subject != "ops@example.com" {
		t.Errorf("expected subject ops@example.com, got %s", prb2.Spec.Subject)
	}
}

func TestRunBootstrap_Idempotent(t *testing.T) {
	store := newPRBStore(t)
	bindings := []BootstrapBinding{
		{Name: "admin-binding", Subject: "admin@example.com", RoleRef: "platform-admin"},
	}

	// First call creates the binding.
	if err := RunBootstrap(context.Background(), store, bindings); err != nil {
		t.Fatalf("RunBootstrap (first): %v", err)
	}
	items := listPRBs(t, store)
	if len(items) != 1 {
		t.Fatalf("expected 1 binding after first call, got %d", len(items))
	}

	// Second call should skip the existing binding without error.
	if err := RunBootstrap(context.Background(), store, bindings); err != nil {
		t.Fatalf("RunBootstrap (second): %v", err)
	}
	items = listPRBs(t, store)
	if len(items) != 1 {
		t.Fatalf("expected 1 binding after idempotent second call, got %d", len(items))
	}
}

func TestRunBootstrap_EmptyBindings(t *testing.T) {
	store := newPRBStore(t)

	if err := RunBootstrap(context.Background(), store, nil); err != nil {
		t.Fatalf("RunBootstrap with nil bindings: %v", err)
	}

	if err := RunBootstrap(context.Background(), store, []BootstrapBinding{}); err != nil {
		t.Fatalf("RunBootstrap with empty bindings: %v", err)
	}

	items := listPRBs(t, store)
	if len(items) != 0 {
		t.Fatalf("expected 0 bindings after empty bootstrap, got %d", len(items))
	}
}
