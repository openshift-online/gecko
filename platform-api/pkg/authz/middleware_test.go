package authz

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	privatev1 "github.com/openshift-online/gecko/platform-api/api/private/v1"
	"github.com/openshift-online/gecko/orlop/pkg/apiserver/handlers"
	"github.com/openshift-online/gecko/orlop/pkg/apiserver/storage"
	"github.com/openshift-online/gecko/platform-api/pkg/authn"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func setupTestRouter(t *testing.T, auth *Authorizer, disableAuth bool) *chi.Mux {
	t.Helper()
	r := chi.NewRouter()

	mw := Middleware(auth, disableAuth)
	r.Use(mw)

	okHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	nsListHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ns := handlers.AuthorizedNamespacesFromContext(r.Context())
		if len(ns) > 0 {
			w.Header().Set("X-Authorized-Namespaces", ns[0])
		}
		w.WriteHeader(http.StatusOK)
	})

	r.Get("/apis/{group}/{version}/namespaces/{namespace}/{plural}", okHandler)
	r.Get("/apis/{group}/{version}/namespaces/{namespace}/{plural}/{name}", okHandler)
	r.Post("/apis/{group}/{version}/namespaces/{namespace}/{plural}", okHandler)
	r.Put("/apis/{group}/{version}/namespaces/{namespace}/{plural}/{name}", okHandler)
	r.Delete("/apis/{group}/{version}/namespaces/{namespace}/{plural}/{name}", okHandler)
	r.Get("/apis/{group}/{version}/{plural}", nsListHandler)
	r.Get("/apis/{group}/{version}/{plural}/{name}", okHandler)
	r.Post("/apis/{group}/{version}/{plural}", okHandler)

	return r
}

func setupMiddlewareAuthorizer(t *testing.T) *Authorizer {
	t.Helper()

	// PlatformRole: cluster-viewer.
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

	// Namespace-scoped Role store: empty.
	roleStore := newMockStore()
	roleStore.listFilter = func(_ storage.ListOptions) []client.Object { return nil }

	// RoleBinding store: alice has cluster-viewer in org-1.
	aliceBinding := &privatev1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "rb1", Namespace: "org-1"},
		Spec:       privatev1.RoleBindingSpec{Subject: "alice@example.com", RoleRef: privatev1.RoleRef{Kind: privatev1.RoleRefKindPlatformRole, Name: "cluster-viewer", APIGroup: "gcp.managed.openshift.io"}},
	}
	rbStore := newMockStore()
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

