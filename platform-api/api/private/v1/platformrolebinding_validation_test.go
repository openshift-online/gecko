package v1

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestPlatformRoleBindingValidation(t *testing.T) {
	SetValidatorDeps(ValidatorDeps{PlatformRoleExists: func(_ context.Context, name string) (bool, error) {
		return name == "authenticated-user", nil
	}})
	t.Cleanup(func() { SetValidatorDeps(ValidatorDeps{}) })
	condition := `context.resourcePlural == "channels" && context.resourceName == "stable"`
	invalid := `context.spec ==`
	injection := `true }; forbid (principal, action, resource) when { true`
	escape := `true) || (true`
	namespace := `resource in Namespace::"customer"`
	tests := []struct {
		name      string
		subject   string
		kind      string
		group     string
		role      string
		namespace string
		condition *string
		wantErr   string
	}{
		{name: "all authenticated", subject: "*"},
		{name: "exact email", subject: " alice@EXAMPLE.COM "},
		{name: "Cedar condition supported", subject: "*", condition: &condition},
		{name: "empty subject", wantErr: "spec.subject"},
		{name: "domain restriction belongs in condition", subject: "*@example.com", wantErr: "spec.condition"},
		{name: "namespace role", subject: "*", kind: "Role", wantErr: "kind must be PlatformRole"},
		{name: "other role kind", subject: "*", kind: "ClusterRole", wantErr: "kind must be PlatformRole"},
		{name: "foreign group", subject: "*", group: "other.example.com", wantErr: "spec.roleRef.apiGroup"},
		{name: "missing role", subject: "*", role: "missing", wantErr: "does not exist"},
		{name: "namespace set", subject: "*", namespace: "customer", wantErr: "empty namespace"},
		{name: "invalid condition", subject: "*", condition: &invalid, wantErr: "not a valid Cedar expression"},
		{name: "policy injection", subject: "*", condition: &injection, wantErr: "single Cedar condition"},
		{name: "escaping binding constraints", subject: "*", condition: &escape, wantErr: "not a valid Cedar expression"},
		{name: "namespace condition", subject: "*", condition: &namespace, wantErr: "must not reference Namespace::"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			binding := &PlatformRoleBinding{Spec: PlatformRoleBindingSpec{
				Subject:   tt.subject,
				RoleRef:   RoleRef{Kind: "PlatformRole", Name: "authenticated-user", APIGroup: GroupVersion.Group},
				Condition: tt.condition,
			}}
			binding.Namespace = tt.namespace
			if tt.kind != "" {
				binding.Spec.RoleRef.Kind = tt.kind
			}
			if tt.group != "" {
				binding.Spec.RoleRef.APIGroup = tt.group
			}
			if tt.role != "" {
				binding.Spec.RoleRef.Name = tt.role
			}
			for _, validate := range []struct {
				name string
				call func() error
			}{
				{name: "create", call: func() error { return binding.ValidateCreate(context.Background()) }},
				{name: "update", call: func() error { return binding.ValidateUpdate(context.Background(), &PlatformRoleBinding{}) }},
			} {
				err := validate.call()
				if tt.wantErr == "" {
					if err != nil {
						t.Fatalf("%s: %v", validate.name, err)
					}
				} else if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("%s error = %v, want containing %q", validate.name, err, tt.wantErr)
				}
			}
			if tt.name == "exact email" && binding.Spec.Subject != "alice@example.com" {
				t.Fatalf("subject = %q, want normalized email", binding.Spec.Subject)
			}
		})
	}
}

func TestPlatformRoleBindingSpecPreservesJSONShape(t *testing.T) {
	condition := "true"
	for _, tt := range []struct {
		name      string
		condition *string
	}{
		{name: "condition omitted"},
		{name: "condition present", condition: &condition},
	} {
		t.Run(tt.name, func(t *testing.T) {
			platform := PlatformRoleBindingSpec{
				Subject:   "alice@example.com",
				RoleRef:   RoleRef{Kind: "PlatformRole", Name: "viewer", APIGroup: GroupVersion.Group},
				Condition: tt.condition,
			}
			namespaced := RoleBindingSpec(platform)
			jsonValue := func(value any) map[string]any {
				t.Helper()
				data, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				var result map[string]any
				if err := json.Unmarshal(data, &result); err != nil {
					t.Fatal(err)
				}
				return result
			}
			if got, want := jsonValue(platform), jsonValue(namespaced); !reflect.DeepEqual(got, want) {
				t.Fatalf("platform spec JSON = %v, want existing binding shape %v", got, want)
			}
		})
	}
}

func TestPlatformBindingKeepsNamespaceBindingSubjectSemantics(t *testing.T) {
	binding := testRoleBinding(nil)
	binding.Spec.Subject = "*"
	if err := binding.ValidateCreate(context.Background()); err == nil {
		t.Fatal("namespace RoleBinding accepted all-authenticated subject")
	}
}

func TestRolesAcceptMixedScopePermissions(t *testing.T) {
	permissions := []string{"cluster.get", "version.get", "channel.list"}
	if err := (&PlatformRole{Spec: PlatformRoleSpec{Permissions: permissions}}).ValidateCreate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := (&Role{Spec: RoleSpec{Permissions: permissions}}).ValidateCreate(context.Background()); err != nil {
		t.Fatal(err)
	}
	permissions = append(permissions, "version.delete")
	if err := (&PlatformRole{Spec: PlatformRoleSpec{Permissions: permissions}}).ValidateCreate(context.Background()); err == nil {
		t.Fatal("unsupported permission accepted")
	}
}
