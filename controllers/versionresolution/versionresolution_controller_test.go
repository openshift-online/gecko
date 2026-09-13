package versionresolution

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	privatev1 "github.com/openshift-online/gecko/platform-api/api/private/v1"

	"github.com/openshift-online/gecko/controllers/util/logger"
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
	cluster   *privatev1.Cluster
}

func (m *mockStatusWriter) Update(_ context.Context, obj client.Object, _ ...client.SubResourceUpdateOption) error {
	m.called = true
	if cluster, ok := obj.(*privatev1.Cluster); ok {
		m.cluster = cluster.DeepCopy()
	}
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

// mockStoreClient is a minimal client.Client backed by a fixed Cluster.
type mockStoreClient struct {
	cluster      *privatev1.Cluster
	getErr       error
	updateCalled bool
	statusWriter *mockStatusWriter
}

func (m *mockStoreClient) Get(_ context.Context, _ client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
	if m.getErr != nil {
		return m.getErr
	}
	if m.cluster == nil {
		return apierrors.NewNotFound(schema.GroupResource{Resource: "cluster"}, "")
	}
	c, ok := obj.(*privatev1.Cluster)
	if !ok {
		return fmt.Errorf("unexpected type %T", obj)
	}
	*c = *m.cluster
	return nil
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

// clusterReq returns a reconcile.Request for the given cluster name.
func clusterReq(name string) reconcile.Request {
	return reconcile.Request{
		NamespacedName: client.ObjectKey{Namespace: "hyperfleet", Name: name},
	}
}

// buildReconciler wires up a Reconciler backed by the store client and Cincinnati mock.
func buildReconciler(
	t *testing.T,
	cluster *privatev1.Cluster,
	release *ReleaseInfo,
) (*Reconciler, *mockStoreClient) {
	t.Helper()
	storeClient := &mockStoreClient{cluster: cluster}
	releases := []ReleaseInfo(nil)
	if release != nil {
		releases = append(releases, *release)
	}
	service, err := NewVersionService(&fakeReleaseSource{releases: releases}, "4.0.0")
	require.NoError(t, err)
	return NewReconciler(service, "4.24.1", newTestLogger(t), storeClient), storeClient
}

// ---- tests ------------------------------------------------------------------

func TestReconciler_HappyPath(t *testing.T) {
	release := &ReleaseInfo{
		Version: "4.22.0-ec.4",
		Payload: "quay.io/openshift-release-dev/ocp-release:4.22.0-ec.4-x86_64",
	}
	cluster := &privatev1.Cluster{}
	cluster.SetName("cluster-1")
	cluster.SetNamespace("hyperfleet")
	cluster.SetGeneration(3)
	cluster.Spec.Release = privatev1.ReleaseSpec{Version: "4.22.0-ec.4"}

	r, storeClient := buildReconciler(t, cluster, release)

	result, err := r.Reconcile(context.Background(), clusterReq("cluster-1"))

	require.NoError(t, err)
	require.Equal(t, reconcile.Result{RequeueAfter: requeueStable}, result)
	require.False(t, storeClient.updateCalled, "expected no spec Update (result written to status)")
	require.NotNil(t, storeClient.statusWriter)
	require.True(t, storeClient.statusWriter.called, "expected Status().Update to be called")
	resolved := storeClient.statusWriter.cluster.Status.VersionResolution
	require.NotNil(t, resolved)
	require.Equal(t, "4.22.0-ec.4", resolved.ReleaseVersion)
	require.Equal(t, "quay.io/openshift-release-dev/ocp-release:4.22.0-ec.4-x86_64", resolved.ReleaseImage)
	require.Equal(t, "4.24.1", resolved.DefaultVersion)
	require.Equal(t, "4.22.0-ec.4", resolved.LatestVersion)
}

func TestReconciler_AlreadyResolved(t *testing.T) {
	release := &ReleaseInfo{
		Version: "4.22.0-ec.4",
		Payload: "quay.io/openshift-release-dev/ocp-release:4.22.0-ec.4-x86_64",
	}
	cluster := &privatev1.Cluster{}
	cluster.SetName("cluster-2")
	cluster.SetNamespace("hyperfleet")
	cluster.Spec.Release = privatev1.ReleaseSpec{Version: "4.22.0-ec.4"}
	cluster.Status.VersionResolution = &privatev1.VersionResolutionResult{
		ReleaseImage:      "quay.io/openshift-release-dev/ocp-release:4.22.0-ec.4-x86_64",
		ReleaseVersion:    "4.22.0-ec.4",
		DefaultVersion:    "4.24.1",
		LatestVersion:     "4.22.0-ec.4",
		CincinnatiChannel: "candidate-4.22",
		ChannelGroup:      "candidate",
	}
	meta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
		Type:               "VersionResolved",
		Status:             metav1.ConditionTrue,
		Reason:             "VersionResolved",
		Message:            "Version 4.22.0-ec.4 resolved to image quay.io/openshift-release-dev/ocp-release:4.22.0-ec.4-x86_64",
		ObservedGeneration: cluster.Generation,
	})

	r, storeClient := buildReconciler(t, cluster, release)

	result, err := r.Reconcile(context.Background(), clusterReq("cluster-2"))

	require.NoError(t, err)
	require.Equal(t, reconcile.Result{RequeueAfter: requeueStable}, result)
	require.False(t, storeClient.updateCalled, "expected no spec Update")
	require.Nil(t, storeClient.statusWriter, "expected no status update")
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
	storeClient := &mockStoreClient{cluster: cluster}
	r := NewReconciler(
		newTestVersionService(t, &fakeReleaseSource{}),
		"4.24.1",
		newTestLogger(t),
		storeClient,
	)

	result, err := r.Reconcile(context.Background(), clusterReq("cluster-3"))

	require.NoError(t, err)
	require.Equal(t, reconcile.Result{}, result)
	require.False(t, storeClient.updateCalled)
	require.NotNil(t, storeClient.statusWriter)
	condition := meta.FindStatusCondition(storeClient.statusWriter.cluster.Status.Conditions, "VersionResolved")
	require.NotNil(t, condition)
	require.Equal(t, metav1.ConditionFalse, condition.Status)
	require.Equal(t, "ReleaseVersionNotSet", condition.Reason)
}

