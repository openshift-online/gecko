package controlplaneupgrade

import (
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	privatev1 "github.com/openshift-online/gecko/platform-api/api/private/v1"

	"github.com/openshift-online/gecko/controllers/util/logger"
)

const (
	testNamespace = "gcp-hcp-dev-customer-pvasanth"
	testCluster   = "upgrade-poc"
)

func TestReconcileSelectsLatestAdvertisedZStreamTarget(t *testing.T) {
	r, c := newTestReconciler(t, readyCluster("4.22.8", "4.22.8", []string{"4.22.10", "4.22.9", "4.22.12"}))

	result, err := r.Reconcile(context.Background(), request(testCluster))
	require.NoError(t, err)
	require.Equal(t, requeueObserving, result.RequeueAfter)

	cluster := getCluster(t, c)
	require.Equal(t, "4.22.12", cluster.Spec.Release.Version)
	require.Equal(t, metav1.ConditionTrue, meta.FindStatusCondition(cluster.Status.Conditions, conditionAvailable).Status)
	require.Equal(t, metav1.ConditionTrue, meta.FindStatusCondition(cluster.Status.Conditions, conditionProgressing).Status)
	require.Equal(t, metav1.ConditionFalse, meta.FindStatusCondition(cluster.Status.Conditions, conditionDegraded).Status)
}

func TestReconcileDoesNotReplaceActiveUpgrade(t *testing.T) {
	cluster := readyCluster("4.22.8", "4.22.12", []string{"4.22.13"})
	cluster.Spec.Release.Version = "4.22.12"
	r, c := newTestReconciler(t, cluster)

	result, err := r.Reconcile(context.Background(), request(testCluster))
	require.NoError(t, err)
	require.Equal(t, requeueObserving, result.RequeueAfter)

	cluster = getCluster(t, c)
	require.Equal(t, "4.22.12", cluster.Spec.Release.Version)
	require.Equal(t, metav1.ConditionTrue, meta.FindStatusCondition(cluster.Status.Conditions, conditionProgressing).Status)
}

func TestReconcileWaitsWhenOnlyMinorUpdateIsAdvertised(t *testing.T) {
	r, c := newTestReconciler(t, readyCluster("4.22.8", "4.22.8", []string{"4.23.1"}))

	result, err := r.Reconcile(context.Background(), request(testCluster))
	require.NoError(t, err)
	require.Equal(t, requeueStable, result.RequeueAfter)

	cluster := getCluster(t, c)
	require.Equal(t, "4.22.8", cluster.Spec.Release.Version)
	require.Equal(t, metav1.ConditionFalse, meta.FindStatusCondition(cluster.Status.Conditions, conditionDegraded).Status)
	require.Equal(t, "NoRecommendedUpdate", meta.FindStatusCondition(cluster.Status.Conditions, conditionAvailable).Reason)
}

func TestReconcileRecordsAutomaticUpgradeCompletion(t *testing.T) {
	cluster := readyCluster("4.22.12", "4.22.12", nil)
	meta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
		Type: conditionProgressing, Status: metav1.ConditionTrue, Reason: "HostedClusterProgressing",
	})
	r, c := newTestReconciler(t, cluster)

	result, err := r.Reconcile(context.Background(), request(testCluster))
	require.NoError(t, err)
	require.Equal(t, requeueStable, result.RequeueAfter)

	condition := meta.FindStatusCondition(getCluster(t, c).Status.Conditions, conditionProgressing)
	require.Equal(t, metav1.ConditionFalse, condition.Status)
	require.Equal(t, "UpgradeCompleted", condition.Reason)
}

func TestReconcileWaitsUntilClusterVersionIsUpgradeable(t *testing.T) {
	cluster := readyCluster("4.22.8", "4.22.8", []string{"4.22.12"})
	meta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
		Type: "ClusterVersionUpgradeable", Status: metav1.ConditionFalse, Reason: "AdminAckRequired", Message: "An administrator acknowledgement is required",
	})
	r, c := newTestReconciler(t, cluster)

	result, err := r.Reconcile(context.Background(), request(testCluster))
	require.NoError(t, err)
	require.Equal(t, requeueObserving, result.RequeueAfter)

	cluster = getCluster(t, c)
	require.Equal(t, "4.22.8", cluster.Spec.Release.Version)
	require.Equal(t, "ClusterVersionNotUpgradeable", meta.FindStatusCondition(cluster.Status.Conditions, conditionAvailable).Reason)
}

func TestReconcileIgnoresUnconfiguredCluster(t *testing.T) {
	r, c := newTestReconciler(t, readyCluster("4.22.8", "4.22.8", []string{"4.22.12"}))

	result, err := r.Reconcile(context.Background(), request("another-cluster"))
	require.NoError(t, err)
	require.Equal(t, reconcile.Result{}, result)
	require.Equal(t, "4.22.8", getCluster(t, c).Spec.Release.Version)
}

func TestSelectTargetIgnoresMinorUpdates(t *testing.T) {
	target, err := selectTarget("4.22.8", []string{"4.23.1", "4.22.12"})
	require.NoError(t, err)
	require.Equal(t, "4.22.12", target)
}

func readyCluster(current, desired string, available []string) *privatev1.Cluster {
	cluster := &privatev1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: testCluster, Generation: 2},
		Spec: privatev1.ClusterSpec{
			Release: privatev1.ReleaseSpec{Version: current, ChannelGroup: "stable"},
		},
		Status: privatev1.ClusterStatus{
			HostedClusterResult: &privatev1.HostedClusterResult{
				Version: current, DesiredVersion: desired, AvailableVersions: available,
			},
		},
	}
	meta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{Type: "HostedClusterAvailable", Status: metav1.ConditionTrue, Reason: "HostedClusterAvailable"})
	meta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{Type: "ClusterVersionUpgradeable", Status: metav1.ConditionTrue, Reason: "AsExpected"})
	meta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{Type: "HostedClusterDegraded", Status: metav1.ConditionFalse, Reason: "AsExpected"})
	return cluster
}

func newTestReconciler(t *testing.T, cluster *privatev1.Cluster) (*Reconciler, client.Client) {
	t.Helper()
	scheme := runtimeScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&privatev1.Cluster{}).WithObjects(cluster).Build()
	log, err := logger.NewLogger(logger.Config{Level: "error", Format: logger.FormatText, Writer: io.Discard, Component: "test"})
	require.NoError(t, err)
	return NewReconciler(c, log), c
}

func runtimeScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, privatev1.AddToScheme(scheme))
	return scheme
}

func request(name string) reconcile.Request {
	return reconcile.Request{NamespacedName: client.ObjectKey{Namespace: testNamespace, Name: name}}
}

func getCluster(t *testing.T, c client.Client) *privatev1.Cluster {
	t.Helper()
	var cluster privatev1.Cluster
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Namespace: testNamespace, Name: testCluster}, &cluster))
	return &cluster
}
