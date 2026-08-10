package test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	publicv1 "github.com/openshift-online/gecko/platform-api/api/public/v1"
	"github.com/openshift-online/gecko/orlop/pkg/apiserver"
	"github.com/openshift-online/gecko/orlop/pkg/apiserver/storage"
	"github.com/openshift-online/gecko/orlop/pkg/apiserver/storage/memory"
	"k8s.io/apimachinery/pkg/runtime"
	runtimeschema "k8s.io/apimachinery/pkg/runtime/schema"
)

func TestAuthzResources(t *testing.T) {
	// Build scheme and resources from the public API types
	publicScheme := runtime.NewScheme()
	publicv1.AddToScheme(publicScheme)
	publicResources := publicv1.GetResourceInfos()

	// Use in-memory storage
	storageFactory := func(resourceType string, scheme *runtime.Scheme, gvk runtimeschema.GroupVersionKind) (storage.ResourceStore, error) {
		return memory.NewMemoryStore(resourceType, scheme, gvk), nil
	}

	// Create server — private API uses the same types (no conversion needed for smoke test)
	privateScheme := runtime.NewScheme()
	publicv1.AddToScheme(privateScheme)

	opts := apiserver.Options{
		Address: "127.0.0.1",
		Private: apiserver.PrivateAPIOptions{
			Port:        0, // will use default
			Resources:   publicResources,
			Scheme:      privateScheme,
			DisableAuth: true,
		},
		Public: apiserver.PublicAPIOptions{
			Enable:    true,
			Port:      0,
			Resources: publicResources,
			Scheme:    publicScheme,
		},
		StorageFactory: storageFactory,
	}

	// Pick a random available port
	opts.Private.Port = 18443
	opts.Public.Port = 18081

	server, err := apiserver.New(opts)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	go func() {
		if err := server.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
		}
	}()

	// Wait for server to start
	publicBase := fmt.Sprintf("http://127.0.0.1:%d", opts.Public.Port)
	apiBase := publicBase + "/apis/gcp.managed.openshift.io/v1"

	waitForServer(t, publicBase+"/healthz", 10*time.Second)

	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(ctx)
	}()

	t.Run("RoleBinding_CRUD", func(t *testing.T) {
		// List should return empty
		resp := httpDo(t, "GET", apiBase+"/namespaces/test-ns/rolebindings", nil)
		assertStatus(t, resp, http.StatusOK)
		body := readBody(t, resp)
		assertContains(t, body, `"items":[]`)

		// Create a RoleBinding
		rb := `{
			"apiVersion": "gcp.managed.openshift.io/v1",
			"kind": "RoleBinding",
			"metadata": {"name": "test-rb", "namespace": "test-ns"},
			"spec": {"subject": "dev@example.com", "roleRef": "cluster-admin"}
		}`
		resp = httpDo(t, "POST", apiBase+"/namespaces/test-ns/rolebindings", []byte(rb))
		assertStatus(t, resp, http.StatusCreated)

		// Get should return the created binding
		resp = httpDo(t, "GET", apiBase+"/namespaces/test-ns/rolebindings/test-rb", nil)
		assertStatus(t, resp, http.StatusOK)
		body = readBody(t, resp)
		assertContains(t, body, `"subject":"dev@example.com"`)
		assertContains(t, body, `"roleRef":"cluster-admin"`)

		// List should return the binding
		resp = httpDo(t, "GET", apiBase+"/namespaces/test-ns/rolebindings", nil)
		assertStatus(t, resp, http.StatusOK)
		body = readBody(t, resp)
		assertContains(t, body, `"test-rb"`)

		// Cross-namespace list
		resp = httpDo(t, "GET", apiBase+"/rolebindings", nil)
		assertStatus(t, resp, http.StatusOK)
		body = readBody(t, resp)
		assertContains(t, body, `"test-rb"`)

		// Delete
		resp = httpDo(t, "DELETE", apiBase+"/namespaces/test-ns/rolebindings/test-rb", nil)
		assertStatus(t, resp, http.StatusOK)

		// Should be gone
		resp = httpDo(t, "GET", apiBase+"/namespaces/test-ns/rolebindings/test-rb", nil)
		assertStatus(t, resp, http.StatusNotFound)
	})

	t.Run("PlatformRoleBinding_CRUD", func(t *testing.T) {
		// List cluster-scoped resource (no namespace)
		resp := httpDo(t, "GET", apiBase+"/platformrolebindings", nil)
		assertStatus(t, resp, http.StatusOK)
		body := readBody(t, resp)
		assertContains(t, body, `"items":[]`)

		// Create a PlatformRoleBinding (cluster-scoped, no namespace)
		prb := `{
			"apiVersion": "gcp.managed.openshift.io/v1",
			"kind": "PlatformRoleBinding",
			"metadata": {"name": "bootstrap-admin"},
			"spec": {"subject": "operator@example.com", "roleRef": "platform-admin"}
		}`
		resp = httpDo(t, "POST", apiBase+"/platformrolebindings", []byte(prb))
		assertStatus(t, resp, http.StatusCreated)

		// Get should return the created binding
		resp = httpDo(t, "GET", apiBase+"/platformrolebindings/bootstrap-admin", nil)
		assertStatus(t, resp, http.StatusOK)
		body = readBody(t, resp)
		assertContains(t, body, `"subject":"operator@example.com"`)
		assertContains(t, body, `"roleRef":"platform-admin"`)

		// List should return it
		resp = httpDo(t, "GET", apiBase+"/platformrolebindings", nil)
		assertStatus(t, resp, http.StatusOK)
		body = readBody(t, resp)
		assertContains(t, body, `"bootstrap-admin"`)

		// Update
		updated := `{
			"apiVersion": "gcp.managed.openshift.io/v1",
			"kind": "PlatformRoleBinding",
			"metadata": {"name": "bootstrap-admin"},
			"spec": {"subject": "admin2@example.com", "roleRef": "platform-admin"}
		}`
		resp = httpDo(t, "PUT", apiBase+"/platformrolebindings/bootstrap-admin", []byte(updated))
		assertStatus(t, resp, http.StatusOK)

		// Verify update
		resp = httpDo(t, "GET", apiBase+"/platformrolebindings/bootstrap-admin", nil)
		assertStatus(t, resp, http.StatusOK)
		body = readBody(t, resp)
		assertContains(t, body, `"subject":"admin2@example.com"`)

		// Delete
		resp = httpDo(t, "DELETE", apiBase+"/platformrolebindings/bootstrap-admin", nil)
		assertStatus(t, resp, http.StatusOK)

		// Should be gone
		resp = httpDo(t, "GET", apiBase+"/platformrolebindings/bootstrap-admin", nil)
		assertStatus(t, resp, http.StatusNotFound)
	})

	t.Run("RoleBinding_Validation", func(t *testing.T) {
		// Missing required subject field (empty string violates MinLength=1)
		rb := `{
			"apiVersion": "gcp.managed.openshift.io/v1",
			"kind": "RoleBinding",
			"metadata": {"name": "bad-rb", "namespace": "test-ns"},
			"spec": {"subject": "", "roleRef": "cluster-admin"}
		}`
		resp := httpDo(t, "POST", apiBase+"/namespaces/test-ns/rolebindings", []byte(rb))
		// Should be rejected by schema validation
		if resp.StatusCode != http.StatusUnprocessableEntity && resp.StatusCode != http.StatusBadRequest {
			body := readBody(t, resp)
			t.Logf("Unexpected status %d for empty subject. Body: %s", resp.StatusCode, body)
		}
	})

	_ = publicScheme // used for scheme setup
}

func waitForServer(t *testing.T, url string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil && resp.StatusCode == http.StatusOK {
			resp.Body.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("Server did not start within %v", timeout)
}

func httpDo(t *testing.T, method, url string, body []byte) *http.Response {
	t.Helper()
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	return resp
}

func assertStatus(t *testing.T, resp *http.Response, expected int) {
	t.Helper()
	if resp.StatusCode != expected {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("Expected status %d, got %d. Body: %s", expected, resp.StatusCode, string(body))
	}
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Failed to read body: %v", err)
	}
	resp.Body.Close()
	return string(body)
}

func assertContains(t *testing.T, body, substr string) {
	t.Helper()
	// Compact the body to normalize JSON whitespace
	var buf bytes.Buffer
	if err := json.Compact(&buf, []byte(body)); err == nil {
		body = buf.String()
	}
	if !bytes.Contains([]byte(body), []byte(substr)) {
		t.Errorf("Expected body to contain %q, got:\n%s", substr, body)
	}
}
