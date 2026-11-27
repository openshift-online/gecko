package hc_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/openshift-online/gecko/controllers/client/transport"
	"github.com/openshift-online/gecko/controllers/client/transport/mock"
	"github.com/openshift-online/gecko/controllers/hc"
	"github.com/openshift-online/gecko/controllers/util/constants"
	privatev1 "github.com/openshift-online/gecko/platform-api/api/private/v1"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const spannerMCName = "mc-cluster-1"

func spannerEtcdResourceKey(cluster *privatev1.Cluster) string {
	return fmt.Sprintf("etcd.gcp.managed.openshift.io/v1alpha1/spanneretcds/clusters-%s/%s", cluster.UID, cluster.Spec.SafeName)
}

// spannerReconciler wires a reconciler with the socketcd backend enabled or
// disabled, returning the transport mock that receives its Apply/Observe calls.
func spannerReconciler(t *testing.T, cluster *privatev1.Cluster, enabled bool, etcdFeedback map[string]string, hcAvailable string) (*hc.Reconciler, *mockStoreClient, *mock.Client) {
	t.Helper()
	groupKey := mustClusterGroupKey("hyperfleet", cluster.Name)

	statuses := map[string]map[string]string{
		hostedClusterResourceKey(cluster): {"availableCondition": hcAvailable},
	}
	if etcdFeedback != nil {
		statuses[spannerEtcdResourceKey(cluster)] = etcdFeedback
	}
	tr := mock.New()
	tr.StatusOverrides[spannerMCName+"/"+groupKey] = &transport.Status{
		Conditions:       []metav1.Condition{{Type: "Applied", Status: metav1.ConditionTrue, Reason: "AppliedSuccessfully"}},
		ResourceStatuses: statuses,
	}

	store := &mockStoreClient{cluster: cluster, statusWriter: &mockStatusWriter{}}
	return hc.New(tr, testLogger(t), store, nil, "", "", hc.WithSpannerEtcd(enabled)), store, tr
}

func hostedClusterManifest(t *testing.T, tr *mock.Client) map[string]any {
	t.Helper()
	require.Len(t, tr.ApplyCalls, 1)
	var obj map[string]any
	require.NoError(t, json.Unmarshal(tr.ApplyCalls[0].Manifests[3], &obj))
	return obj
}

func updatedCluster(t *testing.T, store *mockStoreClient) *privatev1.Cluster {
	t.Helper()
	require.True(t, store.statusWriter.called, "expected Status().Update to be called")
	return store.statusWriter.captured.(*privatev1.Cluster)
}

func TestReconcile_SpannerEtcd_CreatesUnmanagedEtcdAndObservesBackend(t *testing.T) {
	cluster := buildReadyCluster("cluster-spanner", "4.15.0")
	r, _, tr := spannerReconciler(t, cluster, true, map[string]string{"readyCondition": "True"}, "True")

	_, err := r.Reconcile(context.Background(), clusterReq(cluster.Name))
	require.NoError(t, err)

	obj := hostedClusterManifest(t, tr)
	annotations := obj["metadata"].(map[string]any)["annotations"].(map[string]any)
	require.Equal(t, "true", annotations["gcp.managed.openshift.io/spanner-enabled"])
	etcd := obj["spec"].(map[string]any)["etcd"].(map[string]any)
	require.Equal(t, "Unmanaged", etcd["managementType"])

	require.Len(t, tr.ObserveCalls, 1)
	require.Equal(t, spannerMCName, tr.ObserveCalls[0].TargetCluster)
	require.Equal(t, tr.ApplyCalls[0].GroupKey, tr.ObserveCalls[0].GroupKey)
	require.Equal(t, []transport.ResourceRef{{
		Group:     "etcd.gcp.managed.openshift.io",
		Version:   "v1alpha1",
		Resource:  "spanneretcds",
		Namespace: fmt.Sprintf("clusters-%s", cluster.UID),
		Name:      cluster.Spec.SafeName,
	}}, tr.ObserveCalls[0].Refs, "the SpannerEtcd shares the HostedCluster's namespace and name")
}

