package spanner

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/go-logr/logr"
	"github.com/openshift-online/gecko/orlop/pkg/apiserver/storage"
	"google.golang.org/api/iterator"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type csColumnType struct {
	Name           string           `spanner:"name"`
	Type           spanner.NullJSON `spanner:"type"`
	IsPrimaryKey   bool             `spanner:"is_primary_key"`
	OrdinalPosition int64           `spanner:"ordinal_position"`
}

type csMod struct {
	Keys      spanner.NullJSON `spanner:"keys"`
	NewValues spanner.NullJSON `spanner:"new_values"`
	OldValues spanner.NullJSON `spanner:"old_values"`
}

type csDataChangeRecord struct {
	CommitTimestamp                       time.Time      `spanner:"commit_timestamp"`
	RecordSequence                       string         `spanner:"record_sequence"`
	ServerTransactionID                  string         `spanner:"server_transaction_id"`
	IsLastRecordInTransactionInPartition bool           `spanner:"is_last_record_in_transaction_in_partition"`
	TableName                            string         `spanner:"table_name"`
	ColumnTypes                          []*csColumnType `spanner:"column_types"`
	Mods                                 []*csMod       `spanner:"mods"`
	ModType                              string         `spanner:"mod_type"`
	ValueCaptureType                     string         `spanner:"value_capture_type"`
	NumberOfRecordsInTransaction         int64          `spanner:"number_of_records_in_transaction"`
	NumberOfPartitionsInTransaction      int64          `spanner:"number_of_partitions_in_transaction"`
	TransactionTag                       string         `spanner:"transaction_tag"`
	IsSystemTransaction                  bool           `spanner:"is_system_transaction"`
}

type csHeartbeatRecord struct {
	Timestamp time.Time `spanner:"timestamp"`
}

type csChildPartition struct {
	Token                 string   `spanner:"token"`
	ParentPartitionTokens []string `spanner:"parent_partition_tokens"`
}

type csChildPartitionsRecord struct {
	StartTimestamp  time.Time          `spanner:"start_timestamp"`
	RecordSequence  string             `spanner:"record_sequence"`
	ChildPartitions []*csChildPartition `spanner:"child_partitions"`
}

type csRecord struct {
	DataChangeRecords      []*csDataChangeRecord      `spanner:"data_change_record"`
	HeartbeatRecords       []*csHeartbeatRecord        `spanner:"heartbeat_record"`
	ChildPartitionsRecords []*csChildPartitionsRecord  `spanner:"child_partitions_record"`
}

// resourceBinding maps a resource type to the scheme/GVK used to
// reconstruct objects for that resource type from change stream data.
type resourceBinding struct {
	scheme *runtime.Scheme
	gvk    schema.GroupVersionKind
}

// subscriber is a single watch subscription for a specific resource type.
type subscriber struct {
	resourceType string
	ch           chan storage.ResourceEvent
}

// partitionWork is a unit of work for the single change-stream reader: one
// partition to read, identified by its token and the timestamp to start from.
type partitionWork struct {
	token   *string   // nil for the initial root partition
	startTs time.Time
}

type spannerBroadcaster struct {
	client           *spanner.Client
	tableName        string
	changeStreamName string
	ctx              context.Context
	cancel           context.CancelFunc
	logger           logr.Logger

	mu          sync.RWMutex
	types       map[string]resourceBinding
	subscribers map[int]*subscriber
	nextID      int
	closed      bool
	lastRV      int64
	wg          sync.WaitGroup

	// partitionQueue is consumed by a single runPartitionLoop goroutine.
	// Keeping partition reads strictly sequential ensures at most one active
	// change-stream query exists at any time, well within Spanner's 20-reader
	// limit per stream.
	partitionQueue chan partitionWork

	// pendingChildren tracks child partition tokens during partition merges.
	// When multiple parents merge into one child, each parent independently
	// reports the same child token. The counter tracks how many parents have
	// yet to finish; the child is enqueued only when all parents are done.
	pendingChildren map[string]int

	// enqueuedChildren records every child token that has been placed in
	// partitionQueue. This prevents duplicate enqueues if the same
	// ChildPartitionsRecord arrives more than once (e.g. after a retry).
	enqueuedChildren map[string]struct{}
}

type spannerBroadcasterConfig struct {
	Client           *spanner.Client
	ResourceType     string
	TableName        string
	ChangeStreamName string
	Scheme           *runtime.Scheme
	GVK              schema.GroupVersionKind
	Logger           logr.Logger
}

