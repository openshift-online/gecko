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

func etcdType(t *testing.T, tr *mock.Client) string {
	t.Helper()
	require.Len(t, tr.ApplyCalls, 1)
	return appliedEtcdType(t, tr.ApplyCalls[0])
}

func appliedEtcdType(t *testing.T, call mock.ApplyCall) string {
	t.Helper()
	var obj map[string]any
	require.NoError(t, json.Unmarshal(call.Manifests[3], &obj))
	return obj["spec"].(map[string]any)["etcd"].(map[string]any)["managementType"].(string)
}

func recordedBackend(t *testing.T, store *mockStoreClient) string {
	t.Helper()
	require.True(t, store.updateCalled, "expected the selected backend to be recorded with an Update")
	return store.updated.(*privatev1.Cluster).Annotations[constants.AnnotationEtcdBackend]
}

func TestReconcile_EtcdBackend_SelectionIsRecordedOnFirstReconcile(t *testing.T) {
	for _, tc := range []struct {
		name         string
		flag         bool
		prepare      func(*privatev1.Cluster)
		wantBackend  string
		wantEtcdType string
		wantObserved bool
	}{
		{
			name:         "new cluster with the flag on uses Spanner",
			flag:         true,
			wantBackend:  constants.EtcdBackendSpanner,
			wantEtcdType: "Unmanaged",
			wantObserved: true,
		},
		{
			name:         "new cluster with the flag off uses Managed",
			flag:         false,
			wantBackend:  constants.EtcdBackendManaged,
			wantEtcdType: "Managed",
		},
		{
			name: "existing cluster whose HostedCluster was observed stays Managed with the flag on",
			flag: true,
			prepare: func(c *privatev1.Cluster) {
				c.Status.HostedClusterResult = &privatev1.HostedClusterResult{Version: "4.15.0"}
			},
			wantBackend:  constants.EtcdBackendManaged,
			wantEtcdType: "Managed",
		},
		{
			name: "existing cluster whose resources were applied stays Managed with the flag on",
			flag: true,
			prepare: func(c *privatev1.Cluster) {
				c.Status.Conditions = []metav1.Condition{{Type: "ResourcesApplied", Status: metav1.ConditionTrue, Reason: "Applied"}}
			},
			wantBackend:  constants.EtcdBackendManaged,
			wantEtcdType: "Managed",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cluster := buildReadyCluster("cluster-backend", "4.15.0")
			if tc.prepare != nil {
				tc.prepare(cluster)
			}
			r, store, tr := spannerReconciler(t, cluster, tc.flag, nil, "True")

			_, err := r.Reconcile(context.Background(), clusterReq(cluster.Name))
			require.NoError(t, err)

			require.Equal(t, tc.wantBackend, recordedBackend(t, store))
			require.Equal(t, tc.wantEtcdType, etcdType(t, tr))
			require.Equal(t, tc.wantObserved, len(tr.ObserveCalls) > 0)
		})
	}
}

func TestReconcile_EtcdBackend_RecordedBackendWinsOverTheFlag(t *testing.T) {
	for _, tc := range []struct {
		name         string
		flag         bool
		recorded     string
		wantEtcdType string
		wantObserved bool
	}{
		{"Spanner cluster keeps Spanner after the flag is turned off", false, constants.EtcdBackendSpanner, "Unmanaged", true},
		{"Spanner cluster keeps Spanner with the flag on", true, constants.EtcdBackendSpanner, "Unmanaged", true},
		{"Managed cluster keeps Managed after the flag is turned on", true, constants.EtcdBackendManaged, "Managed", false},
		{"Managed cluster keeps Managed with the flag off", false, constants.EtcdBackendManaged, "Managed", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cluster := buildReadyCluster("cluster-backend", "4.15.0")
			cluster.Annotations = map[string]string{constants.AnnotationEtcdBackend: tc.recorded}
			r, store, tr := spannerReconciler(t, cluster, tc.flag, map[string]string{"readyCondition": "True"}, "True")

			_, err := r.Reconcile(context.Background(), clusterReq(cluster.Name))
			require.NoError(t, err)

			require.False(t, store.updateCalled, "a recorded selection is never rewritten")
			require.Equal(t, tc.wantEtcdType, etcdType(t, tr))
			require.Equal(t, tc.wantObserved, len(tr.ObserveCalls) > 0)

			backend := meta.FindStatusCondition(updatedCluster(t, store).Status.Conditions, "EtcdBackendReady")
			if tc.wantObserved {
				require.NotNil(t, backend, "the backend is reported for a Spanner cluster whatever the flag says")
				require.Equal(t, metav1.ConditionTrue, backend.Status)
			} else {
				require.Nil(t, backend, "a Managed cluster never reports a Spanner backend")
			}
		})
	}
}

