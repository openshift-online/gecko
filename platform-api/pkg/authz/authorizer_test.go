package authz

import (
	"context"
	"fmt"
	"testing"

	publicv1 "github.com/openshift-online/gecko/platform-api/api/public/v1"
	"github.com/openshift-online/gecko/orlop/pkg/apiserver/storage/memory"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	runtimeschema "k8s.io/apimachinery/pkg/runtime/schema"
)

// builtinRoles returns the four standard built-in role definitions used
// throughout the test suite.
func builtinRoles() []RoleDefinition {
	return []RoleDefinition{
		{Name: "cluster-viewer", Scope: "namespace", Permissions: []string{
			"cluster.list", "cluster.get", "nodepool.list", "nodepool.get"}},
		{Name: "cluster-admin", Scope: "namespace", Permissions: []string{
			"cluster.create", "cluster.list", "cluster.get", "cluster.update", "cluster.delete",
			"nodepool.create", "nodepool.list", "nodepool.get", "nodepool.update", "nodepool.delete"}},
		{Name: "service-admin", Scope: "namespace", Permissions: []string{
			"rolebinding.create", "rolebinding.list", "rolebinding.get", "rolebinding.update", "rolebinding.delete",
			"customrole.create", "customrole.list", "customrole.get", "customrole.update", "customrole.delete"}},
		{Name: "platform-admin", Scope: "platform", Permissions: []string{
			"platformrolebinding.create", "platformrolebinding.list", "platformrolebinding.get",
			"platformrolebinding.update", "platformrolebinding.delete"}},
	}
}

// testHarness holds all the components needed to run authorizer integration tests.
type testHarness struct {
	authorizer *Authorizer
	rbStore    *memory.MemoryStore
	prbStore   *memory.MemoryStore
	cache      *EntityCache
	eg         *EntityGetter
}

// newTestHarness creates an in-memory test environment with the built-in roles
// wired into a fully functional Authorizer.
func newTestHarness(t *testing.T) *testHarness {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := publicv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add publicv1 to scheme: %v", err)
	}

	rbGVK := runtimeschema.GroupVersionKind{
		Group: "gcp.managed.openshift.io", Version: "v1", Kind: "RoleBinding",
	}
	prbGVK := runtimeschema.GroupVersionKind{
		Group: "gcp.managed.openshift.io", Version: "v1", Kind: "PlatformRoleBinding",
	}

	rbStore := memory.NewMemoryStore("rolebindings", scheme, rbGVK)
	prbStore := memory.NewMemoryStore("platformrolebindings", scheme, prbGVK)

	policies, err := GeneratePolicies(builtinRoles())
	if err != nil {
		t.Fatalf("GeneratePolicies: %v", err)
	}

	cache := NewEntityCache()
	eg := NewEntityGetter(rbStore, prbStore, cache)
	authorizer := NewAuthorizer(policies, eg)

	return &testHarness{
		authorizer: authorizer,
		rbStore:    rbStore,
		prbStore:   prbStore,
		cache:      cache,
		eg:         eg,
	}
}

// createRoleBinding is a helper to create a RoleBinding in the in-memory store.
func (h *testHarness) createRoleBinding(t *testing.T, name, namespace, subject, roleRef string) {
	t.Helper()
	rb := &publicv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec:       publicv1.RoleBindingSpec{Subject: subject, RoleRef: roleRef},
	}
	if err := h.rbStore.Create(context.Background(), rb); err != nil {
		t.Fatalf("failed to create RoleBinding %s/%s: %v", namespace, name, err)
	}
}

// createPlatformRoleBinding is a helper to create a PlatformRoleBinding in the store.
func (h *testHarness) createPlatformRoleBinding(t *testing.T, name, subject, roleRef string) {
	t.Helper()
	prb := &publicv1.PlatformRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       publicv1.PlatformRoleBindingSpec{Subject: subject, RoleRef: roleRef},
	}
	if err := h.prbStore.Create(context.Background(), prb); err != nil {
		t.Fatalf("failed to create PlatformRoleBinding %s: %v", name, err)
	}
}

// --------------------------------------------------------------------------
// How the Authorizer works with Cedar's "principal in resource"
// --------------------------------------------------------------------------
//
// Each generated Cedar policy has:
//   permit ( principal, action in [<role-actions>], resource )
//   when { principal in resource };
//
// The "in" operator checks whether the resource is a transitive ancestor of
// the principal in the entity hierarchy:
//
//   User → NamespaceRole(ns/role) → Namespace(ns) → Platform
//   User → PlatformRole(role)      → Platform
//
// Authorization is checked at the scope entity level:
//   - Namespace-scoped actions → resource = Gecko::Namespace / ns-id
//   - Platform-scoped actions  → resource = Gecko::Platform / "gecko"
//
// See middleware.go (deriveActionAndResource) for the actual HTTP → Cedar mapping.

