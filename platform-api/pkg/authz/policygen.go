package authz

import (
	"fmt"
	"strings"

	cedar "github.com/cedar-policy/cedar-go"
	privatev1 "github.com/openshift-online/gecko/platform-api/api/private/v1"
)

// GeneratePolicies builds a Cedar PolicySet from PlatformRole, Role, and
// RoleBinding resources.
//
// For PlatformRoles (cluster-scoped, system): a single policy per role using
// "principal in resource" for hierarchy-based access.
//
// For namespace-scoped Roles (user-defined): a per-binding namespace-pinned
// policy is generated for each RoleBinding that references the role.
func GeneratePolicies(platformRoles []privatev1.PlatformRole, roles []privatev1.Role, bindings []privatev1.RoleBinding) (*cedar.PolicySet, error) {
	ps := cedar.NewPolicySet()

	// Index bindings by "kind/name" to prevent collisions between
	// PlatformRoles and Roles that share the same name.
	bindingsByRole := make(map[string][]privatev1.RoleBinding)
	for _, b := range bindings {
		key := b.Spec.RoleRef.Kind + "/" + b.Spec.RoleRef.Name
		bindingsByRole[key] = append(bindingsByRole[key], b)
	}

	// PlatformRoles: per-binding namespace-pinned policies.
	// Each binding gets a unique NamespaceRole entity keyed by "ns/roleName/bindingName"
	// so that conditions (or lack thereof) on one binding cannot bleed into another.
	for _, pr := range platformRoles {
		if len(pr.Spec.Permissions) == 0 {
			continue
		}
		actions, err := permissionsToActions(pr.Spec.Permissions)
		if err != nil {
			return nil, fmt.Errorf("platform role %q: %w", pr.Name, err)
		}
		roleBindings := bindingsByRole[privatev1.RoleRefKindPlatformRole+"/"+pr.Name]
		for _, rb := range roleBindings {
			nsRoleKey := fmt.Sprintf("%s/%s/%s", rb.Namespace, pr.Name, rb.Name)
			policyText := fmt.Sprintf(
				"permit (principal, action in [%s], resource) when { principal in NamespaceRole::\"%s\" && resource in Namespace::\"%s\" };",
				formatActionList(actions),
				nsRoleKey,
				rb.Namespace,
			)
			var p cedar.Policy
			if err := p.UnmarshalCedar([]byte(policyText)); err != nil {
				return nil, fmt.Errorf("platform role %q binding %s/%s: parse policy: %w", pr.Name, rb.Namespace, rb.Name, err)
			}
			policyID := fmt.Sprintf("platformrole:%s:binding:%s/%s", pr.Name, rb.Namespace, rb.Name)
			ps.Add(cedar.PolicyID(policyID), &p)
		}
	}

	// Namespace-scoped Roles: per-binding namespace-pinned policies.
	for _, role := range roles {
		if len(role.Spec.Permissions) == 0 {
			continue
		}
		actions, err := permissionsToActions(role.Spec.Permissions)
		if err != nil {
			return nil, fmt.Errorf("role %q: %w", role.Name, err)
		}

		roleBindings := bindingsByRole[privatev1.RoleRefKindRole+"/"+role.Name]
		for _, rb := range roleBindings {
			// Each binding gets a unique NamespaceRole entity keyed by
			// "ns/roleName/bindingName" so that conditions on one binding cannot
			// bleed into another binding for the same role.
			nsRoleKey := fmt.Sprintf("%s/%s/%s", rb.Namespace, role.Name, rb.Name)
			baseCondition := fmt.Sprintf(
				"principal in NamespaceRole::\"%s\" && resource in Namespace::\"%s\"",
				nsRoleKey,
				rb.Namespace,
			)
			condition := baseCondition
			if rb.Spec.Condition != "" {
				// Wrap the condition with a has-check so that list operations
				// (which have no resourceName in the Cedar context) are not denied
				// at the namespace level. The per-item ItemFilter applies the
				// condition to each result individually.
				condition = fmt.Sprintf(
					"%s && (!(context has resourceName) || %s)",
					baseCondition,
					rb.Spec.Condition,
				)
			}
			policyText := fmt.Sprintf(
				"permit (principal, action in [%s], resource) when { %s };",
				formatActionList(actions),
				condition,
			)
			var p cedar.Policy
			if err := p.UnmarshalCedar([]byte(policyText)); err != nil {
				return nil, fmt.Errorf("role %q binding %s/%s: parse policy: %w", role.Name, rb.Namespace, rb.Name, err)
			}
			policyID := fmt.Sprintf("role:%s:binding:%s/%s", role.Name, rb.Namespace, rb.Name)
			ps.Add(cedar.PolicyID(policyID), &p)
		}
	}

	return ps, nil
}

func permissionsToActions(perms []string) ([]string, error) {
	actions := make([]string, 0, len(perms))
	for _, perm := range perms {
		action, ok := PermissionToAction[perm]
		if !ok {
			return nil, fmt.Errorf("unknown permission %q", perm)
		}
		actions = append(actions, action)
	}
	return actions, nil
}

func formatActionList(actions []string) string {
	parts := make([]string, len(actions))
	for i, a := range actions {
		parts[i] = fmt.Sprintf("Action::\"%s\"", a)
	}
	return strings.Join(parts, ", ")
}
