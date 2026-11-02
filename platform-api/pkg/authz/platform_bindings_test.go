package authz

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"

	"github.com/openshift-online/gecko/orlop/pkg/apiserver/storage"
	privatev1 "github.com/openshift-online/gecko/platform-api/api/private/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestPlatformBindingsKeepBindingScopeBoundaries(t *testing.T) {
	ctx := context.Background()
	stores, authorizer := newTestAuthorizer(t)
	seedPermissionRole(t, stores, "mixed", []string{
		"version.get", "version.list", "channel.get", "channel.list", "cluster.get", "cluster.list", "nodepool.get",
	})
	seedPlatformBinding(t, stores, "shared", "mixed", "*", nil)
	namespaced := &privatev1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "shared", Namespace: "project-a"},
		Spec: privatev1.RoleBindingSpec{Subject: "alice@example.com", RoleRef: privatev1.RoleRef{
			Kind: "PlatformRole", Name: "mixed", APIGroup: privatev1.GroupVersion.Group,
		}},
	}
	if err := stores.RoleBindings.Create(ctx, namespaced); err != nil {
		t.Fatal(err)
	}
	if err := authorizer.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name      string
		email     string
		action    Action
		namespace string
		want      bool
	}{
		{name: "all authenticated version get", email: "bob@example.com", action: GetVersion, want: true},
		{name: "all authenticated version list", email: "bob@example.com", action: ListVersions, want: true},
		{name: "all authenticated channel get", email: "bob@example.com", action: GetChannel, want: true},
		{name: "all authenticated channel list", email: "bob@example.com", action: ListChannels, want: true},
		{name: "namespace cluster grant", email: "alice@example.com", action: GetCluster, namespace: "project-a", want: true},
		{name: "namespace nodepool grant", email: "alice@example.com", action: GetNodepool, namespace: "project-a", want: true},
		{name: "platform binding never grants namespace access", email: "bob@example.com", action: GetCluster, namespace: "project-a"},
		{name: "platform binding never grants cross namespace access", email: "bob@example.com", action: ListClusters},
		{name: "namespace grant stays in namespace", email: "alice@example.com", action: GetCluster, namespace: "project-b"},
		{name: "platform action cannot use namespace scope", email: "alice@example.com", action: GetVersion, namespace: "project-a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			allowed, err := authorizer.Authorize(ctx, tt.email, tt.action, tt.namespace)
			if err != nil || allowed != tt.want {
				t.Fatalf("Authorize = %t, %v; want %t", allowed, err, tt.want)
			}
			allowed, err = authorizer.HasPermission(ctx, tt.email, tt.action, tt.namespace)
			if err != nil || allowed != tt.want {
				t.Fatalf("HasPermission = %t, %v; want %t", allowed, err, tt.want)
			}
		})
	}
	if allowed, _ := authorizer.Authorize(ctx, "", GetVersion, ""); allowed {
		t.Fatal("empty principal authorized by *")
	}
}

func TestPlatformBindingExactSubjectAndDefaultDeny(t *testing.T) {
	ctx := context.Background()
	stores, authorizer := newTestAuthorizer(t)
	if allowed, err := authorizer.Authorize(ctx, "alice@example.com", GetVersion, ""); err != nil || allowed {
		t.Fatalf("without a grant = %t, %v; want deny", allowed, err)
	}
	seedPermissionRole(t, stores, "viewer", []string{"version.get"})
	seedPlatformBinding(t, stores, "alice", "viewer", "alice@example.com", nil)
	if err := authorizer.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	for email, want := range map[string]bool{"alice@example.com": true, "bob@example.com": false} {
		allowed, err := authorizer.Authorize(ctx, email, GetVersion, "")
		if err != nil || allowed != want {
			t.Fatalf("%s = %t, %v; want %t", email, allowed, err, want)
		}
	}
}