// --------------------------------------------------------------------------
// Cluster-viewer tests: read-only cluster/nodepool access in a namespace
// --------------------------------------------------------------------------

func TestAuthorizer_ClusterViewer(t *testing.T) {
	h := newTestHarness(t)
	h.createRoleBinding(t, "rb1", "ns1", "viewer@example.com", "cluster-viewer")

	tests := []struct {
		name         string
		action       string
		resourceType string
		resourceID   string
		want         Decision
	}{
		// Allowed: cluster-viewer permits GET/LIST on clusters and nodepools
		// in the bound namespace.
		{"GetCluster", "GetCluster", TypeNamespace, "ns1", Allow},
		{"ListClusters", "ListClusters", TypeNamespace, "ns1", Allow},
		{"GetNodepool", "GetNodepool", TypeNamespace, "ns1", Allow},
		{"ListNodepools", "ListNodepools", TypeNamespace, "ns1", Allow},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := h.authorizer.Authorize(context.Background(), "viewer@example.com",
				tc.action, tc.resourceType, tc.resourceID)
			if err != nil {
				t.Fatalf("Authorize error: %v", err)
			}
			if got != tc.want {
				t.Errorf("Authorize(%q, %q, %q, %q) = %v, want %v",
					"viewer@example.com", tc.action, tc.resourceType, tc.resourceID, got, tc.want)
			}
		})
	}
}

// --------------------------------------------------------------------------
// Cluster-admin tests: full cluster/nodepool CRUD in a namespace
// --------------------------------------------------------------------------

func TestAuthorizer_ClusterAdmin(t *testing.T) {
	h := newTestHarness(t)
	h.createRoleBinding(t, "rb1", "ns1", "admin@example.com", "cluster-admin")

	tests := []struct {
		name         string
		action       string
		resourceType string
		resourceID   string
		want         Decision
	}{
		// Allowed: full CRUD on clusters at namespace scope.
		{"CreateCluster", "CreateCluster", TypeNamespace, "ns1", Allow},
		{"GetCluster", "GetCluster", TypeNamespace, "ns1", Allow},
		{"ListClusters", "ListClusters", TypeNamespace, "ns1", Allow},
		{"UpdateCluster", "UpdateCluster", TypeNamespace, "ns1", Allow},
		{"DeleteCluster", "DeleteCluster", TypeNamespace, "ns1", Allow},
		// Allowed: full CRUD on nodepools at namespace scope.
		{"CreateNodepool", "CreateNodepool", TypeNamespace, "ns1", Allow},
		{"GetNodepool", "GetNodepool", TypeNamespace, "ns1", Allow},
		{"ListNodepools", "ListNodepools", TypeNamespace, "ns1", Allow},
		{"UpdateNodepool", "UpdateNodepool", TypeNamespace, "ns1", Allow},
		{"DeleteNodepool", "DeleteNodepool", TypeNamespace, "ns1", Allow},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := h.authorizer.Authorize(context.Background(), "admin@example.com",
				tc.action, tc.resourceType, tc.resourceID)
			if err != nil {
				t.Fatalf("Authorize error: %v", err)
			}
			if got != tc.want {
				t.Errorf("Authorize(%q, %q, %q, %q) = %v, want %v",
					"admin@example.com", tc.action, tc.resourceType, tc.resourceID, got, tc.want)
			}
		})
	}
}

// --------------------------------------------------------------------------
// Service-admin tests: full rolebinding CRUD in a namespace
// --------------------------------------------------------------------------

