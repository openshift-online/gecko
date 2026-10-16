package versionresolution

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/openshift-online/gecko/controllers/util/logger"
	privatev1 "github.com/openshift-online/gecko/platform-api/api/private/v1"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// ---- helpers ----------------------------------------------------------------

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

// mockStatusWriter captures status update calls.
type mockStatusWriter struct {
	updateErr error
	called    bool
	captured  client.Object
}

func (m *mockStatusWriter) Update(_ context.Context, obj client.Object, _ ...client.SubResourceUpdateOption) error {
	m.called = true
	m.captured = obj
	return m.updateErr
}
func (m *mockStatusWriter) Create(_ context.Context, _ client.Object, _ client.Object, _ ...client.SubResourceCreateOption) error {
	return nil
}
func (m *mockStatusWriter) Patch(_ context.Context, _ client.Object, _ client.Patch, _ ...client.SubResourcePatchOption) error {
	return nil
}
func (m *mockStatusWriter) Apply(_ context.Context, _ runtime.ApplyConfiguration, _ ...client.SubResourceApplyOption) error {
	return nil
}

// mockStoreClient is a minimal client.Client backed by a fixed Cluster and a
// fixed Version catalog entry.
type mockStoreClient struct {
	cluster       *privatev1.Cluster
	version       *privatev1.Version
	getErr        error
	versionGetErr error
	updateCalled  bool
	statusWriter  *mockStatusWriter
}

func (m *mockStoreClient) Get(_ context.Context, _ client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
	switch o := obj.(type) {
	case *privatev1.Cluster:
		if m.getErr != nil {
			return m.getErr
		}
		if m.cluster == nil {
			return apierrors.NewNotFound(schema.GroupResource{Resource: "cluster"}, "")
		}
		*o = *m.cluster
		return nil
	case *privatev1.Version:
		if m.versionGetErr != nil {
			return m.versionGetErr
		}
		if m.version == nil {
			return apierrors.NewNotFound(schema.GroupResource{Resource: "versions"}, "")
		}
		*o = *m.version
		return nil
	default:
		return fmt.Errorf("unexpected type %T", obj)
	}
}

func (m *mockStoreClient) Update(_ context.Context, _ client.Object, _ ...client.UpdateOption) error {
	m.updateCalled = true
	return nil
}

func (m *mockStoreClient) Status() client.SubResourceWriter {
	if m.statusWriter == nil {
		m.statusWriter = &mockStatusWriter{}
	}
	return m.statusWriter
}

func (m *mockStoreClient) List(_ context.Context, _ client.ObjectList, _ ...client.ListOption) error {
	return nil
}
func (m *mockStoreClient) Create(_ context.Context, _ client.Object, _ ...client.CreateOption) error {
	return nil
}
func (m *mockStoreClient) Delete(_ context.Context, _ client.Object, _ ...client.DeleteOption) error {
	return nil
}
func (m *mockStoreClient) Patch(_ context.Context, _ client.Object, _ client.Patch, _ ...client.PatchOption) error {
	return nil
}
func (m *mockStoreClient) DeleteAllOf(_ context.Context, _ client.Object, _ ...client.DeleteAllOfOption) error {
	return nil
}
func (m *mockStoreClient) Apply(_ context.Context, _ runtime.ApplyConfiguration, _ ...client.ApplyOption) error {
	return nil
}
func (m *mockStoreClient) SubResource(_ string) client.SubResourceClient { return nil }
func (m *mockStoreClient) Scheme() *runtime.Scheme                       { return nil }
func (m *mockStoreClient) RESTMapper() meta.RESTMapper                   { return nil }
func (m *mockStoreClient) GroupVersionKindFor(_ runtime.Object) (schema.GroupVersionKind, error) {
	return schema.GroupVersionKind{}, nil
}
func (m *mockStoreClient) IsObjectNamespaced(_ runtime.Object) (bool, error) { return false, nil }

