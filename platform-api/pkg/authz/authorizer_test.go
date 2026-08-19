package authz

import (
	"context"
	"testing"

	privatev1 "github.com/openshift-online/gecko/platform-api/api/private/v1"
	"github.com/openshift-online/gecko/orlop/pkg/apiserver/storage"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func setupTestAuthorizer(t *testing.T) *Authorizer {
	t.Helper()

	// PlatformRole store: cluster-viewer (cluster-scoped).
	clusterViewer := &privatev1.PlatformRole{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster-viewer"},
		Spec: privatev1.PlatformRoleSpec{
			Permissions: []string{"cluster.list", "cluster.get"},
			System:      true,
		},
	}
	prStore := newMockStore()
	prStore.objects["cluster-viewer"] = clusterViewer
	prStore.listItems = []client.Object{clusterViewer}

	// Role store: empty (namespace-scoped roles are user-defined).
	roleStore := newMockStore()
	roleStore.listFilter = func(_ storage.ListOptions) []client.Object { return nil }

	// RoleBinding store: alice has cluster-viewer in org-1.
	aliceBinding := &privatev1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "rb1", Namespace: "org-1"},
		Spec:       privatev1.RoleBindingSpec{Subject: "alice@example.com", RoleRef: privatev1.RoleRef{Kind: privatev1.RoleRefKindPlatformRole, Name: "cluster-viewer", APIGroup: "gcp.managed.openshift.io"}},
	}
	rbStore := newMockStore()
	// listItems used by loadPolicies (unfiltered); listFilter used by BuildEntities (filtered).
	rbStore.listItems = []client.Object{aliceBinding}
	rbStore.listFilter = func(opts storage.ListOptions) []client.Object {
		if opts.FieldFilters["spec.subject"] == "alice@example.com" {
			return []client.Object{aliceBinding}
		}
		return nil
	}

	stores := AuthzStores{
		PlatformRoles: prStore,
		Roles:         roleStore,
		RoleBindings:  rbStore,
	}

	auth, err := NewAuthorizer(context.Background(), stores)
	if err != nil {
		t.Fatalf("failed to create authorizer: %v", err)
	}
	return auth
}

func TestAuthorizer_AllowNamespacedAction(t *testing.T) {
	auth := setupTestAuthorizer(t)

	allowed, err := auth.Authorize(context.Background(), "alice@example.com", "ListClusters", "org-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !allowed {
		t.Fatal("expected allow for alice listing clusters in org-1")
	}
}

func TestAuthorizer_DenyNamespacedAction_WrongNamespace(t *testing.T) {
	auth := setupTestAuthorizer(t)

	allowed, err := auth.Authorize(context.Background(), "alice@example.com", "ListClusters", "org-2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if allowed {
		t.Fatal("expected deny for alice listing clusters in org-2")
	}
}

func TestAuthorizer_DenyNamespacedAction_WrongPermission(t *testing.T) {
	auth := setupTestAuthorizer(t)

	allowed, err := auth.Authorize(context.Background(), "alice@example.com", "CreateCluster", "org-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if allowed {
		t.Fatal("expected deny for alice creating clusters in org-1 (viewer role has no create)")
	}
}

func TestAuthorizer_DenyUnknownUser(t *testing.T) {
	auth := setupTestAuthorizer(t)

	allowed, err := auth.Authorize(context.Background(), "unknown@example.com", "ListClusters", "org-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if allowed {
		t.Fatal("expected deny for unknown user")
	}
}

func TestAuthorizer_CacheInvalidation(t *testing.T) {
	auth := setupTestAuthorizer(t)

	_, err := auth.Authorize(context.Background(), "alice@example.com", "ListClusters", "org-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	auth.InvalidateUser("alice@example.com")
	allowed, err := auth.Authorize(context.Background(), "alice@example.com", "ListClusters", "org-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !allowed {
		t.Fatal("expected allow after cache invalidation")
	}
}

func TestAuthorizer_AuthorizedNamespaces(t *testing.T) {
	auth := setupTestAuthorizer(t)

	ns, err := auth.AuthorizedNamespaces(context.Background(), "alice@example.com", "ListClusters")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ns) != 1 || ns[0] != "org-1" {
		t.Fatalf("got namespaces %v, want [org-1]", ns)
	}
}
