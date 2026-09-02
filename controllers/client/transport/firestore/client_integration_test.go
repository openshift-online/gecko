package firestore_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	fstransport "github.com/openshift-online/gecko/controllers/client/transport/firestore"
	"github.com/openshift-online/gecko/controllers/util/logger"
	"github.com/openshift-online/kube-applier-gcp/pkg/api/kubeapplier"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// testMCName is the GCP project ID that identifies the MC.
	testMCName    = "test-project"
	testClusterID = "cluster-abc"
)

func emulatorOpts(t *testing.T) []option.ClientOption {
	t.Helper()
	host := os.Getenv("FIRESTORE_EMULATOR_HOST")
	if host == "" {
		t.Skip("FIRESTORE_EMULATOR_HOST not set — skipping integration test")
	}
	return []option.ClientOption{
		option.WithEndpoint(host),
		option.WithoutAuthentication(),
		option.WithGRPCDialOption(grpc.WithTransportCredentials(insecure.NewCredentials())),
	}
}

func newTestClient(t *testing.T) *fstransport.Client {
	t.Helper()
	log, err := logger.NewLogger(logger.DefaultConfig())
	require.NoError(t, err)
	opts := emulatorOpts(t)
	return fstransport.New(log, opts...)
}

func hcManifest(t *testing.T, clusterID, clusterName string) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"apiVersion": "hypershift.openshift.io/v1beta1",
		"kind":       "HostedCluster",
		"metadata":   map[string]any{"name": clusterName, "namespace": fmt.Sprintf("clusters-%s", clusterID)},
	})
	require.NoError(t, err)
	return raw
}

func npManifest(t *testing.T, clusterID, npName string) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"apiVersion": "hypershift.openshift.io/v1beta1",
		"kind":       "NodePool",
		"metadata":   map[string]any{"name": npName, "namespace": fmt.Sprintf("clusters-%s", clusterID)},
	})
	require.NoError(t, err)
	return raw
}

// clearCollection deletes all documents in a collection (used between tests).
func clearCollection(ctx context.Context, t *testing.T, client *firestore.Client, coll string) {
	t.Helper()
	snaps, err := client.Collection(coll).Documents(ctx).GetAll()
	require.NoError(t, err)
	for _, snap := range snaps {
		_, err := snap.Ref.Delete(ctx)
		require.NoError(t, err)
	}
}

// applyDesireSpec builds a minimal ApplyDesireSpec for the given clusterID and resource name.
func applyDesireSpec(clusterID, name string) kubeapplier.ApplyDesireSpec {
	return kubeapplier.ApplyDesireSpec{
		ManagementCluster: testMCName,
		ClusterID:         clusterID,
		TargetItem: kubeapplier.ResourceReference{
			Group:     "hypershift.openshift.io",
			Version:   "v1beta1",
			Resource:  "hostedclusters",
			Namespace: fmt.Sprintf("clusters-%s", clusterID),
			Name:      name,
		},
	}
}

// specsApplyDesire returns an ApplyDesire suitable for writing to the specs DB.
func specsApplyDesire(clusterID, name string) kubeapplier.ApplyDesire {
	return kubeapplier.ApplyDesire{Spec: applyDesireSpec(clusterID, name)}
}

// statusApplyDesire returns an ApplyDesire as kube-applier-gcp writes it to the
// status DB: spec fields are empty, status carries the applied conditions.
func statusApplyDesire(conditions []metav1.Condition) kubeapplier.ApplyDesire {
	return kubeapplier.ApplyDesire{
		Status: kubeapplier.ApplyDesireStatus{Conditions: conditions},
	}
}

