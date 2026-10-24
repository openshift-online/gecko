package versionresolution

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/openshift-online/gecko/controllers/util/logger"
	"github.com/openshift-online/gecko/controllers/versionresolution"
	privatev1 "github.com/openshift-online/gecko/platform-api/api/private/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// listClient serves a fixed ClusterList and records the list options it was
// called with.
type listClient struct {
	ctrlclient.Client
	clusters []privatev1.Cluster
	listErr  error
	opts     []ctrlclient.ListOption
}

func (c *listClient) List(_ context.Context, list ctrlclient.ObjectList, opts ...ctrlclient.ListOption) error {
	c.opts = opts
	if c.listErr != nil {
		return c.listErr
	}
	clusters, ok := list.(*privatev1.ClusterList)
	if !ok {
		return fmt.Errorf("unexpected list type %T", list)
	}
	clusters.Items = c.clusters
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

func cluster(namespace, name, version string) privatev1.Cluster {
	c := privatev1.Cluster{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name}}
	c.Spec.Release.Version = version
	return c
}

func TestClusterReleaseVersion(t *testing.T) {
	c := cluster("test", "cluster", "4.22.0-ec.4")
	require.Equal(t, []string{"4.22.0-ec.4"}, clusterReleaseVersion(&c))
	require.Empty(t, clusterReleaseVersion(&privatev1.Cluster{}))
	require.Empty(t, clusterReleaseVersion(&privatev1.NodePool{}))
}

func TestVersionToClusters(t *testing.T) {
	version := &privatev1.Version{ObjectMeta: metav1.ObjectMeta{Name: "4.22.0-ec.4"}}

	t.Run("enqueues every cluster requesting the version", func(t *testing.T) {
		c := &listClient{clusters: []privatev1.Cluster{
			cluster("ns-a", "cluster-1", "4.22.0-ec.4"),
			cluster("ns-b", "cluster-2", "4.22.0-ec.4"),
		}}

		requests := versionToClusters(c, newTestLogger(t))(context.Background(), version)

		require.Len(t, requests, 2)
		require.Equal(t, "ns-a", requests[0].Namespace)
		require.Equal(t, "cluster-1", requests[0].Name)
		require.Equal(t, "ns-b", requests[1].Namespace)
		require.Equal(t, "cluster-2", requests[1].Name)

		require.Len(t, c.opts, 1)
		require.Equal(t,
			ctrlclient.MatchingFields{versionresolution.ClusterReleaseVersionField: "4.22.0-ec.4"},
			c.opts[0],
			"clusters must be selected through the release version index")
	})

	t.Run("enqueues nothing when no cluster requests the version", func(t *testing.T) {
		requests := versionToClusters(&listClient{}, newTestLogger(t))(context.Background(), version)
		require.Empty(t, requests)
	})

	t.Run("enqueues nothing when the list fails", func(t *testing.T) {
		c := &listClient{listErr: errors.New("cache not synced")}
		requests := versionToClusters(c, newTestLogger(t))(context.Background(), version)
		require.Empty(t, requests)
	})

	t.Run("ignores objects that are not Versions", func(t *testing.T) {
		requests := versionToClusters(&listClient{}, newTestLogger(t))(context.Background(), &privatev1.Cluster{})
		require.Empty(t, requests)
	})
}
