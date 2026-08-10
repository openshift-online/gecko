package authz

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTestFile is a helper that writes content to a file in the given directory.
func writeTestFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
}

func TestLoadConfig_ValidRolesAndBootstrap(t *testing.T) {
	dir := t.TempDir()

	writeTestFile(t, dir, "roles.yaml", `
roles:
  - name: cluster-admin
    scope: platform
    permissions:
      - cluster.create
      - cluster.list
      - cluster.get
      - cluster.update
      - cluster.delete
  - name: viewer
    scope: namespace
    permissions:
      - cluster.get
      - cluster.list
      - nodepool.get
      - nodepool.list
`)

	writeTestFile(t, dir, "bootstrap.yaml", `
platformRoleBindings:
  - name: admin-binding
    subject: admin@example.com
    roleRef: cluster-admin
`)

	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if len(cfg.Roles) != 2 {
		t.Fatalf("expected 2 roles, got %d", len(cfg.Roles))
	}
	if len(cfg.BootstrapBindings) != 1 {
		t.Fatalf("expected 1 bootstrap binding, got %d", len(cfg.BootstrapBindings))
	}

	// Verify role labels.
	if !cfg.RoleLabels["cluster-admin"] {
		t.Error("expected cluster-admin in RoleLabels")
	}
	if !cfg.RoleLabels["viewer"] {
		t.Error("expected viewer in RoleLabels")
	}

	// Verify scope-specific labels.
	if !cfg.PlatformRoleLabels["cluster-admin"] {
		t.Error("expected cluster-admin in PlatformRoleLabels")
	}
	if cfg.PlatformRoleLabels["viewer"] {
		t.Error("viewer should not be in PlatformRoleLabels")
	}
	if !cfg.NamespaceRoleLabels["viewer"] {
		t.Error("expected viewer in NamespaceRoleLabels")
	}
	if cfg.NamespaceRoleLabels["cluster-admin"] {
		t.Error("cluster-admin should not be in NamespaceRoleLabels")
	}

	// Verify bootstrap binding.
	b := cfg.BootstrapBindings[0]
	if b.Name != "admin-binding" {
		t.Errorf("expected binding name admin-binding, got %q", b.Name)
	}
	if b.Subject != "admin@example.com" {
		t.Errorf("expected subject admin@example.com, got %q", b.Subject)
	}
	if b.RoleRef != "cluster-admin" {
		t.Errorf("expected roleRef cluster-admin, got %q", b.RoleRef)
	}
}

func TestLoadConfig_MissingBootstrap(t *testing.T) {
	dir := t.TempDir()

	writeTestFile(t, dir, "roles.yaml", `
roles:
  - name: viewer
    scope: namespace
    permissions:
      - cluster.list
`)

	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("LoadConfig should succeed without bootstrap.yaml: %v", err)
	}
	if len(cfg.BootstrapBindings) != 0 {
		t.Errorf("expected 0 bootstrap bindings, got %d", len(cfg.BootstrapBindings))
	}
}

func TestLoadConfig_MissingRolesYAML(t *testing.T) {
	dir := t.TempDir()

	_, err := LoadConfig(dir)
	if err == nil {
		t.Fatal("expected error for missing roles.yaml")
	}
	if !strings.Contains(err.Error(), "roles.yaml") {
		t.Errorf("error should mention roles.yaml, got: %v", err)
	}
}

func TestLoadConfig_InvalidScope(t *testing.T) {
	dir := t.TempDir()

	writeTestFile(t, dir, "roles.yaml", `
roles:
  - name: bad-role
    scope: global
    permissions:
      - cluster.list
`)

	_, err := LoadConfig(dir)
	if err == nil {
		t.Fatal("expected error for invalid scope")
	}
	if !strings.Contains(err.Error(), "invalid scope") {
		t.Errorf("error should mention invalid scope, got: %v", err)
	}
	if !strings.Contains(err.Error(), "global") {
		t.Errorf("error should mention the invalid scope value, got: %v", err)
	}
}

func TestLoadConfig_UnknownPermission(t *testing.T) {
	dir := t.TempDir()

	writeTestFile(t, dir, "roles.yaml", `
roles:
  - name: bad-perms
    scope: namespace
    permissions:
      - cluster.list
      - cluster.fly
`)

	_, err := LoadConfig(dir)
	if err == nil {
		t.Fatal("expected error for unknown permission")
	}
	if !strings.Contains(err.Error(), "unknown permission") {
		t.Errorf("error should mention unknown permission, got: %v", err)
	}
	if !strings.Contains(err.Error(), "cluster.fly") {
		t.Errorf("error should mention the invalid permission, got: %v", err)
	}
}