func TestMiddleware_DisableAuth(t *testing.T) {
	auth := setupMiddlewareAuthorizer(t)
	router := setupTestRouter(t, auth, true)

	req := httptest.NewRequest(http.MethodGet, "/apis/v1/v1/namespaces/org-1/clusters", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestMiddleware_NoUser(t *testing.T) {
	auth := setupMiddlewareAuthorizer(t)
	router := setupTestRouter(t, auth, false)

	req := httptest.NewRequest(http.MethodGet, "/apis/v1/v1/namespaces/org-1/clusters", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got status %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestMiddleware_AllowedNamespacedGet(t *testing.T) {
	auth := setupMiddlewareAuthorizer(t)
	router := setupTestRouter(t, auth, false)

	req := httptest.NewRequest(http.MethodGet, "/apis/v1/v1/namespaces/org-1/clusters/my-cluster", nil)
	req = req.WithContext(authn.WithUser(req.Context(), "alice@example.com"))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestMiddleware_DeniedNamespacedGet_WrongNamespace(t *testing.T) {
	auth := setupMiddlewareAuthorizer(t)
	router := setupTestRouter(t, auth, false)

	req := httptest.NewRequest(http.MethodGet, "/apis/v1/v1/namespaces/org-2/clusters/my-cluster", nil)
	req = req.WithContext(authn.WithUser(req.Context(), "alice@example.com"))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("got status %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestMiddleware_DeniedNamespacedCreate(t *testing.T) {
	auth := setupMiddlewareAuthorizer(t)
	router := setupTestRouter(t, auth, false)

	req := httptest.NewRequest(http.MethodPost, "/apis/v1/v1/namespaces/org-1/clusters", nil)
	req = req.WithContext(authn.WithUser(req.Context(), "alice@example.com"))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("got status %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestMiddleware_CrossNamespaceList(t *testing.T) {
	auth := setupMiddlewareAuthorizer(t)
	router := setupTestRouter(t, auth, false)

	req := httptest.NewRequest(http.MethodGet, "/apis/v1/v1/clusters", nil)
	req = req.WithContext(authn.WithUser(req.Context(), "alice@example.com"))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d", rec.Code, http.StatusOK)
	}

	ns := rec.Header().Get("X-Authorized-Namespaces")
	if ns != "org-1" {
		t.Fatalf("got authorized namespace %q, want %q", ns, "org-1")
	}
}

func TestMiddleware_CrossNamespaceList_NoBindings(t *testing.T) {
	auth := setupMiddlewareAuthorizer(t)
	router := setupTestRouter(t, auth, false)

	req := httptest.NewRequest(http.MethodGet, "/apis/v1/v1/clusters", nil)
	req = req.WithContext(authn.WithUser(req.Context(), "nobody@example.com"))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("got status %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestMiddleware_NamespacedList(t *testing.T) {
	auth := setupMiddlewareAuthorizer(t)
	router := setupTestRouter(t, auth, false)

	req := httptest.NewRequest(http.MethodGet, "/apis/v1/v1/namespaces/org-1/clusters", nil)
	req = req.WithContext(authn.WithUser(req.Context(), "alice@example.com"))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestResolveAction(t *testing.T) {
	tests := []struct {
		name       string
		plural     string
		method     string
		resName    string
		wantAction string
	}{
		{"list clusters", "clusters", "GET", "", "ListClusters"},
		{"get cluster", "clusters", "GET", "my-cluster", "GetCluster"},
		{"create cluster", "clusters", "POST", "", "CreateCluster"},
		{"update cluster", "clusters", "PUT", "my-cluster", "UpdateCluster"},
		{"delete cluster", "clusters", "DELETE", "my-cluster", "DeleteCluster"},
		{"list nodepools", "nodepools", "GET", "", "ListNodepools"},
		{"get nodepool", "nodepools", "GET", "np-1", "GetNodepool"},
		{"list rolebindings", "rolebindings", "GET", "", "ListRoleBindings"},
		{"unknown resource", "unknown", "GET", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveAction(tt.plural, tt.method, tt.resName)
			if got != tt.wantAction {
				t.Fatalf("resolveAction(%q, %q, %q) = %q, want %q",
					tt.plural, tt.method, tt.resName, got, tt.wantAction)
			}
		})
	}
}

func TestParseURLPath(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		wantOK    bool
		wantRoute parsedRoute
	}{
		{"namespaced list", "/apis/v1/v1/namespaces/org-1/clusters", true, parsedRoute{plural: "clusters", namespace: "org-1"}},
		{"namespaced get", "/apis/v1/v1/namespaces/org-1/clusters/my-cluster", true, parsedRoute{plural: "clusters", namespace: "org-1", name: "my-cluster"}},
		{"cluster-scoped list", "/apis/v1/v1/clusters", true, parsedRoute{plural: "clusters"}},
		{"cluster-scoped get", "/apis/v1/v1/clusters/my-cluster", true, parsedRoute{plural: "clusters", name: "my-cluster"}},
		{"too short", "/apis/v1/v1", false, parsedRoute{}},
		{"not apis", "/healthz", false, parsedRoute{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseURLPath(tt.path)
			if ok != tt.wantOK {
				t.Fatalf("parseURLPath(%q) ok = %v, want %v", tt.path, ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if got != tt.wantRoute {
				t.Fatalf("parseURLPath(%q) = %+v, want %+v", tt.path, got, tt.wantRoute)
			}
		})
	}
}
