package nodepoolvrresolution

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/openshift-online/gecko/controllers/nodepoolvrresolution"
	"github.com/openshift-online/gecko/controllers/util/logger"
	"github.com/openshift-online/gecko/controllers/util/setup"
	privatev1 "github.com/openshift-online/gecko/platform-api/api/private/v1"

	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// NewCommand returns the nodepool-vr subcommand.
func NewCommand(rf *setup.RootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "nodepool-vr",
		Short: "Run the nodepool version-resolution controller",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			log, err := rf.NewLogger("nodepool-vr-controller")
			if err != nil {
				return fmt.Errorf("create logger: %w", err)
			}

			scheme := setup.NewScheme()
			mgr, err := rf.NewManager(scheme, log)
			if err != nil {
				return fmt.Errorf("create manager: %w", err)
			}
			if err := mgr.GetFieldIndexer().IndexField(ctx, &privatev1.NodePool{}, nodepoolvrresolution.NodePoolReleaseVersionField, nodePoolReleaseVersion); err != nil {
				return fmt.Errorf("index nodepools by release version: %w", err)
			}

			rec := nodepoolvrresolution.NewReconciler(log, mgr.GetClient())

			if err := ctrl.NewControllerManagedBy(mgr).
				For(&privatev1.NodePool{}).
				Watches(&privatev1.Version{}, handler.EnqueueRequestsFromMapFunc(versionToNodePools(mgr.GetClient(), log))).
				WithOptions(rf.ControllerOpts()).
				Complete(rec); err != nil {
				return fmt.Errorf("setup controller: %w", err)
			}

			return mgr.Start(ctx)
		},
	}

	return cmd
}

func nodePoolReleaseVersion(obj ctrlclient.Object) []string {
	nodePool, ok := obj.(*privatev1.NodePool)
	if !ok || nodePool.Spec.Release.Version == "" {
		return nil
	}
	return []string{nodePool.Spec.Release.Version}
}

// versionToNodePools enqueues every node pool requesting the release version a
// Version event refers to, so node pools created before the catalog caught up
// resolve as soon as their version appears.
func versionToNodePools(c ctrlclient.Client, log logger.Logger) handler.MapFunc {
	return func(ctx context.Context, obj ctrlclient.Object) []reconcile.Request {
		version, ok := obj.(*privatev1.Version)
		if !ok {
			return nil
		}

		var nodePools privatev1.NodePoolList
		if err := c.List(ctx, &nodePools, ctrlclient.MatchingFields{nodepoolvrresolution.NodePoolReleaseVersionField: version.Name}); err != nil {
			log.Errorf(ctx, "list nodepools for version %s: %v", version.Name, err)
			return nil
		}

		requests := make([]reconcile.Request, 0, len(nodePools.Items))
		for i := range nodePools.Items {
			requests = append(requests, reconcile.Request{
				NamespacedName: ctrlclient.ObjectKeyFromObject(&nodePools.Items[i]),
			})
		}
		return requests
	}
}
