package spanner

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/openshift-online/gecko/orlop/pkg/apiserver/storage"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var broadcasterCounterID = "test_broadcaster"

// TestBroadcaster_PartitionManagement verifies the partition bookkeeping logic
// in handleChildPartitionsRecord without requiring a Spanner connection:
//
//   - Each child partition is enqueued exactly once (deduplication via
//     enqueuedChildren prevents re-enqueuing if the same token appears again).
//   - For merges, the child is only enqueued once all parent partitions have
//     reported the same child token (pendingChildren counter).
func TestBroadcaster_PartitionManagement(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	startTs := time.Now()

	b := &spannerBroadcaster{
		pendingChildren:  make(map[string]int),
		enqueuedChildren: make(map[string]struct{}),
		// Large buffer so sends never block in this unit test.
		partitionQueue: make(chan partitionWork, 128),
	}

	// Simulate a split: two children each with one parent.
	cpr := &csChildPartitionsRecord{
		StartTimestamp: startTs,
		ChildPartitions: []*csChildPartition{
			{Token: "child-A", ParentPartitionTokens: []string{"root"}},
			{Token: "child-B", ParentPartitionTokens: []string{"root"}},
		},
	}
	b.handleChildPartitionsRecord(ctx, cpr)

	if got := len(b.partitionQueue); got != 2 {
		t.Errorf("expected 2 items in partitionQueue after split, got %d", got)
	}
	b.mu.Lock()
	if _, ok := b.enqueuedChildren["child-A"]; !ok {
		t.Error("child-A not in enqueuedChildren")
	}
	if _, ok := b.enqueuedChildren["child-B"]; !ok {
		t.Error("child-B not in enqueuedChildren")
	}
	b.mu.Unlock()

	// Calling again with the same record must not re-enqueue (deduplication).
	b.handleChildPartitionsRecord(ctx, cpr)
	if got := len(b.partitionQueue); got != 2 {
		t.Errorf("expected still 2 items in partitionQueue after duplicate report, got %d", got)
	}

	// Drain the queue so we have a clean count for the merge test.
	for len(b.partitionQueue) > 0 {
		<-b.partitionQueue
	}

	// Simulate a merge: one child with two parents. The child must not be
	// enqueued until both parents have reported it.
	mergeToken := "merged"
	cprMerge := &csChildPartitionsRecord{
		StartTimestamp: startTs,
		ChildPartitions: []*csChildPartition{
			{Token: mergeToken, ParentPartitionTokens: []string{"child-A", "child-B"}},
		},
	}

	// First parent reports — child not yet ready.
	b.handleChildPartitionsRecord(ctx, cprMerge)
	if got := len(b.partitionQueue); got != 0 {
		t.Errorf("expected 0 items after first parent, got %d", got)
	}

	// Second parent reports — child is now ready and should be enqueued.
	b.handleChildPartitionsRecord(ctx, cprMerge)
	if got := len(b.partitionQueue); got != 1 {
		t.Errorf("expected 1 item after both parents reported, got %d", got)
	}
	b.mu.Lock()
	if _, ok := b.enqueuedChildren[mergeToken]; !ok {
		t.Error("merge child not in enqueuedChildren after both parents reported")
	}
	b.mu.Unlock()

	work := <-b.partitionQueue
	if work.token == nil || *work.token != mergeToken {
		t.Errorf("expected token %q in queue, got %v", mergeToken, work.token)
	}
}

func testSchemeAndGVK() (*runtime.Scheme, schema.GroupVersionKind) {
	scheme := runtime.NewScheme()
	gv := schema.GroupVersion{Group: "test.example.com", Version: "v1"}
	scheme.AddKnownTypeWithName(gv.WithKind("TestObject"), &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(gv.WithKind("TestObjectList"), &unstructured.UnstructuredList{})
	gvk := schema.GroupVersionKind{Group: "test.example.com", Version: "v1", Kind: "TestObject"}
	return scheme, gvk
}

func setupBroadcasterWithStore(t *testing.T) (*spannerBroadcaster, *SpannerStore) {
	t.Helper()
	requireEmulator(t)

	scheme, gvk := testSchemeAndGVK()

	rt := gvkString(gvk)

	changeStreamName := "cs_" + resourcesTable

	broadcaster, err := newSpannerBroadcaster(context.Background(), spannerBroadcasterConfig{
		Client:           sharedClient,
		ResourceType:     rt,
		TableName:        resourcesTable,
		ChangeStreamName: changeStreamName,
		Scheme:           scheme,
		GVK:              gvk,
	})
	if err != nil {
		t.Fatalf("newSpannerBroadcaster() failed: %v", err)
	}
	t.Cleanup(func() { broadcaster.Close() })

	store := &SpannerStore{
		client:        sharedClient,
		resourceType:  rt,
		scheme:        scheme,
		gvk:           gvk,
		broadcaster:   broadcaster,
		tableName:     resourcesTable,
		countersTable: countersTable,
		counterID:     broadcasterCounterID,
	}

	return broadcaster, store
}

func uniqueNS(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, testCounterSeq.Add(1))
}