func TestAuthorizer_ServiceAdmin(t *testing.T) {
	h := newTestHarness(t)
	h.createRoleBinding(t, "rb1", "ns1", "svc@example.com", "service-admin")

	tests := []struct {
		name         string
		action       string
		resourceType string
		resourceID   string
		want         Decision
	}{
		// Allowed: full CRUD on rolebindings (checked at namespace scope).
		{"CreateRoleBinding", "CreateRoleBinding", TypeNamespace, "ns1", Allow},
		{"GetRoleBinding", "GetRoleBinding", TypeNamespace, "ns1", Allow},
		{"ListRoleBindings", "ListRoleBindings", TypeNamespace, "ns1", Allow},
		{"UpdateRoleBinding", "UpdateRoleBinding", TypeNamespace, "ns1", Allow},
		{"DeleteRoleBinding", "DeleteRoleBinding", TypeNamespace, "ns1", Allow},
		// Allowed: full CRUD on custom roles (checked at namespace scope).
		{"CreateCustomRole", "CreateCustomRole", TypeNamespace, "ns1", Allow},
		{"GetCustomRole", "GetCustomRole", TypeNamespace, "ns1", Allow},
		{"ListCustomRoles", "ListCustomRoles", TypeNamespace, "ns1", Allow},
		{"UpdateCustomRole", "UpdateCustomRole", TypeNamespace, "ns1", Allow},
		{"DeleteCustomRole", "DeleteCustomRole", TypeNamespace, "ns1", Allow},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := h.authorizer.Authorize(context.Background(), "svc@example.com",
				tc.action, tc.resourceType, tc.resourceID)
			if err != nil {
				t.Fatalf("Authorize error: %v", err)
			}
			if got != tc.want {
				t.Errorf("Authorize(%q, %q, %q, %q) = %v, want %v",
					"svc@example.com", tc.action, tc.resourceType, tc.resourceID, got, tc.want)
			}
		})
	}
}

// --------------------------------------------------------------------------
// Platform-admin tests: full platformrolebinding CRUD
// --------------------------------------------------------------------------

func TestAuthorizer_PlatformAdmin(t *testing.T) {
	h := newTestHarness(t)
	h.createPlatformRoleBinding(t, "prb1", "padmin@example.com", "platform-admin")

	tests := []struct {
		name         string
		action       string
		resourceType string
		resourceID   string
		want         Decision
	}{
		// Allowed: full CRUD on platformrolebindings at platform scope.
		{"CreatePlatformRoleBinding", "CreatePlatformRoleBinding", TypePlatform, PlatformEntity, Allow},
		{"GetPlatformRoleBinding", "GetPlatformRoleBinding", TypePlatform, PlatformEntity, Allow},
		{"ListPlatformRoleBindings", "ListPlatformRoleBindings", TypePlatform, PlatformEntity, Allow},
		{"UpdatePlatformRoleBinding", "UpdatePlatformRoleBinding", TypePlatform, PlatformEntity, Allow},
		{"DeletePlatformRoleBinding", "DeletePlatformRoleBinding", TypePlatform, PlatformEntity, Allow},
		// Denied: platform-admin has no namespace RoleBinding, so namespace-
		// scoped resources are not accessible (user is NOT "in" any namespace).
		{"CreateCluster", "CreateCluster", TypeNamespace, "ns1", Deny},
		{"GetCluster", "GetCluster", TypeNamespace, "ns1", Deny},
		{"CreateRoleBinding", "CreateRoleBinding", TypeNamespace, "ns1", Deny},
		{"ListRoleBindings", "ListRoleBindings", TypeNamespace, "ns1", Deny},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := h.authorizer.Authorize(context.Background(), "padmin@example.com",
				tc.action, tc.resourceType, tc.resourceID)
			if err != nil {
				t.Fatalf("Authorize error: %v", err)
			}
			if got != tc.want {
				t.Errorf("Authorize(%q, %q, %q, %q) = %v, want %v",
					"padmin@example.com", tc.action, tc.resourceType, tc.resourceID, got, tc.want)
			}
		})
	}
}

// --------------------------------------------------------------------------
// No bindings — every action is denied
// --------------------------------------------------------------------------

func TestAuthorizer_NoBindings(t *testing.T) {
	h := newTestHarness(t)

	tests := []struct {
		name         string
		action       string
		resourceType string
		resourceID   string
	}{
		{"GetCluster_namespace", "GetCluster", TypeNamespace, "ns1"},
		{"ListClusters_namespace", "ListClusters", TypeNamespace, "ns1"},
		{"CreateCluster_namespace", "CreateCluster", TypeNamespace, "ns1"},
		{"GetNodepool_namespace", "GetNodepool", TypeNamespace, "ns1"},
		{"CreateRoleBinding_namespace", "CreateRoleBinding", TypeNamespace, "ns1"},
		{"CreatePlatformRoleBinding_platform", "CreatePlatformRoleBinding", TypePlatform, PlatformEntity},
		{"ListPlatformRoleBindings_platform", "ListPlatformRoleBindings", TypePlatform, PlatformEntity},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := h.authorizer.Authorize(context.Background(), "nobody@example.com",
				tc.action, tc.resourceType, tc.resourceID)
			if err != nil {
				t.Fatalf("Authorize error: %v", err)
			}
			if got != Deny {
				t.Errorf("Authorize(%q, %q, %q, %q) = %v, want Deny",
					"nobody@example.com", tc.action, tc.resourceType, tc.resourceID, got)
			}
		})
	}
}

