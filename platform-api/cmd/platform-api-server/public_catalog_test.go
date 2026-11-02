package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"

	"github.com/openshift-online/gecko/orlop/pkg/apiserver"
	"github.com/openshift-online/gecko/orlop/pkg/apiserver/constants"
	"github.com/openshift-online/gecko/orlop/pkg/apiserver/storage"
	"github.com/openshift-online/gecko/orlop/pkg/apiserver/storage/memory"
	privatev1 "github.com/openshift-online/gecko/platform-api/api/private/v1"
	publicv1 "github.com/openshift-online/gecko/platform-api/api/public/v1"
	"github.com/openshift-online/gecko/platform-api/pkg/authn"
	"github.com/openshift-online/gecko/platform-api/pkg/authz"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	runtimeschema "k8s.io/apimachinery/pkg/runtime/schema"
)

type catalogPublicRequester func(method, path string, authenticated bool) (int, string)
type catalogPrivateRequester func(method, path string, body []byte) (int, string)

//nolint:kubeapilinter // Test-only HTTP harness, not a serialized API type.
type catalogAuthorizationTestServer struct {
	request        catalogPublicRequester
	privateRequest catalogPrivateRequester
	stores         authz.Stores
	authorizer     *authz.Authorizer
	version        *privatev1.Version
	channel        *privatev1.Channel
	versionStore   storage.ResourceStore
	channelStore   storage.ResourceStore
}

