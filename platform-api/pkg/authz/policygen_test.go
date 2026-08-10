package authz

import (
	"strings"
	"testing"

	cedar "github.com/cedar-policy/cedar-go"
)

func TestGeneratePolicies_SampleRoles(t *testing.T) {
	roles := []RoleDefinition{
		{
			Name:  "cluster-viewer",
			Scope: "namespace",
			Permissions: []string{
				"cluster.list",
				"cluster.get",
				"nodepool.list",
				"nodepool.get",
			},
		},
		{
			Name:  "platform-admin",
			Scope: "platform",
			Permissions: []string{
				"platformrolebinding.create",
				"platformrolebinding.list",
				"platformrolebinding.get",
				"platformrolebinding.update",
				"platformrolebinding.delete",
				"customrole.create",
				"customrole.list",
				"customrole.get",
				"customrole.update",
				"customrole.delete",
			},
		},
	}

	ps, err := GeneratePolicies(roles)
	if err != nil {
		t.Fatalf("GeneratePolicies failed: %v", err)
	}

	// Should have one policy per role.
	count := 0
	for range ps.All() {
		count++
	}
	if count != 2 {
		t.Fatalf("expected 2 policies, got %d", count)
	}

	// Verify the viewer policy exists and contains expected actions.
	viewerPolicy := ps.Get("role-cluster-viewer")
	if viewerPolicy == nil {
		t.Fatal("expected policy role-cluster-viewer to exist")
	}
	viewerCedar := string(viewerPolicy.MarshalCedar())
	for _, action := range []string{"ListClusters", "GetCluster", "ListNodepools", "GetNodepool"} {
		if !strings.Contains(viewerCedar, `Action::"`+action+`"`) {
			t.Errorf("viewer policy should contain action %q, got:\n%s", action, viewerCedar)
		}
	}
	if !strings.Contains(viewerCedar, "principal in resource") {
		t.Errorf("viewer policy should contain 'principal in resource', got:\n%s", viewerCedar)
	}

	// Verify the platform-admin policy exists and contains expected actions.
	adminPolicy := ps.Get("role-platform-admin")
	if adminPolicy == nil {
		t.Fatal("expected policy role-platform-admin to exist")
	}
	adminCedar := string(adminPolicy.MarshalCedar())
	for _, action := range []string{"CreatePlatformRoleBinding", "ListPlatformRoleBindings", "CreateCustomRole"} {
		if !strings.Contains(adminCedar, `Action::"`+action+`"`) {
			t.Errorf("admin policy should contain action %q, got:\n%s", action, adminCedar)
		}
	}
	if !strings.Contains(adminCedar, "principal in resource") {
		t.Errorf("admin policy should contain 'principal in resource', got:\n%s", adminCedar)
	}
}

func TestGeneratePolicies_EmptyRoles(t *testing.T) {
	ps, err := GeneratePolicies(nil)
	if err != nil {
		t.Fatalf("GeneratePolicies failed: %v", err)
	}

	count := 0
	for range ps.All() {
		count++
	}
	if count != 0 {
		t.Fatalf("expected 0 policies for empty roles, got %d", count)
	}
}

func TestGeneratePolicies_EmptySlice(t *testing.T) {
	ps, err := GeneratePolicies([]RoleDefinition{})
	if err != nil {
		t.Fatalf("GeneratePolicies failed: %v", err)
	}

	count := 0
	for range ps.All() {
		count++
	}
	if count != 0 {
		t.Fatalf("expected 0 policies for empty slice, got %d", count)
	}
}

func TestGeneratePolicies_RoleWithNoPermissions(t *testing.T) {
	roles := []RoleDefinition{
		{
			Name:        "empty-role",
			Scope:       "namespace",
			Permissions: []string{},
		},
	}

	ps, err := GeneratePolicies(roles)
	if err != nil {
		t.Fatalf("GeneratePolicies failed: %v", err)
	}

	count := 0
	for range ps.All() {
		count++
	}
	if count != 0 {
		t.Fatalf("expected 0 policies for role with no permissions, got %d", count)
	}
}

func TestGeneratePolicies_NamespaceScopedRole(t *testing.T) {
	roles := []RoleDefinition{
		{
			Name:  "ns-editor",
			Scope: "namespace",
			Permissions: []string{
				"cluster.create",
				"cluster.list",
				"cluster.get",
				"cluster.update",
				"cluster.delete",
				"nodepool.create",
				"nodepool.list",
				"nodepool.get",
				"nodepool.update",
				"nodepool.delete",
			},
		},
	}

	ps, err := GeneratePolicies(roles)
	if err != nil {
		t.Fatalf("GeneratePolicies failed: %v", err)
	}

	count := 0
	for range ps.All() {
		count++
	}
	if count != 1 {
		t.Fatalf("expected 1 policy, got %d", count)
	}

	policy := ps.Get("role-ns-editor")
	if policy == nil {
		t.Fatal("expected policy role-ns-editor to exist")
	}

	cedarText := string(policy.MarshalCedar())

	// Verify it's a permit policy.
	if !strings.HasPrefix(cedarText, "permit") {
		t.Errorf("expected policy to start with 'permit', got:\n%s", cedarText)
	}

	// Verify it has the when condition.
	if !strings.Contains(cedarText, "principal in resource") {
		t.Errorf("expected 'principal in resource' condition, got:\n%s", cedarText)
	}
}

