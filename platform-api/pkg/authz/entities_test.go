package authz

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"testing"

	cedar "github.com/cedar-policy/cedar-go"
	privatev1 "github.com/openshift-online/gecko/platform-api/api/private/v1"
	"github.com/openshift-online/gecko/orlop/pkg/apiserver/storage"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// mockStore is a minimal mock implementation of storage.ResourceStore for testing.
type mockStore struct {
	objects    map[string]client.Object
	listItems  []client.Object
	listFilter func(opts storage.ListOptions) []client.Object
}

func newMockStore() *mockStore {
	return &mockStore{
		objects: make(map[string]client.Object),
	}
}

func (s *mockStore) Create(_ context.Context, obj client.Object) error {
	key := storeKey(obj.GetNamespace(), obj.GetName())
	s.objects[key] = obj
	return nil
}

func (s *mockStore) Get(_ context.Context, namespace, name string) (client.Object, error) {
	key := storeKey(namespace, name)
	obj, ok := s.objects[key]
	if !ok {
		return nil, fmt.Errorf("not found: %s", key)
	}
	return obj, nil
}

func (s *mockStore) List(_ context.Context, opts storage.ListOptions) (client.ObjectList, error) {
	var items []client.Object
	if s.listFilter != nil {
		items = s.listFilter(opts)
		// Fall back to listItems for unfiltered calls (e.g. loadPolicies).
		if items == nil && len(opts.FieldFilters) == 0 {
			items = s.listItems
		}
	} else {
		items = s.listItems
	}

	if len(items) > 0 {
		switch items[0].(type) {
		case *privatev1.RoleBinding:
			list := &privatev1.RoleBindingList{}
			for _, item := range items {
				if rb, ok := item.(*privatev1.RoleBinding); ok {
					list.Items = append(list.Items, *rb)
				}
			}
			return list, nil
		case *privatev1.PlatformRole:
			list := &privatev1.PlatformRoleList{}
			for _, item := range items {
				if pr, ok := item.(*privatev1.PlatformRole); ok {
					list.Items = append(list.Items, *pr)
				}
			}
			return list, nil
		case *privatev1.Role:
			list := &privatev1.RoleList{}
			for _, item := range items {
				if r, ok := item.(*privatev1.Role); ok {
					list.Items = append(list.Items, *r)
				}
			}
			return list, nil
		}
	}

	return &privatev1.RoleBindingList{}, nil
}

func (s *mockStore) Update(_ context.Context, obj client.Object) error {
	key := storeKey(obj.GetNamespace(), obj.GetName())
	s.objects[key] = obj
	return nil
}

func (s *mockStore) Delete(_ context.Context, namespace, name string) error {
	key := storeKey(namespace, name)
	delete(s.objects, key)
	return nil
}

func (s *mockStore) Watch(_ context.Context, _ storage.ListOptions, _ string) (<-chan storage.ResourceEvent, func(), error) {
	ch := make(chan storage.ResourceEvent)
	return ch, func() { close(ch) }, nil
}

func storeKey(namespace, name string) string {
	if namespace == "" {
		return name
	}
	return namespace + "/" + name
}

func TestBuildEntities_WithRoleBindings(t *testing.T) {
	rbStore := newMockStore()
	rbStore.listItems = []client.Object{
		&privatev1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{Name: "rb1", Namespace: "org-123"},
			Spec:       privatev1.RoleBindingSpec{Subject: "alice@example.com", RoleRef: privatev1.RoleRef{Kind: privatev1.RoleRefKindPlatformRole, Name: "cluster-viewer", APIGroup: "gcp.managed.openshift.io"}},
		},
	}

	eg := NewEntityGetter(AuthzStores{
		PlatformRoles: newMockStore(),
		Roles:         newMockStore(),
		RoleBindings:  rbStore,
	})

	entities, err := eg.BuildEntities(context.Background(), "alice@example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have: User, NamespaceRole, Namespace = 3 entities.
	if len(entities) != 3 {
		t.Fatalf("got %d entities, want 3", len(entities))
	}

	userUID := cedar.NewEntityUID("User", cedar.String("alice@example.com"))
	userEntity, ok := entities[userUID]
	if !ok {
		t.Fatal("expected User entity")
	}

	nsRoleUID := cedar.NewEntityUID("NamespaceRole", cedar.String("org-123/cluster-viewer/rb1"))
	if !userEntity.Parents.Contains(nsRoleUID) {
		t.Fatal("expected User to have NamespaceRole parent")
	}

	nsUID := cedar.NewEntityUID("Namespace", cedar.String("org-123"))
	nsRoleEntity, ok := entities[nsRoleUID]
	if !ok {
		t.Fatal("expected NamespaceRole entity")
	}
	if !nsRoleEntity.Parents.Contains(nsUID) {
		t.Fatal("expected NamespaceRole to have Namespace parent")
	}
}

