package authz

import (
	"context"
	"testing"

	cedar "github.com/cedar-policy/cedar-go"
	cedartypes "github.com/cedar-policy/cedar-go/types"
)

func TestBuildEntityMap_NoBindings(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()

	em, err := h.eg.BuildEntityMap(ctx, "nobody@example.com")
	if err != nil {
		t.Fatalf("BuildEntityMap: %v", err)
	}

	// Should contain the user entity and the platform entity only.
	userUID := cedar.NewEntityUID(TypeUser, cedartypes.String("nobody@example.com"))
	userEnt, ok := em.Get(userUID)
	if !ok {
		t.Fatal("expected user entity in map")
	}
	// User should have no parent roles.
	count := 0
	for range userEnt.Parents.All() {
		count++
	}
	if count != 0 {
		t.Errorf("expected 0 parent roles for unbound user, got %d", count)
	}

	// Platform entity should exist.
	platformUID := cedar.NewEntityUID(TypePlatform, cedartypes.String(PlatformEntity))
	if _, ok := em.Get(platformUID); !ok {
		t.Error("expected platform entity in map")
	}
}

func TestBuildEntityMap_OneNamespaceBinding(t *testing.T) {
	h := newTestHarness(t)
	h.createRoleBinding(t, "rb1", "ns1", "user@example.com", "cluster-viewer")
	ctx := context.Background()

	em, err := h.eg.BuildEntityMap(ctx, "user@example.com")
	if err != nil {
		t.Fatalf("BuildEntityMap: %v", err)
	}

	// User entity
	userUID := cedar.NewEntityUID(TypeUser, cedartypes.String("user@example.com"))
	userEnt, ok := em.Get(userUID)
	if !ok {
		t.Fatal("expected user entity")
	}

	// User should have one parent: the NamespaceRole ns1/cluster-viewer
	roleUID := cedar.NewEntityUID(TypeNamespaceRole, cedartypes.String("ns1/cluster-viewer"))
	found := false
	for parent := range userEnt.Parents.All() {
		if parent == roleUID {
			found = true
		}
	}
	if !found {
		t.Error("expected user to have NamespaceRole ns1/cluster-viewer as parent")
	}

	// NamespaceRole → Namespace
	roleEnt, ok := em.Get(roleUID)
	if !ok {
		t.Fatal("expected NamespaceRole entity")
	}
	nsUID := cedar.NewEntityUID(TypeNamespace, cedartypes.String("ns1"))
	foundNS := false
	for parent := range roleEnt.Parents.All() {
		if parent == nsUID {
			foundNS = true
		}
	}
	if !foundNS {
		t.Error("expected NamespaceRole to have Namespace ns1 as parent")
	}

	// Namespace → Platform
	nsEnt, ok := em.Get(nsUID)
	if !ok {
		t.Fatal("expected Namespace entity")
	}
	platformUID := cedar.NewEntityUID(TypePlatform, cedartypes.String(PlatformEntity))
	foundPlatform := false
	for parent := range nsEnt.Parents.All() {
		if parent == platformUID {
			foundPlatform = true
		}
	}
	if !foundPlatform {
		t.Error("expected Namespace to have Platform as parent")
	}
}

func TestBuildEntityMap_MultipleNamespaceBindings(t *testing.T) {
	h := newTestHarness(t)
	h.createRoleBinding(t, "rb1", "ns1", "user@example.com", "cluster-viewer")
	h.createRoleBinding(t, "rb2", "ns2", "user@example.com", "cluster-admin")
	ctx := context.Background()

	em, err := h.eg.BuildEntityMap(ctx, "user@example.com")
	if err != nil {
		t.Fatalf("BuildEntityMap: %v", err)
	}

	userUID := cedar.NewEntityUID(TypeUser, cedartypes.String("user@example.com"))
	userEnt, ok := em.Get(userUID)
	if !ok {
		t.Fatal("expected user entity")
	}

	// User should have two parent roles.
	role1 := cedar.NewEntityUID(TypeNamespaceRole, cedartypes.String("ns1/cluster-viewer"))
	role2 := cedar.NewEntityUID(TypeNamespaceRole, cedartypes.String("ns2/cluster-admin"))
	foundRole1, foundRole2 := false, false
	for parent := range userEnt.Parents.All() {
		if parent == role1 {
			foundRole1 = true
		}
		if parent == role2 {
			foundRole2 = true
		}
	}
	if !foundRole1 {
		t.Error("expected user to have NamespaceRole ns1/cluster-viewer as parent")
	}
	if !foundRole2 {
		t.Error("expected user to have NamespaceRole ns2/cluster-admin as parent")
	}

	// Both namespaces should exist.
	ns1UID := cedar.NewEntityUID(TypeNamespace, cedartypes.String("ns1"))
	ns2UID := cedar.NewEntityUID(TypeNamespace, cedartypes.String("ns2"))
	if _, ok := em.Get(ns1UID); !ok {
		t.Error("expected Namespace ns1 entity")
	}
	if _, ok := em.Get(ns2UID); !ok {
		t.Error("expected Namespace ns2 entity")
	}
}

