package authz

import (
	"context"
	"testing"
	"time"

	privatev1 "github.com/openshift-online/gecko/platform-api/api/private/v1"
	"github.com/openshift-online/gecko/orlop/pkg/apiserver/storage"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// watchableMockStore extends mockStore with controllable watch channels.
type watchableMockStore struct {
	*mockStore
	watchCh chan storage.ResourceEvent
}

func newWatchableMockStore() *watchableMockStore {
	return &watchableMockStore{
		mockStore: newMockStore(),
		watchCh:   make(chan storage.ResourceEvent, 10),
	}
}

func (s *watchableMockStore) Watch(_ context.Context, _ storage.ListOptions, _ string) (<-chan storage.ResourceEvent, func(), error) {
	return s.watchCh, func() {}, nil
}

func TestStartWatching_PlatformRoleChange(t *testing.T) {
	prStore := newWatchableMockStore()
	prStore.listItems = []client.Object{
		&privatev1.PlatformRole{
			ObjectMeta: metav1.ObjectMeta{Name: "cluster-viewer"},
			Spec:       privatev1.PlatformRoleSpec{Permissions: []string{"cluster.list"}, System: true},
		},
	}
	roleStore := newWatchableMockStore()
	roleStore.listFilter = func(_ storage.ListOptions) []client.Object { return nil }
	rbStore := newWatchableMockStore()
	rbStore.listFilter = func(_ storage.ListOptions) []client.Object { return nil }

	stores := AuthzStores{
		PlatformRoles: prStore,
		Roles:         roleStore,
		RoleBindings:  rbStore,
	}

	auth, err := NewAuthorizer(context.Background(), stores)
	if err != nil {
		t.Fatalf("failed to create authorizer: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := auth.StartWatching(ctx); err != nil {
		t.Fatalf("failed to start watching: %v", err)
	}

	// Send a platform role change event.
	prStore.watchCh <- storage.ResourceEvent{
		Type: storage.EventModified,
		Object: &privatev1.PlatformRole{
			ObjectMeta: metav1.ObjectMeta{Name: "cluster-viewer"},
			Spec:       privatev1.PlatformRoleSpec{Permissions: []string{"cluster.list", "cluster.get"}, System: true},
		},
	}

	time.Sleep(100 * time.Millisecond)
}

func TestStartWatching_RoleChange(t *testing.T) {
	prStore := newWatchableMockStore()
	prStore.listFilter = func(_ storage.ListOptions) []client.Object { return nil }
	roleStore := newWatchableMockStore()
	roleStore.listItems = []client.Object{
		&privatev1.Role{
			ObjectMeta: metav1.ObjectMeta{Name: "cluster-ro", Namespace: "org-1"},
			Spec:       privatev1.RoleSpec{Permissions: []string{"cluster.list"}},
		},
	}
	rbStore := newWatchableMockStore()
	rbStore.listFilter = func(_ storage.ListOptions) []client.Object { return nil }

	stores := AuthzStores{
		PlatformRoles: prStore,
		Roles:         roleStore,
		RoleBindings:  rbStore,
	}

	auth, err := NewAuthorizer(context.Background(), stores)
	if err != nil {
		t.Fatalf("failed to create authorizer: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := auth.StartWatching(ctx); err != nil {
		t.Fatalf("failed to start watching: %v", err)
	}

	// Send a namespace-scoped role change event.
	roleStore.watchCh <- storage.ResourceEvent{
		Type: storage.EventModified,
		Object: &privatev1.Role{
			ObjectMeta: metav1.ObjectMeta{Name: "cluster-ro", Namespace: "org-1"},
			Spec:       privatev1.RoleSpec{Permissions: []string{"cluster.list", "cluster.get"}},
		},
	}

	time.Sleep(100 * time.Millisecond)
}

func TestStartWatching_RoleBindingChange_InvalidatesUser(t *testing.T) {
	prStore := newWatchableMockStore()
	prStore.listItems = []client.Object{
		&privatev1.PlatformRole{
			ObjectMeta: metav1.ObjectMeta{Name: "cluster-viewer"},
			Spec:       privatev1.PlatformRoleSpec{Permissions: []string{"cluster.list"}, System: true},
		},
	}
	roleStore := newWatchableMockStore()
	roleStore.listFilter = func(_ storage.ListOptions) []client.Object { return nil }
	aliceBinding := &privatev1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "rb1", Namespace: "org-1"},
		Spec:       privatev1.RoleBindingSpec{Subject: "alice@example.com", RoleRef: privatev1.RoleRef{Kind: privatev1.RoleRefKindPlatformRole, Name: "cluster-viewer", APIGroup: "gcp.managed.openshift.io"}},
	}
	rbStore := newWatchableMockStore()
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

	// Pre-warm cache.
	_, err = auth.Authorize(context.Background(), "alice@example.com", "ListClusters", "org-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := auth.cache.Get("alice@example.com"); !ok {
		t.Fatal("expected cache entry for alice")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := auth.StartWatching(ctx); err != nil {
		t.Fatalf("failed to start watching: %v", err)
	}

	rbStore.watchCh <- storage.ResourceEvent{
		Type: storage.EventModified,
		Object: &privatev1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{Name: "rb1", Namespace: "org-1"},
			Spec:       privatev1.RoleBindingSpec{Subject: "alice@example.com", RoleRef: privatev1.RoleRef{Kind: privatev1.RoleRefKindPlatformRole, Name: "cluster-viewer", APIGroup: "gcp.managed.openshift.io"}},
		},
	}

	time.Sleep(100 * time.Millisecond)

	if _, ok := auth.cache.Get("alice@example.com"); ok {
		t.Fatal("expected cache to be invalidated for alice after role binding change")
	}
}

func TestStartWatching_BookmarkEventsIgnored(t *testing.T) {
	prStore := newWatchableMockStore()
	prStore.listFilter = func(_ storage.ListOptions) []client.Object { return nil }
	roleStore := newWatchableMockStore()
	roleStore.listFilter = func(_ storage.ListOptions) []client.Object { return nil }
	rbStore := newWatchableMockStore()
	rbStore.listFilter = func(_ storage.ListOptions) []client.Object { return nil }

	stores := AuthzStores{
		PlatformRoles: prStore,
		Roles:         roleStore,
		RoleBindings:  rbStore,
	}

	auth, err := NewAuthorizer(context.Background(), stores)
	if err != nil {
		t.Fatalf("failed to create authorizer: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := auth.StartWatching(ctx); err != nil {
		t.Fatalf("failed to start watching: %v", err)
	}

	prStore.watchCh <- storage.ResourceEvent{Type: storage.EventBookmark}
	roleStore.watchCh <- storage.ResourceEvent{Type: storage.EventBookmark}
	rbStore.watchCh <- storage.ResourceEvent{Type: storage.EventBookmark}

	time.Sleep(50 * time.Millisecond)
}