func TestGeneratePolicies_PlatformScopedRole(t *testing.T) {
	roles := []RoleDefinition{
		{
			Name:  "platform-viewer",
			Scope: "platform",
			Permissions: []string{
				"platformrolebinding.list",
				"platformrolebinding.get",
				"customrole.list",
				"customrole.get",
			},
		},
	}

	ps, err := GeneratePolicies(roles)
	if err != nil {
		t.Fatalf("GeneratePolicies failed: %v", err)
	}

	count := 0
	for range ps.All() {
		count++
	}
	if count != 1 {
		t.Fatalf("expected 1 policy, got %d", count)
	}

	policy := ps.Get("role-platform-viewer")
	if policy == nil {
		t.Fatal("expected policy role-platform-viewer to exist")
	}

	cedarText := string(policy.MarshalCedar())

	// Verify it's a permit policy.
	if !strings.HasPrefix(cedarText, "permit") {
		t.Errorf("expected policy to start with 'permit', got:\n%s", cedarText)
	}

	// Verify expected actions are present.
	for _, action := range []string{"GetCustomRole", "GetPlatformRoleBinding", "ListCustomRoles", "ListPlatformRoleBindings"} {
		if !strings.Contains(cedarText, `Action::"`+action+`"`) {
			t.Errorf("platform-viewer policy should contain action %q, got:\n%s", action, cedarText)
		}
	}

	// Verify the when condition.
	if !strings.Contains(cedarText, "principal in resource") {
		t.Errorf("expected 'principal in resource' condition, got:\n%s", cedarText)
	}
}

func TestGeneratePolicies_AllBuiltInRoles(t *testing.T) {
	// Simulate the full set of built-in roles from a typical config.
	roles := []RoleDefinition{
		{
			Name:  "cluster-viewer",
			Scope: "namespace",
			Permissions: []string{
				"cluster.list", "cluster.get",
				"nodepool.list", "nodepool.get",
			},
		},
		{
			Name:  "cluster-editor",
			Scope: "namespace",
			Permissions: []string{
				"cluster.create", "cluster.list", "cluster.get",
				"cluster.update", "cluster.delete",
				"nodepool.create", "nodepool.list", "nodepool.get",
				"nodepool.update", "nodepool.delete",
			},
		},
		{
			Name:  "namespace-admin",
			Scope: "namespace",
			Permissions: []string{
				"cluster.create", "cluster.list", "cluster.get",
				"cluster.update", "cluster.delete",
				"nodepool.create", "nodepool.list", "nodepool.get",
				"nodepool.update", "nodepool.delete",
				"rolebinding.create", "rolebinding.list", "rolebinding.get",
				"rolebinding.update", "rolebinding.delete",
			},
		},
		{
			Name:  "platform-admin",
			Scope: "platform",
			Permissions: []string{
				"platformrolebinding.create", "platformrolebinding.list",
				"platformrolebinding.get", "platformrolebinding.update",
				"platformrolebinding.delete",
				"customrole.create", "customrole.list", "customrole.get",
				"customrole.update", "customrole.delete",
			},
		},
	}

	ps, err := GeneratePolicies(roles)
	if err != nil {
		t.Fatalf("GeneratePolicies failed: %v", err)
	}

	count := 0
	for range ps.All() {
		count++
	}
	if count != 4 {
		t.Fatalf("expected 4 policies, got %d", count)
	}

	// Verify each role produced a policy with the correct ID.
	for _, role := range roles {
		policyID := "role-" + role.Name
		if ps.Get(cedar.PolicyID(policyID)) == nil {
			t.Errorf("expected policy %q to exist", policyID)
		}
	}
}

func TestGeneratePolicies_SinglePermission(t *testing.T) {
	roles := []RoleDefinition{
		{
			Name:        "minimal",
			Scope:       "namespace",
			Permissions: []string{"cluster.get"},
		},
	}

	ps, err := GeneratePolicies(roles)
	if err != nil {
		t.Fatalf("GeneratePolicies failed: %v", err)
	}

	policy := ps.Get("role-minimal")
	if policy == nil {
		t.Fatal("expected policy role-minimal to exist")
	}

	cedarText := string(policy.MarshalCedar())

	// With a single action, the cedar output may use `action ==` or `action in [...]`.
	if !strings.Contains(cedarText, "GetCluster") {
		t.Errorf("expected GetCluster action in policy, got:\n%s", cedarText)
	}
	if !strings.Contains(cedarText, "principal in resource") {
		t.Errorf("expected 'principal in resource' condition, got:\n%s", cedarText)
	}
}

func TestGeneratePolicies_PolicyCedarFormat(t *testing.T) {
	roles := []RoleDefinition{
		{
			Name:  "cluster-viewer",
			Scope: "namespace",
			Permissions: []string{
				"cluster.list",
				"cluster.get",
			},
		},
	}

	ps, err := GeneratePolicies(roles)
	if err != nil {
		t.Fatalf("GeneratePolicies failed: %v", err)
	}

	policy := ps.Get("role-cluster-viewer")
	if policy == nil {
		t.Fatal("expected policy to exist")
	}

	cedarText := string(policy.MarshalCedar())
	t.Logf("Generated Cedar policy:\n%s", cedarText)

	// Verify the policy structure.
	if !strings.Contains(cedarText, "permit") {
		t.Error("expected 'permit' in policy text")
	}
	if !strings.Contains(cedarText, "when") {
		t.Error("expected 'when' clause in policy text")
	}
}