func TestBuildEntityMap_PlatformBinding(t *testing.T) {
	h := newTestHarness(t)
	h.createPlatformRoleBinding(t, "prb1", "padmin@example.com", "platform-admin")
	ctx := context.Background()

	em, err := h.eg.BuildEntityMap(ctx, "padmin@example.com")
	if err != nil {
		t.Fatalf("BuildEntityMap: %v", err)
	}

	userUID := cedar.NewEntityUID(TypeUser, cedartypes.String("padmin@example.com"))
	userEnt, ok := em.Get(userUID)
	if !ok {
		t.Fatal("expected user entity")
	}

	// User should have PlatformRole as parent
	roleUID := cedar.NewEntityUID(TypePlatformRole, cedartypes.String("platform-admin"))
	found := false
	for parent := range userEnt.Parents.All() {
		if parent == roleUID {
			found = true
		}
	}
	if !found {
		t.Error("expected user to have PlatformRole platform-admin as parent")
	}

	// PlatformRole → Platform
	roleEnt, ok := em.Get(roleUID)
	if !ok {
		t.Fatal("expected PlatformRole entity")
	}
	platformUID := cedar.NewEntityUID(TypePlatform, cedartypes.String(PlatformEntity))
	foundPlatform := false
	for parent := range roleEnt.Parents.All() {
		if parent == platformUID {
			foundPlatform = true
		}
	}
	if !foundPlatform {
		t.Error("expected PlatformRole to have Platform as parent")
	}
}

func TestBuildEntityMap_BothNamespaceAndPlatformBindings(t *testing.T) {
	h := newTestHarness(t)
	h.createRoleBinding(t, "rb1", "ns1", "both@example.com", "cluster-viewer")
	h.createPlatformRoleBinding(t, "prb1", "both@example.com", "platform-admin")
	ctx := context.Background()

	em, err := h.eg.BuildEntityMap(ctx, "both@example.com")
	if err != nil {
		t.Fatalf("BuildEntityMap: %v", err)
	}

	userUID := cedar.NewEntityUID(TypeUser, cedartypes.String("both@example.com"))
	userEnt, ok := em.Get(userUID)
	if !ok {
		t.Fatal("expected user entity")
	}

	nsRoleUID := cedar.NewEntityUID(TypeNamespaceRole, cedartypes.String("ns1/cluster-viewer"))
	platRoleUID := cedar.NewEntityUID(TypePlatformRole, cedartypes.String("platform-admin"))
	foundNS, foundPlat := false, false
	for parent := range userEnt.Parents.All() {
		if parent == nsRoleUID {
			foundNS = true
		}
		if parent == platRoleUID {
			foundPlat = true
		}
	}
	if !foundNS {
		t.Error("expected user to have NamespaceRole ns1/cluster-viewer as parent")
	}
	if !foundPlat {
		t.Error("expected user to have PlatformRole platform-admin as parent")
	}

	// Verify both hierarchies exist
	if _, ok := em.Get(nsRoleUID); !ok {
		t.Error("expected NamespaceRole entity")
	}
	if _, ok := em.Get(platRoleUID); !ok {
		t.Error("expected PlatformRole entity")
	}
	nsUID := cedar.NewEntityUID(TypeNamespace, cedartypes.String("ns1"))
	if _, ok := em.Get(nsUID); !ok {
		t.Error("expected Namespace entity")
	}
}

func TestAuthorizedNamespaces_WithBindings(t *testing.T) {
	h := newTestHarness(t)
	h.createRoleBinding(t, "rb1", "alpha", "user@example.com", "cluster-viewer")
	h.createRoleBinding(t, "rb2", "beta", "user@example.com", "cluster-admin")
	ctx := context.Background()

	namespaces, err := h.eg.AuthorizedNamespaces(ctx, "user@example.com")
	if err != nil {
		t.Fatalf("AuthorizedNamespaces: %v", err)
	}

	nsSet := make(map[string]bool, len(namespaces))
	for _, ns := range namespaces {
		nsSet[ns] = true
	}

	if !nsSet["alpha"] {
		t.Error("expected alpha in authorized namespaces")
	}
	if !nsSet["beta"] {
		t.Error("expected beta in authorized namespaces")
	}
	if len(namespaces) != 2 {
		t.Errorf("expected 2 authorized namespaces, got %d: %v", len(namespaces), namespaces)
	}
}

func TestAuthorizedNamespaces_NoBindings(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()

	namespaces, err := h.eg.AuthorizedNamespaces(ctx, "nobody@example.com")
	if err != nil {
		t.Fatalf("AuthorizedNamespaces: %v", err)
	}
	if len(namespaces) != 0 {
		t.Errorf("expected 0 authorized namespaces, got %d: %v", len(namespaces), namespaces)
	}
}

func TestAddResourceEntity(t *testing.T) {
	em := make(cedartypes.EntityMap)

	nsUID := cedar.NewEntityUID(TypeNamespace, cedartypes.String("ns1"))
	em[nsUID] = cedartypes.Entity{
		UID:        nsUID,
		Parents:    cedar.NewEntityUIDSet(),
		Attributes: cedartypes.NewRecord(nil),
	}

	// Add a cluster entity with the namespace as parent.
	AddResourceEntity(em, TypeCluster, "ns1/my-cluster", nsUID)

	clusterUID := cedar.NewEntityUID(cedartypes.EntityType(TypeCluster), cedartypes.String("ns1/my-cluster"))
	ent, ok := em.Get(clusterUID)
	if !ok {
		t.Fatal("expected cluster entity in map after AddResourceEntity")
	}

	foundParent := false
	for parent := range ent.Parents.All() {
		if parent == nsUID {
			foundParent = true
		}
	}
	if !foundParent {
		t.Error("expected cluster entity to have namespace as parent")
	}
}
