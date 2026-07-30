package bigtable

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"sync"
	"time"

	bt "cloud.google.com/go/bigtable"
	btpb "cloud.google.com/go/bigtable/apiv2/bigtablepb"
	"github.com/openshift-online/gecko/orlop/pkg/apiserver/storage"
	"google.golang.org/protobuf/types/known/timestamppb"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	familyEvent = "e"

	colType      = "type"
	colEventRV   = "rv"
	colData      = "data"
	colCF        = "cf"
	colTimestamp = "ts"
)

// BigtableBroadcaster implements storage.EventBroadcaster using a Bigtable event log table
// and Bigtable change streams for real-time event distribution.
type BigtableBroadcaster struct {
	dataClient    *bt.Client
	grpcClient    btpb.BigtableClient
	eventLogTable *bt.Table
	tablePath     string // fully qualified table path for gRPC calls
	ctx           context.Context
	cancel        context.CancelFunc
	scheme        *runtime.Scheme
	gvk           schema.GroupVersionKind

	mu          sync.RWMutex
	subscribers map[int]chan storage.ResourceEvent
	nextID      int
	closed      bool
}

// BigtableBroadcasterConfig configures the Bigtable broadcaster.
type BigtableBroadcasterConfig struct {
	DataClient    *bt.Client
	GRPCClient    btpb.BigtableClient
	EventLogTable string
	TablePath     string // e.g. "projects/P/instances/I/tables/T"
	Scheme        *runtime.Scheme
	GVK           schema.GroupVersionKind
}

// NewBigtableBroadcaster creates a broadcaster backed by Bigtable change streams.
func NewBigtableBroadcaster(ctx context.Context, config BigtableBroadcasterConfig) (*BigtableBroadcaster, error) {
	if config.DataClient == nil {
		return nil, fmt.Errorf("bigtable data client is required")
	}
	if config.GRPCClient == nil {
		return nil, fmt.Errorf("bigtable gRPC client is required")
	}

	tableName := config.EventLogTable
	if tableName == "" {
		tableName = "event_log"
	}

	bCtx, cancel := context.WithCancel(ctx)

	b := &BigtableBroadcaster{
		dataClient:    config.DataClient,
		grpcClient:    config.GRPCClient,
		eventLogTable: config.DataClient.Open(tableName),
		tablePath:     config.TablePath,
		ctx:           bCtx,
		cancel:        cancel,
		scheme:        config.Scheme,
		gvk:           config.GVK,
		subscribers:   make(map[int]chan storage.ResourceEvent),
	}

	go b.watchChangeStream()

	return b, nil
}

// watchChangeStream uses the low-level Bigtable gRPC API to read a change stream
// on the event log table and broadcast new events to subscribers.
func (b *BigtableBroadcaster) watchChangeStream() {
	for {
		select {
		case <-b.ctx.Done():
			return
		default:
		}

		err := b.readChangeStream()
		if err != nil {
			if b.ctx.Err() != nil {
				return
			}
			time.Sleep(time.Second)
		}
	}
}

func (b *BigtableBroadcaster) readChangeStream() error {
	stream, err := b.grpcClient.ReadChangeStream(b.ctx, &btpb.ReadChangeStreamRequest{
		TableName: b.tablePath,
		StartFrom: &btpb.ReadChangeStreamRequest_StartTime{
			StartTime: timestamppb.Now(),
		},
	})
	if err != nil {
		return fmt.Errorf("failed to start change stream: %w", err)
	}

	for {
		resp, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("change stream recv error: %w", err)
		}

		dc := resp.GetDataChange()
		if dc == nil || !dc.Done {
			continue
		}

		event, err := b.dataChangeToEvent(dc)
		if err != nil {
			continue
		}

		b.broadcastToSubscribers(event)
	}
}

func (b *BigtableBroadcaster) dataChangeToEvent(dc *btpb.ReadChangeStreamResponse_DataChange) (storage.ResourceEvent, error) {
	var eventType string
	var rvBytes []byte
	var objectData []byte
	var contextFilter string

	for _, chunk := range dc.Chunks {
		mut := chunk.GetMutation()
		if mut == nil {
			continue
		}
		sc := mut.GetSetCell()
		if sc == nil || sc.FamilyName != familyEvent {
			continue
		}
		switch string(sc.ColumnQualifier) {
		case colType:
			eventType = string(sc.Value)
		case colEventRV:
			rvBytes = sc.Value
		case colData:
			objectData = sc.Value
		case colCF:
			contextFilter = string(sc.Value)
		}
	}

	if objectData == nil {
		return storage.ResourceEvent{}, fmt.Errorf("no object data in change stream mutation")
	}

	rv := bytesToInt64(rvBytes)

	obj, err := b.reconstructObject(objectData)
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

func (b *BigtableBroadcaster) reconstructObject(data []byte) (client.Object, error) {
	if b.scheme == nil || b.gvk.Empty() {
		obj := &unstructured.Unstructured{}
		if err := json.Unmarshal(data, &obj.Object); err != nil {
			return nil, err
		}
		return obj, nil
	}

	rObj, err := b.scheme.New(b.gvk)
	if err != nil {
		unstruct := &unstructured.Unstructured{}
		if err := json.Unmarshal(data, &unstruct.Object); err != nil {
			return nil, err
		}
		unstruct.SetGroupVersionKind(b.gvk)
		return unstruct, nil
	}

	if err := json.Unmarshal(data, rObj); err != nil {
		return nil, fmt.Errorf("failed to unmarshal into typed object: %w", err)
	}

	rObj.GetObjectKind().SetGroupVersionKind(b.gvk)

	clientObj, ok := rObj.(client.Object)
	if !ok {
		return nil, fmt.Errorf("object does not implement client.Object")
	}

	return clientObj, nil
}

func (b *BigtableBroadcaster) broadcastToSubscribers(event storage.ResourceEvent) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	for _, ch := range b.subscribers {
		select {
		case ch <- event:
		default:
		}
	}
}

