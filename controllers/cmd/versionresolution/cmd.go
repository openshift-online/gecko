package versionresolution

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/openshift-online/gecko/controllers/util/logger"
	"github.com/openshift-online/gecko/controllers/util/setup"
	"github.com/openshift-online/gecko/controllers/versionresolution"
	privatev1 "github.com/openshift-online/gecko/platform-api/api/private/v1"

	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// NewCommand returns the version-resolution subcommand.
func NewCommand(rf *setup.RootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "version-resolution",
		Short: "Run the version-resolution controller",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			log, err := rf.NewLogger("version-resolution-controller")
			if err != nil {
				return fmt.Errorf("create logger: %w", err)
			}

			scheme := setup.NewScheme()
			mgr, err := rf.NewManager(scheme, log)
			if err != nil {
				return fmt.Errorf("create manager: %w", err)
			}
			if err := mgr.GetFieldIndexer().IndexField(ctx, &privatev1.Cluster{}, versionresolution.ClusterReleaseVersionField, clusterReleaseVersion); err != nil {
				return fmt.Errorf("index clusters by release version: %w", err)
			}

			rec := versionresolution.NewReconciler(log, mgr.GetClient())

			if err := ctrl.NewControllerManagedBy(mgr).
				For(&privatev1.Cluster{}).
				Watches(&privatev1.Version{}, handler.EnqueueRequestsFromMapFunc(versionToClusters(mgr.GetClient(), log))).
				WithOptions(rf.ControllerOpts()).
				Complete(rec); err != nil {
				return fmt.Errorf("setup controller: %w", err)
			}

			return mgr.Start(ctx)
		},
	}

	return cmd
}

func clusterReleaseVersion(obj ctrlclient.Object) []string {
	cluster, ok := obj.(*privatev1.Cluster)
	if !ok || cluster.Spec.Release.Version == "" {
		return nil
	}
	return []string{cluster.Spec.Release.Version}
}

// versionToClusters enqueues every cluster requesting the release version a
// Version event refers to, so clusters created before the catalog caught up
// resolve as soon as their version appears.
func versionToClusters(c ctrlclient.Client, log logger.Logger) handler.MapFunc {
	return func(ctx context.Context, obj ctrlclient.Object) []reconcile.Request {
		version, ok := obj.(*privatev1.Version)
		if !ok {
			return nil
		}

		var clusters privatev1.ClusterList
		if err := c.List(ctx, &clusters, ctrlclient.MatchingFields{versionresolution.ClusterReleaseVersionField: version.Name}); err != nil {
			log.Errorf(ctx, "list clusters for version %s: %v", version.Name, err)
			return nil
		}

		requests := make([]reconcile.Request, 0, len(clusters.Items))
		for i := range clusters.Items {
			requests = append(requests, reconcile.Request{
				NamespacedName: ctrlclient.ObjectKeyFromObject(&clusters.Items[i]),
			})
		}
		return requests
	}
}