func TestReconcile_SpannerEtcd_DisabledKeepsManagedEtcd(t *testing.T) {
	cluster := buildReadyCluster("cluster-managed", "4.15.0")
	r, store, tr := spannerReconciler(t, cluster, false, nil, "True")

	result, err := r.Reconcile(context.Background(), clusterReq(cluster.Name))
	require.NoError(t, err)
	require.Equal(t, 5*time.Minute, result.RequeueAfter)

	obj := hostedClusterManifest(t, tr)
	annotations := obj["metadata"].(map[string]any)["annotations"].(map[string]any)
	require.NotContains(t, annotations, "gcp.managed.openshift.io/spanner-enabled")
	require.Equal(t, "Managed", obj["spec"].(map[string]any)["etcd"].(map[string]any)["managementType"])

	require.Empty(t, tr.ObserveCalls)
	require.Nil(t, meta.FindStatusCondition(updatedCluster(t, store).Status.Conditions, "EtcdBackendReady"))
}

func TestReconcile_SpannerEtcd_BackendStatus(t *testing.T) {
	for _, tc := range []struct {
		name             string
		feedback         map[string]string
		wantBackend      metav1.ConditionStatus
		wantBackendWhy   string
		wantAvailable    metav1.ConditionStatus
		wantAvailableWhy string
		wantBackendMsg   string
		wantAvailableMsg string
		wantRequeue      time.Duration
	}{
		{
			name:             "backend ready",
			feedback:         map[string]string{"readyCondition": "True"},
			wantBackend:      metav1.ConditionTrue,
			wantBackendWhy:   "BackendReady",
			wantAvailable:    metav1.ConditionTrue,
			wantAvailableWhy: "HostedClusterAvailable",
			wantBackendMsg:   "The Spanner etcd backend is ready",
			wantRequeue:      5 * time.Minute,
		},
		{
			name: "backend failure is reported and keeps the cluster unavailable",
			feedback: map[string]string{
				"readyCondition":        "False",
				"readyConditionReason":  "WaitingForBackend",
				"readyConditionMessage": "BackendReady: Spanner instance not ready",
			},
			wantBackend:      metav1.ConditionFalse,
			wantBackendWhy:   "WaitingForBackend",
			wantAvailable:    metav1.ConditionFalse,
			wantAvailableWhy: "EtcdBackendNotReady",
			wantBackendMsg:   "BackendReady: Spanner instance not ready",
			wantAvailableMsg: "The etcd backend of the cluster is not ready",
			wantRequeue:      15 * time.Second,
		},
		{
			name:             "failure without a reason still reports a reason",
			feedback:         map[string]string{"readyCondition": "False"},
			wantBackend:      metav1.ConditionFalse,
			wantBackendWhy:   "BackendNotReady",
			wantAvailable:    metav1.ConditionFalse,
			wantAvailableWhy: "EtcdBackendNotReady",
			wantAvailableMsg: "The etcd backend of the cluster is not ready",
			wantRequeue:      15 * time.Second,
		},
		{
			name: "a failure that reuses the not-reported reason still blocks",
			feedback: map[string]string{
				"readyCondition":        "False",
				"readyConditionReason":  "BackendNotReported",
				"readyConditionMessage": "reported by socketcd",
			},
			wantBackend:      metav1.ConditionFalse,
			wantBackendWhy:   "BackendNotReported",
			wantAvailable:    metav1.ConditionFalse,
			wantAvailableWhy: "EtcdBackendNotReady",
			wantBackendMsg:   "reported by socketcd",
			wantAvailableMsg: "The etcd backend of the cluster is not ready",
			wantRequeue:      15 * time.Second,
		},
		{
			name: "reported unknown is not a failure and does not gate availability",
			feedback: map[string]string{
				"readyCondition":        "Unknown",
				"readyConditionReason":  "Provisioning",
				"readyConditionMessage": "Spanner instance is being created",
			},
			wantBackend:      metav1.ConditionUnknown,
			wantBackendWhy:   "Provisioning",
			wantBackendMsg:   "Spanner instance is being created",
			wantAvailable:    metav1.ConditionTrue,
			wantAvailableWhy: "HostedClusterAvailable",
			wantRequeue:      5 * time.Minute,
		},
		{
			name:             "backend not reported yet is unknown and does not gate availability",
			feedback:         map[string]string{},
			wantBackend:      metav1.ConditionUnknown,
			wantBackendWhy:   "BackendNotReported",
			wantAvailable:    metav1.ConditionTrue,
			wantAvailableWhy: "HostedClusterAvailable",
			wantBackendMsg:   "socketcd has not reported the Spanner etcd backend status yet",
			wantRequeue:      5 * time.Minute,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cluster := buildReadyCluster("cluster-spanner", "4.15.0")
			r, store, _ := spannerReconciler(t, cluster, true, tc.feedback, "True")

			result, err := r.Reconcile(context.Background(), clusterReq(cluster.Name))
			require.NoError(t, err)
			require.Equal(t, tc.wantRequeue, result.RequeueAfter)

			conditions := updatedCluster(t, store).Status.Conditions
			backend := meta.FindStatusCondition(conditions, "EtcdBackendReady")
			require.NotNil(t, backend)
			require.Equal(t, tc.wantBackend, backend.Status)
			require.Equal(t, tc.wantBackendWhy, backend.Reason)
			require.Equal(t, tc.wantBackendMsg, backend.Message)

			available := meta.FindStatusCondition(conditions, "HostedClusterAvailable")
			require.NotNil(t, available)
			require.Equal(t, tc.wantAvailable, available.Status)
			require.Equal(t, tc.wantAvailableWhy, available.Reason)
			require.Equal(t, tc.wantAvailableMsg, available.Message)
		})
	}
}