func TestLoadConfig_BootstrapUnknownRoleRef(t *testing.T) {
	dir := t.TempDir()

	writeTestFile(t, dir, "roles.yaml", `
roles:
  - name: viewer
    scope: platform
    permissions:
      - cluster.list
`)

	writeTestFile(t, dir, "bootstrap.yaml", `
platformRoleBindings:
  - name: bad-binding
    subject: user@example.com
    roleRef: nonexistent-role
`)

	_, err := LoadConfig(dir)
	if err == nil {
		t.Fatal("expected error for unknown roleRef")
	}
	if !strings.Contains(err.Error(), "unknown role") {
		t.Errorf("error should mention unknown role, got: %v", err)
	}
	if !strings.Contains(err.Error(), "nonexistent-role") {
		t.Errorf("error should mention the invalid roleRef, got: %v", err)
	}
}

func TestLoadConfig_BootstrapNamespaceScopedRoleRef(t *testing.T) {
	dir := t.TempDir()

	writeTestFile(t, dir, "roles.yaml", `
roles:
  - name: ns-viewer
    scope: namespace
    permissions:
      - cluster.list
  - name: platform-admin
    scope: platform
    permissions:
      - cluster.create
`)

	writeTestFile(t, dir, "bootstrap.yaml", `
platformRoleBindings:
  - name: bad-binding
    subject: user@example.com
    roleRef: ns-viewer
`)

	_, err := LoadConfig(dir)
	if err == nil {
		t.Fatal("expected error for namespace-scoped roleRef in bootstrap")
	}
	if !strings.Contains(err.Error(), "namespace-scoped") {
		t.Errorf("error should mention namespace-scoped, got: %v", err)
	}
	if !strings.Contains(err.Error(), "ns-viewer") {
		t.Errorf("error should mention the role name, got: %v", err)
	}
}

func TestLoadConfig_EmptyRoleName(t *testing.T) {
	dir := t.TempDir()

	writeTestFile(t, dir, "roles.yaml", `
roles:
  - name: ""
    scope: namespace
    permissions:
      - cluster.list
`)

	_, err := LoadConfig(dir)
	if err == nil {
		t.Fatal("expected error for empty role name")
	}
	if !strings.Contains(err.Error(), "empty name") {
		t.Errorf("error should mention empty name, got: %v", err)
	}
}

func TestPermissionToAction(t *testing.T) {
	tests := []struct {
		perm string
		want string
	}{
		// Cluster permissions.
		{"cluster.create", "CreateCluster"},
		{"cluster.list", "ListClusters"},
		{"cluster.get", "GetCluster"},
		{"cluster.update", "UpdateCluster"},
		{"cluster.delete", "DeleteCluster"},

		// Nodepool permissions.
		{"nodepool.create", "CreateNodepool"},
		{"nodepool.list", "ListNodepools"},
		{"nodepool.get", "GetNodepool"},
		{"nodepool.update", "UpdateNodepool"},
		{"nodepool.delete", "DeleteNodepool"},

		// RoleBinding permissions.
		{"rolebinding.create", "CreateRoleBinding"},
		{"rolebinding.list", "ListRoleBindings"},
		{"rolebinding.get", "GetRoleBinding"},
		{"rolebinding.update", "UpdateRoleBinding"},
		{"rolebinding.delete", "DeleteRoleBinding"},

		// PlatformRoleBinding permissions.
		{"platformrolebinding.create", "CreatePlatformRoleBinding"},
		{"platformrolebinding.list", "ListPlatformRoleBindings"},
		{"platformrolebinding.get", "GetPlatformRoleBinding"},
		{"platformrolebinding.update", "UpdatePlatformRoleBinding"},
		{"platformrolebinding.delete", "DeletePlatformRoleBinding"},

		// CustomRole permissions.
		{"customrole.create", "CreateCustomRole"},
		{"customrole.list", "ListCustomRoles"},
		{"customrole.get", "GetCustomRole"},
		{"customrole.update", "UpdateCustomRole"},
		{"customrole.delete", "DeleteCustomRole"},
	}

	for _, tt := range tests {
		t.Run(tt.perm, func(t *testing.T) {
			got := PermissionToAction(tt.perm)
			if got != tt.want {
				t.Errorf("PermissionToAction(%q) = %q, want %q", tt.perm, got, tt.want)
			}
		})
	}
}

func TestPermissionToAction_InvalidFormat(t *testing.T) {
	// A string without a dot should be returned as-is.
	got := PermissionToAction("nodotshere")
	if got != "nodotshere" {
		t.Errorf("PermissionToAction(\"nodotshere\") = %q, want \"nodotshere\"", got)
	}
}