// --------------------------------------------------------------------------
// Cross-namespace isolation: binding in ns1 must not grant access to ns2
// --------------------------------------------------------------------------

func TestAuthorizer_CrossNamespace(t *testing.T) {
	h := newTestHarness(t)
	h.createRoleBinding(t, "rb1", "ns1", "user@example.com", "cluster-viewer")
	// user has cluster-viewer in ns1 only, NOT in ns2

	tests := []struct {
		name         string
		action       string
		resourceType string
		resourceID   string
		want         Decision
	}{
		// ns1 – allowed (user has binding in ns1)
		{"GetCluster_ns1", "GetCluster", TypeNamespace, "ns1", Allow},
		{"ListClusters_ns1", "ListClusters", TypeNamespace, "ns1", Allow},
		{"GetNodepool_ns1", "GetNodepool", TypeNamespace, "ns1", Allow},

		// ns2 – denied (no binding in ns2)
		{"GetCluster_ns2", "GetCluster", TypeNamespace, "ns2", Deny},
		{"ListClusters_ns2", "ListClusters", TypeNamespace, "ns2", Deny},
		{"GetNodepool_ns2", "GetNodepool", TypeNamespace, "ns2", Deny},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := h.authorizer.Authorize(context.Background(), "user@example.com",
				tc.action, tc.resourceType, tc.resourceID)
			if err != nil {
				t.Fatalf("Authorize error: %v", err)
			}
			if got != tc.want {
				t.Errorf("Authorize(%q, %q, %q, %q) = %v, want %v",
					"user@example.com", tc.action, tc.resourceType, tc.resourceID, got, tc.want)
			}
		})
	}
}

// --------------------------------------------------------------------------
// Entity cache tests
// --------------------------------------------------------------------------

func TestAuthorizer_Cache(t *testing.T) {
	h := newTestHarness(t)
	h.createRoleBinding(t, "rb1", "ns1", "cached@example.com", "cluster-viewer")

	ctx := context.Background()

	// First call should populate the cache.
	em1, err := h.eg.BuildEntityMap(ctx, "cached@example.com")
	if err != nil {
		t.Fatalf("BuildEntityMap error: %v", err)
	}
	if em1 == nil {
		t.Fatal("expected non-nil entity map")
	}
	if h.cache.Len() != 1 {
		t.Fatalf("expected cache len 1, got %d", h.cache.Len())
	}

	// Second call should return the cached value (same map pointer).
	em2, err := h.eg.BuildEntityMap(ctx, "cached@example.com")
	if err != nil {
		t.Fatalf("BuildEntityMap error on second call: %v", err)
	}
	if fmt.Sprintf("%p", em1) != fmt.Sprintf("%p", em2) {
		t.Error("expected second call to return cached entity map (same pointer)")
	}

	// Invalidate and verify cache is cleared.
	h.cache.Invalidate("cached@example.com")
	if h.cache.Len() != 0 {
		t.Fatalf("expected cache len 0 after invalidation, got %d", h.cache.Len())
	}

	// Third call should rebuild the entity map.
	em3, err := h.eg.BuildEntityMap(ctx, "cached@example.com")
	if err != nil {
		t.Fatalf("BuildEntityMap error after invalidation: %v", err)
	}
	if em3 == nil {
		t.Fatal("expected non-nil entity map after rebuild")
	}
	if h.cache.Len() != 1 {
		t.Fatalf("expected cache len 1 after rebuild, got %d", h.cache.Len())
	}

	// The rebuilt map should be a different pointer than the original.
	if fmt.Sprintf("%p", em1) == fmt.Sprintf("%p", em3) {
		t.Error("expected rebuilt entity map to be a new instance")
	}
}

// --------------------------------------------------------------------------
// InvalidateAll clears every entry in the cache
// --------------------------------------------------------------------------

func TestAuthorizer_CacheInvalidateAll(t *testing.T) {
	h := newTestHarness(t)
	h.createRoleBinding(t, "rb1", "ns1", "a@example.com", "cluster-viewer")
	h.createRoleBinding(t, "rb2", "ns1", "b@example.com", "cluster-admin")

	ctx := context.Background()

	// Populate cache for two users.
	if _, err := h.eg.BuildEntityMap(ctx, "a@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.eg.BuildEntityMap(ctx, "b@example.com"); err != nil {
		t.Fatal(err)
	}
	if h.cache.Len() != 2 {
		t.Fatalf("expected cache len 2, got %d", h.cache.Len())
	}

	h.cache.InvalidateAll()
	if h.cache.Len() != 0 {
		t.Fatalf("expected cache len 0 after InvalidateAll, got %d", h.cache.Len())
	}
}