func newSpannerBroadcaster(ctx context.Context, config spannerBroadcasterConfig) (*spannerBroadcaster, error) {
	if config.Client == nil {
		return nil, fmt.Errorf("spanner client is required")
	}

	tableName := config.TableName
	if tableName == "" {
		tableName = "resources"
	}

	bCtx, cancel := context.WithCancel(ctx)

	broadcasterLogger := config.Logger
	if broadcasterLogger.GetSink() == nil {
		broadcasterLogger = logr.Discard()
	}

	b := &spannerBroadcaster{
		client:           config.Client,
		tableName:        tableName,
		changeStreamName: config.ChangeStreamName,
		ctx:              bCtx,
		cancel:           cancel,
		logger:           broadcasterLogger,
		types:            make(map[string]resourceBinding),
		subscribers:      make(map[int]*subscriber),
		pendingChildren:  make(map[string]int),
		enqueuedChildren: make(map[string]struct{}),
		// 128 is far beyond any realistic partition fan-out depth.
		partitionQueue: make(chan partitionWork, 128),
	}

	if config.ResourceType != "" {
		b.RegisterType(config.ResourceType, config.Scheme, config.GVK)
	}

	// Phase 1: discover initial partitions by querying the root (nil-token)
	// partition synchronously. This fails fast if the change stream is not
	// available (e.g. missing DDL) instead of silently retrying in the
	// background.
	startTs := time.Now()
	partitions, err := b.discoverPartitions(bCtx, startTs)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to discover change stream partitions: %w", err)
	}

	// Phase 2: enqueue the discovered partitions and start the reader loop.
	for _, p := range partitions {
		b.partitionQueue <- p
	}
	b.wg.Go(func() {
		b.runPartitionLoop(bCtx)
	})

	return b, nil
}

// discoverPartitions queries the root (nil-token) partition of the change
// stream to obtain the initial set of child partition tokens. This is a
// synchronous setup step that separates partition discovery from the steady-
// state read loop, following the approach used by spanner-etcd.
//
// If the change stream returns no child partitions (e.g. it has not been split
// yet), a single root partition work item is returned so the reader loop reads
// the stream directly from the nil token.
func (b *spannerBroadcaster) discoverPartitions(ctx context.Context, startTs time.Time) ([]partitionWork, error) {
	stmt := spanner.Statement{
		SQL: fmt.Sprintf(
			"SELECT ChangeRecord FROM READ_%s(start_timestamp => @startTimestamp, end_timestamp => @endTimestamp, partition_token => @partitionToken, heartbeat_milliseconds => @heartbeatMilliseconds)",
			b.changeStreamName,
		),
		Params: map[string]any{
			"startTimestamp":         startTs,
			"endTimestamp":           (*time.Time)(nil),
			"partitionToken":         (*string)(nil),
			"heartbeatMilliseconds":  int64(1000),
		},
	}

	iter := b.client.Single().Query(ctx, stmt)
	defer iter.Stop()

	var partitions []partitionWork
	for {
		row, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("root partition query: %w", err)
		}

		var records []*csRecord
		if err := row.Column(0, &records); err != nil {
			continue
		}

		for _, rec := range records {
			for _, cpr := range rec.ChildPartitionsRecords {
				for _, cp := range cpr.ChildPartitions {
					token := cp.Token
					partitions = append(partitions, partitionWork{
						token:   &token,
						startTs: cpr.StartTimestamp,
					})
				}
			}
		}
	}

	// If the stream has not been split yet, read from the root partition
	// directly. This is the common case on production Spanner when the
	// table has few splits.
	if len(partitions) == 0 {
		partitions = append(partitions, partitionWork{token: nil, startTs: startTs})
	}

	b.logger.Info("discovered change stream partitions", "count", len(partitions))
	return partitions, nil
}

// RegisterType registers a resource type with the broadcaster. Once
// registered, the broadcaster reconstructs and broadcasts events for that
// resource type from the shared change stream watch.
func (b *spannerBroadcaster) RegisterType(resourceType string, scheme *runtime.Scheme, gvk schema.GroupVersionKind) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.types[resourceType] = resourceBinding{scheme: scheme, gvk: gvk}
}