// Broadcast implements storage.EventBroadcaster.
func (b *BigtableBroadcaster) Broadcast(event storage.ResourceEvent) {
	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return
	}
	b.mu.RUnlock()

	rv, _ := strconv.ParseInt(event.ResourceVersion, 10, 64)
	rowKey := padResourceVersion(rv)

	objectData, err := marshalData(event.Object)
	if err != nil {
		return
	}

	ts := bt.Now()
	mut := bt.NewMutation()
	mut.Set(familyEvent, colType, ts, []byte(string(event.Type)))
	mut.Set(familyEvent, colEventRV, ts, int64ToBytes(rv))
	mut.Set(familyEvent, colData, ts, objectData)
	mut.Set(familyEvent, colCF, ts, []byte(event.ContextFilterValue))
	mut.Set(familyEvent, colTimestamp, ts, []byte(time.Now().Format(time.RFC3339Nano)))

	_ = b.eventLogTable.Apply(b.ctx, rowKey, mut)
}

// Subscribe implements storage.EventBroadcaster.
func (b *BigtableBroadcaster) Subscribe(sinceResourceVersion string) (<-chan storage.ResourceEvent, func(), error) {
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

func (b *BigtableBroadcaster) sendHistoricalEvents(ch chan storage.ResourceEvent, sinceResourceVersion string) {
	rv, err := strconv.ParseInt(sinceResourceVersion, 10, 64)
	if err != nil {
		return
	}

	startKey := padResourceVersion(rv + 1)
	rr := bt.InfiniteRange(startKey)

	count := 0
	_ = b.eventLogTable.ReadRows(b.ctx, rr, func(row bt.Row) bool {
		if count >= 1000 {
			return false
		}

		event, parseErr := b.eventLogRowToEvent(row)
		if parseErr != nil {
			return true
		}

		select {
		case ch <- event:
			count++
			return true
		default:
			return false
		}
	})
}

func (b *BigtableBroadcaster) eventLogRowToEvent(row bt.Row) (storage.ResourceEvent, error) {
	cells := row[familyEvent]

	var eventType string
	var rv int64
	var objectData []byte
	var contextFilter string

	for _, cell := range cells {
		switch cell.Column {
		case familyEvent + ":" + colType:
			eventType = string(cell.Value)
		case familyEvent + ":" + colEventRV:
			rv = bytesToInt64(cell.Value)
		case familyEvent + ":" + colData:
			objectData = cell.Value
		case familyEvent + ":" + colCF:
			contextFilter = string(cell.Value)
		}
	}

	if objectData == nil {
		return storage.ResourceEvent{}, fmt.Errorf("no object data in event log row")
	}

	obj, err := b.reconstructObject(objectData)
	if err != nil {
		return storage.ResourceEvent{}, err
	}
	obj.SetResourceVersion(strconv.FormatInt(rv, 10))

	return storage.ResourceEvent{
		Type:               storage.EventType(eventType),
		ResourceVersion:    strconv.FormatInt(rv, 10),
		Object:             obj,
		ContextFilterValue: contextFilter,
	}, nil
}

func (b *BigtableBroadcaster) unsubscribe(id int) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if ch, exists := b.subscribers[id]; exists {
		close(ch)
		delete(b.subscribers, id)
	}
}

// Close implements storage.EventBroadcaster.
func (b *BigtableBroadcaster) Close() error {
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

// PruneOldEvents implements storage.EventPruner.
func (b *BigtableBroadcaster) PruneOldEvents(ctx context.Context, olderThan time.Duration) error {
	cutoff := time.Now().Add(-olderThan)

	var rowKeysToDelete []string
	err := b.eventLogTable.ReadRows(ctx, bt.InfiniteRange(""),
		func(row bt.Row) bool {
			cells := row[familyEvent]
			for _, cell := range cells {
				if cell.Column == familyEvent+":"+colTimestamp {
					t, parseErr := time.Parse(time.RFC3339Nano, string(cell.Value))
					if parseErr == nil && t.Before(cutoff) {
						rowKeysToDelete = append(rowKeysToDelete, row.Key())
					}
				}
			}
			return true
		},
		bt.RowFilter(bt.ColumnFilter(colTimestamp)),
	)
	if err != nil {
		return fmt.Errorf("failed to scan event log for pruning: %w", err)
	}

	for _, key := range rowKeysToDelete {
		mut := bt.NewMutation()
		mut.DeleteRow()
		if err := b.eventLogTable.Apply(ctx, key, mut); err != nil {
			return fmt.Errorf("failed to delete event row %s: %w", key, err)
		}
	}

	return nil
}

var (
	_ storage.EventBroadcaster = (*BigtableBroadcaster)(nil)
	_ storage.EventPruner      = (*BigtableBroadcaster)(nil)
)