func TestReconcile_EtcdBackend_SpannerFailureStillGatesAvailabilityWithFlagOff(t *testing.T) {
	cluster := buildReadyCluster("cluster-backend", "4.15.0")
	cluster.Annotations = map[string]string{constants.AnnotationEtcdBackend: constants.EtcdBackendSpanner}
	r, store, _ := spannerReconciler(t, cluster, false, map[string]string{
		"readyCondition": "False", "readyConditionReason": "WaitingForBackend", "readyConditionMessage": "waiting",
	}, "True")

	_, err := r.Reconcile(context.Background(), clusterReq(cluster.Name))
	require.NoError(t, err)

	available := meta.FindStatusCondition(updatedCluster(t, store).Status.Conditions, "HostedClusterAvailable")
	require.Equal(t, metav1.ConditionFalse, available.Status)
	require.Equal(t, "WaitingForBackend", available.Reason)
}

func TestReconcile_EtcdBackend_FlagChangeDuringCreationDoesNotChangeTheBackend(t *testing.T) {
	cluster := buildReadyCluster("cluster-backend", "4.15.0")
	r, store, tr := spannerReconciler(t, cluster, true, nil, "False")

	_, err := r.Reconcile(context.Background(), clusterReq(cluster.Name))
	require.NoError(t, err)
	require.Equal(t, "Unmanaged", etcdType(t, tr))

	// The HostedCluster was applied but has not reported yet, so the Cluster
	// carries no status that would show it exists. The flag is turned off and
	// the controller restarts, retrying from the persisted Cluster.
	recorded := store.updated.(*privatev1.Cluster)
	require.Equal(t, constants.EtcdBackendSpanner, recorded.Annotations[constants.AnnotationEtcdBackend])
	restarted := buildReadyCluster("cluster-backend", "4.15.0")
	restarted.Annotations = recorded.Annotations

	r2, _, tr2 := spannerReconciler(t, restarted, false, nil, "False")
	_, err = r2.Reconcile(context.Background(), clusterReq(restarted.Name))
	require.NoError(t, err)
	require.Equal(t, "Unmanaged", etcdType(t, tr2), "the retry must not render a different etcd type")
	require.Len(t, tr2.ObserveCalls, 1)
}

func TestReconcile_EtcdBackend_RecordFailureStopsBeforeAnythingIsApplied(t *testing.T) {
	cluster := buildReadyCluster("cluster-backend", "4.15.0")
	r, store, tr := spannerReconciler(t, cluster, true, nil, "True")
	store.updateErr = fmt.Errorf("conflict")

	_, err := r.Reconcile(context.Background(), clusterReq(cluster.Name))
	require.ErrorContains(t, err, "record etcd backend")
	require.Empty(t, tr.ApplyCalls, "the HostedCluster must not be created before the selection is recorded")
	require.Empty(t, tr.ObserveCalls)
}

func TestReconcile_EtcdBackend_UnknownRecordedValueIsRejected(t *testing.T) {
	cluster := buildReadyCluster("cluster-backend", "4.15.0")
	cluster.Annotations = map[string]string{constants.AnnotationEtcdBackend: "etcd"}
	r, store, tr := spannerReconciler(t, cluster, true, nil, "True")

	_, err := r.Reconcile(context.Background(), clusterReq(cluster.Name))
	require.ErrorContains(t, err, "unknown etcd backend")
	require.False(t, store.updateCalled, "an unrecognized value is not overwritten")
	require.Empty(t, tr.ApplyCalls)
}

func TestReconcile_EtcdBackend_DeletingClusterIsNotRecorded(t *testing.T) {
	cluster := deletingCluster("cluster-backend", false)
	store := &mockStoreClient{cluster: cluster, statusWriter: &mockStatusWriter{}}
	r := hc.New(mock.New(), testLogger(t), store, nil, "", "", hc.WithSpannerEtcd(true))

	_, err := r.Reconcile(context.Background(), clusterReq(cluster.Name))
	require.NoError(t, err)
	require.NotContains(t, store.updated.(*privatev1.Cluster).Annotations, constants.AnnotationEtcdBackend,
		"deletion removes the finalizer without selecting a backend")
}

