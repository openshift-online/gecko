package nodepoolvrresolution

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/openshift-online/gecko/controllers/nodepoolvrresolution"
	"github.com/openshift-online/gecko/controllers/util/logger"
	privatev1 "github.com/openshift-online/gecko/platform-api/api/private/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// listClient serves a fixed NodePoolList and records the list options it was
// called with.
type listClient struct {
	ctrlclient.Client
	nodePools []privatev1.NodePool
	listErr   error
	opts      []ctrlclient.ListOption
}

func (c *listClient) List(_ context.Context, list ctrlclient.ObjectList, opts ...ctrlclient.ListOption) error {
	c.opts = opts
	if c.listErr != nil {
		return c.listErr
	}
	nodePools, ok := list.(*privatev1.NodePoolList)
	if !ok {
		return fmt.Errorf("unexpected list type %T", list)
	}
	nodePools.Items = c.nodePools
	return nil
}

func newTestLogger(t *testing.T) logger.Logger {
	t.Helper()
	log, err := logger.NewLogger(logger.Config{
		Level:     "error",
		Format:    logger.FormatText,
		Component: "test",
		Version:   "test",
	})
	require.NoError(t, err)
	return log
}

func nodePool(namespace, name, version string) privatev1.NodePool {
	np := privatev1.NodePool{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name}}
	np.Spec.Release.Version = version
	return np
}

func TestNodePoolReleaseVersion(t *testing.T) {
	np := nodePool("test", "nodepool", "4.22.0-ec.4")
	require.Equal(t, []string{"4.22.0-ec.4"}, nodePoolReleaseVersion(&np))
	require.Empty(t, nodePoolReleaseVersion(&privatev1.NodePool{}))
	require.Empty(t, nodePoolReleaseVersion(&privatev1.Cluster{}))
}

func TestVersionToNodePools(t *testing.T) {
	version := &privatev1.Version{ObjectMeta: metav1.ObjectMeta{Name: "4.22.0-ec.4"}}

	t.Run("enqueues every nodepool requesting the version", func(t *testing.T) {
		c := &listClient{nodePools: []privatev1.NodePool{
			nodePool("cluster-1", "np-1", "4.22.0-ec.4"),
			nodePool("cluster-2", "np-2", "4.22.0-ec.4"),
		}}

		requests := versionToNodePools(c, newTestLogger(t))(context.Background(), version)

		require.Len(t, requests, 2)
		require.Equal(t, "cluster-1", requests[0].Namespace)
		require.Equal(t, "np-1", requests[0].Name)
		require.Equal(t, "cluster-2", requests[1].Namespace)
		require.Equal(t, "np-2", requests[1].Name)

		require.Len(t, c.opts, 1)
		require.Equal(t,
			ctrlclient.MatchingFields{nodepoolvrresolution.NodePoolReleaseVersionField: "4.22.0-ec.4"},
			c.opts[0],
			"nodepools must be selected through the release version index")
	})

	t.Run("enqueues nothing when no nodepool requests the version", func(t *testing.T) {
		requests := versionToNodePools(&listClient{}, newTestLogger(t))(context.Background(), version)
		require.Empty(t, requests)
	})

	t.Run("enqueues nothing when the list fails", func(t *testing.T) {
		c := &listClient{listErr: errors.New("cache not synced")}
		requests := versionToNodePools(c, newTestLogger(t))(context.Background(), version)
		require.Empty(t, requests)
	})

	t.Run("ignores objects that are not Versions", func(t *testing.T) {
		requests := versionToNodePools(&listClient{}, newTestLogger(t))(context.Background(), &privatev1.NodePool{})
		require.Empty(t, requests)
	})
}