// runPartitionLoop is the single goroutine that reads change-stream partitions
// one at a time. Keeping reads strictly serial ensures at most one active
// Spanner change-stream query exists, staying well within the 20-reader limit.
//
// Partition work items are seeded by newSpannerBroadcaster (root partition)
// and by handleChildPartitionsRecord (child partitions after a split/merge).
func (b *spannerBroadcaster) runPartitionLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case work, ok := <-b.partitionQueue:
			if !ok {
				return
			}
			b.readChangeStream(ctx, work.token, work.startTs)
		}
	}
}

// readChangeStream reads one change-stream partition to completion. It loops
// continuously, re-issuing the query each time the iterator returns Done
// without children (the emulator and real Spanner may flush a batch and return
// Done before the partition has been split). When Done is returned after
// children have been reported (sawChildren == true), the partition has been
// handed off to its children and this function returns so runPartitionLoop can
// dequeue and read those children.
//
// Transient errors are retried with exponential backoff, resuming from the
// last successfully processed timestamp so no events are lost.
func (b *spannerBroadcaster) readChangeStream(ctx context.Context, partitionToken *string, startTs time.Time) {
	backoff := time.Second
	lastTs := startTs

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		params := map[string]any{
			"startTimestamp":        lastTs,
			"endTimestamp":          (*time.Time)(nil),
			"partitionToken":        partitionToken,
			"heartbeatMilliseconds": int64(5000),
		}

		stmt := spanner.Statement{
			SQL: fmt.Sprintf(
				"SELECT ChangeRecord FROM READ_%s(start_timestamp => @startTimestamp, end_timestamp => @endTimestamp, partition_token => @partitionToken, heartbeat_milliseconds => @heartbeatMilliseconds)",
				b.changeStreamName,
			),
			Params: params,
		}

		iter := b.client.Single().Query(ctx, stmt)
		sawChildren, err := b.processChangeRecords(ctx, iter, &lastTs)
		iter.Stop()

		if ctx.Err() != nil {
			return
		}

		if err != nil {
			b.logger.Error(err, "Change stream read error, retrying",
				"backoff", backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}

		// The partition ended after reporting a split/merge. Children are
		// already in partitionQueue; return so runPartitionLoop reads them.
		if sawChildren {
			return
		}

		// Done with no children: re-issue immediately to stay live. The
		// emulator (and sometimes real Spanner) returns Done in short batches
		// even for long-lived partitions. Events committed while no query was
		// active are buffered and will appear in the next query.
		backoff = time.Second
	}
}

// processChangeRecords drains the iterator and dispatches each record type.
// It returns true in sawChildren when at least one ChildPartitionsRecord was
// processed, signalling that this partition has been split/merged and the
// caller should exit instead of re-querying.
func (b *spannerBroadcaster) processChangeRecords(ctx context.Context, iter *spanner.RowIterator, lastTs *time.Time) (sawChildren bool, err error) {
	for {
		row, err := iter.Next()
		if err == iterator.Done {
			return sawChildren, nil
		}
		if err != nil {
			return sawChildren, err
		}

		var records []*csRecord
		if err := row.Column(0, &records); err != nil {
			continue
		}

		for _, rec := range records {
			for _, dcr := range rec.DataChangeRecords {
				b.handleDataChangeRecord(dcr)
				if dcr.CommitTimestamp.After(*lastTs) {
					*lastTs = dcr.CommitTimestamp
				}
			}

			for _, hr := range rec.HeartbeatRecords {
				if hr.Timestamp.After(*lastTs) {
					*lastTs = hr.Timestamp
				}
			}

			for _, cpr := range rec.ChildPartitionsRecords {
				b.handleChildPartitionsRecord(ctx, cpr)
				if cpr.StartTimestamp.After(*lastTs) {
					*lastTs = cpr.StartTimestamp
				}
				sawChildren = true
			}
		}
	}
}