func drainUntil(t *testing.T, ch <-chan storage.ResourceEvent, ns, name string, timeout time.Duration) storage.ResourceEvent {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case event, ok := <-ch:
			if !ok {
				t.Fatal("event channel closed unexpectedly")
			}
			if event.Object.GetNamespace() == ns && event.Object.GetName() == name {
				return event
			}
		case <-deadline:
			t.Fatalf("timed out waiting for event (ns=%s, name=%s)", ns, name)
		}
	}
}

// eventTimeout is the maximum time to wait for a change-stream event in
// integration tests. The Spanner emulator can take ~20 seconds to deliver
// DELETE events through the change stream, so this must exceed that latency.
const eventTimeout = 30 * time.Second

func TestBroadcaster_SubscribeReceivesLiveEvents(t *testing.T) {
	_, store := setupBroadcasterWithStore(t)
	ns := uniqueNS("bc-live")

	eventCh, stop, err := store.broadcaster.Subscribe("")
	if err != nil {
		t.Fatalf("Subscribe() failed: %v", err)
	}
	defer stop()

	obj := newTestObject(withName("obj"), withNamespace(ns))
	if err := store.Create(context.Background(), obj); err != nil {
		t.Fatalf("Create() failed: %v", err)
	}

	event := drainUntil(t, eventCh, ns, "obj", eventTimeout)
	if event.Type != storage.EventAdded {
		t.Errorf("expected event type %s, got %s", storage.EventAdded, event.Type)
	}
}

func TestBroadcaster_SubscribeReplaysHistoricalEvents(t *testing.T) {
	_, store := setupBroadcasterWithStore(t)
	ns := uniqueNS("bc-hist")

	obj := newTestObject(withName("obj"), withNamespace(ns))
	if err := store.Create(context.Background(), obj); err != nil {
		t.Fatalf("Create() failed: %v", err)
	}

	eventCh, stop, err := store.broadcaster.Subscribe("0")
	if err != nil {
		t.Fatalf("Subscribe() failed: %v", err)
	}
	defer stop()

	drainUntil(t, eventCh, ns, "obj", eventTimeout)
}

