package hc_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/openshift-online/gecko/controllers/client/transport/mock"
	"github.com/openshift-online/gecko/controllers/hc"
	"github.com/openshift-online/gecko/controllers/util/constants"
	privatev1 "github.com/openshift-online/gecko/platform-api/api/private/v1"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func etcdType(t *testing.T, tr *mock.Client) string {
	t.Helper()
	obj := hostedClusterManifest(t, tr)
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
