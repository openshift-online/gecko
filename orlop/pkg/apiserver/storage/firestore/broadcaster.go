package firestore

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/openshift-online/gecko/orlop/pkg/apiserver/storage"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type FirestoreBroadcaster struct {
	client       *firestore.Client
	eventLogColl string
	ctx          context.Context
	cancel       context.CancelFunc
	scheme       *runtime.Scheme
	gvk          schema.GroupVersionKind

	mu          sync.RWMutex
	subscribers map[int]chan storage.ResourceEvent
	nextID      int
	closed      bool
}

type FirestoreBroadcasterConfig struct {
	Client       *firestore.Client
	EventLogColl string
	Scheme       *runtime.Scheme
	GVK          schema.GroupVersionKind
}

func NewFirestoreBroadcaster(ctx context.Context, config FirestoreBroadcasterConfig) (*FirestoreBroadcaster, error) {
	if config.Client == nil {
		return nil, fmt.Errorf("firestore client is required")
	}

	eventLogColl := config.EventLogColl
	if eventLogColl == "" {
		eventLogColl = "event_log"
	}

	bCtx, cancel := context.WithCancel(ctx)

	b := &FirestoreBroadcaster{
		client:       config.Client,
		eventLogColl: eventLogColl,
		ctx:          bCtx,
		cancel:       cancel,
		scheme:       config.Scheme,
		gvk:          config.GVK,
		subscribers:  make(map[int]chan storage.ResourceEvent),
	}

	go b.watchSnapshots()

	return b, nil
}

func (b *FirestoreBroadcaster) watchSnapshots() {
	for {
		select {
		case <-b.ctx.Done():
			return
		default:
		}

		err := b.readSnapshots()
		if err != nil {
			if b.ctx.Err() != nil {
				return
			}
			time.Sleep(time.Second)
		}
	}
}

func (b *FirestoreBroadcaster) readSnapshots() error {
	q := b.client.Collection(b.eventLogColl).OrderBy("rv", firestore.Asc)
	iter := q.Snapshots(b.ctx)
	defer iter.Stop()

	for {
		snap, err := iter.Next()
		if err != nil {
			return fmt.Errorf("snapshot iterator error: %w", err)
		}

		for _, change := range snap.Changes {
			if change.Kind != firestore.DocumentAdded {
				continue
			}

			event, err := b.docToEvent(change.Doc)
			if err != nil {
				continue
			}

			b.broadcastToSubscribers(event)
		}
	}
}

func (b *FirestoreBroadcaster) docToEvent(snap *firestore.DocumentSnapshot) (storage.ResourceEvent, error) {
	data := snap.Data()

	eventType, _ := data["type"].(string)
	rv := toInt64(data["rv"])
	contextFilter, _ := data["contextFilter"].(string)

	rawData, ok := data["data"]
	if !ok {
		return storage.ResourceEvent{}, fmt.Errorf("no data field in event log document")
	}

	obj, err := b.reconstructObject(rawData)
	if err != nil {
		return storage.ResourceEvent{}, fmt.Errorf("failed to reconstruct object: %w", err)
	}
	obj.SetResourceVersion(strconv.FormatInt(rv, 10))

	return storage.ResourceEvent{
		Type:               storage.EventType(eventType),
		ResourceVersion:    strconv.FormatInt(rv, 10),
		Object:             obj,
		ContextFilterValue: contextFilter,
	}, nil
}

func (b *FirestoreBroadcaster) reconstructObject(rawData any) (client.Object, error) {
	dataMap, ok := rawData.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("data is not a map")
	}

	jsonBytes, err := json.Marshal(dataMap)
	if err != nil {
		return nil, err
	}

	if b.scheme != nil && !b.gvk.Empty() {
		rObj, err := b.scheme.New(b.gvk)
		if err == nil {
			if err := json.Unmarshal(jsonBytes, rObj); err == nil {
				rObj.GetObjectKind().SetGroupVersionKind(b.gvk)
				if clientObj, ok := rObj.(client.Object); ok {
					return clientObj, nil
				}
			}
		}
	}

	unstruct := &unstructured.Unstructured{}
	if err := json.Unmarshal(jsonBytes, &unstruct.Object); err != nil {
		return nil, err
	}
	if !b.gvk.Empty() {
		unstruct.SetGroupVersionKind(b.gvk)
	}
	return unstruct, nil
}

func (b *FirestoreBroadcaster) broadcastToSubscribers(event storage.ResourceEvent) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	for _, ch := range b.subscribers {
		select {
		case ch <- event:
		default:
		}
	}
}

func (b *FirestoreBroadcaster) Broadcast(event storage.ResourceEvent) {
	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return
	}
	b.mu.RUnlock()

	rv, _ := strconv.ParseInt(event.ResourceVersion, 10, 64)
	docID := padResourceVersion(rv)

	objectData, err := marshalData(event.Object)
	if err != nil {
		return
	}

	doc := map[string]any{
		"type":          string(event.Type),
		"rv":            rv,
		"data":          objectData,
		"contextFilter": event.ContextFilterValue,
		"timestamp":     time.Now(),
	}

	_, _ = b.client.Collection(b.eventLogColl).Doc(docID).Set(b.ctx, doc)
}

func (b *FirestoreBroadcaster) Subscribe(sinceResourceVersion string) (<-chan storage.ResourceEvent, func(), error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return nil, nil, fmt.Errorf("broadcaster is closed")
	}

	id := b.nextID
	b.nextID++

	ch := make(chan storage.ResourceEvent, 100)
	b.subscribers[id] = ch

	if sinceResourceVersion != "" {
		go b.sendHistoricalEvents(ch, sinceResourceVersion)
	}

	stopFunc := func() {
		b.unsubscribe(id)
	}

	return ch, stopFunc, nil
}

func (b *FirestoreBroadcaster) sendHistoricalEvents(ch chan storage.ResourceEvent, sinceResourceVersion string) {
	rv, err := strconv.ParseInt(sinceResourceVersion, 10, 64)
	if err != nil {
		return
	}

	docs, err := b.client.Collection(b.eventLogColl).
		Where("rv", ">", rv).
		OrderBy("rv", firestore.Asc).
		Limit(1000).
		Documents(b.ctx).
		GetAll()
	if err != nil {
		return
	}

	for _, snap := range docs {
		event, parseErr := b.docToEvent(snap)
		if parseErr != nil {
			continue
		}

		select {
		case ch <- event:
		default:
			return
		}
	}
}

func (b *FirestoreBroadcaster) unsubscribe(id int) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if ch, exists := b.subscribers[id]; exists {
		close(ch)
		delete(b.subscribers, id)
	}
}

func (b *FirestoreBroadcaster) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return nil
	}

	b.closed = true
	b.cancel()

	for id, ch := range b.subscribers {
		close(ch)
		delete(b.subscribers, id)
	}

	return nil
}

func (b *FirestoreBroadcaster) PruneOldEvents(ctx context.Context, olderThan time.Duration) error {
	cutoff := time.Now().Add(-olderThan)

	docs, err := b.client.Collection(b.eventLogColl).
		Where("timestamp", "<", cutoff).
		Documents(ctx).
		GetAll()
	if err != nil {
		return fmt.Errorf("failed to query events for pruning: %w", err)
	}

	for _, snap := range docs {
		if _, err := snap.Ref.Delete(ctx); err != nil {
			return fmt.Errorf("failed to delete event %s: %w", snap.Ref.ID, err)
		}
	}

	return nil
}

var (
	_ storage.EventBroadcaster = (*FirestoreBroadcaster)(nil)
	_ storage.EventPruner      = (*FirestoreBroadcaster)(nil)
)