func (b *spannerBroadcaster) handleDataChangeRecord(dcr *csDataChangeRecord) {
	var eventType storage.EventType
	switch dcr.ModType {
	case "INSERT":
		eventType = storage.EventAdded
	case "UPDATE":
		eventType = storage.EventModified
	case "DELETE":
		eventType = storage.EventDeleted
	default:
		return
	}

	for _, m := range dcr.Mods {
		keys, err := csModToMap(m.Keys)
		if err != nil {
			continue
		}

		resourceType, _ := keys["resource_type"].(string)

		b.mu.RLock()
		binding, ok := b.types[resourceType]
		b.mu.RUnlock()
		if !ok {
			continue
		}

		contextFilter, _ := keys["context_filter"].(string)

		// For DELETE, new_values is empty — read from old_values instead
		var values map[string]any
		if dcr.ModType == "DELETE" {
			values, err = csModToMap(m.OldValues)
		} else {
			values, err = csModToMap(m.NewValues)
		}
		if err != nil {
			continue
		}

		rv, _ := jsonInt64(values["resource_version"])

		objectData := values["data"]
		if objectData == nil {
			continue
		}
		var objectDataBytes []byte
		switch v := objectData.(type) {
		case string:
			objectDataBytes = []byte(v)
		default:
			objectDataBytes, err = json.Marshal(v)
			if err != nil {
				continue
			}
		}

		obj, err := b.reconstructObject(objectDataBytes, binding)
		if err != nil {
			continue
		}

		obj.SetResourceVersion(strconv.FormatInt(rv, 10))

		event := storage.ResourceEvent{
			Type:               eventType,
			ResourceVersion:    strconv.FormatInt(rv, 10),
			Object:             obj,
			ContextFilterValue: contextFilter,
		}

		b.broadcastToSubscribers(resourceType, event)

		b.mu.Lock()
		if rv > b.lastRV {
			b.lastRV = rv
		}
		b.mu.Unlock()
	}
}

func csModToMap(nj spanner.NullJSON) (map[string]any, error) {
	if !nj.Valid {
		return nil, fmt.Errorf("null JSON")
	}
	data, err := json.Marshal(nj.Value)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func jsonInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case json.Number:
		i, err := n.Int64()
		return i, err == nil
	case string:
		i, err := strconv.ParseInt(n, 10, 64)
		return i, err == nil
	default:
		return 0, false
	}
}

func (b *spannerBroadcaster) handleChildPartitionsRecord(ctx context.Context, cpr *csChildPartitionsRecord) {
	// Collect ready tokens under the lock, then enqueue without holding it.
	var ready []partitionWork

	b.mu.Lock()
	for _, cp := range cpr.ChildPartitions {
		token := cp.Token
		startTs := cpr.StartTimestamp

		// Skip tokens already queued to prevent duplicate reads.
		if _, enqueued := b.enqueuedChildren[token]; enqueued {
			continue
		}

		if _, exists := b.pendingChildren[token]; !exists {
			b.pendingChildren[token] = len(cp.ParentPartitionTokens)
		}
		b.pendingChildren[token]--

		if b.pendingChildren[token] <= 0 {
			delete(b.pendingChildren, token)
			b.enqueuedChildren[token] = struct{}{}
			ready = append(ready, partitionWork{token: &token, startTs: startTs})
		}
	}
	b.mu.Unlock()

	// Enqueue into the partition queue. The single runPartitionLoop goroutine
	// will read each child after the current partition's reader returns.
	for _, work := range ready {
		select {
		case b.partitionQueue <- work:
		case <-ctx.Done():
			return
		}
	}
}

func (b *spannerBroadcaster) reconstructObject(data []byte, binding resourceBinding) (client.Object, error) {
	if binding.scheme == nil || binding.gvk.Empty() {
		obj := &unstructured.Unstructured{}
		if err := json.Unmarshal(data, &obj.Object); err != nil {
			return nil, err
		}
		return obj, nil
	}

	obj, err := binding.scheme.New(binding.gvk)
	if err != nil {
		unstruct := &unstructured.Unstructured{}
		if err := json.Unmarshal(data, &unstruct.Object); err != nil {
			return nil, err
		}
		unstruct.SetGroupVersionKind(binding.gvk)
		return unstruct, nil
	}

	if err := json.Unmarshal(data, obj); err != nil {
		return nil, fmt.Errorf("failed to unmarshal into typed object: %w", err)
	}

	obj.GetObjectKind().SetGroupVersionKind(binding.gvk)

	clientObj, ok := obj.(client.Object)
	if !ok {
		return nil, fmt.Errorf("object does not implement client.Object")
	}

	return clientObj, nil
}

func (b *spannerBroadcaster) broadcastToSubscribers(resourceType string, event storage.ResourceEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()

	for id, sub := range b.subscribers {
		if sub.resourceType != resourceType {
			continue
		}
		select {
		case sub.ch <- event:
		default:
			// Channel full — cancel this watch so the controller knows to relist
			close(sub.ch)
			delete(b.subscribers, id)
		}
	}
}

func (b *spannerBroadcaster) Broadcast(event storage.ResourceEvent) {
}