// dispatchTransport is a mock transport that records how often the dispatched
// resources of a cluster are queried.
type dispatchTransport struct {
	*mock.Client
	queries int
	err     error
}

func (d *dispatchTransport) GetDeleteStatus(ctx context.Context, mc, groupKey string) (*transport.DeleteStatus, error) {
	d.queries++
	if d.err != nil {
		return nil, d.err
	}
	return d.Client.GetDeleteStatus(ctx, mc, groupKey)
}

func dispatchReconciler(t *testing.T, cluster *privatev1.Cluster, flag bool, status *transport.DeleteStatus) (*hc.Reconciler, *mockStoreClient, *dispatchTransport) {
	t.Helper()
	tr := &dispatchTransport{Client: mock.New()}
	if status != nil {
		tr.DeleteStatusOverrides[spannerMCName+"/"+mustClusterGroupKey("hyperfleet", cluster.Name)] = status
	}
	store := &mockStoreClient{cluster: cluster, statusWriter: &mockStatusWriter{}}
	return hc.New(tr, testLogger(t), store, nil, "", "", hc.WithSpannerEtcd(flag)), store, tr
}

func TestReconcile_EtcdBackend_DispatchedResourcesMakeAClusterLegacy(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status *transport.DeleteStatus
	}{
		{"apply desires exist", &transport.DeleteStatus{ApplyDesiresCount: 5}},
		{"delete desires exist", &transport.DeleteStatus{TotalCount: 5, PendingCount: 5}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// An earlier controller dispatched the HostedCluster, but nothing is
			// reflected in the Cluster's status yet.
			cluster := buildReadyCluster("cluster-legacy", "4.15.0")
			r, store, tr := dispatchReconciler(t, cluster, true, tc.status)

			_, err := r.Reconcile(context.Background(), clusterReq(cluster.Name))
			require.NoError(t, err)

			require.Equal(t, constants.EtcdBackendManaged, recordedBackend(t, store))
			require.Equal(t, 1, tr.queries)
			require.Len(t, tr.ApplyCalls, 1)
			require.Equal(t, "Managed", etcdType(t, tr.Client))
			require.Empty(t, tr.ObserveCalls)
		})
	}
}

func TestReconcile_EtcdBackend_NothingDispatchedSelectsTheFlagBackend(t *testing.T) {
	cluster := buildReadyCluster("cluster-new", "4.15.0")
	r, store, tr := dispatchReconciler(t, cluster, true, &transport.DeleteStatus{AllSuccessful: true})

	_, err := r.Reconcile(context.Background(), clusterReq(cluster.Name))
	require.NoError(t, err)

	require.Equal(t, constants.EtcdBackendSpanner, recordedBackend(t, store))
	require.Equal(t, 1, tr.queries)
	require.Equal(t, "Unmanaged", etcdType(t, tr.Client))
}

func TestReconcile_EtcdBackend_DispatchCheckFailureIsNotGuessed(t *testing.T) {
	cluster := buildReadyCluster("cluster-unsure", "4.15.0")
	r, store, tr := dispatchReconciler(t, cluster, true, nil)
	tr.err = fmt.Errorf("firestore unavailable")

	_, err := r.Reconcile(context.Background(), clusterReq(cluster.Name))
	require.ErrorContains(t, err, "check for dispatched resources")
	require.False(t, store.updateCalled, "no backend is recorded while it is unknown")
	require.Empty(t, tr.ApplyCalls)
}

