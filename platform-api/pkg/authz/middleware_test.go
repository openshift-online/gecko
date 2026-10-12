package authz

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-logr/logr"

	"github.com/openshift-online/gecko/orlop/pkg/apiserver/types"
	publicv1 "github.com/openshift-online/gecko/platform-api/api/public/v1"
	"github.com/openshift-online/gecko/platform-api/pkg/authn"

	runtimeschema "k8s.io/apimachinery/pkg/runtime/schema"
)

func TestMiddlewareDoesNotBypassAuthorizationForClusterOrNodePool(t *testing.T) {
	resources := []types.ResourceInfo{
		{
			GVK:    runtimeschema.GroupVersionKind{Group: "gcp.managed.openshift.io", Version: "v1", Kind: "Cluster"},
			Plural: "clusters",
		},
		{
			GVK:        runtimeschema.GroupVersionKind{Group: "gcp.managed.openshift.io", Version: "v1", Kind: "NodePool"},
			Plural:     "nodepools",
			Namespaced: true,
		},
	}
	for _, requestPath := range []string{
		"/apis/gcp.managed.openshift.io/v1/clusters",
		"/apis/gcp.managed.openshift.io/v1/clusters/example",
		"/apis/gcp.managed.openshift.io/v1/namespaces/project-a/nodepools",
		"/apis/gcp.managed.openshift.io/v1/namespaces/project-a/nodepools/example",
	} {
		t.Run(requestPath, func(t *testing.T) {
			request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, requestPath, nil)
			response := httptest.NewRecorder()
			Middleware(nil, logr.Discard(), resources)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Fatal("unauthorized request reached the next handler")
			})).ServeHTTP(response, request)
			if response.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusForbidden)
			}
		})
	}
}

func TestMiddlewareFailsClosedWithoutAuthorizer(t *testing.T) {
	resources := []types.ResourceInfo{{
		GVK:        runtimeschema.GroupVersionKind{Group: "gcp.managed.openshift.io", Version: "v1", Kind: "NodePool"},
		Plural:     "nodepools",
		Namespaced: true,
	}}
	ctx := authn.WithUser(context.Background(), "alice@example.com")
	request := httptest.NewRequestWithContext(ctx, http.MethodGet, "/apis/gcp.managed.openshift.io/v1/namespaces/project-a/nodepools", nil)
	response := httptest.NewRecorder()
	Middleware(nil, logr.Discard(), resources)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("request reached the next handler without an authorizer")
	})).ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusForbidden)
	}
}

func TestMiddlewareDoesNotBypassCatalogPoliciesWithoutAuthorizer(t *testing.T) {
	resources := []types.ResourceInfo{
		publicv1.VersionResourceInfo,
		publicv1.ChannelResourceInfo,
	}
	for _, requestPath := range []string{
		"/apis/gcp.managed.openshift.io/v1/versions",
		"/apis/gcp.managed.openshift.io/v1/versions/4.22.1",
		"/apis/gcp.managed.openshift.io/v1/channels",
		"/apis/gcp.managed.openshift.io/v1/channels/stable",
	} {
		t.Run(requestPath, func(t *testing.T) {
			ctx := authn.WithUser(context.Background(), "alice@example.com")
			request := httptest.NewRequestWithContext(ctx, http.MethodGet, requestPath, nil)
			response := httptest.NewRecorder()
			Middleware(nil, logr.Discard(), resources)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Fatal("catalog request reached the next handler without an authorizer")
			})).ServeHTTP(response, request)
			if response.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusForbidden)
			}
		})
	}
}

func TestMiddlewareAuthorizesPlatformCatalogReads(t *testing.T) {
	authorizer := newEmptyAuthorizer(t)
	resources := []types.ResourceInfo{
		publicv1.VersionResourceInfo,
		publicv1.ChannelResourceInfo,
	}
	tests := []struct {
		name   string
		method string
		path   string
		status int
	}{
		{name: "list versions", method: http.MethodGet, path: "/apis/gcp.managed.openshift.io/v1/versions", status: http.StatusNoContent},
		{name: "get version", method: http.MethodGet, path: "/apis/gcp.managed.openshift.io/v1/versions/4.22.1", status: http.StatusNoContent},
		{name: "list channels", method: http.MethodGet, path: "/apis/gcp.managed.openshift.io/v1/channels", status: http.StatusNoContent},
		{name: "get channel", method: http.MethodGet, path: "/apis/gcp.managed.openshift.io/v1/channels/stable", status: http.StatusNoContent},
		{name: "watch is denied", method: http.MethodGet, path: "/apis/gcp.managed.openshift.io/v1/versions?watch=true", status: http.StatusForbidden},
		{name: "write is denied", method: http.MethodPost, path: "/apis/gcp.managed.openshift.io/v1/versions", status: http.StatusForbidden},
		{name: "unconfigured cluster resource is denied", method: http.MethodGet, path: "/apis/gcp.managed.openshift.io/v1/platformroles", status: http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := authn.WithUser(context.Background(), "alice@example.com")
			request := httptest.NewRequestWithContext(ctx, tt.method, tt.path, nil)
			response := httptest.NewRecorder()
			Middleware(authorizer, logr.Discard(), resources)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			})).ServeHTTP(response, request)
			if response.Code != tt.status {
				t.Fatalf("status = %d, want %d", response.Code, tt.status)
			}
		})
	}
}