func TestIntegration_Apply_WritesApplyAndReadDesires(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := newTestClient(t)
	opts := emulatorOpts(t)

	specsClient, err := firestore.NewClientWithDatabase(ctx, testMCName, "specs", opts...)
	require.NoError(t, err)
	defer specsClient.Close()
	clearCollection(ctx, t, specsClient, "applydesires")
	clearCollection(ctx, t, specsClient, "readdesires")
	defer clearCollection(ctx, t, specsClient, "applydesires")
	defer clearCollection(ctx, t, specsClient, "readdesires")

	manifests := [][]byte{
		hcManifest(t, testClusterID, "my-hc"),
	}

	_, err = c.Apply(ctx, testMCName, testClusterID, manifests)
	require.NoError(t, err)

	snaps, err := specsClient.Collection("applydesires").
		Where("spec.clusterID", "==", testClusterID).
		Documents(ctx).GetAll()
	require.NoError(t, err)
	assert.Len(t, snaps, 1, "expected 1 ApplyDesire for the HostedCluster manifest")

	data := snaps[0].Data()
	assert.NotContains(t, data, "manifestWorkName")
	assert.NotNil(t, data["spec_kubeContent"])

	readSnaps, err := specsClient.Collection("readdesires").
		Where("spec.clusterID", "==", testClusterID).
		Documents(ctx).GetAll()
	require.NoError(t, err)
	assert.Len(t, readSnaps, 1, "expected 1 ReadDesire for the HostedCluster manifest")
}

func TestIntegration_GetStatus_AllSuccessful(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := newTestClient(t)
	opts := emulatorOpts(t)

	specsClient, err := firestore.NewClientWithDatabase(ctx, testMCName, "specs", opts...)
	require.NoError(t, err)
	defer specsClient.Close()

	statusClient, err := firestore.NewClientWithDatabase(ctx, testMCName, "status", opts...)
	require.NoError(t, err)
	defer statusClient.Close()

	clearCollection(ctx, t, specsClient, "applydesires")
	clearCollection(ctx, t, statusClient, "applydesires")
	clearCollection(ctx, t, statusClient, "readdesires")
	defer clearCollection(ctx, t, specsClient, "applydesires")
	defer clearCollection(ctx, t, statusClient, "applydesires")
	defer clearCollection(ctx, t, statusClient, "readdesires")

	const docID = "doc-1"

	_, err = specsClient.Collection("applydesires").Doc(docID).Set(ctx, specsApplyDesire(testClusterID, "my-hc"))
	require.NoError(t, err)

	_, err = statusClient.Collection("applydesires").Doc(docID).Set(ctx, statusApplyDesire([]metav1.Condition{
		{Type: "Successful", Status: metav1.ConditionTrue, Reason: "NoErrors"},
	}))
	require.NoError(t, err)

	status, err := c.GetStatus(ctx, testMCName, testClusterID)
	require.NoError(t, err)
	require.Len(t, status.Conditions, 1)
	assert.Equal(t, "Applied", status.Conditions[0].Type)
	assert.Equal(t, metav1.ConditionTrue, status.Conditions[0].Status)
}

// TestIntegration_GetStatus_MissingStatusDocReportsPending verifies that when
// some resources have no status document yet, GetStatus reports Applied=False
// rather than prematurely reporting Applied=True based on the subset that do.
func TestIntegration_GetStatus_MissingStatusDocReportsPending(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := newTestClient(t)
	opts := emulatorOpts(t)

	specsClient, err := firestore.NewClientWithDatabase(ctx, testMCName, "specs", opts...)
	require.NoError(t, err)
	defer specsClient.Close()

	statusClient, err := firestore.NewClientWithDatabase(ctx, testMCName, "status", opts...)
	require.NoError(t, err)
	defer statusClient.Close()

	clearCollection(ctx, t, specsClient, "applydesires")
	clearCollection(ctx, t, statusClient, "applydesires")
	defer clearCollection(ctx, t, specsClient, "applydesires")
	defer clearCollection(ctx, t, statusClient, "applydesires")

	// Two resources in specs DB.
	_, err = specsClient.Collection("applydesires").Doc("doc-1").Set(ctx, specsApplyDesire(testClusterID, "hc-1"))
	require.NoError(t, err)
	_, err = specsClient.Collection("applydesires").Doc("doc-2").Set(ctx, specsApplyDesire(testClusterID, "hc-2"))
	require.NoError(t, err)

	// Only doc-1 has a status doc with Successful=True; doc-2 is missing.
	_, err = statusClient.Collection("applydesires").Doc("doc-1").Set(ctx, statusApplyDesire([]metav1.Condition{
		{Type: "Successful", Status: metav1.ConditionTrue, Reason: "NoErrors"},
	}))
	require.NoError(t, err)

	status, err := c.GetStatus(ctx, testMCName, testClusterID)
	require.NoError(t, err)
	require.Len(t, status.Conditions, 1)
	assert.Equal(t, "Applied", status.Conditions[0].Type)
	// Must be False/Unknown — not True — because doc-2 has no status yet.
	assert.NotEqual(t, metav1.ConditionTrue, status.Conditions[0].Status)
}

