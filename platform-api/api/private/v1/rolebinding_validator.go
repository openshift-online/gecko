package v1

import (
	"context"
	"fmt"
	"strings"

	cedar "github.com/cedar-policy/cedar-go"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/openshift-online/gecko/platform-api/pkg/authn"
)

const (
	// RoleRefKindPlatformRole references a cluster-scoped PlatformRole.
	RoleRefKindPlatformRole = "PlatformRole"
	// RoleRefKindRole references a namespace-scoped Role.
	RoleRefKindRole = "Role"
)

// ValidateCreate validates RoleBinding creation.
func (rb *RoleBinding) ValidateCreate(ctx context.Context) error {
	return validateRoleBinding(ctx, rb)
}

// ValidateUpdate validates RoleBinding updates.
func (rb *RoleBinding) ValidateUpdate(ctx context.Context, oldObj runtime.Object) error {
	return validateRoleBinding(ctx, rb)
}

// ValidateDelete validates RoleBinding deletion.
func (rb *RoleBinding) ValidateDelete(ctx context.Context) error {
	return nil
}

// validateBindingCondition checks that the condition is valid Cedar syntax.
func validateBindingCondition(condition string) error {
	if strings.Contains(condition, "Namespace::") {
		return fmt.Errorf("condition cannot reference namespace entities directly")
	}
	policyText := fmt.Sprintf("permit (principal, action, resource) when { %s };", condition)
	var p cedar.Policy
	if err := p.UnmarshalCedar([]byte(policyText)); err != nil {
		return fmt.Errorf("invalid Cedar condition syntax")
	}
	return nil
}
func validateRoleBinding(ctx context.Context, rb *RoleBinding) error {
	if rb.Spec.Subject == "" {
		return fmt.Errorf("subject is required")
	}

	// Validate Cedar condition if present.
	if rb.Spec.Condition != "" {
		if err := validateBindingCondition(rb.Spec.Condition); err != nil {
			return err
		}
	}

	// Validate roleRef.
	ref := rb.Spec.RoleRef
	if ref.Name == "" {
		return fmt.Errorf("roleRef.name is required")
	}
	if ref.Kind != RoleRefKindPlatformRole && ref.Kind != RoleRefKindRole {
		return fmt.Errorf("roleRef.kind must be %q or %q, got %q", RoleRefKindPlatformRole, RoleRefKindRole, ref.Kind)
	}
	if ref.APIGroup != GroupName {
		return fmt.Errorf("roleRef.apiGroup must be %q, got %q", GroupName, ref.APIGroup)
	}

	// Prevent self-grant: a user cannot bind a role to themselves.
	if caller, ok := authn.UserFromContext(ctx); ok {
		if rb.Spec.Subject == caller {
			return fmt.Errorf("cannot grant a role to yourself")
		}
	}

	// Check that the referenced role exists.
	deps := getValidatorDeps()
	if deps != nil {
		switch ref.Kind {
		case RoleRefKindPlatformRole:
			if deps.PlatformRoleExists != nil && !deps.PlatformRoleExists(ctx, ref.Name) {
				return fmt.Errorf("platform role %q not found", ref.Name)
			}
		case RoleRefKindRole:
			if deps.RoleExists != nil && !deps.RoleExists(ctx, rb.Namespace, ref.Name) {
				return fmt.Errorf("role %q not found in namespace %q", ref.Name, rb.Namespace)
			}
		}
	}

	return nil
}