// catalogVersion builds a Version catalog entry.
func catalogVersion(version, releaseImage string, channelGroups ...string) *privatev1.Version {
	v := &privatev1.Version{}
	v.SetName(version)
	v.Spec = privatev1.VersionSpec{
		ChannelGroups: channelGroups,
		ReleaseImage:  releaseImage,
	}
	return v
}

// clusterReq returns a reconcile.Request for the given cluster name.
func clusterReq(name string) reconcile.Request {
	return reconcile.Request{
		NamespacedName: client.ObjectKey{Namespace: "hyperfleet", Name: name},
	}
}

// buildReconciler wires up a Reconciler backed by the store client.
func buildReconciler(
	t *testing.T,
	cluster *privatev1.Cluster,
	version *privatev1.Version,
) (*Reconciler, *mockStoreClient) {
	t.Helper()
	storeClient := &mockStoreClient{cluster: cluster, version: version, statusWriter: &mockStatusWriter{}}
	return NewReconciler(newTestLogger(t), storeClient), storeClient
}

// ---- tests ------------------------------------------------------------------

func TestReconciler_HappyPath(t *testing.T) {
	const (
		version      = "4.22.0-ec.4"
		releaseImage = "quay.io/openshift-release-dev/ocp-release:4.22.0-ec.4-x86_64"
	)

	cluster := &privatev1.Cluster{}
	cluster.SetName("cluster-1")
	cluster.SetNamespace("hyperfleet")
	cluster.SetGeneration(3)
	cluster.Spec.Release = privatev1.ReleaseSpec{Version: version, ChannelGroup: "candidate"}

	r, storeClient := buildReconciler(t, cluster, catalogVersion(version, releaseImage, "candidate", "fast"))

	result, err := r.Reconcile(context.Background(), clusterReq("cluster-1"))

	require.NoError(t, err)
	require.Equal(t, reconcile.Result{RequeueAfter: requeueStable}, result)
	require.False(t, storeClient.updateCalled, "expected no spec Update (result written to status)")
	require.NotNil(t, storeClient.statusWriter)
	require.True(t, storeClient.statusWriter.called, "expected Status().Update to be called")

	updated := storeClient.statusWriter.captured.(*privatev1.Cluster)
	require.NotNil(t, updated.Status.VersionResolution)
	require.Equal(t, releaseImage, updated.Status.VersionResolution.ReleaseImage)
	require.Equal(t, version, updated.Status.VersionResolution.ReleaseVersion)
	require.Equal(t, "candidate", updated.Status.VersionResolution.ChannelGroup)
	require.Equal(t, "candidate-4.22", updated.Status.VersionResolution.CincinnatiChannel)

	require.Len(t, updated.Status.Conditions, 1)
	require.Equal(t, "VersionResolved", updated.Status.Conditions[0].Type)
	require.Equal(t, metav1.ConditionTrue, updated.Status.Conditions[0].Status)
}

func TestReconciler_ResolvesNightlyChannelGroup(t *testing.T) {
	const (
		version      = "4.22.0-0.nightly-2026-10-07-123456"
		releaseImage = "registry.ci.openshift.org/ocp/release:4.22.0-0.nightly-2026-10-07-123456"
	)

	cluster := &privatev1.Cluster{}
	cluster.SetName("cluster-nightly")
	cluster.SetNamespace("hyperfleet")
	cluster.Spec.Release = privatev1.ReleaseSpec{Version: version, ChannelGroup: "nightly"}

	r, storeClient := buildReconciler(t, cluster, catalogVersion(version, releaseImage, "nightly"))

	_, err := r.Reconcile(context.Background(), clusterReq("cluster-nightly"))

	require.NoError(t, err)
	updated := storeClient.statusWriter.captured.(*privatev1.Cluster)
	require.Equal(t, releaseImage, updated.Status.VersionResolution.ReleaseImage)
}