func TestIntegration_GetStatus_ExtractsHCKubeContent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := newTestClient(t)
	opts := emulatorOpts(t)

	specsClient, err := firestore.NewClientWithDatabase(ctx, testMCName, "specs", opts...)
	require.NoError(t, err)
	defer specsClient.Close()

	statusClient, err := firestore.NewClientWithDatabase(ctx, testMCName, "status", opts...)
	require.NoError(t, err)
	defer statusClient.Close()

	clearCollection(ctx, t, specsClient, "readdesires")
	clearCollection(ctx, t, statusClient, "readdesires")
	defer clearCollection(ctx, t, specsClient, "readdesires")
	defer clearCollection(ctx, t, statusClient, "readdesires")

	const docID = "rd-1"

	specsReadDesire := kubeapplier.ReadDesire{
		Spec: kubeapplier.ReadDesireSpec{
			ManagementCluster: testMCName,
			ClusterID:         testClusterID,
			TargetItem: kubeapplier.ResourceReference{
				Group:     "hypershift.openshift.io",
				Version:   "v1beta1",
				Resource:  "hostedclusters",
				Namespace: "clusters-abc",
				Name:      "my-hc",
			},
		},
	}
	_, err = specsClient.Collection("readdesires").Doc(docID).Set(ctx, specsReadDesire)
	require.NoError(t, err)

	// Status DB: spec fields empty (kube-applier-gcp doesn't copy them),
	// status_kubeContent carries the live object — stored at doc root, not in the struct.
	hcLiveObject := map[string]any{
		"status": map[string]any{
			"conditions": []any{
				map[string]any{"type": "Available", "status": "True"},
			},
			"controlPlaneEndpoint": map[string]any{"host": "api.my-hc.example.com"},
			"version": map[string]any{
				"history": []any{map[string]any{"version": "4.14.1", "state": "Completed"}},
			},
		},
	}
	_, err = statusClient.Collection("readdesires").Doc(docID).Set(ctx, map[string]any{
		"spec":               kubeapplier.ReadDesireSpec{},
		"status":             kubeapplier.ReadDesireStatus{},
		"status_kubeContent": hcLiveObject,
	})
	require.NoError(t, err)

	status, err := c.GetStatus(ctx, testMCName, testClusterID)
	require.NoError(t, err)

	key := "hypershift.openshift.io/v1beta1/hostedclusters/clusters-abc/my-hc"
	require.Contains(t, status.ResourceStatuses, key)
	assert.Equal(t, "True", status.ResourceStatuses[key]["availableCondition"])
	assert.Equal(t, "api.my-hc.example.com", status.ResourceStatuses[key]["controlPlaneEndpoint"])
	assert.Equal(t, "4.14.1", status.ResourceStatuses[key]["version"])
}

