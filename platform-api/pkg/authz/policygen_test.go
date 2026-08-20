package authz

import (
	"testing"

	cedar "github.com/cedar-policy/cedar-go"
	privatev1 "github.com/openshift-online/gecko/platform-api/api/private/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestGeneratePolicies_BasicPlatformRole(t *testing.T) {
	// PlatformRole with no bindings: no policy generated (per-binding approach).
	platformRoles := []privatev1.PlatformRole{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "cluster-viewer"},
			Spec: privatev1.PlatformRoleSpec{
				Permissions: []string{"cluster.list", "cluster.get"},
				System:      true,
			},
		},
	}
	ps, err := GeneratePolicies(platformRoles, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ps == nil {
		t.Fatal("expected non-nil PolicySet")
	}
	// No bindings reference this role, so no policy should be generated.
	if ps.Get("platformrole:cluster-viewer") != nil {
		t.Fatal("expected no policy for PlatformRole with no bindings")
	}
}

func TestGeneratePolicies_PlatformRoleWithBinding(t *testing.T) {
	platformRoles := []privatev1.PlatformRole{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "cluster-viewer"},
			Spec: privatev1.PlatformRoleSpec{
				Permissions: []string{"cluster.list", "cluster.get"},
				System:      true,
			},
		},
	}
	bindings := []privatev1.RoleBinding{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "rb1", Namespace: "org-1"},
			Spec: privatev1.RoleBindingSpec{
				Subject: "alice@example.com",
				RoleRef: privatev1.RoleRef{Kind: privatev1.RoleRefKindPlatformRole, Name: "cluster-viewer", APIGroup: "gcp.managed.openshift.io"},
			},
		},
	}
	ps, err := GeneratePolicies(platformRoles, nil, bindings)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	policyID := cedar.PolicyID("platformrole:cluster-viewer:binding:org-1/rb1")
	if ps.Get(policyID) == nil {
		t.Fatalf("expected policy %q in PolicySet", policyID)
	}
}

func TestGeneratePolicies_NamespacedRole(t *testing.T) {
	roles := []privatev1.Role{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "cluster-ro", Namespace: "ns-a"},
			Spec: privatev1.RoleSpec{
				Permissions: []string{"cluster.list", "cluster.get"},
			},
		},
	}
	bindings := []privatev1.RoleBinding{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "rb1", Namespace: "ns-a"},
			Spec: privatev1.RoleBindingSpec{
				Subject: "alice",
				RoleRef: privatev1.RoleRef{Kind: privatev1.RoleRefKindRole, Name: "cluster-ro", APIGroup: "gcp.managed.openshift.io"},
			},
		},
	}
	ps, err := GeneratePolicies(nil, roles, bindings)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	policyID := cedar.PolicyID("role:cluster-ro:binding:ns-a/rb1")
	if ps.Get(policyID) == nil {
		t.Fatalf("expected policy %q in PolicySet", policyID)
	}
}

func TestGeneratePolicies_UnknownPermission(t *testing.T) {
	platformRoles := []privatev1.PlatformRole{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "bad-role"},
			Spec: privatev1.PlatformRoleSpec{
				Permissions: []string{"invalid.permission"},
			},
		},
	}
	_, err := GeneratePolicies(platformRoles, nil, nil)
	if err == nil {
		t.Fatal("expected error for unknown permission")
	}
}

func TestGeneratePolicies_EmptyPermissions(t *testing.T) {
	platformRoles := []privatev1.PlatformRole{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "empty-role"},
			Spec:       privatev1.PlatformRoleSpec{Permissions: nil},
		},
	}
	ps, err := GeneratePolicies(platformRoles, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ps.Get("platformrole:empty-role") != nil {
		t.Fatal("expected no policy for role with empty permissions")
	}
}

func TestGeneratePolicies_MultiplePlatformRoles(t *testing.T) {
	platformRoles := []privatev1.PlatformRole{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "viewer"},
			Spec:       privatev1.PlatformRoleSpec{Permissions: []string{"cluster.list", "cluster.get"}, System: true},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "admin"},
			Spec:       privatev1.PlatformRoleSpec{Permissions: []string{"cluster.create", "cluster.update", "cluster.delete"}, System: true},
		},
	}
	bindings := []privatev1.RoleBinding{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "rb-v", Namespace: "ns-1"},
			Spec: privatev1.RoleBindingSpec{
				Subject: "alice",
				RoleRef: privatev1.RoleRef{Kind: privatev1.RoleRefKindPlatformRole, Name: "viewer", APIGroup: "gcp.managed.openshift.io"},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "rb-a", Namespace: "ns-1"},
			Spec: privatev1.RoleBindingSpec{
				Subject: "bob",
				RoleRef: privatev1.RoleRef{Kind: privatev1.RoleRefKindPlatformRole, Name: "admin", APIGroup: "gcp.managed.openshift.io"},
			},
		},
	}
	ps, err := GeneratePolicies(platformRoles, nil, bindings)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, id := range []string{
		"platformrole:viewer:binding:ns-1/rb-v",
		"platformrole:admin:binding:ns-1/rb-a",
	} {
		if ps.Get(cedar.PolicyID(id)) == nil {
			t.Fatalf("expected policy %q in PolicySet", id)
		}
	}
	// Verify that both expected policies exist (isolation by binding).
	rbvPolicy := ps.Get("platformrole:viewer:binding:ns-1/rb-v")
	if rbvPolicy == nil {
		t.Fatal("expected rb-v policy to exist in PolicySet")
	}
	rbaPolicy := ps.Get("platformrole:admin:binding:ns-1/rb-a")
	if rbaPolicy == nil {
		t.Fatal("expected rb-a policy to exist in PolicySet")
	}
}

func TestGeneratePolicies_NamespacedRoleNoBindings(t *testing.T) {
	roles := []privatev1.Role{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "orphan-role", Namespace: "ns-a"},
			Spec:       privatev1.RoleSpec{Permissions: []string{"cluster.list"}},
		},
	}
	ps, err := GeneratePolicies(nil, roles, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ps.Get("role:orphan-role") != nil {
		t.Fatal("expected no policy for role with no bindings")
	}
}