func TestReconciler_RejectsVersionBeforeMinimum(t *testing.T) {
	cluster := &privatev1.Cluster{}
	cluster.SetName("cluster-unsupported-version")
	cluster.Spec.Release.Version = "4.23.9"
	storeClient := &mockStoreClient{cluster: cluster}
	r := NewReconciler(
		newTestVersionService(t, &fakeReleaseSource{}),
		"4.24.1",
		newTestLogger(t),
		storeClient,
	)

	result, err := r.Reconcile(context.Background(), clusterReq(cluster.Name))

	require.NoError(t, err)
	require.Equal(t, reconcile.Result{}, result)
	require.NotNil(t, storeClient.statusWriter)
	require.NotNil(t, storeClient.statusWriter.cluster)
	condition := meta.FindStatusCondition(storeClient.statusWriter.cluster.Status.Conditions, "VersionResolved")
	require.NotNil(t, condition)
	require.Equal(t, metav1.ConditionFalse, condition.Status)
	require.Equal(t, "UnsupportedVersion", condition.Reason)
}

func TestReconciler_RejectsMalformedVersion(t *testing.T) {
	cluster := &privatev1.Cluster{}
	cluster.SetName("cluster-invalid-version")
	cluster.Spec.Release.Version = "not-a-version"
	source := &fakeReleaseSource{}
	storeClient := &mockStoreClient{cluster: cluster}
	r := NewReconciler(
		newTestVersionService(t, source),
		"4.24.1",
		newTestLogger(t),
		storeClient,
	)

	result, err := r.Reconcile(context.Background(), clusterReq(cluster.Name))

	require.NoError(t, err)
	require.Equal(t, reconcile.Result{}, result)
	require.Zero(t, source.callCount())
	require.NotNil(t, storeClient.statusWriter)
	condition := meta.FindStatusCondition(storeClient.statusWriter.cluster.Status.Conditions, "VersionResolved")
	require.NotNil(t, condition)
	require.Equal(t, metav1.ConditionFalse, condition.Status)
	require.Equal(t, "InvalidVersion", condition.Reason)
	require.Nil(t, storeClient.statusWriter.cluster.Status.VersionResolution)
}

func TestReconciler_EmptyCincinnatiResponse(t *testing.T) {
	// Cincinnati returns an empty graph (no matching node).
	cluster := &privatev1.Cluster{}
	cluster.SetName("cluster-5")
	cluster.SetNamespace("hyperfleet")
	cluster.Spec.Release = privatev1.ReleaseSpec{Version: "4.22.0-ec.4"}

	r, storeClient := buildReconciler(t, cluster, nil)

	result, err := r.Reconcile(context.Background(), clusterReq("cluster-5"))

	require.ErrorIs(t, err, ErrNoValidReleases)
	require.Equal(t, reconcile.Result{}, result)
	require.False(t, storeClient.updateCalled)
	require.NotNil(t, storeClient.statusWriter)
	condition := meta.FindStatusCondition(storeClient.statusWriter.cluster.Status.Conditions, "VersionResolved")
	require.NotNil(t, condition)
	require.Equal(t, metav1.ConditionUnknown, condition.Status)
	require.Equal(t, "CincinnatiDataInvalid", condition.Reason)
}

