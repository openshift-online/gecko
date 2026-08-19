package main

import (
	"fmt"

	"github.com/openshift-online/gecko/orlop/pkg/apiserver"
	"github.com/openshift-online/gecko/orlop/pkg/apiserver/types"
	privatev1 "github.com/openshift-online/gecko/platform-api/api/private/v1"
	publicv1 "github.com/openshift-online/gecko/platform-api/api/public/v1"
	"github.com/openshift-online/gecko/platform-api/pkg/authz"
	"k8s.io/apimachinery/pkg/runtime"
)

// getPrivateResources returns the resource definitions for the private API.
func getPrivateResources() []types.ResourceInfo {
	resources := privatev1.GetResourceInfos()
	for i := range resources {
		if resources[i].GVK.Kind == "NodePool" {
			resources[i].ParentResource = &types.ParentResourceInfo{
				Plural:  "clusters",
				IDField: "spec.clusterID",
			}
		}
	}
	return resources
}

// getPublicResources returns the resource definitions for the public API.
func getPublicResources() []types.ResourceInfo {
	resources := publicv1.GetResourceInfos()
	for i := range resources {
		if resources[i].GVK.Kind == "NodePool" {
			resources[i].ParentResource = &types.ParentResourceInfo{
				Plural:  "clusters",
				IDField: "spec.clusterID",
			}
		}
	}
	return resources
}

// getPrivateScheme creates and returns a runtime.Scheme with private API types registered.
func getPrivateScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	privatev1.AddToScheme(scheme)
	return scheme
}

// getPublicScheme creates and returns a runtime.Scheme with public API types registered.
func getPublicScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	publicv1.AddToScheme(scheme)
	return scheme
}

// buildAuthzStores creates ResourceStore instances for all authz types
// using the given storage factory and scheme.
//
// The resourceType strings must match those used by the aggregated server's
// internal registry (apiserver.GroupKindResourceType) so that both the
// authorizer and the request handlers share the same memoized store instance.
func buildAuthzStores(factory apiserver.StorageFactory, scheme *runtime.Scheme) (authz.AuthzStores, error) {
	gv := privatev1.GroupVersion

	platformRoleGVK := gv.WithKind("PlatformRole")
	platformRoleStore, err := factory(apiserver.GroupKindResourceType(platformRoleGVK.GroupKind()), scheme, platformRoleGVK)
	if err != nil {
		return authz.AuthzStores{}, fmt.Errorf("create platform role store: %w", err)
	}

	roleGVK := gv.WithKind("Role")
	roleStore, err := factory(apiserver.GroupKindResourceType(roleGVK.GroupKind()), scheme, roleGVK)
	if err != nil {
		return authz.AuthzStores{}, fmt.Errorf("create role store: %w", err)
	}

	roleBindingGVK := gv.WithKind("RoleBinding")
	roleBindingStore, err := factory(apiserver.GroupKindResourceType(roleBindingGVK.GroupKind()), scheme, roleBindingGVK)
	if err != nil {
		return authz.AuthzStores{}, fmt.Errorf("create role binding store: %w", err)
	}

	return authz.AuthzStores{
		PlatformRoles: platformRoleStore,
		Roles:         roleStore,
		RoleBindings:  roleBindingStore,
	}, nil
}
