package nodepoolvrresolution

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openshift-online/gecko/controllers/util/catalog"
	"github.com/openshift-online/gecko/controllers/util/logger"
	"github.com/openshift-online/gecko/controllers/util/setup"
	privatev1 "github.com/openshift-online/gecko/platform-api/api/private/v1"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// ─── mocks ────────────────────────────────────────────────────────────────────

// statusWriter is a SubResourceWriter that captures the object passed to Update
// and can return a configured error.
type statusWriter struct {
	called    bool
	updateErr error
	captured  client.Object
}

func (m *statusWriter) Update(_ context.Context, obj client.Object, _ ...client.SubResourceUpdateOption) error {
	m.called = true
	m.captured = obj
	return m.updateErr
}
func (m *statusWriter) Create(_ context.Context, _ client.Object, _ client.Object, _ ...client.SubResourceCreateOption) error {
	return nil
}
func (m *statusWriter) Patch(_ context.Context, _ client.Object, _ client.Patch, _ ...client.SubResourcePatchOption) error {
	return nil
}
func (m *statusWriter) Apply(_ context.Context, _ runtime.ApplyConfiguration, _ ...client.SubResourceApplyOption) error {
	return nil
}

// storeClient is a minimal client.Client backed by a fixed NodePool and Cluster
// with configurable error injection for Get and Status().Update.
type storeClient struct {
	nodepool     *privatev1.NodePool
	cluster      *privatev1.Cluster
	version      *privatev1.Version
	versionErr   error
	npGetErr     error
	clsGetErr    error
	statusWriter *statusWriter
}

func (m *storeClient) Get(_ context.Context, key client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
	switch o := obj.(type) {
	case *privatev1.NodePool:
		if m.npGetErr != nil {
			return m.npGetErr
		}
		if m.nodepool == nil {
			return apierrors.NewNotFound(schema.GroupResource{Resource: "nodepool"}, "")
		}
		m.nodepool.DeepCopyInto(o)
		return nil
	case *privatev1.Cluster:
		if m.clsGetErr != nil {
			return m.clsGetErr
		}
		if m.cluster == nil {
			return apierrors.NewNotFound(schema.GroupResource{Resource: "cluster"}, "")
		}
		m.cluster.DeepCopyInto(o)
		return nil
	case *privatev1.Version:
		if m.versionErr != nil {
			return m.versionErr
		}
		if m.version == nil || m.version.Name != key.Name {
			return apierrors.NewNotFound(schema.GroupResource{Resource: "versions"}, key.Name)
		}
		m.version.DeepCopyInto(o)
		return nil
	default:
		return fmt.Errorf("unexpected type %T", obj)
	}
}

func (m *storeClient) Status() client.SubResourceWriter { return m.statusWriter }

func (m *storeClient) List(_ context.Context, _ client.ObjectList, _ ...client.ListOption) error {
	return nil
}
func (m *storeClient) Create(_ context.Context, _ client.Object, _ ...client.CreateOption) error {
	return nil
}
func (m *storeClient) Delete(_ context.Context, _ client.Object, _ ...client.DeleteOption) error {
	return nil
}
func (m *storeClient) Update(_ context.Context, _ client.Object, _ ...client.UpdateOption) error {
	return nil
}
func (m *storeClient) Patch(_ context.Context, _ client.Object, _ client.Patch, _ ...client.PatchOption) error {
	return nil
}
func (m *storeClient) DeleteAllOf(_ context.Context, _ client.Object, _ ...client.DeleteAllOfOption) error {
	return nil
}
func (m *storeClient) Apply(_ context.Context, _ runtime.ApplyConfiguration, _ ...client.ApplyOption) error {
	return nil
}
func (m *storeClient) SubResource(_ string) client.SubResourceClient { return nil }
func (m *storeClient) Scheme() *runtime.Scheme                       { return nil }
func (m *storeClient) RESTMapper() meta.RESTMapper                   { return nil }
func (m *storeClient) GroupVersionKindFor(_ runtime.Object) (schema.GroupVersionKind, error) {
	return schema.GroupVersionKind{}, nil
}
func (m *storeClient) IsObjectNamespaced(_ runtime.Object) (bool, error) { return false, nil }