func TestReconciler_AlreadyResolved(t *testing.T) {
	const version = "4.22.0-ec.4"

	cluster := &privatev1.Cluster{}
	cluster.SetName("cluster-2")
	cluster.SetNamespace("hyperfleet")
	cluster.Spec.Release = privatev1.ReleaseSpec{Version: version, ChannelGroup: "candidate"}
	cluster.Status.VersionResolution = &privatev1.VersionResolutionResult{
		ReleaseImage:   "quay.io/openshift-release-dev/ocp-release:4.22.0-ec.4-x86_64",
		ReleaseVersion: version,
		ChannelGroup:   "candidate",
	}

	// The catalog now advertises a different image for the same version. The
	// pinned result must win so a running cluster is never moved.
	r, storeClient := buildReconciler(t, cluster, catalogVersion(version, "quay.io/ocp:rebuilt", "candidate"))

	result, err := r.Reconcile(context.Background(), clusterReq("cluster-2"))

	require.NoError(t, err)
	require.Equal(t, reconcile.Result{}, result)
	require.False(t, storeClient.updateCalled, "expected no spec Update")
	require.False(t, storeClient.statusWriter.called, "pinned resolution must not be rewritten")
}

func TestReconciler_ChannelGroupChangeTriggersReresolution(t *testing.T) {
	const version = "4.22.0-ec.4"

	cluster := &privatev1.Cluster{}
	cluster.SetName("cluster-regroup")
	cluster.SetNamespace("hyperfleet")
	cluster.Spec.Release = privatev1.ReleaseSpec{Version: version, ChannelGroup: "fast"}
	cluster.Status.VersionResolution = &privatev1.VersionResolutionResult{
		ReleaseImage:   "quay.io/ocp:old",
		ReleaseVersion: version,
		ChannelGroup:   "candidate",
	}

	r, storeClient := buildReconciler(t, cluster, catalogVersion(version, "quay.io/ocp:current", "candidate", "fast"))

	_, err := r.Reconcile(context.Background(), clusterReq("cluster-regroup"))

	require.NoError(t, err)
	require.True(t, storeClient.statusWriter.called)
	updated := storeClient.statusWriter.captured.(*privatev1.Cluster)
	require.Equal(t, "quay.io/ocp:current", updated.Status.VersionResolution.ReleaseImage)
	require.Equal(t, "fast", updated.Status.VersionResolution.ChannelGroup)
}

func TestReconciler_ClusterNotFound(t *testing.T) {
	r, _ := buildReconciler(t, nil, nil) // nil cluster → NotFound

	result, err := r.Reconcile(context.Background(), clusterReq("cluster-404"))

	require.NoError(t, err)
	require.Equal(t, reconcile.Result{}, result)
}

func TestReconciler_VersionNotSet(t *testing.T) {
	cluster := &privatev1.Cluster{}
	cluster.SetName("cluster-3")
	cluster.SetNamespace("hyperfleet")
	// Release is empty — version not set

	r, storeClient := buildReconciler(t, cluster, nil)

	result, err := r.Reconcile(context.Background(), clusterReq("cluster-3"))

	require.NoError(t, err)
	require.Equal(t, reconcile.Result{}, result)
	require.False(t, storeClient.updateCalled)
}

func TestReconciler_VersionNotInCatalog(t *testing.T) {
	cluster := &privatev1.Cluster{}
	cluster.SetName("cluster-5")
	cluster.SetNamespace("hyperfleet")
	cluster.Spec.Release = privatev1.ReleaseSpec{Version: "4.22.0-ec.4", ChannelGroup: "candidate"}

	r, storeClient := buildReconciler(t, cluster, nil) // no catalog entry

	result, err := r.Reconcile(context.Background(), clusterReq("cluster-5"))

	require.NoError(t, err)
	require.Equal(t, reconcile.Result{}, result)
	require.False(t, storeClient.updateCalled)
	require.True(t, storeClient.statusWriter.called)

	updated := storeClient.statusWriter.captured.(*privatev1.Cluster)
	require.Nil(t, updated.Status.VersionResolution)
	require.Len(t, updated.Status.Conditions, 1)
	cond := updated.Status.Conditions[0]
	require.Equal(t, "VersionResolved", cond.Type)
	require.Equal(t, metav1.ConditionFalse, cond.Status)
	require.Equal(t, ReasonVersionNotInCatalog, cond.Reason)
}

