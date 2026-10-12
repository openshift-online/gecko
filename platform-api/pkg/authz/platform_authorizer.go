package authz

import (
	"context"
	"fmt"

	"github.com/cedar-policy/cedar-go"

	"github.com/openshift-online/gecko/orlop/pkg/apiserver/types"
)

const platformScopeID = "default"

// AuthorizePlatform evaluates a selected policy for a cluster-scoped public API
// operation in the default platform scope.
func (a *Authorizer) AuthorizePlatform(_ context.Context, email string, action Action, resource types.ResourceInfo, verb, name string) (bool, error) {
	if email == "" {
		return false, fmt.Errorf("principal is empty")
	}
	if resource.Namespaced {
		return false, fmt.Errorf("platform authorization requires a cluster-scoped resource")
	}
	policyRef := resource.AuthorizationPolicyRefs[verb]
	if policyRef == "" {
		return false, nil
	}
	policySets := a.policies.Load()
	if policySets == nil {
		return false, nil
	}
	policies, found := policySets.platform[policyRef]
	if !found {
		return false, fmt.Errorf("platform policy %q is not loaded", policyRef)
	}

	principal := cedar.NewEntityUID("User", cedar.String(email))
	scope := cedar.NewEntityUID("PlatformScope", cedar.String(platformScopeID))
	if name == "" {
		name = "collection"
	}
	resourceUID := cedar.NewEntityUID("PlatformResource", cedar.String(fmt.Sprintf("%s/%s/%s/%s", resource.GVK.Group, resource.GVK.Version, resource.Plural, name)))
	entities := cedar.EntityMap{
		principal: {
			UID:        principal,
			Parents:    cedar.NewEntityUIDSet(),
			Attributes: cedar.NewRecord(nil),
			Tags:       cedar.NewRecord(nil),
		},
		scope: {
			UID:        scope,
			Parents:    cedar.NewEntityUIDSet(),
			Attributes: cedar.NewRecord(nil),
			Tags:       cedar.NewRecord(nil),
		},
		resourceUID: {
			UID:     resourceUID,
			Parents: cedar.NewEntityUIDSet(scope),
			Attributes: cedar.NewRecord(cedar.RecordMap{
				"resourceType": cedar.String(resource.Plural),
			}),
			Tags: cedar.NewRecord(nil),
		},
	}
	decision, _ := cedar.Authorize(policies, entities, cedar.Request{
		Principal: principal,
		Action:    cedar.NewEntityUID("Action", cedar.String(action)),
		Resource:  resourceUID,
		Context:   cedar.NewRecord(nil),
	})
	return decision == cedar.Allow, nil
}