func TestIntegration_Delete_WritesDeleteDesireAndRemovesApplyRead(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := newTestClient(t)
	opts := emulatorOpts(t)

	specsClient, err := firestore.NewClientWithDatabase(ctx, testMCName, "specs", opts...)
	require.NoError(t, err)
	defer specsClient.Close()

	clearCollection(ctx, t, specsClient, "applydesires")
	clearCollection(ctx, t, specsClient, "readdesires")
	clearCollection(ctx, t, specsClient, "deletedesires")
	defer clearCollection(ctx, t, specsClient, "applydesires")
	defer clearCollection(ctx, t, specsClient, "readdesires")
	defer clearCollection(ctx, t, specsClient, "deletedesires")

	manifests := [][]byte{
		npManifest(t, testClusterID, "my-np"),
	}

	_, err = c.Apply(ctx, testMCName, testClusterID, manifests)
	require.NoError(t, err)

	err = c.Delete(ctx, testMCName, testClusterID)
	require.NoError(t, err)

	applySnaps, err := specsClient.Collection("applydesires").
		Where("spec.clusterID", "==", testClusterID).
		Documents(ctx).GetAll()
	require.NoError(t, err)
	assert.Empty(t, applySnaps, "ApplyDesires should be deleted")

	readSnaps, err := specsClient.Collection("readdesires").
		Where("spec.clusterID", "==", testClusterID).
		Documents(ctx).GetAll()
	require.NoError(t, err)
	assert.Empty(t, readSnaps, "ReadDesires should be deleted")

	deleteSnaps, err := specsClient.Collection("deletedesires").Documents(ctx).GetAll()
	require.NoError(t, err)
	assert.Len(t, deleteSnaps, 1, "DeleteDesire should have been written")

	var dd kubeapplier.DeleteDesire
	require.NoError(t, deleteSnaps[0].DataTo(&dd))
	assert.Equal(t, testClusterID, dd.Spec.ClusterID)
}

func TestIntegration_Delete_IsolatesNodePoolsWithSameNameAcrossClusters(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := newTestClient(t)
	defer c.Close()
	opts := emulatorOpts(t)

	specsClient, err := firestore.NewClientWithDatabase(ctx, testMCName, "specs", opts...)
	require.NoError(t, err)
	defer specsClient.Close()

	statusClient, err := firestore.NewClientWithDatabase(ctx, testMCName, "status", opts...)
	require.NoError(t, err)
	defer statusClient.Close()

	collections := []string{"applydesires", "readdesires", "deletedesires"}
	for _, coll := range collections {
		clearCollection(ctx, t, specsClient, coll)
		clearCollection(ctx, t, statusClient, coll)
	}
	defer func() {
		for _, coll := range collections {
			clearCollection(ctx, t, specsClient, coll)
			clearCollection(ctx, t, statusClient, coll)
		}
	}()

	const (
		nodePoolName   = "workers"
		firstClusterID = "customer-cluster-a"
		otherClusterID = "customer-cluster-b"
	)

	_, err = c.Apply(ctx, testMCName, nodePoolName, [][]byte{
		npManifest(t, firstClusterID, nodePoolName),
	})
	require.NoError(t, err)
	_, err = c.Apply(ctx, testMCName, nodePoolName, [][]byte{
		npManifest(t, otherClusterID, nodePoolName),
	})
	require.NoError(t, err)

	applySnaps, err := specsClient.Collection("applydesires").
		Where("spec.clusterID", "==", nodePoolName).
		Documents(ctx).GetAll()
	require.NoError(t, err)
	require.Len(t, applySnaps, 2)

	var otherClusterRef *firestore.DocumentRef
	for _, snap := range applySnaps {
		var desire kubeapplier.ApplyDesire
		require.NoError(t, snap.DataTo(&desire))
		if desire.Spec.TargetItem.Namespace == "clusters-"+otherClusterID {
			otherClusterRef = snap.Ref
		}
	}
	require.NotNil(t, otherClusterRef, "expected an ApplyDesire for the other customer cluster")

	err = c.Delete(ctx, testMCName, nodePoolName)
	require.NoError(t, err)

	otherClusterSnap, err := otherClusterRef.Get(ctx)
	if assert.NoError(t, err, "deleting one NodePool must not delete the other cluster's ApplyDesire") {
		assert.True(t, otherClusterSnap.Exists())
	}
}

