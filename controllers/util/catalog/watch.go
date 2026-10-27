package catalog

import (
	"context"

	"github.com/openshift-online/gecko/controllers/util/logger"
	privatev1 "github.com/openshift-online/gecko/platform-api/api/private/v1"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// ReleaseVersionField indexes Clusters and NodePools by requested version.
const ReleaseVersionField = "spec.release.version"

// ReleaseVersion extracts the requested version for the cache index.
func ReleaseVersion(obj client.Object) []string {
	var version string
	switch resource := obj.(type) {
	case *privatev1.Cluster:
		version = resource.Spec.Release.Version
	case *privatev1.NodePool:
		version = resource.Spec.Release.Version
	}
	if version == "" {
		return nil
	}
	return []string{version}
}

// ToClusters wakes Clusters when their requested catalog entry changes.
func ToClusters(apiClient client.Client, log logger.Logger) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		return versionRequests(ctx, apiClient, log, obj, &privatev1.ClusterList{})
	}
}

// ToNodePools wakes NodePools when their requested catalog entry changes.
func ToNodePools(apiClient client.Client, log logger.Logger) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		return versionRequests(ctx, apiClient, log, obj, &privatev1.NodePoolList{})
	}
}

func versionRequests(ctx context.Context, apiClient client.Client, log logger.Logger, obj client.Object, list client.ObjectList) []reconcile.Request {
	version, ok := obj.(*privatev1.Version)
	if !ok {
		return nil
	}
	if err := apiClient.List(ctx, list, client.MatchingFields{ReleaseVersionField: version.Name}); err != nil {
		// Periodic reconciliation recovers from a missed watch event.
		log.Errorf(ctx, "catalog: list resources for Version failed")
		return nil
	}
	requests := make([]reconcile.Request, 0)
	if err := meta.EachListItem(list, func(item runtime.Object) error {
		accessor, err := meta.Accessor(item)
		if err != nil {
			return err
		}
		requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKey{
			Namespace: accessor.GetNamespace(), Name: accessor.GetName(),
		}})
		return nil
	}); err != nil {
		log.Errorf(ctx, "catalog: read resource list failed")
		return nil
	}
	return requests
}