func TestReconcile_SpannerEtcd_UnavailableHostedClusterStaysUnavailable(t *testing.T) {
	cluster := buildReadyCluster("cluster-spanner", "4.15.0")
	r, store, _ := spannerReconciler(t, cluster, true, map[string]string{"readyCondition": "True"}, "False")

	_, err := r.Reconcile(context.Background(), clusterReq(cluster.Name))
	require.NoError(t, err)

	conditions := updatedCluster(t, store).Status.Conditions
	require.Equal(t, metav1.ConditionTrue, meta.FindStatusCondition(conditions, "EtcdBackendReady").Status)
	available := meta.FindStatusCondition(conditions, "HostedClusterAvailable")
	require.Equal(t, metav1.ConditionFalse, available.Status, "a ready backend does not make the cluster available")
	require.Equal(t, "HostedClusterNotAvailable", available.Reason)
}

func TestReconcile_SpannerEtcd_ReconcileIsIdempotent(t *testing.T) {
	cluster := buildReadyCluster("cluster-spanner", "4.15.0")
	r, store, tr := spannerReconciler(t, cluster, true, map[string]string{
		"readyCondition": "False", "readyConditionReason": "WaitingForBackend", "readyConditionMessage": "waiting",
	}, "True")

	_, err := r.Reconcile(context.Background(), clusterReq(cluster.Name))
	require.NoError(t, err)
	require.Equal(t, 1, store.statusWriter.updates)

	store.cluster = updatedCluster(t, store)
	_, err = r.Reconcile(context.Background(), clusterReq(cluster.Name))
	require.NoError(t, err)
	require.Equal(t, 1, store.statusWriter.updates, "unchanged backend status must not rewrite the cluster status")
	require.Len(t, tr.ObserveCalls, 2, "the read-only target is re-registered every reconcile")
}

func TestReconcile_SpannerEtcd_DisablingRemovesBackendCondition(t *testing.T) {
	cluster := buildReadyCluster("cluster-spanner", "4.15.0")
	cluster.Status.Conditions = []metav1.Condition{{
		Type: "EtcdBackendReady", Status: metav1.ConditionFalse, Reason: "WaitingForBackend", ObservedGeneration: 2,
	}}
	r, store, tr := spannerReconciler(t, cluster, false, nil, "True")

	_, err := r.Reconcile(context.Background(), clusterReq(cluster.Name))
	require.NoError(t, err)

	require.Nil(t, meta.FindStatusCondition(updatedCluster(t, store).Status.Conditions, "EtcdBackendReady"))
	require.Empty(t, tr.ObserveCalls)
}