func newCatalogAuthorizationTestServer(t *testing.T) catalogAuthorizationTestServer {
	t.Helper()
	privatePort := freePublicAPITestPort(t)
	publicPort := freePublicAPITestPort(t)
	publicResources := getPublicResources()
	logger := logr.Discard()
	privateScheme := getPrivateScheme()
	versionGVK := privatev1.GroupVersion.WithKind("Version")
	versionStore := memory.NewMemoryStore(apiserver.GroupKindResourceType(versionGVK.GroupKind()), privateScheme, versionGVK)
	version := &privatev1.Version{
		TypeMeta: metav1.TypeMeta{
			APIVersion: privatev1.GroupVersion.String(),
			Kind:       "Version",
		},
		ObjectMeta: metav1.ObjectMeta{Name: "4.22.1"},
		Spec: privatev1.VersionSpec{
			ChannelGroups: []string{"stable"},
			ReleaseImage:  "quay.io/openshift-release-dev/ocp-release@sha256:internal-only",
		},
	}
	version.SetGroupVersionKind(versionGVK)
	if err := versionStore.Create(context.Background(), version); err != nil {
		t.Fatalf("seed Version: %v", err)
	}
	channelGVK := privatev1.GroupVersion.WithKind("Channel")
	channelStore := memory.NewMemoryStore(apiserver.GroupKindResourceType(channelGVK.GroupKind()), privateScheme, channelGVK)
	channel := &privatev1.Channel{
		TypeMeta: metav1.TypeMeta{
			APIVersion: privatev1.GroupVersion.String(),
			Kind:       "Channel",
		},
		ObjectMeta: metav1.ObjectMeta{Name: "stable"},
		Spec: privatev1.ChannelSpec{
			InstallDefaultVersion: "4.22.1",
			FleetMinorVersion:     "4.22",
		},
	}
	channel.SetGroupVersionKind(channelGVK)
	if err := channelStore.Create(context.Background(), channel); err != nil {
		t.Fatalf("seed Channel: %v", err)
	}
	backingStores := map[runtimeschema.GroupVersionKind]storage.ResourceStore{versionGVK: versionStore, channelGVK: channelStore}
	storageFactory := func(resourceType string, scheme *runtime.Scheme, gvk runtimeschema.GroupVersionKind) (storage.ResourceStore, error) {
		if store, found := backingStores[gvk]; found {
			return store, nil
		}
		store := memory.NewMemoryStore(resourceType, scheme, gvk)
		backingStores[gvk] = store
		return store, nil
	}
	stores, err := authz.NewStores(storageFactory, privateScheme)
	if err != nil {
		t.Fatal(err)
	}
	authorizer, err := authz.NewAuthorizer(context.Background(), stores, logger)
	if err != nil {
		t.Fatal(err)
	}
	privatev1.SetValidatorDeps(privatev1.ValidatorDeps{RoleExists: stores.RoleExists, PlatformRoleExists: stores.PlatformRoleExists})
	t.Cleanup(func() { privatev1.SetValidatorDeps(privatev1.ValidatorDeps{}) })

	server, err := apiserver.New(apiserver.Options{
		Address: "127.0.0.1",
		Private: apiserver.PrivateAPIOptions{
			Port:        privatePort,
			Scheme:      privateScheme,
			Resources:   getPrivateResources(),
			DisableAuth: true,
		},
		Public: apiserver.PublicAPIOptions{
			Enable:    true,
			Address:   "127.0.0.1",
			Port:      publicPort,
			Scheme:    getPublicScheme(),
			Resources: publicResources,
			Middleware: []func(http.Handler) http.Handler{
				authn.Middleware(authn.Config{}),
				authz.Middleware(authorizer, logger, publicResources),
			},
		},
		StorageFactory: storageFactory,
		Logger:         logger,
	})
	if err != nil {
		t.Fatalf("create API server: %v", err)
	}

	runDone := make(chan struct{})
	var runErr error
	go func() {
		runErr = server.Run()
		close(runDone)
	}()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			t.Errorf("shutdown API server: %v", err)
		}
		select {
		case <-runDone:
			if runErr != nil {
				t.Errorf("API server stopped with error: %v", runErr)
			}
		case <-ctx.Done():
			t.Errorf("API server did not stop: %v", ctx.Err())
		}
	})

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", publicPort)
	client := &http.Client{Timeout: 3 * time.Second}
	waitForPublicAPITestServer(t, client, baseURL+"/apis", runDone, &runErr)

	userInfo := platformAPITestUserInfo(t)
	request := func(method, path string, authenticated bool) (int, string) {
		t.Helper()
		req, err := http.NewRequestWithContext(context.Background(), method, baseURL+path, nil)
		if err != nil {
			t.Fatalf("create request: %v", err)
		}
		if authenticated {
			req.Header.Set(constants.HeaderEndpointAPIUserInfo, userInfo)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("request %s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read response for %s %s: %v", method, path, err)
		}
		return resp.StatusCode, string(body)
	}

	privateURL := fmt.Sprintf("https://127.0.0.1:%d", privatePort)
	privateTransport := &http.Transport{TLSClientConfig: &tls.Config{
		InsecureSkipVerify: true, //nolint:gosec // Loopback API server uses a self-signed test certificate.
	}}
	t.Cleanup(privateTransport.CloseIdleConnections)
	privateClient := &http.Client{Timeout: 3 * time.Second, Transport: privateTransport}
	waitForPublicAPITestServer(t, privateClient, privateURL+"/apis", runDone, &runErr)
	privateRequest := func(method, path string, body []byte) (int, string) {
		t.Helper()
		req, err := http.NewRequestWithContext(context.Background(), method, privateURL+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := privateClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode, string(data)
	}
	return catalogAuthorizationTestServer{
		request: request, privateRequest: privateRequest, stores: stores, authorizer: authorizer,
		version: version, channel: channel, versionStore: versionStore, channelStore: channelStore,
	}
}