// TestIntegration_Delete_ChunksLargeBatches verifies that Delete correctly
// processes more resources than the Firestore 500-write-per-transaction limit
// by chunking them into multiple transactions.
func TestIntegration_Delete_ChunksLargeBatches(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	c := newTestClient(t)
	opts := emulatorOpts(t)

	specsClient, err := firestore.NewClientWithDatabase(ctx, testMCName, "specs", opts...)
	require.NoError(t, err)
	defer specsClient.Close()

	clearCollection(ctx, t, specsClient, "applydesires")
	clearCollection(ctx, t, specsClient, "readdesires")
	clearCollection(ctx, t, specsClient, "deletedesires")
	defer clearCollection(ctx, t, specsClient, "applydesires")
	defer clearCollection(ctx, t, specsClient, "readdesires")
	defer clearCollection(ctx, t, specsClient, "deletedesires")

	// Seed 200 ApplyDesire + ReadDesire docs — enough to require 2 chunks
	// (maxDeleteBatchSize = 166, so 200 requires 2 transactions).
	const resourceCount = 200
	const chunkedClusterID = "cluster-chunked"

	batch := specsClient.BulkWriter(ctx)
	for i := range resourceCount {
		name := fmt.Sprintf("resource-%d", i)
		docID := fmt.Sprintf("chunked-%d", i)

		ad := specsApplyDesire(chunkedClusterID, name)
		applyRef := specsClient.Collection("applydesires").Doc(docID)
		_, err := batch.Set(applyRef, ad)
		require.NoError(t, err)

		rd := kubeapplier.ReadDesire{Spec: kubeapplier.ReadDesireSpec{
			ManagementCluster: testMCName,
			ClusterID:         chunkedClusterID,
			TargetItem:        ad.Spec.TargetItem,
		}}
		readRef := specsClient.Collection("readdesires").Doc(docID)
		_, err = batch.Set(readRef, rd)
		require.NoError(t, err)
	}
	batch.Flush()

	err = c.Delete(ctx, testMCName, chunkedClusterID)
	require.NoError(t, err)

	applySnaps, err := specsClient.Collection("applydesires").
		Where("spec.clusterID", "==", chunkedClusterID).
		Documents(ctx).GetAll()
	require.NoError(t, err)
	assert.Empty(t, applySnaps, "all ApplyDesires should be deleted")

	readSnaps, err := specsClient.Collection("readdesires").
		Where("spec.clusterID", "==", chunkedClusterID).
		Documents(ctx).GetAll()
	require.NoError(t, err)
	assert.Empty(t, readSnaps, "all ReadDesires should be deleted")

	deleteSnaps, err := specsClient.Collection("deletedesires").Documents(ctx).GetAll()
	require.NoError(t, err)
	assert.Len(t, deleteSnaps, resourceCount, "one DeleteDesire per resource")
}