func TestBuildEntities_NoBindings(t *testing.T) {
	rbStore := newMockStore()
	rbStore.listFilter = func(_ storage.ListOptions) []client.Object { return nil }

	eg := NewEntityGetter(AuthzStores{
		PlatformRoles: newMockStore(),
		Roles:         newMockStore(),
		RoleBindings:  rbStore,
	})

	entities, err := eg.BuildEntities(context.Background(), "nobody@example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(entities) != 1 {
		t.Fatalf("got %d entities, want 1", len(entities))
	}
}

func TestAuthorizedNamespaces_ViaPlatformRole(t *testing.T) {
	// alice has a binding to cluster-viewer (a PlatformRole) in org-1 and org-3.
	rbStore := newMockStore()
	rbStore.listItems = []client.Object{
		&privatev1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{Name: "rb1", Namespace: "org-1"},
			Spec:       privatev1.RoleBindingSpec{Subject: "alice@example.com", RoleRef: privatev1.RoleRef{Kind: privatev1.RoleRefKindPlatformRole, Name: "cluster-viewer", APIGroup: "gcp.managed.openshift.io"}},
		},
		&privatev1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{Name: "rb2", Namespace: "org-2"},
			Spec:       privatev1.RoleBindingSpec{Subject: "alice@example.com", RoleRef: privatev1.RoleRef{Kind: privatev1.RoleRefKindPlatformRole, Name: "cluster-admin", APIGroup: "gcp.managed.openshift.io"}},
		},
		&privatev1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{Name: "rb3", Namespace: "org-3"},
			Spec:       privatev1.RoleBindingSpec{Subject: "alice@example.com", RoleRef: privatev1.RoleRef{Kind: privatev1.RoleRefKindPlatformRole, Name: "cluster-viewer", APIGroup: "gcp.managed.openshift.io"}},
		},
	}

	prStore := newMockStore()
	prStore.objects["cluster-viewer"] = &privatev1.PlatformRole{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster-viewer"},
		Spec:       privatev1.PlatformRoleSpec{Permissions: []string{"cluster.list", "cluster.get"}},
	}
	prStore.objects["cluster-admin"] = &privatev1.PlatformRole{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster-admin"},
		Spec:       privatev1.PlatformRoleSpec{Permissions: []string{"cluster.create", "cluster.update"}},
	}

	eg := NewEntityGetter(AuthzStores{
		PlatformRoles: prStore,
		Roles:         newMockStore(),
		RoleBindings:  rbStore,
	})

	// cluster-viewer has cluster.list → ListClusters.
	ns, err := eg.AuthorizedNamespaces(context.Background(), "alice@example.com", "ListClusters")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sort.Strings(ns)
	if !reflect.DeepEqual(ns, []string{"org-1", "org-3"}) {
		t.Fatalf("got namespaces %v, want [org-1 org-3]", ns)
	}

	// cluster-admin has cluster.create → CreateCluster.
	ns, err = eg.AuthorizedNamespaces(context.Background(), "alice@example.com", "CreateCluster")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ns) != 1 || ns[0] != "org-2" {
		t.Fatalf("got namespaces %v, want [org-2]", ns)
	}
}

func TestAuthorizedNamespaces_ViaNamespacedRole(t *testing.T) {
	// alice has a binding to a namespace-scoped Role "cluster-ro" in org-1.
	rbStore := newMockStore()
	rbStore.listItems = []client.Object{
		&privatev1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{Name: "rb1", Namespace: "org-1"},
			Spec:       privatev1.RoleBindingSpec{Subject: "alice@example.com", RoleRef: privatev1.RoleRef{Kind: privatev1.RoleRefKindRole, Name: "cluster-ro", APIGroup: "gcp.managed.openshift.io"}},
		},
	}

	roleStore := newMockStore()
	roleStore.objects["org-1/cluster-ro"] = &privatev1.Role{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster-ro", Namespace: "org-1"},
		Spec:       privatev1.RoleSpec{Permissions: []string{"cluster.list", "cluster.get"}},
	}

	eg := NewEntityGetter(AuthzStores{
		PlatformRoles: newMockStore(),
		Roles:         roleStore,
		RoleBindings:  rbStore,
	})

	ns, err := eg.AuthorizedNamespaces(context.Background(), "alice@example.com", "ListClusters")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ns) != 1 || ns[0] != "org-1" {
		t.Fatalf("got namespaces %v, want [org-1]", ns)
	}
}