// ─── helpers ──────────────────────────────────────────────────────────────────

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

// makeNP builds a NodePool with Spec.Release.Version set.
func makeNP(clusterID, npID, version string) *privatev1.NodePool {
	np := &privatev1.NodePool{}
	np.SetName(npID)
	np.SetNamespace(clusterID)
	np.Spec.ClusterID = clusterID
	np.Spec.Release.Version = version
	return np
}

// makeCluster builds a minimal Cluster.
func makeCluster(id string) *privatev1.Cluster {
	c := &privatev1.Cluster{}
	c.SetName(id)
	c.SetNamespace("hyperfleet")
	return c
}

// buildReconciler constructs a Reconciler backed by the given NodePool and
// Cluster and catalog release. statusErr is returned by Status().Update.
func buildReconciler(
	t *testing.T,
	np *privatev1.NodePool,
	cluster *privatev1.Cluster,
	release *privatev1.Version,
	npGetErr, clsGetErr, statusErr error,
) (*Reconciler, *storeClient) {
	t.Helper()
	sw := &statusWriter{updateErr: statusErr}
	store := &storeClient{
		nodepool: np, cluster: cluster, version: release,
		npGetErr: npGetErr, clsGetErr: clsGetErr,
		statusWriter: sw,
	}
	return NewReconciler(newTestLogger(t), store), store
}

// npReq returns a reconcile.Request for the given clusterID/nodepoolID pair.
func npReq(clusterID, nodepoolID string) reconcile.Request {
	return reconcile.Request{NamespacedName: client.ObjectKey{Namespace: clusterID, Name: nodepoolID}}
}

func catalogVersion(version, image string, groups ...string) *privatev1.Version {
	return &privatev1.Version{ObjectMeta: metav1.ObjectMeta{Name: version},
		Spec: privatev1.VersionSpec{ReleaseImage: image, ChannelGroups: groups}}
}

// conflictErr returns a Kubernetes 409 Conflict error.
func conflictErr() error {
	return apierrors.NewConflict(schema.GroupResource{Resource: "nodepools"}, "np", errors.New("conflict"))
}

// ─── NodePool / Cluster fetch errors ─────────────────────────────────────────

func TestReconciler_NodepoolNotFound(t *testing.T) {
	r, _ := buildReconciler(t, nil, nil, nil, nil, nil, nil)

	result, err := r.Reconcile(context.Background(), npReq("cluster-1", "np-404"))

	require.NoError(t, err)
	require.Equal(t, reconcile.Result{}, result)
}

func TestReconciler_NodepoolGetError(t *testing.T) {
	r, _ := buildReconciler(t, nil, nil, nil, fmt.Errorf("etcd timeout"), nil, nil)

	_, err := r.Reconcile(context.Background(), npReq("cluster-1", "np-1"))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "get nodepool")
}

func TestReconciler_ClusterNotFoundForNodepool(t *testing.T) {
	np := makeNP("cluster-1", "np-1", "")
	r, _ := buildReconciler(t, np, nil, nil, nil, nil, nil)

	result, err := r.Reconcile(context.Background(), npReq("cluster-1", "np-1"))

	require.NoError(t, err)
	require.Equal(t, reconcile.Result{}, result)
}

func TestReconciler_ClusterGetError(t *testing.T) {
	np := makeNP("cluster-1", "np-1", "4.15.0")
	r, _ := buildReconciler(t, np, nil, nil, nil, fmt.Errorf("etcd timeout"), nil)

	_, err := r.Reconcile(context.Background(), npReq("cluster-1", "np-1"))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "get cluster")
}

// ─── release version not set ──────────────────────────────────────────────────

func TestReconciler_VersionNotSet_SetsConditionAndReturns(t *testing.T) {
	np := makeNP("cluster-1", "np-1", "") // no version

	r, store := buildReconciler(t, np, makeCluster("cluster-1"), nil, nil, nil, nil)
	result, err := r.Reconcile(context.Background(), npReq("cluster-1", "np-1"))

	require.NoError(t, err)
	assert.Equal(t, reconcile.Result{}, result)
	assert.True(t, store.statusWriter.called)

	updated := store.statusWriter.captured.(*privatev1.NodePool)
	require.Len(t, updated.Status.Conditions, 1)
	assert.Equal(t, "ReleaseVersionNotSet", updated.Status.Conditions[0].Reason)
}