// TestIntegration_Apply_StaleWhenStatusNotProcessed verifies that Apply returns
// Stale=true when the status DB has not yet been updated by kube-applier-gcp
// (i.e. ObservedDesireUpdateTime does not match the spec write timestamp).
func TestIntegration_Apply_StaleWhenStatusNotProcessed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := newTestClient(t)
	opts := emulatorOpts(t)

	specsClient, err := firestore.NewClientWithDatabase(ctx, testMCName, "specs", opts...)
	require.NoError(t, err)
	defer specsClient.Close()

	statusClient, err := firestore.NewClientWithDatabase(ctx, testMCName, "status", opts...)
	require.NoError(t, err)
	defer statusClient.Close()

	clearCollection(ctx, t, specsClient, "applydesires")
	clearCollection(ctx, t, specsClient, "readdesires")
	clearCollection(ctx, t, statusClient, "applydesires")
	clearCollection(ctx, t, statusClient, "readdesires")
	defer clearCollection(ctx, t, specsClient, "applydesires")
	defer clearCollection(ctx, t, specsClient, "readdesires")
	defer clearCollection(ctx, t, statusClient, "applydesires")
	defer clearCollection(ctx, t, statusClient, "readdesires")

	manifests := [][]byte{npManifest(t, testClusterID, "stale-np")}

	// First Apply — no status docs exist yet → stale.
	status, err := c.Apply(ctx, testMCName, testClusterID, manifests)
	require.NoError(t, err)
	assert.True(t, status.Stale, "should be stale when no status docs exist")

	// Simulate kube-applier-gcp processing: write status docs with a
	// mismatched ObservedDesireUpdateTime (an old timestamp).
	applySnaps, err := specsClient.Collection("applydesires").
		Where("spec.clusterID", "==", testClusterID).
		Documents(ctx).GetAll()
	require.NoError(t, err)
	require.Len(t, applySnaps, 1)

	oldTime := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	_, err = statusClient.Collection("applydesires").Doc(applySnaps[0].Ref.ID).Set(ctx, map[string]any{
		"spec": kubeapplier.ApplyDesireSpec{},
		"status": kubeapplier.ApplyDesireStatus{
			Conditions:               []metav1.Condition{{Type: "Successful", Status: metav1.ConditionTrue, Reason: "NoErrors"}},
			ObservedDesireUpdateTime: oldTime,
		},
	})
	require.NoError(t, err)

	readSnaps, err := specsClient.Collection("readdesires").
		Where("spec.clusterID", "==", testClusterID).
		Documents(ctx).GetAll()
	require.NoError(t, err)
	require.Len(t, readSnaps, 1)

	_, err = statusClient.Collection("readdesires").Doc(readSnaps[0].Ref.ID).Set(ctx, map[string]any{
		"spec": kubeapplier.ReadDesireSpec{},
		"status": kubeapplier.ReadDesireStatus{
			ObservedDesireUpdateTime: oldTime,
		},
	})
	require.NoError(t, err)

	// Second Apply — status docs exist but with old timestamps → stale.
	status, err = c.Apply(ctx, testMCName, testClusterID, manifests)
	require.NoError(t, err)
	assert.True(t, status.Stale, "should be stale when ObservedDesireUpdateTime is old")

	// Now simulate kube-applier-gcp catching up: update status docs with the
	// current spec write timestamps. We need to read the spec UpdateTime.
	applySnaps, err = specsClient.Collection("applydesires").
		Where("spec.clusterID", "==", testClusterID).
		Documents(ctx).GetAll()
	require.NoError(t, err)
	require.Len(t, applySnaps, 1)

	_, err = statusClient.Collection("applydesires").Doc(applySnaps[0].Ref.ID).Set(ctx, map[string]any{
		"spec": kubeapplier.ApplyDesireSpec{},
		"status": kubeapplier.ApplyDesireStatus{
			Conditions:               []metav1.Condition{{Type: "Successful", Status: metav1.ConditionTrue, Reason: "NoErrors"}},
			ObservedDesireUpdateTime: applySnaps[0].UpdateTime,
		},
	})
	require.NoError(t, err)

	readSnaps, err = specsClient.Collection("readdesires").
		Where("spec.clusterID", "==", testClusterID).
		Documents(ctx).GetAll()
	require.NoError(t, err)
	require.Len(t, readSnaps, 1)

	_, err = statusClient.Collection("readdesires").Doc(readSnaps[0].Ref.ID).Set(ctx, map[string]any{
		"spec": kubeapplier.ReadDesireSpec{},
		"status": kubeapplier.ReadDesireStatus{
			ObservedDesireUpdateTime: readSnaps[0].UpdateTime,
		},
	})
	require.NoError(t, err)

	// Third Apply — same manifests, status timestamps now match → not stale.
	status, err = c.Apply(ctx, testMCName, testClusterID, manifests)
	require.NoError(t, err)
	assert.False(t, status.Stale, "should not be stale when ObservedDesireUpdateTime matches")
}

