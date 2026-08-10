package authz

import (
	"fmt"
	"sort"

	cedar "github.com/cedar-policy/cedar-go"
	"github.com/cedar-policy/cedar-go/ast"
	"github.com/cedar-policy/cedar-go/types"
)

// GeneratePolicies builds a Cedar PolicySet from the given role definitions.
//
// For each role, it creates a permit policy whose action scope is the set of
// Cedar actions derived from the role's permissions (via PermissionToAction),
// with a `when { principal in resource }` condition.
//
// This condition works because of Cedar's transitive `in` operator:
//   - Users are `in` their bound NamespaceRole or PlatformRole (via entity parents)
//   - NamespaceRoles are `in` their Namespace
//   - Resources are `in` their Namespace
//   - So the principal (user) is transitively `in` the resource's hierarchy
func GeneratePolicies(roles []RoleDefinition) (*cedar.PolicySet, error) {
	ps := cedar.NewPolicySet()

	for _, role := range roles {
		if len(role.Permissions) == 0 {
			continue
		}

		// Build the list of action EntityUIDs from the role's permissions.
		actions := make([]types.EntityUID, 0, len(role.Permissions))
		for _, perm := range role.Permissions {
			actionName := PermissionToAction(perm)
			actions = append(actions, types.NewEntityUID("Action", types.String(actionName)))
		}

		// Sort actions by ID for deterministic policy output.
		sort.Slice(actions, func(i, j int) bool {
			return string(actions[i].ID) < string(actions[j].ID)
		})

		// Build the policy using the AST builder:
		//
		//   permit (
		//       principal,
		//       action in [Action::"...", ...],
		//       resource
		//   )
		//   when { principal in resource };
		policy := ast.Permit().
			ActionInSet(actions...).
			When(ast.Principal().In(ast.Resource()))

		policyID := cedar.PolicyID(fmt.Sprintf("role-%s", role.Name))
		ps.Add(policyID, cedar.NewPolicyFromAST(policy))
	}

	return ps, nil
}