func TestReconciler_VersionNotSet_StatusUpdateError(t *testing.T) {
	np := makeNP("cluster-1", "np-1", "")

	r, _ := buildReconciler(t, np, makeCluster("cluster-1"), nil, nil, nil, fmt.Errorf("api-server busy"))
	_, err := r.Reconcile(context.Background(), npReq("cluster-1", "np-1"))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "update nodepool status")
}

// ─── already resolved ─────────────────────────────────────────────────────────

func TestReconciler_AlreadyResolved_SkipsWithoutCatalog(t *testing.T) {
	np := makeNP("cluster-1", "np-1", "4.15.0")
	np.Status.VersionResolution = &privatev1.VersionResolutionResult{ReleaseVersion: "4.15.0", ChannelGroup: "candidate"}

	r, store := buildReconciler(t, np, makeCluster("cluster-1"), nil, nil, nil, nil)
	result, err := r.Reconcile(context.Background(), npReq("cluster-1", "np-1"))

	require.NoError(t, err)
	assert.Equal(t, reconcile.Result{}, result)
	assert.False(t, store.statusWriter.called, "status must not be updated when already resolved")
}

// ─── channel construction ─────────────────────────────────────────────────────

func TestReconciler_InvalidVersion_BuildChannelError(t *testing.T) {
	np := makeNP("cluster-1", "np-1", "4") // no minor → invalid for buildChannel

	r, _ := buildReconciler(t, np, makeCluster("cluster-1"), nil, nil, nil, nil)
	_, err := r.Reconcile(context.Background(), npReq("cluster-1", "np-1"))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "build channel")
}

func TestReconciler_CustomChannelGroup_UsesCatalogMembership(t *testing.T) {
	np := makeNP("cluster-1", "np-1", "5.0.0-ec.6")
	np.Spec.Release.ChannelGroup = "dev-preview"
	release := catalogVersion(np.Spec.Release.Version, "ci-image", "dev-preview")
	r, store := buildReconciler(t, np, makeCluster("cluster-1"), release, nil, nil, nil)
	_, err := r.Reconcile(context.Background(), npReq("cluster-1", "np-1"))
	require.NoError(t, err)
	updated := store.statusWriter.captured.(*privatev1.NodePool)
	require.Equal(t, "ci-image", updated.Status.VersionResolution.ReleaseImage)
	require.Equal(t, "dev-preview-5.0", updated.Status.VersionResolution.CincinnatiChannel)
}

func TestReconciler_CatalogErrorIsRetryable(t *testing.T) {
	np := makeNP("cluster-1", "np-1", "4.15.0")
	r, store := buildReconciler(t, np, makeCluster("cluster-1"), nil, nil, nil, nil)
	store.versionErr = errors.New("cache not synced")
	_, err := r.Reconcile(context.Background(), npReq("cluster-1", "np-1"))
	require.ErrorIs(t, err, store.versionErr)
	require.False(t, store.statusWriter.called, "infrastructure failures must not set a terminal False condition")
}

func TestReconciler_CatalogUnavailable_SetsCondition(t *testing.T) {
	for _, tc := range []struct {
		name    string
		release *privatev1.Version
		reason  string
	}{
		{"missing", nil, "VersionNotFoundInCatalog"},
		{"wrong channel", catalogVersion("4.15.0", "image", "stable"), "VersionNotInChannelGroup"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			np := makeNP("cluster-1", "np-1", "4.15.0")
			r, store := buildReconciler(t, np, makeCluster("cluster-1"), tc.release, nil, nil, nil)
			result, err := r.Reconcile(context.Background(), npReq("cluster-1", "np-1"))
			require.NoError(t, err)
			require.Equal(t, requeueStable, result.RequeueAfter)
			updated := store.statusWriter.captured.(*privatev1.NodePool)
			require.Nil(t, updated.Status.VersionResolution)
			cond := meta.FindStatusCondition(updated.Status.Conditions, "NodePoolVersionResolved")
			require.Equal(t, metav1.ConditionFalse, cond.Status)
			require.Equal(t, tc.reason, cond.Reason)
			require.Contains(t, cond.Message, "4.15.0")
		})
	}
}