// TestIntegration_Regression_HCCollision is a regression test for GCP-1153.
// This test fails until groupKey is used as the Firestore query predicate.
//
// It proves that two HostedClusters with the same name in different namespaces
// collide when both pass the bare cluster name as the clusterID: Apply from the
// second namespace overwrites the ApplyDesires written by the first.
func TestIntegration_Regression_HCCollision(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := newTestClient(t)
	defer c.Close()
	opts := emulatorOpts(t)

	specsClient, err := firestore.NewClientWithDatabase(ctx, testMCName, "specs", opts...)
	require.NoError(t, err)
	defer specsClient.Close()

	clearCollection(ctx, t, specsClient, "applydesires")
	clearCollection(ctx, t, specsClient, "readdesires")
	defer clearCollection(ctx, t, specsClient, "applydesires")
	defer clearCollection(ctx, t, specsClient, "readdesires")

	const clusterName = "my-cluster"

	// hcManifestNS builds an HC manifest with an explicit namespace rather than
	// deriving it from clusterID, so we can simulate the two-namespace scenario.
	hcManifestNS := func(ns, name string) []byte {
		raw, jsonErr := json.Marshal(map[string]any{
			"apiVersion": "hypershift.openshift.io/v1beta1",
			"kind":       "HostedCluster",
			"metadata":   map[string]any{"name": name, "namespace": ns},
		})
		require.NoError(t, jsonErr)
		return raw
	}

	// Buggy behaviour: both namespaces use the bare cluster name as the clusterID.
	// After the fix the caller will pass "projects/ns-alpha/clusters/my-cluster"
	// and "projects/ns-beta/clusters/my-cluster" as distinct groupKeys instead.
	_, err = c.Apply(ctx, testMCName, clusterName, [][]byte{
		hcManifestNS("ns-alpha", clusterName),
	})
	require.NoError(t, err)

	_, err = c.Apply(ctx, testMCName, clusterName, [][]byte{
		hcManifestNS("ns-beta", clusterName),
	})
	require.NoError(t, err)

	// Both namespaces must have an ApplyDesire; with the bug only 1 survives.
	snaps, err := specsClient.Collection("applydesires").
		Where("spec.clusterID", "==", clusterName).
		Documents(ctx).GetAll()
	require.NoError(t, err)

	var nsAlphaFound, nsBetaFound bool
	for _, snap := range snaps {
		var desire kubeapplier.ApplyDesire
		require.NoError(t, snap.DataTo(&desire))
		switch desire.Spec.TargetItem.Namespace {
		case "ns-alpha":
			nsAlphaFound = true
		case "ns-beta":
			nsBetaFound = true
		}
	}

	require.True(t, nsAlphaFound, "ApplyDesire for ns-alpha must survive after ns-beta Apply — bug: clusterID collision overwrites it")
	require.True(t, nsBetaFound, "ApplyDesire for ns-beta must be present")
}