func (b *spannerBroadcaster) Subscribe(sinceResourceVersion string) (<-chan storage.ResourceEvent, func(), error) {
	b.mu.RLock()
	var resourceType string
	for rt := range b.types {
		resourceType = rt
		break
	}
	b.mu.RUnlock()
	if resourceType == "" {
		return nil, nil, fmt.Errorf("no resource types registered")
	}
	return b.subscribeFor(resourceType, sinceResourceVersion)
}

// subscribeFor creates a watch for a specific resource type on the shared
// change stream watch.
func (b *spannerBroadcaster) subscribeFor(resourceType, sinceResourceVersion string) (<-chan storage.ResourceEvent, func(), error) {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil, nil, fmt.Errorf("broadcaster is closed")
	}

	binding, ok := b.types[resourceType]
	if !ok {
		b.mu.Unlock()
		return nil, nil, fmt.Errorf("resource type not registered: %s", resourceType)
	}

	id := b.nextID
	b.nextID++

	// Internal channel receives live events immediately
	liveCh := make(chan storage.ResourceEvent, 100)
	b.subscribers[id] = &subscriber{resourceType: resourceType, ch: liveCh}
	b.mu.Unlock()

	// Output channel returned to caller — events arrive in order
	outCh := make(chan storage.ResourceEvent, 100)

	go func() {
		defer close(outCh)

		var lastReplayedRV int64
		if sinceResourceVersion != "" {
			var ok bool
			lastReplayedRV, ok = b.sendHistoricalEvents(outCh, resourceType, binding, sinceResourceVersion)
			if !ok {
				b.unsubscribe(id)
				return
			}
		}

		for event := range liveCh {
			if lastReplayedRV > 0 {
				rv, _ := strconv.ParseInt(event.ResourceVersion, 10, 64)
				if rv <= lastReplayedRV {
					continue
				}
			}
			select {
			case outCh <- event:
			default:
				b.unsubscribe(id)
				return
			}
		}
	}()

	stopFunc := func() {
		b.unsubscribe(id)
	}

	return outCh, stopFunc, nil
}

// sendHistoricalEvents replays events since the given RV. Returns the last
// replayed RV (for deduplication against live events) and false if the output
// channel is full (watch should be cancelled).
func (b *spannerBroadcaster) sendHistoricalEvents(outCh chan storage.ResourceEvent, resourceType string, binding resourceBinding, sinceResourceVersion string) (int64, bool) {
	rv, err := strconv.ParseInt(sinceResourceVersion, 10, 64)
	if err != nil {
		return 0, true
	}

	stmt := spanner.Statement{
		SQL: fmt.Sprintf(
			"SELECT resource_version, data, context_filter FROM %s WHERE resource_type = @resourceType AND resource_version > @sinceRV ORDER BY resource_version ASC LIMIT 1000",
			b.tableName,
		),
		Params: map[string]any{
			"resourceType": resourceType,
			"sinceRV":      rv,
		},
	}

	iter := b.client.Single().Query(b.ctx, stmt)
	defer iter.Stop()

	var lastRV int64
	for {
		row, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return lastRV, true
		}

		var eventRV int64
		var objectDataJSON spanner.NullJSON
		var contextFilter string
		if err := row.Columns(&eventRV, &objectDataJSON, &contextFilter); err != nil {
			continue
		}

		objectDataBytes, err := json.Marshal(objectDataJSON.Value)
		if err != nil {
			continue
		}

		obj, err := b.reconstructObject(objectDataBytes, binding)
		if err != nil {
			continue
		}

		obj.SetResourceVersion(strconv.FormatInt(eventRV, 10))

		event := storage.ResourceEvent{
			Type:               storage.EventAdded,
			ResourceVersion:    strconv.FormatInt(eventRV, 10),
			Object:             obj,
			ContextFilterValue: contextFilter,
		}

		select {
		case outCh <- event:
			lastRV = eventRV
		default:
			return lastRV, false
		}
	}
	return lastRV, true
}

func (b *spannerBroadcaster) unsubscribe(id int) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if sub, exists := b.subscribers[id]; exists {
		close(sub.ch)
		delete(b.subscribers, id)
	}
}

func (b *spannerBroadcaster) Close() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}

	b.closed = true
	b.cancel()

	for id, sub := range b.subscribers {
		close(sub.ch)
		delete(b.subscribers, id)
	}
	b.mu.Unlock()

	b.wg.Wait()

	return nil
}

var _ storage.EventBroadcaster = (*spannerBroadcaster)(nil)