func TestNamespaceManagedRoleCannotGrantPlatformAccess(t *testing.T) {
	ctx := context.Background()
	stores, authorizer := newTestAuthorizer(t)
	role := &privatev1.Role{
		ObjectMeta: metav1.ObjectMeta{Name: "viewer", Namespace: "project-a"},
		Spec:       privatev1.RoleSpec{Permissions: []string{"cluster.list", "version.get", "version.list"}},
	}
	if err := stores.Roles.Create(ctx, role); err != nil {
		t.Fatal(err)
	}
	binding := &privatev1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "viewer", Namespace: "project-a"},
		Spec: privatev1.RoleBindingSpec{Subject: "alice@example.com", RoleRef: privatev1.RoleRef{
			Kind: "Role", Name: "viewer", APIGroup: privatev1.GroupVersion.Group,
		}},
	}
	if err := stores.RoleBindings.Create(ctx, binding); err != nil {
		t.Fatal(err)
	}
	if err := authorizer.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	if allowed, err := authorizer.Authorize(ctx, "alice@example.com", ListClusters, "project-a"); err != nil || !allowed {
		t.Fatalf("namespace permission = %t, %v; want allow", allowed, err)
	}
	for _, namespace := range []string{"", "project-a"} {
		if allowed, err := authorizer.Authorize(ctx, "alice@example.com", GetVersion, namespace); err != nil || allowed {
			t.Fatalf("namespace role granting Version access = %t, %v; want deny", allowed, err)
		}
		if allowed, err := authorizer.HasPermission(ctx, "alice@example.com", ListVersions, namespace); err != nil || allowed {
			t.Fatalf("namespace role granting Version collection access = %t, %v; want deny", allowed, err)
		}
	}
}

func TestPlatformBindingConditionsRemainIndependent(t *testing.T) {
	ctx := context.Background()
	stores, authorizer := newTestAuthorizer(t)
	seedPermissionRole(t, stores, "viewer", []string{"version.get", "version.list"})
	condition := `principal == User::"bob@example.com" && context.spec.channelGroups.contains("stable")`
	seedPlatformBinding(t, stores, "conditional", "viewer", "*", &condition)
	seedPlatformBinding(t, stores, "unconditional", "viewer", "alice@example.com", nil)
	if err := authorizer.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		email string
		group string
		want  bool
	}{
		{email: "alice@example.com", group: "stable", want: true},
		{email: "alice@example.com", group: "candidate", want: true},
		{email: "bob@example.com", group: "stable", want: true},
		{email: "bob@example.com", group: "candidate"},
		{email: "carol@example.com", group: "stable"},
	} {
		requestContext, err := cedarContext("4.22.1", "versions", "GET", map[string]any{"channelGroups": []any{tt.group}})
		if err != nil {
			t.Fatal(err)
		}
		allowed, err := authorizer.AuthorizeWithContext(ctx, tt.email, GetVersion, "", requestContext)
		if err != nil || allowed != tt.want {
			t.Fatalf("%s/%s = %t, %v; want %t", tt.email, tt.group, allowed, err, tt.want)
		}
	}
	allowed, err := authorizer.HasPermission(ctx, "carol@example.com", ListVersions, "")
	if err != nil || !allowed {
		t.Fatalf("coarse list permission = %t, %v; want true before conditions", allowed, err)
	}
}

func TestPlatformBindingConditionErrorsKeepExistingSemantics(t *testing.T) {
	ctx := context.Background()
	stores, authorizer := newTestAuthorizer(t)
	seedPermissionRole(t, stores, "viewer", []string{"version.get"})
	condition := `context.spec.missing == "stable"`
	seedPlatformBinding(t, stores, "conditional", "viewer", "*", &condition)
	if err := authorizer.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	if allowed, err := authorizer.Authorize(ctx, "alice@example.com", GetVersion, ""); err == nil || allowed {
		t.Fatalf("missing required context = %t, %v; want denied evaluation error", allowed, err)
	}
	seedPlatformBinding(t, stores, "independent", "viewer", "alice@example.com", nil)
	if err := authorizer.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	if allowed, err := authorizer.Authorize(ctx, "alice@example.com", GetVersion, ""); err != nil || !allowed {
		t.Fatalf("independent valid grant = %t, %v; want allow", allowed, err)
	}
}