// TestIntegration_Regression_NodePoolCollision is a regression test for GCP-1153.
// This test fails until groupKey is used as the Firestore query predicate.
//
// It proves that two NodePools with the same name in different namespace/cluster
// scopes collide when both pass the bare NodePool name as the clusterID: Delete
// for the first NodePool removes the second NodePool's ApplyDesires as well.
func TestIntegration_Regression_NodePoolCollision(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := newTestClient(t)
	defer c.Close()
	opts := emulatorOpts(t)

	specsClient, err := firestore.NewClientWithDatabase(ctx, testMCName, "specs", opts...)
	require.NoError(t, err)
	defer specsClient.Close()

	clearCollection(ctx, t, specsClient, "applydesires")
	clearCollection(ctx, t, specsClient, "readdesires")
	clearCollection(ctx, t, specsClient, "deletedesires")
	defer clearCollection(ctx, t, specsClient, "applydesires")
	defer clearCollection(ctx, t, specsClient, "readdesires")
	defer clearCollection(ctx, t, specsClient, "deletedesires")

	const npName = "workers"

	// npManifestNS builds a NodePool manifest with an explicit namespace.
	npManifestNS := func(ns, name string) []byte {
		raw, jsonErr := json.Marshal(map[string]any{
			"apiVersion": "hypershift.openshift.io/v1beta1",
			"kind":       "NodePool",
			"metadata":   map[string]any{"name": name, "namespace": ns},
		})
		require.NoError(t, jsonErr)
		return raw
	}

	// Buggy behaviour: both NodePools use the bare node pool name as the clusterID.
	// After the fix the caller will pass distinct groupKeys such as
	// "projects/ns-alpha/clusters/cluster-a/nodepools/workers" and
	// "projects/ns-beta/clusters/cluster-b/nodepools/workers".
	_, err = c.Apply(ctx, testMCName, npName, [][]byte{
		npManifestNS("ns-alpha", npName),
	})
	require.NoError(t, err)

	_, err = c.Apply(ctx, testMCName, npName, [][]byte{
		npManifestNS("ns-beta", npName),
	})
	require.NoError(t, err)

	// Capture a reference to the ns-beta ApplyDesire before Delete is called.
	snaps, err := specsClient.Collection("applydesires").
		Where("spec.clusterID", "==", npName).
		Documents(ctx).GetAll()
	require.NoError(t, err)

	var nsBetaRef *firestore.DocumentRef
	for _, snap := range snaps {
		var desire kubeapplier.ApplyDesire
		require.NoError(t, snap.DataTo(&desire))
		if desire.Spec.TargetItem.Namespace == "ns-beta" {
			nsBetaRef = snap.Ref
		}
	}
	require.NotNil(t, nsBetaRef, "expected an ApplyDesire for ns-beta before Delete")

	// Delete the first NodePool (ns-alpha / cluster-a). With the bug, this wipes
	// ALL ApplyDesires sharing clusterID="workers", including ns-beta's.
	err = c.Delete(ctx, testMCName, npName)
	require.NoError(t, err)

	// ns-beta's ApplyDesire must still exist after deleting ns-alpha's NodePool.
	nsBetaSnap, err := nsBetaRef.Get(ctx)
	require.NoError(t, err, "deleting ns-alpha NodePool must not delete ns-beta's ApplyDesire — bug: clusterID collision removes it")
	require.True(t, nsBetaSnap.Exists(), "ns-beta ApplyDesire must still exist after ns-alpha NodePool Delete")
}

// TestIntegration_GetStatus_NeverStale verifies that GetStatus (without Apply)
// never reports stale, since there are no write timestamps to compare against.
func TestIntegration_GetStatus_NeverStale(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := newTestClient(t)
	opts := emulatorOpts(t)

	specsClient, err := firestore.NewClientWithDatabase(ctx, testMCName, "specs", opts...)
	require.NoError(t, err)
	defer specsClient.Close()

	statusClient, err := firestore.NewClientWithDatabase(ctx, testMCName, "status", opts...)
	require.NoError(t, err)
	defer statusClient.Close()

	clearCollection(ctx, t, specsClient, "applydesires")
	clearCollection(ctx, t, statusClient, "applydesires")
	defer clearCollection(ctx, t, specsClient, "applydesires")
	defer clearCollection(ctx, t, statusClient, "applydesires")

	const docID = "doc-getstatus"
	_, err = specsClient.Collection("applydesires").Doc(docID).Set(ctx, specsApplyDesire(testClusterID, "my-hc"))
	require.NoError(t, err)

	_, err = statusClient.Collection("applydesires").Doc(docID).Set(ctx, statusApplyDesire([]metav1.Condition{
		{Type: "Successful", Status: metav1.ConditionTrue, Reason: "NoErrors"},
	}))
	require.NoError(t, err)

	status, err := c.GetStatus(ctx, testMCName, testClusterID)
	require.NoError(t, err)
	assert.False(t, status.Stale, "GetStatus without Apply should never report stale")
}