func TestReconciler_CatalogUnavailable_StatusUpdateError(t *testing.T) {
	np := makeNP("cluster-1", "np-1", "4.15.0")
	r, _ := buildReconciler(t, np, makeCluster("cluster-1"), nil, nil, nil, fmt.Errorf("api busy"))
	_, err := r.Reconcile(context.Background(), npReq("cluster-1", "np-1"))
	require.ErrorContains(t, err, "update nodepool status")
}

func TestReconciler_ResolvesVersion_WritesStatusAndRequeues(t *testing.T) {
	np := makeNP("cluster-1", "np-1", "4.15.0")
	np.Generation = 3
	release := catalogVersion("4.15.0", "quay.io/ocp@sha256:original", "candidate")
	r, store := buildReconciler(t, np, makeCluster("cluster-1"), release, nil, nil, nil)
	result, err := r.Reconcile(context.Background(), npReq("cluster-1", "np-1"))
	require.NoError(t, err)
	require.Equal(t, requeueStable, result.RequeueAfter)
	updated := store.statusWriter.captured.(*privatev1.NodePool)
	require.Equal(t, &privatev1.VersionResolutionResult{ReleaseImage: release.Spec.ReleaseImage,
		ReleaseVersion: release.Name, ChannelGroup: "candidate", CincinnatiChannel: "candidate-4.15"}, updated.Status.VersionResolution)
	cond := meta.FindStatusCondition(updated.Status.Conditions, "NodePoolVersionResolved")
	require.Equal(t, metav1.ConditionTrue, cond.Status)
	require.Equal(t, int64(3), cond.ObservedGeneration)
}

func TestReconciler_ResolvesVersion_StatusUpdateConflict_ReturnsNoError(t *testing.T) {
	np := makeNP("cluster-1", "np-1", "4.15.0")
	release := catalogVersion("4.15.0", "image", "candidate")
	r, store := buildReconciler(t, np, makeCluster("cluster-1"), release, nil, nil, conflictErr())
	result, err := r.Reconcile(context.Background(), npReq("cluster-1", "np-1"))
	require.NoError(t, err)
	require.Equal(t, reconcile.Result{}, result)
	require.True(t, store.statusWriter.called)
}

func TestReconciler_ResolvesVersion_StatusUpdateError(t *testing.T) {
	np := makeNP("cluster-1", "np-1", "4.15.0")
	release := catalogVersion("4.15.0", "image", "candidate")
	r, _ := buildReconciler(t, np, makeCluster("cluster-1"), release, nil, nil, fmt.Errorf("disk full"))
	_, err := r.Reconcile(context.Background(), npReq("cluster-1", "np-1"))
	require.ErrorContains(t, err, "update nodepool status")
}

func TestReconciler_CatalogChangesPreservePinnedImage(t *testing.T) {
	ctx := context.Background()
	np := makeNP("cluster-1", "np-1", "4.15.0")
	cluster := makeCluster("cluster-1")
	cluster.Namespace = np.Namespace
	release := catalogVersion("4.15.0", "original", "candidate")
	apiClient := fake.NewClientBuilder().WithScheme(setup.NewScheme()).WithStatusSubresource(np).
		WithObjects(np, cluster, release).Build()
	r := NewReconciler(newTestLogger(t), apiClient)
	_, err := r.Reconcile(ctx, npReq(np.Namespace, np.Name))
	require.NoError(t, err)
	release.Spec.ReleaseImage = "changed"
	release.Spec.ChannelGroups = []string{"stable"}
	require.NoError(t, apiClient.Update(ctx, release))
	_, err = r.Reconcile(ctx, npReq(np.Namespace, np.Name))
	require.NoError(t, err)
	require.NoError(t, apiClient.Get(ctx, client.ObjectKeyFromObject(np), np))
	require.Equal(t, "original", np.Status.VersionResolution.ReleaseImage)
	require.NoError(t, apiClient.Delete(ctx, release))
	_, err = r.Reconcile(ctx, npReq(np.Namespace, np.Name))
	require.NoError(t, err)
	require.NoError(t, apiClient.Get(ctx, client.ObjectKeyFromObject(np), np))
	require.Equal(t, "original", np.Status.VersionResolution.ReleaseImage)
}