func TestReconcile_EtcdBackend_DispatchIsOnlyCheckedWhenItCanMatter(t *testing.T) {
	for _, tc := range []struct {
		name    string
		flag    bool
		prepare func(*privatev1.Cluster)
	}{
		{"flag off always selects Managed", false, nil},
		{"recorded backend is final", true, func(c *privatev1.Cluster) {
			c.Annotations = map[string]string{constants.AnnotationEtcdBackend: constants.EtcdBackendSpanner}
		}},
		{"status already shows a HostedCluster", true, func(c *privatev1.Cluster) {
			c.Status.HostedClusterResult = &privatev1.HostedClusterResult{Version: "4.15.0"}
		}},
		{"nothing can be dispatched before placement", true, func(c *privatev1.Cluster) {
			c.Status.PlacementResult = nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cluster := buildReadyCluster("cluster-backend", "4.15.0")
			if tc.prepare != nil {
				tc.prepare(cluster)
			}
			r, _, tr := dispatchReconciler(t, cluster, tc.flag, nil)

			_, err := r.Reconcile(context.Background(), clusterReq(cluster.Name))
			require.NoError(t, err)
			require.Zero(t, tr.queries)
		})
	}
}

// liveTypeReconciler wires a reconciler whose HostedCluster feedback reports the
// given live etcd management type, for a cluster with the given recorded backend.
func liveTypeReconciler(t *testing.T, recorded string, hcFeedback map[string]string, stale bool) (*hc.Reconciler, *mockStoreClient, *mock.Client, *privatev1.Cluster) {
	t.Helper()
	cluster := buildReadyCluster("cluster-live", "4.15.0")
	cluster.Annotations = map[string]string{constants.AnnotationEtcdBackend: recorded}
	groupKey := mustClusterGroupKey("hyperfleet", cluster.Name)

	tr := mock.New()
	tr.StatusOverrides[spannerMCName+"/"+groupKey] = &transport.Status{
		Conditions:       []metav1.Condition{{Type: "Applied", Status: metav1.ConditionTrue, Reason: "AppliedSuccessfully"}},
		ResourceStatuses: map[string]map[string]string{hostedClusterResourceKey(cluster): hcFeedback},
		Stale:            stale,
	}
	store := &mockStoreClient{cluster: cluster, statusWriter: &mockStatusWriter{}}
	return hc.New(tr, testLogger(t), store, nil, "", "", hc.WithSpannerEtcd(true)), store, tr, cluster
}

func TestReconcile_EtcdBackend_RecordIsCorrectedFromTheLiveHostedCluster(t *testing.T) {
	for _, tc := range []struct {
		name         string
		recorded     string
		live         map[string]string
		want         string
		wantReleased bool
	}{
		{"Spanner record of a Managed HostedCluster", constants.EtcdBackendSpanner,
			map[string]string{"etcdManagementType": "Managed"}, constants.EtcdBackendManaged, true},
		{"Managed record of a socketcd-backed HostedCluster", constants.EtcdBackendManaged,
			map[string]string{"etcdManagementType": "Unmanaged", "spannerEnabled": "true"}, constants.EtcdBackendSpanner, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			feedback := map[string]string{"availableCondition": "False"}
			for k, v := range tc.live {
				feedback[k] = v
			}
			r, store, tr, cluster := liveTypeReconciler(t, tc.recorded, feedback, false)

			result, err := r.Reconcile(context.Background(), clusterReq(cluster.Name))
			require.NoError(t, err)
			require.Equal(t, time.Second, result.RequeueAfter, "the corrected backend is applied by the next reconcile")

			require.Equal(t, tc.want, recordedBackend(t, store))
			require.False(t, store.statusWriter.called, "conditions are written once the record is right")
			if tc.wantReleased {
				require.Len(t, tr.ReleaseObservedCalls, 1, "the SpannerEtcd target of a mistaken record is released")
			} else {
				require.Empty(t, tr.ReleaseObservedCalls)
			}
		})
	}
}

func TestReconcile_EtcdBackend_CorrectionConvergesWithoutLooping(t *testing.T) {
	feedback := map[string]string{"availableCondition": "False", "etcdManagementType": "Managed"}
	r, store, tr, cluster := liveTypeReconciler(t, constants.EtcdBackendSpanner, feedback, false)

	_, err := r.Reconcile(context.Background(), clusterReq(cluster.Name))
	require.NoError(t, err)
	require.Equal(t, constants.EtcdBackendManaged, recordedBackend(t, store))

	store.cluster = store.updated.(*privatev1.Cluster)
	store.updateCalled = false
	result, err := r.Reconcile(context.Background(), clusterReq(cluster.Name))
	require.NoError(t, err)
	require.NotEqual(t, time.Second, result.RequeueAfter, "no correction means no immediate requeue")
	require.False(t, store.updateCalled, "a record that matches the live type is left alone")
	require.Len(t, tr.ApplyCalls, 2)
	require.Equal(t, "Managed", appliedEtcdType(t, tr.ApplyCalls[1]), "the next reconcile applies the type the HostedCluster has")
}

