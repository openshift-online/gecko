package catalog

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/openshift-online/gecko/controllers/util/logger"
	"github.com/openshift-online/gecko/controllers/util/setup"
	privatev1 "github.com/openshift-online/gecko/platform-api/api/private/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestReleaseVersion(t *testing.T) {
	for _, tc := range []struct {
		name     string
		object   client.Object
		expected []string
	}{
		{name: "cluster", object: &privatev1.Cluster{Spec: privatev1.ClusterSpec{Release: privatev1.ReleaseSpec{Version: "4.22.14"}}}, expected: []string{"4.22.14"}},
		{name: "nodepool", object: &privatev1.NodePool{Spec: privatev1.NodePoolSpec{Release: privatev1.ReleaseSpec{Version: "5.0.0-ec.6"}}}, expected: []string{"5.0.0-ec.6"}},
		{name: "unset cluster", object: &privatev1.Cluster{}},
		{name: "unset nodepool", object: &privatev1.NodePool{}},
		{name: "other resource", object: &privatev1.Version{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.expected, ReleaseVersion(tc.object))
		})
	}
}

func TestVersionWatchUsesReleaseIndexAcrossNamespaces(t *testing.T) {
	apiClient := fake.NewClientBuilder().WithScheme(setup.NewScheme()).WithObjects(
		&privatev1.Cluster{ObjectMeta: metav1.ObjectMeta{Namespace: "project-a", Name: "cluster-a"}, Spec: privatev1.ClusterSpec{Release: privatev1.ReleaseSpec{Version: "4.22.14"}}},
		&privatev1.Cluster{ObjectMeta: metav1.ObjectMeta{Namespace: "project-b", Name: "cluster-b"}, Spec: privatev1.ClusterSpec{Release: privatev1.ReleaseSpec{Version: "4.22.14"}}},
		&privatev1.Cluster{ObjectMeta: metav1.ObjectMeta{Namespace: "project-a", Name: "other"}, Spec: privatev1.ClusterSpec{Release: privatev1.ReleaseSpec{Version: "4.23.1"}}},
		&privatev1.NodePool{ObjectMeta: metav1.ObjectMeta{Namespace: "project-a", Name: "np-a"}, Spec: privatev1.NodePoolSpec{Release: privatev1.ReleaseSpec{Version: "4.22.14"}}},
		&privatev1.NodePool{ObjectMeta: metav1.ObjectMeta{Namespace: "project-b", Name: "np-b"}, Spec: privatev1.NodePoolSpec{Release: privatev1.ReleaseSpec{Version: "4.22.14"}}},
		&privatev1.NodePool{ObjectMeta: metav1.ObjectMeta{Namespace: "project-a", Name: "other"}, Spec: privatev1.NodePoolSpec{Release: privatev1.ReleaseSpec{Version: "4.23.1"}}},
	).WithIndex(&privatev1.Cluster{}, ReleaseVersionField, ReleaseVersion).
		WithIndex(&privatev1.NodePool{}, ReleaseVersionField, ReleaseVersion).Build()
	log, err := logger.NewLogger(logger.Config{Level: "error", Format: logger.FormatText, Writer: &bytes.Buffer{}, Component: "test"})
	require.NoError(t, err)
	for _, tc := range []struct {
		name     string
		mapEvent handler.MapFunc
		requests []reconcile.Request
	}{
		{name: "clusters", mapEvent: ToClusters(apiClient, log), requests: []reconcile.Request{
			{NamespacedName: client.ObjectKey{Namespace: "project-a", Name: "cluster-a"}},
			{NamespacedName: client.ObjectKey{Namespace: "project-b", Name: "cluster-b"}},
		}},
		{name: "nodepools", mapEvent: ToNodePools(apiClient, log), requests: []reconcile.Request{
			{NamespacedName: client.ObjectKey{Namespace: "project-a", Name: "np-a"}},
			{NamespacedName: client.ObjectKey{Namespace: "project-b", Name: "np-b"}},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			require.ElementsMatch(t, tc.requests, tc.mapEvent(ctx, &privatev1.Version{ObjectMeta: metav1.ObjectMeta{Name: "4.22.14"}}))
			require.Empty(t, tc.mapEvent(ctx, &privatev1.Version{ObjectMeta: metav1.ObjectMeta{Name: "missing"}}))
			require.Empty(t, tc.mapEvent(ctx, &privatev1.Channel{}))
		})
	}
}

type failingListClient struct{ client.Client }

func (failingListClient) List(context.Context, client.ObjectList, ...client.ListOption) error {
	return errors.New("list unavailable")
}

func TestVersionWatchListFailure(t *testing.T) {
	var output bytes.Buffer
	log, err := logger.NewLogger(logger.Config{Level: "error", Format: logger.FormatText, Writer: &output, Component: "test"})
	require.NoError(t, err)
	for _, mapEvent := range []handler.MapFunc{ToClusters(failingListClient{}, log), ToNodePools(failingListClient{}, log)} {
		require.Empty(t, mapEvent(context.Background(), &privatev1.Version{ObjectMeta: metav1.ObjectMeta{Name: "4.22.14"}}))
	}
	require.Contains(t, output.String(), "catalog: list resources for Version failed")
}