func TestReconciler_VersionEventResolvesPendingNodePool(t *testing.T) {
	ctx := context.Background()
	np := makeNP("cluster-1", "np-1", "5.0.0-ec.6")
	np.Spec.Release.ChannelGroup = "dev-preview"
	cluster := makeCluster("cluster-1")
	cluster.Namespace = np.Namespace
	// A resolved parent must not act as a fallback for a missing catalog entry.
	cluster.Status.VersionResolution = &privatev1.VersionResolutionResult{ReleaseVersion: "5.0.0-ec.6", ReleaseImage: "parent-image"}
	apiClient := fake.NewClientBuilder().WithScheme(setup.NewScheme()).WithStatusSubresource(np).
		WithObjects(np, cluster).WithIndex(&privatev1.NodePool{}, catalog.ReleaseVersionField, catalog.ReleaseVersion).Build()
	r := NewReconciler(newTestLogger(t), apiClient)
	_, err := r.Reconcile(ctx, npReq(np.Namespace, np.Name))
	require.NoError(t, err)
	require.NoError(t, apiClient.Get(ctx, client.ObjectKeyFromObject(np), np))
	require.Nil(t, np.Status.VersionResolution)
	release := catalogVersion("5.0.0-ec.6", "ci-image", "dev-preview")
	require.NoError(t, apiClient.Create(ctx, release))
	requests := catalog.ToNodePools(apiClient, newTestLogger(t))(ctx, release)
	require.Equal(t, []reconcile.Request{npReq(np.Namespace, np.Name)}, requests)
	_, err = r.Reconcile(ctx, requests[0])
	require.NoError(t, err)
	require.NoError(t, apiClient.Get(ctx, client.ObjectKeyFromObject(np), np))
	require.Equal(t, "ci-image", np.Status.VersionResolution.ReleaseImage)
	require.True(t, meta.IsStatusConditionTrue(np.Status.Conditions, "NodePoolVersionResolved"))
}

// ─── buildChannel ─────────────────────────────────────────────────────────────

func TestBuildChannel(t *testing.T) {
	cases := []struct {
		version      string
		channelGroup string
		want         string
		wantErr      bool
	}{
		{"4.22.0-ec.4", "candidate", "candidate-4.22", false},
		{"4.16.3", "candidate", "candidate-4.16", false},
		{"4.15.0", "candidate", "candidate-4.15", false},
		{"4", "candidate", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.version, func(t *testing.T) {
			got, err := buildChannel(tc.version, tc.channelGroup)
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.want, got)
			}
		})
	}
}

// ─── conditions helper (smoke test via reconciler) ────────────────────────────

func TestSetCondition(t *testing.T) {
	t.Run("appends new condition", func(t *testing.T) {
		var conds []metav1.Condition
		meta.SetStatusCondition(&conds, metav1.Condition{Type: "Applied", Status: metav1.ConditionTrue, Reason: "Test"})
		require.Len(t, conds, 1)
		require.Equal(t, "Applied", conds[0].Type)
		require.Equal(t, metav1.ConditionTrue, conds[0].Status)
		require.False(t, conds[0].LastTransitionTime.IsZero())
	})

	t.Run("preserves LastTransitionTime on same status", func(t *testing.T) {
		ts := metav1.Now()
		conds := []metav1.Condition{
			{Type: "Applied", Status: metav1.ConditionTrue, LastTransitionTime: ts, Reason: "Test"},
		}
		meta.SetStatusCondition(&conds, metav1.Condition{Type: "Applied", Status: metav1.ConditionTrue, Reason: "Test"})
		require.Len(t, conds, 1)
		require.Equal(t, ts, conds[0].LastTransitionTime)
	})

	t.Run("updates LastTransitionTime on status change", func(t *testing.T) {
		old := metav1.Now()
		conds := []metav1.Condition{
			{Type: "Applied", Status: metav1.ConditionFalse, LastTransitionTime: old, Reason: "Test"},
		}
		meta.SetStatusCondition(&conds, metav1.Condition{Type: "Applied", Status: metav1.ConditionTrue, Reason: "Test"})
		require.Len(t, conds, 1)
		require.False(t, conds[0].LastTransitionTime.IsZero())
	})
}