func TestReconciler_VersionNotInCatalog_StatusUpdateError(t *testing.T) {
	cluster := &privatev1.Cluster{}
	cluster.SetName("cluster-5b")
	cluster.SetNamespace("hyperfleet")
	cluster.Spec.Release = privatev1.ReleaseSpec{Version: "4.22.0-ec.4", ChannelGroup: "candidate"}

	storeClient := &mockStoreClient{
		cluster:      cluster,
		statusWriter: &mockStatusWriter{updateErr: fmt.Errorf("api-server busy")},
	}
	r := NewReconciler(newTestLogger(t), storeClient)

	_, err := r.Reconcile(context.Background(), clusterReq("cluster-5b"))

	require.Error(t, err)
	require.Contains(t, err.Error(), "update cluster status")
}

func TestReconciler_VersionNotInChannelGroup(t *testing.T) {
	const version = "4.22.0-ec.4"

	cluster := &privatev1.Cluster{}
	cluster.SetName("cluster-6")
	cluster.SetNamespace("hyperfleet")
	cluster.Spec.Release = privatev1.ReleaseSpec{Version: version, ChannelGroup: "stable"}

	r, storeClient := buildReconciler(t, cluster, catalogVersion(version, "quay.io/ocp:ec4", "candidate"))

	result, err := r.Reconcile(context.Background(), clusterReq("cluster-6"))

	require.NoError(t, err)
	require.Equal(t, reconcile.Result{}, result)
	require.True(t, storeClient.statusWriter.called)

	updated := storeClient.statusWriter.captured.(*privatev1.Cluster)
	require.Nil(t, updated.Status.VersionResolution)
	require.Len(t, updated.Status.Conditions, 1)
	require.Equal(t, ReasonVersionNotInChannelGroup, updated.Status.Conditions[0].Reason)
}

func TestReconciler_InvalidVersion_BuildChannelError(t *testing.T) {
	cluster := &privatev1.Cluster{}
	cluster.SetName("cluster-8")
	cluster.SetNamespace("hyperfleet")
	cluster.Spec.Release = privatev1.ReleaseSpec{Version: "4", ChannelGroup: "candidate"} // no minor

	r, _ := buildReconciler(t, cluster, nil)

	_, err := r.Reconcile(context.Background(), clusterReq("cluster-8"))

	require.Error(t, err)
	require.Contains(t, err.Error(), "build channel")
}

func TestReconciler_CatalogGetError(t *testing.T) {
	cluster := &privatev1.Cluster{}
	cluster.SetName("cluster-7")
	cluster.SetNamespace("hyperfleet")
	cluster.Spec.Release = privatev1.ReleaseSpec{Version: "4.22.0-ec.4", ChannelGroup: "candidate"}

	storeClient := &mockStoreClient{cluster: cluster, versionGetErr: fmt.Errorf("etcd timeout")}
	r := NewReconciler(newTestLogger(t), storeClient)

	_, err := r.Reconcile(context.Background(), clusterReq("cluster-7"))

	require.Error(t, err)
	require.Contains(t, err.Error(), "resolve version for cluster")
}

func TestBuildChannel(t *testing.T) {
	cases := []struct {
		version string
		want    string
		wantErr bool
	}{
		{"4.22.0-ec.4", "candidate-4.22", false},
		{"4.16.3", "candidate-4.16", false},
		{"4.15.0", "candidate-4.15", false},
		{"invalid", "", true},
		{"4", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.version, func(t *testing.T) {
			got, err := buildChannel(tc.version, "candidate")
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.want, got)
			}
		})
	}
}