func TestReconcile_SpannerEtcd_ObserveErrorStopsBeforeApply(t *testing.T) {
	cluster := buildReadyCluster("cluster-spanner", "4.15.0")
	r, _, tr := spannerReconciler(t, cluster, true, nil, "True")
	tr.ObserveErr = fmt.Errorf("firestore unavailable")

	_, err := r.Reconcile(context.Background(), clusterReq(cluster.Name))
	require.ErrorContains(t, err, "observe spanner etcd")
	require.Empty(t, tr.ApplyCalls)
}

func deletingCluster(name string, resourcesApplied bool) *privatev1.Cluster {
	cluster := buildReadyCluster(name, "4.15.0")
	if resourcesApplied {
		cluster.Status.Conditions = append(cluster.Status.Conditions, metav1.Condition{
			Type: "ResourcesApplied", Status: metav1.ConditionTrue, Reason: "Applied",
		})
	}
	now := metav1.Now()
	cluster.SetDeletionTimestamp(&now)
	return cluster
}

func TestReconcile_Deletion_ReleasesObservedTargetsWhenNothingWasApplied(t *testing.T) {
	cluster := deletingCluster("cluster-spanner", false)
	groupKey := mustClusterGroupKey("hyperfleet", cluster.Name)
	tr := mock.New()
	r, store := buildReconciler(t, cluster, nil, tr, nil)

	result, err := r.Reconcile(context.Background(), clusterReq(cluster.Name))
	require.NoError(t, err)
	require.Zero(t, result.RequeueAfter)

	require.Empty(t, tr.DeleteCalls, "nothing was applied, so there is nothing to delete")
	require.Equal(t, []mock.ReleaseObservedCall{{TargetCluster: spannerMCName, GroupKey: groupKey}}, tr.ReleaseObservedCalls)
	require.True(t, store.updateCalled, "expected Update to remove finalizer")
	require.NotContains(t, store.updated.(*privatev1.Cluster).Finalizers, constants.FinalizerCluster)
}

func TestReconcile_Deletion_ReleasesObservedTargetsOnlyAfterResourcesAreGone(t *testing.T) {
	cluster := deletingCluster("cluster-spanner", true)
	groupKey := mustClusterGroupKey("hyperfleet", cluster.Name)
	key := spannerMCName + "/" + groupKey
	tr := mock.New()
	r, store := buildReconciler(t, cluster, nil, tr, nil)

	tr.DeleteStatusOverrides[key] = &transport.DeleteStatus{ApplyDesiresCount: 2}
	result, err := r.Reconcile(context.Background(), clusterReq(cluster.Name))
	require.NoError(t, err)
	require.Equal(t, 15*time.Second, result.RequeueAfter)
	require.Len(t, tr.DeleteCalls, 1)
	require.Empty(t, tr.ReleaseObservedCalls, "targets stay while resources are being deleted")

	tr.DeleteStatusOverrides[key] = &transport.DeleteStatus{PendingCount: 1, TotalCount: 2}
	result, err = r.Reconcile(context.Background(), clusterReq(cluster.Name))
	require.NoError(t, err)
	require.Equal(t, 15*time.Second, result.RequeueAfter)
	require.Empty(t, tr.ReleaseObservedCalls)

	tr.DeleteStatusOverrides[key] = &transport.DeleteStatus{AllSuccessful: true, TotalCount: 2}
	result, err = r.Reconcile(context.Background(), clusterReq(cluster.Name))
	require.NoError(t, err)
	require.Zero(t, result.RequeueAfter)
	require.Len(t, tr.CleanupDeleteDesiresCalls, 1)
	require.Len(t, tr.ReleaseObservedCalls, 1)
	require.True(t, store.updateCalled, "expected Update to remove finalizer")
}

func TestReconcile_Deletion_KeepsFinalizerWhenReleasingObservedTargetsFails(t *testing.T) {
	cluster := deletingCluster("cluster-spanner", false)
	tr := mock.New()
	tr.ReleaseObservedErr = fmt.Errorf("firestore unavailable")
	r, store := buildReconciler(t, cluster, nil, tr, nil)

	_, err := r.Reconcile(context.Background(), clusterReq(cluster.Name))
	require.ErrorContains(t, err, "release observed resources")
	require.False(t, store.updateCalled, "the finalizer must stay so the release is retried")

	tr.ReleaseObservedErr = nil
	_, err = r.Reconcile(context.Background(), clusterReq(cluster.Name))
	require.NoError(t, err)
	require.True(t, store.updateCalled, "the release is retried and the finalizer removed")
}