func TestReconcile_EtcdBackend_MatchingLiveTypeChangesNothing(t *testing.T) {
	for _, tc := range []struct {
		recorded string
		live     map[string]string
	}{
		{constants.EtcdBackendSpanner, map[string]string{"etcdManagementType": "Unmanaged", "spannerEnabled": "true"}},
		{constants.EtcdBackendManaged, map[string]string{"etcdManagementType": "Managed"}},
	} {
		t.Run(tc.recorded, func(t *testing.T) {
			feedback := map[string]string{"availableCondition": "True"}
			for k, v := range tc.live {
				feedback[k] = v
			}
			r, store, tr, cluster := liveTypeReconciler(t, tc.recorded, feedback, false)

			result, err := r.Reconcile(context.Background(), clusterReq(cluster.Name))
			require.NoError(t, err)
			require.NotEqual(t, time.Second, result.RequeueAfter, "no correction means no immediate requeue")
			require.False(t, store.updateCalled)
			require.Empty(t, tr.ReleaseObservedCalls)
			require.True(t, store.statusWriter.called)
		})
	}
}

func TestReconcile_EtcdBackend_UnreadableLiveTypeNeverChangesTheRecord(t *testing.T) {
	for _, tc := range []struct {
		name     string
		feedback map[string]string
		stale    bool
	}{
		{"no feedback", map[string]string{}, false},
		{"feedback without the type", map[string]string{"availableCondition": "True"}, false},
		{"unrecognized type", map[string]string{"availableCondition": "True", "etcdManagementType": "Other"}, false},
		{"stale status", map[string]string{"availableCondition": "True", "etcdManagementType": "Managed"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, store, tr, cluster := liveTypeReconciler(t, constants.EtcdBackendSpanner, tc.feedback, tc.stale)

			_, err := r.Reconcile(context.Background(), clusterReq(cluster.Name))
			require.NoError(t, err)
			require.False(t, store.updateCalled, "the record is only corrected from a recognized live type")
			require.Empty(t, tr.ReleaseObservedCalls)
		})
	}
}

func TestReconcile_EtcdBackend_CorrectionFailuresAreRetried(t *testing.T) {
	feedback := map[string]string{"availableCondition": "False", "etcdManagementType": "Managed"}

	t.Run("record update fails", func(t *testing.T) {
		r, store, _, cluster := liveTypeReconciler(t, constants.EtcdBackendSpanner, feedback, false)
		store.updateErr = fmt.Errorf("conflict")
		_, err := r.Reconcile(context.Background(), clusterReq(cluster.Name))
		require.ErrorContains(t, err, "correct etcd backend")
	})

	t.Run("release fails before the record changes", func(t *testing.T) {
		r, store, tr, cluster := liveTypeReconciler(t, constants.EtcdBackendSpanner, feedback, false)
		tr.ReleaseObservedErr = fmt.Errorf("firestore unavailable")
		_, err := r.Reconcile(context.Background(), clusterReq(cluster.Name))
		require.ErrorContains(t, err, "release observed resources")
		require.False(t, store.updateCalled, "the record is kept so the release is retried")
	})
}

func TestReconcile_EtcdBackend_UnmanagedEtcdWithoutSocketcdIsNotReadAsSpanner(t *testing.T) {
	for _, tc := range []struct {
		name     string
		feedback map[string]string
	}{
		{"no opt-in annotation", map[string]string{"availableCondition": "True", "etcdManagementType": "Unmanaged"}},
		{"opt-in explicitly absent", map[string]string{"availableCondition": "True", "etcdManagementType": "Unmanaged", "spannerEnabled": "false"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A Managed record that a socketcd reading of Unmanaged would flip to
			// Spanner. Gecko did not set this HostedCluster up for socketcd, so the
			// record must stay as it is.
			r, store, tr, cluster := liveTypeReconciler(t, constants.EtcdBackendManaged, tc.feedback, false)

			_, err := r.Reconcile(context.Background(), clusterReq(cluster.Name))
			require.NoError(t, err)
			require.False(t, store.updateCalled, "the record must not be flipped to Spanner")
			require.Empty(t, tr.ObserveCalls)
		})
	}
}