// --------------------------------------------------------------------------
// Validator tests
// --------------------------------------------------------------------------

func TestAuthorizer_Validator(t *testing.T) {
	// Configure the global validator with our built-in roles.
	roles := builtinRoles()
	rc := &RoleConfig{
		Roles:               roles,
		RoleLabels:          make(map[string]bool),
		NamespaceRoleLabels: make(map[string]bool),
		PlatformRoleLabels:  make(map[string]bool),
	}
	for _, r := range roles {
		rc.RoleLabels[r.Name] = true
		switch r.Scope {
		case "namespace":
			rc.NamespaceRoleLabels[r.Name] = true
		case "platform":
			rc.PlatformRoleLabels[r.Name] = true
		}
	}
	SetRoleValidator(rc)

	t.Run("ValidateNamespaceRoleRef", func(t *testing.T) {
		tests := []struct {
			name    string
			roleRef string
			wantErr bool
			errMsg  string
		}{
			{"known namespace role cluster-viewer", "cluster-viewer", false, ""},
			{"known namespace role cluster-admin", "cluster-admin", false, ""},
			{"known namespace role service-admin", "service-admin", false, ""},
			{"platform role in namespace context", "platform-admin", true, "platform-scoped role"},
			{"unknown role", "nonexistent", true, "not a known role"},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				err := ValidateNamespaceRoleRef(tc.roleRef)
				if tc.wantErr {
					if err == nil {
						t.Errorf("ValidateNamespaceRoleRef(%q) = nil, want error containing %q",
							tc.roleRef, tc.errMsg)
					} else if !contains(err.Error(), tc.errMsg) {
						t.Errorf("ValidateNamespaceRoleRef(%q) error = %q, want it to contain %q",
							tc.roleRef, err.Error(), tc.errMsg)
					}
				} else {
					if err != nil {
						t.Errorf("ValidateNamespaceRoleRef(%q) = %v, want nil", tc.roleRef, err)
					}
				}
			})
		}
	})

	t.Run("ValidatePlatformRoleRef", func(t *testing.T) {
		tests := []struct {
			name    string
			roleRef string
			wantErr bool
			errMsg  string
		}{
			{"known platform role", "platform-admin", false, ""},
			{"namespace role in platform context", "cluster-viewer", true, "namespace-scoped role"},
			{"unknown role", "nonexistent", true, "not a known role"},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				err := ValidatePlatformRoleRef(tc.roleRef)
				if tc.wantErr {
					if err == nil {
						t.Errorf("ValidatePlatformRoleRef(%q) = nil, want error containing %q",
							tc.roleRef, tc.errMsg)
					} else if !contains(err.Error(), tc.errMsg) {
						t.Errorf("ValidatePlatformRoleRef(%q) error = %q, want it to contain %q",
							tc.roleRef, err.Error(), tc.errMsg)
					}
				} else {
					if err != nil {
						t.Errorf("ValidatePlatformRoleRef(%q) = %v, want nil", tc.roleRef, err)
					}
				}
			})
		}
	})
}

// --------------------------------------------------------------------------
// AuthorizedNamespaces tests
// --------------------------------------------------------------------------

func TestAuthorizer_AuthorizedNamespaces(t *testing.T) {
	h := newTestHarness(t)
	h.createRoleBinding(t, "rb1", "ns1", "multi@example.com", "cluster-viewer")
	h.createRoleBinding(t, "rb2", "ns2", "multi@example.com", "cluster-admin")

	ctx := context.Background()
	namespaces, err := h.eg.AuthorizedNamespaces(ctx, "multi@example.com")
	if err != nil {
		t.Fatalf("AuthorizedNamespaces error: %v", err)
	}

	nsSet := make(map[string]bool, len(namespaces))
	for _, ns := range namespaces {
		nsSet[ns] = true
	}

	if !nsSet["ns1"] {
		t.Error("expected ns1 in authorized namespaces")
	}
	if !nsSet["ns2"] {
		t.Error("expected ns2 in authorized namespaces")
	}
	if len(namespaces) != 2 {
		t.Errorf("expected 2 authorized namespaces, got %d: %v", len(namespaces), namespaces)
	}

	// User with no bindings should have zero authorized namespaces.
	ns2, err := h.eg.AuthorizedNamespaces(ctx, "nobody@example.com")
	if err != nil {
		t.Fatalf("AuthorizedNamespaces error: %v", err)
	}
	if len(ns2) != 0 {
		t.Errorf("expected 0 authorized namespaces for unbound user, got %d", len(ns2))
	}
}

// contains checks if s contains substr.
func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