func TestBroadcaster_SubscribeSinceResourceVersion(t *testing.T) {
	_, store := setupBroadcasterWithStore(t)
	ns := uniqueNS("bc-since")

	obj1 := newTestObject(withName("before"), withNamespace(ns))
	if err := store.Create(context.Background(), obj1); err != nil {
		t.Fatalf("Create() obj1 failed: %v", err)
	}
	sinceRV := obj1.GetResourceVersion()

	obj2 := newTestObject(withName("after"), withNamespace(ns))
	if err := store.Create(context.Background(), obj2); err != nil {
		t.Fatalf("Create() obj2 failed: %v", err)
	}

	eventCh, stop, err := store.broadcaster.Subscribe(sinceRV)
	if err != nil {
		t.Fatalf("Subscribe() failed: %v", err)
	}
	defer stop()

	deadline := time.After(eventTimeout)
	for {
		select {
		case event, ok := <-eventCh:
			if !ok {
				t.Fatal("event channel closed unexpectedly")
			}
			if event.Object.GetNamespace() != ns {
				continue
			}
			if event.Object.GetName() == "before" {
				t.Fatal("received event for 'before' which should have been filtered by sinceResourceVersion")
			}
			if event.Object.GetName() == "after" {
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for 'after' event")
		}
	}
}

func TestBroadcaster_MultipleSubscribers(t *testing.T) {
	_, store := setupBroadcasterWithStore(t)
	ns := uniqueNS("bc-multi")

	ch1, stop1, err := store.broadcaster.Subscribe("")
	if err != nil {
		t.Fatalf("Subscribe() 1 failed: %v", err)
	}
	defer stop1()

	ch2, stop2, err := store.broadcaster.Subscribe("")
	if err != nil {
		t.Fatalf("Subscribe() 2 failed: %v", err)
	}
	defer stop2()

	obj := newTestObject(withName("obj"), withNamespace(ns))
	if err := store.Create(context.Background(), obj); err != nil {
		t.Fatalf("Create() failed: %v", err)
	}

	drainUntil(t, ch1, ns, "obj", eventTimeout)
	drainUntil(t, ch2, ns, "obj", eventTimeout)
}

func TestBroadcaster_UnsubscribeStopsDelivery(t *testing.T) {
	broadcaster, _ := setupBroadcasterWithStore(t)

	eventCh, stop, err := broadcaster.Subscribe("")
	if err != nil {
		t.Fatalf("Subscribe() failed: %v", err)
	}

	stop()

	select {
	case _, ok := <-eventCh:
		if ok {
			return
		}
	case <-time.After(2 * time.Second):
		t.Fatal("event channel not closed after unsubscribe")
	}
}

func TestBroadcaster_DeleteEvent(t *testing.T) {
	_, store := setupBroadcasterWithStore(t)
	ns := uniqueNS("bc-delete")

	// Subscribe before writing so the CREATE event is never missed.
	eventCh, stop, err := store.broadcaster.Subscribe("")
	if err != nil {
		t.Fatalf("Subscribe() failed: %v", err)
	}
	defer stop()

	obj := newTestObject(withName("to-delete"), withNamespace(ns))
	if err := store.Create(context.Background(), obj); err != nil {
		t.Fatalf("Create() failed: %v", err)
	}

	// Wait for the CREATE event to arrive before deleting.
	drainUntil(t, eventCh, ns, "to-delete", eventTimeout)

	if err := store.Delete(context.Background(), ns, "to-delete"); err != nil {
		t.Fatalf("Delete() failed: %v", err)
	}

	deadline := time.After(eventTimeout)
	for {
		select {
		case event, ok := <-eventCh:
			if !ok {
				t.Fatal("event channel closed unexpectedly")
			}
			if event.Object.GetNamespace() != ns || event.Object.GetName() != "to-delete" {
				continue
			}
			if event.Type == storage.EventDeleted {
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for DELETE event")
		}
	}
}

func TestBroadcaster_UpdateEvent(t *testing.T) {
	_, store := setupBroadcasterWithStore(t)
	ns := uniqueNS("bc-update")

	// Subscribe before writing so the CREATE event is never missed.
	eventCh, stop, err := store.broadcaster.Subscribe("")
	if err != nil {
		t.Fatalf("Subscribe() failed: %v", err)
	}
	defer stop()

	obj := newTestObject(withName("to-update"), withNamespace(ns))
	if err := store.Create(context.Background(), obj); err != nil {
		t.Fatalf("Create() failed: %v", err)
	}

	// Wait for the CREATE event before updating.
	drainUntil(t, eventCh, ns, "to-update", eventTimeout)

	obj.Object["spec"] = map[string]any{"updated": true}
	if err := store.Update(context.Background(), obj); err != nil {
		t.Fatalf("Update() failed: %v", err)
	}

	deadline := time.After(eventTimeout)
	for {
		select {
		case event, ok := <-eventCh:
			if !ok {
				t.Fatal("event channel closed unexpectedly")
			}
			if event.Object.GetNamespace() != ns || event.Object.GetName() != "to-update" {
				continue
			}
			if event.Type != storage.EventModified {
				continue // might be the ADDED from create, keep draining
			}
			return
		case <-deadline:
			t.Fatal("timed out waiting for MODIFIED event")
		}
	}
}

func TestBroadcaster_CloseShutdown(t *testing.T) {
	broadcaster, _ := setupBroadcasterWithStore(t)

	eventCh, _, err := broadcaster.Subscribe("")
	if err != nil {
		t.Fatalf("Subscribe() failed: %v", err)
	}

	if err := broadcaster.Close(); err != nil {
		t.Fatalf("Close() failed: %v", err)
	}

	select {
	case _, ok := <-eventCh:
		if ok {
			select {
			case _, ok := <-eventCh:
				if ok {
					t.Error("channel still open after Close and drain")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("channel not closed after Close")
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("channel not closed after Close")
	}

	_, _, err = broadcaster.Subscribe("")
	if err == nil {
		t.Error("expected error subscribing to closed broadcaster")
	}
}

