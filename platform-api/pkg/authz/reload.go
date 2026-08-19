package authz

import (
	"context"
	"log"

	privatev1 "github.com/openshift-online/gecko/platform-api/api/private/v1"
	"github.com/openshift-online/gecko/orlop/pkg/apiserver/storage"
)

// StartWatching starts background goroutines that watch for changes to
// PlatformRoles, Roles, and RoleBindings.
//
// When a PlatformRole or Role changes, the policy set is reloaded and all
// cached entities are invalidated.
//
// When a RoleBinding changes, the policy set is reloaded and the affected
// user's cached entities are invalidated.
//
// The goroutines stop when the context is cancelled.
func (a *Authorizer) StartWatching(ctx context.Context) error {
	// Watch PlatformRoles for policy changes.
	prCh, prStop, err := a.stores.PlatformRoles.Watch(ctx, storage.ListOptions{}, "")
	if err != nil {
		return err
	}

	// Watch Roles for policy changes.
	roleCh, roleStop, err := a.stores.Roles.Watch(ctx, storage.ListOptions{}, "")
	if err != nil {
		prStop()
		return err
	}

	// Watch RoleBindings for policy reload and cache invalidation.
	rbCh, rbStop, err := a.stores.RoleBindings.Watch(ctx, storage.ListOptions{}, "")
	if err != nil {
		prStop()
		roleStop()
		return err
	}

	// Goroutine for PlatformRole and Role changes.
	go func() {
		defer prStop()
		defer roleStop()
		for {
			if prCh == nil && roleCh == nil {
				return
			}
			select {
			case <-ctx.Done():
				return
			case event, ok := <-prCh:
				if !ok {
					prCh = nil
					continue
				}
				if event.Type == storage.EventBookmark {
					continue
				}
				log.Printf("authz: platform role change detected (%s), reloading policies", event.Type)
				if err := a.ReloadPolicies(ctx); err != nil {
					log.Printf("authz: failed to reload policies: %v", err)
				}
				a.InvalidateCache()
			case event, ok := <-roleCh:
				if !ok {
					roleCh = nil
					continue
				}
				if event.Type == storage.EventBookmark {
					continue
				}
				log.Printf("authz: role change detected (%s), reloading policies", event.Type)
				if err := a.ReloadPolicies(ctx); err != nil {
					log.Printf("authz: failed to reload policies: %v", err)
				}
				a.InvalidateCache()
			}
		}
	}()

	// Goroutine for RoleBinding changes.
	go func() {
		defer rbStop()
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-rbCh:
				if !ok {
					return
				}
				if event.Type == storage.EventBookmark {
					continue
				}
				log.Printf("authz: role binding change detected (%s), reloading policies", event.Type)
				if err := a.ReloadPolicies(ctx); err != nil {
					log.Printf("authz: failed to reload policies on binding change: %v", err)
				}
				a.invalidateFromRoleBinding(event)
			}
		}
	}()

	return nil
}

func (a *Authorizer) invalidateFromRoleBinding(event storage.ResourceEvent) {
	if event.Object == nil {
		return
	}
	rb, ok := event.Object.(*privatev1.RoleBinding)
	if !ok {
		return
	}
	if rb.Spec.Subject != "" {
		log.Printf("authz: role binding %s/%s changed, invalidating subject cache", rb.Namespace, rb.Name)
		a.InvalidateUser(rb.Spec.Subject)
	}
}