func TestReconciler_VersionNotFound(t *testing.T) {
	cluster := &privatev1.Cluster{}
	cluster.SetName("cluster-version-not-found")
	cluster.Spec.Release.Version = "4.24.2"
	cluster.Status.VersionResolution = &privatev1.VersionResolutionResult{ReleaseVersion: "4.24.1"}
	storeClient := &mockStoreClient{cluster: cluster}
	source := &fakeReleaseSource{releases: []ReleaseInfo{{Version: "4.24.1", Payload: "image-1"}}}
	r := NewReconciler(newTestVersionService(t, source), "4.24.1", newTestLogger(t), storeClient)

	result, err := r.Reconcile(context.Background(), clusterReq(cluster.Name))

	require.NoError(t, err)
	require.Equal(t, reconcile.Result{}, result)
	condition := meta.FindStatusCondition(storeClient.statusWriter.cluster.Status.Conditions, "VersionResolved")
	require.NotNil(t, condition)
	require.Equal(t, metav1.ConditionFalse, condition.Status)
	require.Equal(t, "VersionNotFoundInCincinnati", condition.Reason)
	require.Nil(t, storeClient.statusWriter.cluster.Status.VersionResolution)
}

func TestReconciler_ReleaseWithEmptyPayloadIsNotResolved(t *testing.T) {
	cluster := &privatev1.Cluster{}
	cluster.SetName("cluster-empty-release-payload")
	cluster.Spec.Release.Version = "4.24.2"
	storeClient := &mockStoreClient{cluster: cluster}
	source := &fakeReleaseSource{releases: []ReleaseInfo{{Version: "4.24.2"}}}
	r := NewReconciler(newTestVersionService(t, source), "4.24.1", newTestLogger(t), storeClient)

	result, err := r.Reconcile(context.Background(), clusterReq(cluster.Name))

	require.NoError(t, err)
	require.Equal(t, reconcile.Result{}, result)
	condition := meta.FindStatusCondition(storeClient.statusWriter.cluster.Status.Conditions, "VersionResolved")
	require.NotNil(t, condition)
	require.Equal(t, metav1.ConditionFalse, condition.Status)
	require.Equal(t, "VersionNotFoundInCincinnati", condition.Reason)
	require.Nil(t, storeClient.statusWriter.cluster.Status.VersionResolution)
}

func TestReconciler_CincinnatiUnavailable(t *testing.T) {
	cluster := &privatev1.Cluster{}
	cluster.SetName("cluster-cincinnati-unavailable")
	cluster.Spec.Release.Version = "4.24.1"
	storeClient := &mockStoreClient{cluster: cluster}
	source := &fakeReleaseSource{err: errors.New("request timed out")}
	r := NewReconciler(newTestVersionService(t, source), "4.24.1", newTestLogger(t), storeClient)

	result, err := r.Reconcile(context.Background(), clusterReq(cluster.Name))

	require.ErrorContains(t, err, "request timed out")
	require.Equal(t, reconcile.Result{}, result)
	condition := meta.FindStatusCondition(storeClient.statusWriter.cluster.Status.Conditions, "VersionResolved")
	require.NotNil(t, condition)
	require.Equal(t, metav1.ConditionUnknown, condition.Status)
	require.Equal(t, "CincinnatiUnavailable", condition.Reason)
}

func TestReconciler_MalformedCincinnatiResponse(t *testing.T) {
	cluster := &privatev1.Cluster{}
	cluster.SetName("cluster-cincinnati-malformed")
	cluster.Spec.Release.Version = "4.24.1"
	storeClient := &mockStoreClient{cluster: cluster}
	source := &fakeReleaseSource{err: errors.New("cincinnati: unmarshal response: invalid character")}
	r := NewReconciler(newTestVersionService(t, source), "4.24.1", newTestLogger(t), storeClient)

	result, err := r.Reconcile(context.Background(), clusterReq(cluster.Name))

	require.ErrorContains(t, err, "unmarshal response")
	require.Equal(t, reconcile.Result{}, result)
	condition := meta.FindStatusCondition(storeClient.statusWriter.cluster.Status.Conditions, "VersionResolved")
	require.NotNil(t, condition)
	require.Equal(t, metav1.ConditionUnknown, condition.Status)
	require.Equal(t, "CincinnatiUnavailable", condition.Reason)
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