func TestPublicAPICatalogBindingAuthorization(t *testing.T) {
	server := newCatalogAuthorizationTestServer(t)
	request, privateRequest := server.request, server.privateRequest
	stores, authorizer := server.stores, server.authorizer
	apiPrefix := "/apis/gcp.managed.openshift.io/v1"
	for _, path := range []string{"/versions", "/versions/4.22.1", "/channels", "/channels/stable"} {
		if status, body := request(http.MethodGet, apiPrefix+path, true); status != http.StatusForbidden {
			t.Fatalf("catalog without binding %s = %d: %s", path, status, body)
		}
	}
	if status, body := privateRequest(http.MethodGet, apiPrefix, nil); status != http.StatusOK || !strings.Contains(body, `"platformrolebindings"`) {
		t.Fatalf("private binding discovery = %d: %s", status, body)
	}
	if status, body := request(http.MethodGet, apiPrefix, true); status != http.StatusOK || strings.Contains(body, `"platformrolebindings"`) {
		t.Fatalf("public discovery = %d: %s", status, body)
	}
	seedPlatformRole(t, stores, "authenticated-user", []string{"version.get", "version.list", "channel.get", "channel.list"})
	binding := &privatev1.PlatformRoleBinding{
		TypeMeta:   metav1.TypeMeta{APIVersion: privatev1.GroupVersion.String(), Kind: "PlatformRoleBinding"},
		ObjectMeta: metav1.ObjectMeta{Name: "authenticated-users"},
		Spec: privatev1.RoleBindingSpec{Subject: "*", RoleRef: privatev1.RoleRef{
			APIGroup: privatev1.GroupVersion.Group, Kind: "PlatformRole", Name: "authenticated-user",
		}},
	}
	writeBinding := func(method string) {
		t.Helper()
		data, err := json.Marshal(binding)
		if err != nil {
			t.Fatal(err)
		}
		path, want := apiPrefix+"/platformrolebindings", http.StatusCreated
		if method == http.MethodPut {
			path += "/" + binding.Name
			want = http.StatusOK
		}
		status, body := privateRequest(method, path, data)
		if status != want {
			t.Fatalf("private binding %s = %d: %s", method, status, body)
		}
		if err := json.Unmarshal([]byte(body), binding); err != nil {
			t.Fatal(err)
		}
		if err := authorizer.Reload(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	writeBinding(http.MethodPost)
	testCatalogReadsAndDeniedRequests(t, request, apiPrefix)
	t.Run("conditions filter public objects before pagination", func(t *testing.T) {
		testCatalogConditionalLists(t, server, apiPrefix, binding, writeBinding)
	})
	t.Run("principal condition retains empty list semantics", func(t *testing.T) {
		testCatalogPrincipalCondition(t, request, apiPrefix, binding, writeBinding)
	})
	t.Run("deleting binding revokes catalog grant", func(t *testing.T) {
		testCatalogRevocation(t, server, apiPrefix, binding, writeBinding)
	})
}

func testCatalogReadsAndDeniedRequests(t *testing.T, request catalogPublicRequester, apiPrefix string) {
	t.Helper()
	for _, path := range []struct {
		name string
		path string
	}{
		{name: "list versions", path: apiPrefix + "/versions"},
		{name: "list channels", path: apiPrefix + "/channels"},
	} {
		t.Run(path.name, func(t *testing.T) {
			if got, _ := request(http.MethodGet, path.path, true); got != http.StatusOK {
				t.Errorf("authenticated GET %s = %d, want %d", path.path, got, http.StatusOK)
			}
		})
	}
	for _, path := range []struct {
		name         string
		path         string
		wantContains string
		wantAbsent   string
	}{
		{name: "get version", path: apiPrefix + "/versions/4.22.1", wantContains: "4.22.1", wantAbsent: "releaseImage"},
		{name: "get channel", path: apiPrefix + "/channels/stable", wantContains: "4.22.1"},
	} {
		t.Run(path.name, func(t *testing.T) {
			status, body := request(http.MethodGet, path.path, true)
			if status != http.StatusOK {
				t.Fatalf("authenticated GET %s = %d, want %d: %s", path.path, status, http.StatusOK, body)
			}
			if !strings.Contains(body, path.wantContains) {
				t.Errorf("response %s does not contain %q", path.path, path.wantContains)
			}
			if path.wantAbsent != "" && strings.Contains(body, path.wantAbsent) {
				t.Errorf("response %s unexpectedly contains %q", path.path, path.wantAbsent)
			}
		})
	}
	for _, path := range []string{
		apiPrefix + "/versions/4.22.2",
		apiPrefix + "/channels/fast",
	} {
		if got, _ := request(http.MethodGet, path, true); got != http.StatusNotFound {
			t.Errorf("authenticated GET %s = %d, want %d for missing catalog item", path, got, http.StatusNotFound)
		}
	}

	deniedRequests := []struct {
		name          string
		method        string
		path          string
		authenticated bool
		wantStatus    int
	}{
		{name: "unauthenticated cluster list", method: http.MethodGet, path: apiPrefix + "/clusters", wantStatus: http.StatusUnauthorized},
		{name: "unauthenticated cluster create", method: http.MethodPost, path: apiPrefix + "/namespaces/project-a/clusters", wantStatus: http.StatusUnauthorized},
		{name: "unauthenticated catalog list", method: http.MethodGet, path: apiPrefix + "/versions", wantStatus: http.StatusUnauthorized},
		{name: "unauthenticated catalog item", method: http.MethodGet, path: apiPrefix + "/versions/4.22.1", wantStatus: http.StatusUnauthorized},
		{name: "unauthenticated catalog write", method: http.MethodPost, path: apiPrefix + "/versions", wantStatus: http.StatusUnauthorized},
		{name: "unauthenticated catalog watch", method: http.MethodGet, path: apiPrefix + "/versions?watch=true", wantStatus: http.StatusUnauthorized},
		{name: "authenticated catalog write", method: http.MethodPost, path: apiPrefix + "/versions", authenticated: true, wantStatus: http.StatusForbidden},
		{name: "authenticated catalog watch", method: http.MethodGet, path: apiPrefix + "/versions?watch=true", authenticated: true, wantStatus: http.StatusForbidden},
		{name: "authenticated channel write", method: http.MethodPost, path: apiPrefix + "/channels", authenticated: true, wantStatus: http.StatusForbidden},
		{name: "authenticated channel watch", method: http.MethodGet, path: apiPrefix + "/channels?watch=true", authenticated: true, wantStatus: http.StatusForbidden},
		{name: "private binding list", method: http.MethodGet, path: apiPrefix + "/platformrolebindings", authenticated: true, wantStatus: http.StatusForbidden},
		{name: "private binding create", method: http.MethodPost, path: apiPrefix + "/platformrolebindings", authenticated: true, wantStatus: http.StatusForbidden},
		{name: "private binding update", method: http.MethodPut, path: apiPrefix + "/platformrolebindings/authenticated-users", authenticated: true, wantStatus: http.StatusForbidden},
		{name: "private binding delete", method: http.MethodDelete, path: apiPrefix + "/platformrolebindings/authenticated-users", authenticated: true, wantStatus: http.StatusForbidden},
		{name: "namespace resource", method: http.MethodGet, path: apiPrefix + "/namespaces/project-a/clusters", authenticated: true, wantStatus: http.StatusForbidden},
		{name: "cross namespace collection", method: http.MethodGet, path: apiPrefix + "/clusters", authenticated: true, wantStatus: http.StatusForbidden},
		{name: "forged namespace for platform resource", method: http.MethodGet, path: apiPrefix + "/namespaces/project-a/versions", authenticated: true, wantStatus: http.StatusForbidden},
		{name: "foreign API group", method: http.MethodGet, path: "/apis/other.example.com/v1/versions", authenticated: true, wantStatus: http.StatusForbidden},
	}
	for _, tt := range deniedRequests {
		t.Run(tt.name, func(t *testing.T) {
			if got, _ := request(tt.method, tt.path, tt.authenticated); got != tt.wantStatus {
				t.Errorf("%s %s = %d, want %d", tt.method, tt.path, got, tt.wantStatus)
			}
		})
	}

}

func testCatalogConditionalLists(t *testing.T, server catalogAuthorizationTestServer, apiPrefix string, binding *privatev1.PlatformRoleBinding, writeBinding func(string)) {
	t.Helper()
	request := server.request
	version, channel := server.version, server.channel
	versionStore, channelStore := server.versionStore, server.channelStore
	for _, name := range []string{"candidate", "eus"} {
		item := channel.DeepCopy()
		item.Name = name
		if err := channelStore.Create(context.Background(), item); err != nil {
			t.Fatal(err)
		}
	}
	candidate := version.DeepCopy()
	candidate.Name = "4.23.0"
	candidate.Spec.ChannelGroups = []string{"candidate"}
	if err := versionStore.Create(context.Background(), candidate); err != nil {
		t.Fatal(err)
	}
	condition := `(context.resourcePlural == "channels" && ["stable", "eus"].contains(context.resourceName)) || (context.resourcePlural == "versions" && context.spec.channelGroups.contains("stable"))`
	binding.Spec.Condition = &condition
	writeBinding(http.MethodPut)
	path := apiPrefix + "/channels?limit=1"
	var names []string
	for range 3 {
		status, body := request(http.MethodGet, path, true)
		if status != http.StatusOK {
			t.Fatalf("filtered list = %d: %s", status, body)
		}
		var list publicv1.ChannelList
		if err := json.Unmarshal([]byte(body), &list); err != nil {
			t.Fatal(err)
		}
		if len(list.Items) != 1 {
			t.Fatalf("page items = %d, want one visible item: %s", len(list.Items), body)
		}
		names = append(names, list.Items[0].Name)
		if list.Continue == "" {
			break
		}
		path = apiPrefix + "/channels?limit=1&continue=" + url.QueryEscape(list.Continue)
	}
	if !slices.Equal(names, []string{"eus", "stable"}) {
		t.Fatalf("visible channels = %v", names)
	}
	for _, path := range []string{"/channels/candidate", "/versions/4.23.0"} {
		if status, body := request(http.MethodGet, apiPrefix+path, true); status != http.StatusForbidden {
			t.Fatalf("hidden item %s = %d: %s", path, status, body)
		}
	}
	if status, body := request(http.MethodGet, apiPrefix+"/versions", true); status != http.StatusOK || strings.Contains(body, candidate.Name) || strings.Contains(body, "releaseImage") {
		t.Fatalf("filtered public versions = %d: %s", status, body)
	}
}

func testCatalogPrincipalCondition(t *testing.T, request catalogPublicRequester, apiPrefix string, binding *privatev1.PlatformRoleBinding, writeBinding func(string)) {
	t.Helper()
	condition := `principal == User::"other@example.com"`
	binding.Spec.Condition = &condition
	writeBinding(http.MethodPut)
	status, body := request(http.MethodGet, apiPrefix+"/versions", true)
	var list publicv1.VersionList
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatal(err)
	}
	if status != http.StatusOK || len(list.Items) != 0 {
		t.Fatalf("principal-filtered list = %d: %s", status, body)
	}
	if status, body := request(http.MethodGet, apiPrefix+"/versions/4.22.1", true); status != http.StatusForbidden {
		t.Fatalf("principal-filtered get = %d: %s", status, body)
	}
}

func testCatalogRevocation(t *testing.T, server catalogAuthorizationTestServer, apiPrefix string, binding *privatev1.PlatformRoleBinding, writeBinding func(string)) {
	t.Helper()
	request, privateRequest := server.request, server.privateRequest
	authorizer := server.authorizer
	binding.Spec.Condition = nil
	writeBinding(http.MethodPut)
	if status, body := request(http.MethodGet, apiPrefix+"/versions", true); status != http.StatusOK {
		t.Fatalf("restored grant = %d: %s", status, body)
	}
	if status, body := privateRequest(http.MethodDelete, apiPrefix+"/platformrolebindings/"+binding.Name, nil); status != http.StatusOK {
		t.Fatalf("delete binding = %d: %s", status, body)
	}
	if err := authorizer.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/versions", "/versions/4.22.1", "/channels", "/channels/stable"} {
		if status, body := request(http.MethodGet, apiPrefix+path, true); status != http.StatusForbidden {
			t.Fatalf("after revocation %s = %d: %s", path, status, body)
		}
	}
}

func freePublicAPITestPort(t *testing.T) int {
	t.Helper()
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve test port: %v", err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func waitForPublicAPITestServer(t *testing.T, client *http.Client, url string, runDone <-chan struct{}, runErr *error) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-runDone:
			t.Fatalf("API server stopped before becoming ready: %v", *runErr)
		default:
		}
		request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
		if err != nil {
			t.Fatalf("create readiness request: %v", err)
		}
		resp, err := client.Do(request)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("API server at %s did not become ready", url)
}

func platformAPITestUserInfo(t *testing.T) string {
	t.Helper()
	claims, err := json.Marshal(map[string]any{
		"email":          "alice@example.com",
		"email_verified": true,
	})
	if err != nil {
		t.Fatalf("marshal user claims: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(claims)
}
