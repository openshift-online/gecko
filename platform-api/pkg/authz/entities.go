package authz

import (
	"context"

	"github.com/cedar-policy/cedar-go"
)

func entitiesForUser(ctx context.Context, stores Stores, cache *EntityCache, email string) (cedar.EntityMap, error) {
	if entities, ok := cache.Get(email); ok {
		return entities, nil
	}

	bindings, err := bindingsForUser(ctx, stores, email, false)
	if err != nil {
		return nil, err
	}
	platformBindings, err := bindingsForUser(ctx, stores, email, true)
	if err != nil {
		return nil, err
	}
	bindings = append(bindings, platformBindings...)

	userUID := cedar.NewEntityUID("User", cedar.String(email))
	entities := cedar.EntityMap{
		userUID: {
			UID:        userUID,
			Parents:    cedar.NewEntityUIDSet(),
			Attributes: cedar.NewRecord(nil),
			Tags:       cedar.NewRecord(nil),
		},
	}
	parents := make([]cedar.EntityUID, 0, len(bindings))
	seenScopes := make(map[cedar.EntityUID]struct{})
	for _, binding := range bindings {
		resourceUID := scopeResource(binding.Namespace)
		if _, seen := seenScopes[resourceUID]; !seen {
			seenScopes[resourceUID] = struct{}{}
			entities[resourceUID] = cedar.Entity{
				UID:        resourceUID,
				Parents:    cedar.NewEntityUIDSet(),
				Attributes: cedar.NewRecord(nil),
				Tags:       cedar.NewRecord(nil),
			}
		}

		roleUID := binding.roleUID()
		entities[roleUID] = cedar.Entity{
			UID:        roleUID,
			Parents:    cedar.NewEntityUIDSet(resourceUID),
			Attributes: cedar.NewRecord(nil),
			Tags:       cedar.NewRecord(nil),
		}
		parents = append(parents, roleUID)
	}
	entities[userUID] = cedar.Entity{
		UID:        userUID,
		Parents:    cedar.NewEntityUIDSet(parents...),
		Attributes: cedar.NewRecord(nil),
		Tags:       cedar.NewRecord(nil),
	}

	cache.Put(email, entities)
	return entities, nil
}