func TestMalformedPlatformBindingsGrantNothing(t *testing.T) {
	for _, tt := range []struct {
		name   string
		mutate func(*privatev1.PlatformRoleBinding)
	}{
		{name: "missing role", mutate: func(b *privatev1.PlatformRoleBinding) { b.Spec.RoleRef.Name = "missing" }},
		{name: "namespace role", mutate: func(b *privatev1.PlatformRoleBinding) { b.Spec.RoleRef.Kind = "Role" }},
		{name: "foreign group", mutate: func(b *privatev1.PlatformRoleBinding) { b.Spec.RoleRef.APIGroup = "other.example.com" }},
		{name: "forged namespace", mutate: func(b *privatev1.PlatformRoleBinding) { b.Namespace = "project-a" }},
		{name: "malformed condition", mutate: func(b *privatev1.PlatformRoleBinding) {
			condition := `true }; forbid (principal, action, resource) when { true`
			b.Spec.Condition = &condition
		}},
		{name: "condition escaping binding constraints", mutate: func(b *privatev1.PlatformRoleBinding) {
			condition := `true) || (true`
			b.Spec.Condition = &condition
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			stores, authorizer := newTestAuthorizer(t)
			seedPermissionRole(t, stores, "viewer", []string{"version.get", "cluster.get"})
			binding := &privatev1.PlatformRoleBinding{
				ObjectMeta: metav1.ObjectMeta{Name: "invalid"},
				Spec:       privatev1.RoleBindingSpec{Subject: "*", RoleRef: privatev1.RoleRef{Kind: "PlatformRole", Name: "viewer", APIGroup: privatev1.GroupVersion.Group}},
			}
			tt.mutate(binding)
			if err := stores.PlatformRoleBindings.Create(ctx, binding); err != nil {
				t.Fatal(err)
			}
			if err := authorizer.Reload(ctx); err != nil {
				t.Fatal(err)
			}
			if allowed, err := authorizer.Authorize(ctx, "alice@example.com", GetVersion, ""); err != nil || allowed {
				t.Fatalf("malformed binding = %t, %v; want deny", allowed, err)
			}
		})
	}
}

func TestPlatformBindingWatchesGrantUpdateAndRevocation(t *testing.T) {
	ctx := context.Background()
	stores, authorizer := newTestAuthorizer(t)
	seedPermissionRole(t, stores, "viewer", []string{"version.get"})
	stopCh := make(chan struct{})
	authorizer.StartWatching(stopCh)
	defer close(stopCh)
	binding := seedPlatformBinding(t, stores, "viewer", "viewer", "*", nil)
	waitForPlatformAuthorization(t, authorizer, true)
	condition := "false"
	binding.Spec.Condition = &condition
	if err := stores.PlatformRoleBindings.Update(ctx, binding); err != nil {
		t.Fatal(err)
	}
	waitForPlatformAuthorization(t, authorizer, false)
	binding.Spec.Condition = nil
	if err := stores.PlatformRoleBindings.Update(ctx, binding); err != nil {
		t.Fatal(err)
	}
	waitForPlatformAuthorization(t, authorizer, true)
	if err := stores.PlatformRoleBindings.Delete(ctx, "", binding.Name); err != nil {
		t.Fatal(err)
	}
	waitForPlatformAuthorization(t, authorizer, false)
	seedPlatformBinding(t, stores, "viewer", "viewer", "*", nil)
	waitForPlatformAuthorization(t, authorizer, true)
	if err := stores.PlatformRoles.Delete(ctx, "", "viewer"); err != nil {
		t.Fatal(err)
	}
	waitForPlatformAuthorization(t, authorizer, false)
}