func TestReconcile_Deletion_DoesNotReleaseWithoutPlacement(t *testing.T) {
	cluster := deletingCluster("cluster-unplaced", false)
	cluster.Status.PlacementResult = nil
	tr := mock.New()
	r, store := buildReconciler(t, cluster, nil, tr, nil)

	_, err := r.Reconcile(context.Background(), clusterReq(cluster.Name))
	require.NoError(t, err)
	require.Empty(t, tr.ReleaseObservedCalls, "no management cluster means nothing was registered")
	require.True(t, store.updateCalled)
}

func TestReconcile_SpannerEtcd_StaleStatusIsNotWritten(t *testing.T) {
	cluster := buildReadyCluster("cluster-spanner", "4.15.0")
	groupKey := mustClusterGroupKey("hyperfleet", cluster.Name)
	tr := mock.New()
	tr.StatusOverrides[spannerMCName+"/"+groupKey] = &transport.Status{
		Conditions: []metav1.Condition{{Type: "Applied", Status: metav1.ConditionTrue, Reason: "AppliedSuccessfully"}},
		Stale:      true,
	}
	store := &mockStoreClient{cluster: cluster, statusWriter: &mockStatusWriter{}}
	r := hc.New(tr, testLogger(t), store, nil, "", "", hc.WithSpannerEtcd(true))

	result, err := r.Reconcile(context.Background(), clusterReq(cluster.Name))
	require.NoError(t, err)
	require.Equal(t, 15*time.Second, result.RequeueAfter)
	require.Len(t, tr.ObserveCalls, 1)
	require.Nil(t, store.statusWriter.captured, "no condition, including EtcdBackendReady, is written from stale status")
}

func TestReconcile_SpannerEtcd_NoStatusYetReportsBackendNotFound(t *testing.T) {
	cluster := buildReadyCluster("cluster-spanner", "4.15.0")
	// Apply returns a nil status to simulate resources that have no status yet,
	// and nothing was dispatched before this reconcile.
	store := &mockStoreClient{cluster: cluster, statusWriter: &mockStatusWriter{}}
	tr := &errTransport{deleteStatus: &transport.DeleteStatus{AllSuccessful: true}}
	r := hc.New(tr, testLogger(t), store, nil, "", "", hc.WithSpannerEtcd(true))

	result, err := r.Reconcile(context.Background(), clusterReq(cluster.Name))
	require.NoError(t, err)
	require.Equal(t, 15*time.Second, result.RequeueAfter)

	backend := meta.FindStatusCondition(updatedCluster(t, store).Status.Conditions, "EtcdBackendReady")
	require.NotNil(t, backend)
	require.Equal(t, metav1.ConditionFalse, backend.Status)
	require.Equal(t, "ResourcesNotFound", backend.Reason)
}

func TestReconcile_SpannerEtcd_BackendDetailsStayOffThePublicCondition(t *testing.T) {
	const internal = "ApplyFailed: spanneretcds is forbidden: project central-spanner-prod"
	cluster := buildReadyCluster("cluster-spanner", "4.15.0")
	r, store, _ := spannerReconciler(t, cluster, true, map[string]string{
		"readyCondition":        "False",
		"readyConditionReason":  "ApplyFailed",
		"readyConditionMessage": internal,
	}, "True")

	_, err := r.Reconcile(context.Background(), clusterReq(cluster.Name))
	require.NoError(t, err)

	conditions := updatedCluster(t, store).Status.Conditions
	backend := meta.FindStatusCondition(conditions, "EtcdBackendReady")
	require.Equal(t, internal, backend.Message, "operators get socketcd's detail on the private condition")
	require.Equal(t, "ApplyFailed", backend.Reason)

	available := meta.FindStatusCondition(conditions, "HostedClusterAvailable")
	require.Equal(t, metav1.ConditionFalse, available.Status)
	require.NotContains(t, available.Message, "central-spanner-prod", "HostedClusterAvailable is public")
	require.NotContains(t, available.Reason, "ApplyFailed")
}