func TestPlatformBindingRefreshFailureRetainsLastKnownGood(t *testing.T) {
	ctx := context.Background()
	stores, authorizer := newTestAuthorizer(t)
	seedPermissionRole(t, stores, "viewer", []string{"version.get"})
	seedPlatformBinding(t, stores, "viewer", "viewer", "*", nil)
	failing := &failingBindingListStore{ResourceStore: stores.PlatformRoleBindings}
	authorizer.stores.PlatformRoleBindings = failing
	if err := authorizer.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	if allowed, err := authorizer.Authorize(ctx, "alice@example.com", GetVersion, ""); err != nil || !allowed {
		t.Fatalf("initial grant = %t, %v", allowed, err)
	}
	if err := stores.PlatformRoleBindings.Delete(ctx, "", "viewer"); err != nil {
		t.Fatal(err)
	}
	failing.fail = true
	if err := authorizer.Reload(ctx); err == nil {
		t.Fatal("expected reload failure")
	}
	if allowed, err := authorizer.Authorize(ctx, "alice@example.com", GetVersion, ""); err != nil || !allowed {
		t.Fatalf("last-known-good = %t, %v; want allow", allowed, err)
	}
	failing.fail = false
	if err := authorizer.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	if allowed, err := authorizer.Authorize(ctx, "alice@example.com", GetVersion, ""); err != nil || allowed {
		t.Fatalf("after recovery = %t, %v; want deny", allowed, err)
	}
}

func TestPlatformBindingInvalidReferenceLogsAreSanitized(t *testing.T) {
	stores, _ := newTestAuthorizer(t)
	seedPlatformBinding(t, stores, "sensitive-binding", "sensitive-missing-role", "sensitive@example.com", nil)
	entries := []capturedAuthorizationLog{}
	logger := logr.FromSlogHandler(&capturedAuthorizationLogHandler{entries: &entries})
	if _, err := generatePolicySet(context.Background(), stores, logger); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].attributes["reason"] != "invalid_binding_reference" {
		t.Fatalf("logs = %v", entries)
	}
	if strings.Contains(entries[0].String(), "sensitive") {
		t.Fatalf("log leaked binding data: %s", entries[0])
	}
}

func seedPermissionRole(t *testing.T, stores Stores, name string, permissions []string) {
	t.Helper()
	role := &privatev1.PlatformRole{ObjectMeta: metav1.ObjectMeta{Name: name}, Spec: privatev1.PlatformRoleSpec{Permissions: permissions}}
	role.SetGroupVersionKind(privatev1.GroupVersion.WithKind("PlatformRole"))
	if err := stores.PlatformRoles.Create(context.Background(), role); err != nil {
		t.Fatal(err)
	}
}

func seedPlatformBinding(t *testing.T, stores Stores, name, role, subject string, condition *string) *privatev1.PlatformRoleBinding {
	t.Helper()
	binding := &privatev1.PlatformRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       privatev1.RoleBindingSpec{Subject: subject, RoleRef: privatev1.RoleRef{Kind: "PlatformRole", Name: role, APIGroup: privatev1.GroupVersion.Group}, Condition: condition},
	}
	binding.SetGroupVersionKind(privatev1.GroupVersion.WithKind("PlatformRoleBinding"))
	if err := stores.PlatformRoleBindings.Create(context.Background(), binding); err != nil {
		t.Fatal(err)
	}
	return binding
}

func waitForPlatformAuthorization(t *testing.T, authorizer *Authorizer, want bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		allowed, err := authorizer.Authorize(context.Background(), "alice@example.com", GetVersion, "")
		if err != nil {
			t.Fatal(err)
		}
		if allowed == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("platform authorization did not converge to %t", want)
}

type failingBindingListStore struct {
	storage.ResourceStore
	fail bool
}

func (s *failingBindingListStore) List(ctx context.Context, options storage.ListOptions) (client.ObjectList, error) {
	if s.fail {
		return nil, errors.New("injected authorization storage failure")
	}
	return s.ResourceStore.List(ctx, options)
}
