package bigtable

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"cloud.google.com/go/bigtable"
	"github.com/openshift-online/gecko/orlop/pkg/apiserver/storage"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	familyData    = "d"
	familyCounter = "c"

	colJSON   = "json"
	colRV     = "rv"
	colLabels = "labels"
	colValue  = "v"
)

// BigtableStore implements storage.ResourceStore using Google Cloud Bigtable.
type BigtableStore struct {
	client           *bigtable.Client
	resourceType     string
	scheme           *runtime.Scheme
	gvk              schema.GroupVersionKind
	broadcaster      storage.EventBroadcaster
	resourceTable    *bigtable.Table
	counterTable     *bigtable.Table
	counterRowKey    string
	contextFilterKey any
}

// BigtableStoreConfig configures a Bigtable storage backend.
type BigtableStoreConfig struct {
	Client           *bigtable.Client
	ResourceType     string
	Scheme           *runtime.Scheme
	GVK              schema.GroupVersionKind
	Broadcaster      storage.EventBroadcaster
	ResourceTable    string
	CounterTable     string
	ContextFilterKey any
}

// NewBigtableStore creates a new Bigtable-backed resource store.
func NewBigtableStore(_ context.Context, config BigtableStoreConfig) (*BigtableStore, error) {
	if config.Client == nil {
		return nil, fmt.Errorf("bigtable client is required")
	}
	if config.ResourceType == "" {
		return nil, fmt.Errorf("resource type is required")
	}
	if config.Scheme == nil {
		return nil, fmt.Errorf("scheme is required")
	}

	resourceTableName := config.ResourceTable
	if resourceTableName == "" {
		resourceTableName = "resources_" + config.ResourceType
	}
	counterTableName := config.CounterTable
	if counterTableName == "" {
		counterTableName = "counters"
	}

	return &BigtableStore{
		client:           config.Client,
		resourceType:     config.ResourceType,
		scheme:           config.Scheme,
		gvk:              config.GVK,
		broadcaster:      config.Broadcaster,
		resourceTable:    config.Client.Open(resourceTableName),
		counterTable:     config.Client.Open(counterTableName),
		counterRowKey:    "rv_" + config.ResourceType,
		contextFilterKey: config.ContextFilterKey,
	}, nil
}

func (s *BigtableStore) nextResourceVersion(ctx context.Context) (int64, error) {
	rmw := bigtable.NewReadModifyWrite()
	rmw.Increment(familyCounter, colValue, 1)
	row, err := s.counterTable.ApplyReadModifyWrite(ctx, s.counterRowKey, rmw)
	if err != nil {
		return 0, fmt.Errorf("failed to increment resource version: %w", err)
	}
	cells := row[familyCounter]
	if len(cells) == 0 {
		return 0, fmt.Errorf("no counter value returned")
	}
	rv := int64(binary.BigEndian.Uint64(cells[0].Value))
	return rv, nil
}

func (s *BigtableStore) contextFilterValue(ctx context.Context) (string, error) {
	if s.contextFilterKey == nil {
		return "", nil
	}
	v := ctx.Value(s.contextFilterKey)
	if v == nil {
		return "", fmt.Errorf("context filter key %v not found in context", s.contextFilterKey)
	}
	str, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("context filter value must be a string, got %T", v)
	}
	return str, nil
}

// Create implements storage.ResourceStore.
func (s *BigtableStore) Create(ctx context.Context, obj client.Object) error {
	filterValue, err := s.contextFilterValue(ctx)
	if err != nil {
		return err
	}

	namespace := obj.GetNamespace()
	name := obj.GetName()
	useGenerateName := name == "" && obj.GetGenerateName() != ""

	maxAttempts := 1
	if useGenerateName {
		maxAttempts = 5
	}

	for attempt := range maxAttempts {
		if useGenerateName {
			name = storage.GenerateName(obj.GetGenerateName())
			obj.SetName(name)
		}

		now := time.Now()
		creationTime := obj.GetCreationTimestamp()
		if creationTime.IsZero() {
			obj.SetCreationTimestamp(metav1.NewTime(now))
		}

		rv, err := s.nextResourceVersion(ctx)
		if err != nil {
			return err
		}

		data, err := marshalData(obj)
		if err != nil {
			return err
		}

		labelsJSON, err := json.Marshal(obj.GetLabels())
		if err != nil {
			return fmt.Errorf("failed to marshal labels: %w", err)
		}

		rowKey := buildRowKey(filterValue, namespace, name)
		ts := bigtable.Now()

		mut := bigtable.NewMutation()
		mut.Set(familyData, colJSON, ts, data)
		mut.Set(familyData, colRV, ts, int64ToBytes(rv))
		mut.Set(familyData, colLabels, ts, labelsJSON)

		// Conditional insert: only apply if the row does not exist.
		condMut := bigtable.NewCondMutation(
			bigtable.CellsPerRowLimitFilter(1),
			nil, // row exists: do nothing
			mut, // row does not exist: insert
		)

		var matched bool
		if err := s.resourceTable.Apply(ctx, rowKey, condMut, bigtable.GetCondMutationResult(&matched)); err != nil {
			return fmt.Errorf("failed to apply conditional mutation: %w", err)
		}

		if matched {
			// Row already existed
			if useGenerateName && attempt < maxAttempts-1 {
				continue
			}
			return errors.NewAlreadyExists(
				schema.GroupResource{Resource: s.resourceType},
				name,
			)
		}

		obj.SetResourceVersion(strconv.FormatInt(rv, 10))

		if s.broadcaster != nil {
			s.broadcaster.Broadcast(storage.ResourceEvent{
				Type:               storage.EventAdded,
				Object:             obj.DeepCopyObject().(client.Object),
				ResourceVersion:    strconv.FormatInt(rv, 10),
				ContextFilterValue: filterValue,
			})
		}

		return nil
	}

	return fmt.Errorf("failed to generate unique name after retries")
}

// Get implements storage.ResourceStore.
func (s *BigtableStore) Get(ctx context.Context, namespace, name string) (client.Object, error) {
	filterValue, err := s.contextFilterValue(ctx)
	if err != nil {
		return nil, err
	}

	rowKey := buildRowKey(filterValue, namespace, name)
	row, err := s.resourceTable.ReadRow(ctx, rowKey, bigtable.RowFilter(bigtable.LatestNFilter(1)))
	if err != nil {
		return nil, fmt.Errorf("failed to read row: %w", err)
	}
	if row == nil {
		return nil, errors.NewNotFound(
			schema.GroupResource{Resource: s.resourceType},
			name,
		)
	}

	return rowToObject(row)
}

// List implements storage.ResourceStore.
func (s *BigtableStore) List(ctx context.Context, opts storage.ListOptions) (client.ObjectList, error) {
	filterValue, err := s.contextFilterValue(ctx)
	if err != nil {
		return nil, err
	}

	var labelSelector labels.Selector
	if opts.LabelSelector != "" {
		labelSelector, err = labels.Parse(opts.LabelSelector)
		if err != nil {
			return nil, err
		}
	}

	var continueToken *storage.ContinueToken
	if opts.Continue != "" {
		continueToken, err = storage.DecodeContinueToken(opts.Continue)
		if err != nil {
			return nil, fmt.Errorf("invalid continue token: %w", err)
		}
	}

	prefix := buildPrefixForList(filterValue, opts.Namespace)
	var rowSet bigtable.RowSet

	if continueToken != nil {
		afterKey := buildRowKey(filterValue, continueToken.Namespace, continueToken.Name)
		endKey := prefixSuccessor(prefix)
		if endKey == "" {
			rowSet = bigtable.InfiniteRange(afterKey + "\x00")
		} else {
			rowSet = bigtable.NewRange(afterKey+"\x00", endKey)
		}
	} else {
		rowSet = bigtable.PrefixRange(prefix)
	}

	var items []unstructured.Unstructured
	var maxRV int64
	limit := opts.Limit
	hasMore := false

	err = s.resourceTable.ReadRows(ctx, rowSet, func(row bigtable.Row) bool {
		obj, rv, parseErr := parseResourceRow(row)
		if parseErr != nil {
			return true
		}

		if labelSelector != nil && !labelSelector.Matches(labels.Set(obj.GetLabels())) {
			return true
		}

		if opts.ShardSelector != nil {
			matches, matchErr := storage.MatchesShard(obj, opts.ShardSelector)
			if matchErr != nil || !matches {
				return true
			}
		}

		if len(opts.FieldFilters) > 0 && !matchesFieldFilters(obj, opts.FieldFilters) {
			return true
		}

		if limit > 0 && int64(len(items)) >= limit {
			hasMore = true
			return false
		}

		items = append(items, *obj)
		if rv > maxRV {
			maxRV = rv
		}
		return true
	}, bigtable.RowFilter(bigtable.LatestNFilter(1)))
	if err != nil {
		return nil, fmt.Errorf("failed to read rows: %w", err)
	}

	listGVK := s.gvk.GroupVersion().WithKind(s.gvk.Kind + "List")
	listObj, err := s.scheme.New(listGVK)
	if err != nil {
		return nil, fmt.Errorf("failed to create list object: %w", err)
	}

	list := listObj.(*unstructured.UnstructuredList)
	list.SetResourceVersion(strconv.FormatInt(maxRV, 10))
	list.Items = items

	if hasMore && len(items) > 0 {
		listMeta, err := meta.ListAccessor(list)
		if err == nil {
			lastItem := &items[len(items)-1]
			token := &storage.ContinueToken{
				Namespace:       lastItem.GetNamespace(),
				Name:            lastItem.GetName(),
				ResourceVersion: strconv.FormatInt(maxRV, 10),
			}
			continueStr, encErr := storage.EncodeContinueToken(token)
			if encErr == nil {
				listMeta.SetContinue(continueStr)
			}
		}
	}

	return list, nil
}

// Update implements storage.ResourceStore.
func (s *BigtableStore) Update(ctx context.Context, obj client.Object) error {
	filterValue, err := s.contextFilterValue(ctx)
	if err != nil {
		return err
	}

	namespace := obj.GetNamespace()
	name := obj.GetName()

	_, err = s.Get(ctx, namespace, name)
	if err != nil {
		return err
	}

	rv, err := s.nextResourceVersion(ctx)
	if err != nil {
		return err
	}

	data, err := marshalData(obj)
	if err != nil {
		return err
	}

	labelsJSON, err := json.Marshal(obj.GetLabels())
	if err != nil {
		return fmt.Errorf("failed to marshal labels: %w", err)
	}

	rowKey := buildRowKey(filterValue, namespace, name)
	ts := bigtable.Now()

	mut := bigtable.NewMutation()
	mut.Set(familyData, colJSON, ts, data)
	mut.Set(familyData, colRV, ts, int64ToBytes(rv))
	mut.Set(familyData, colLabels, ts, labelsJSON)

	if err := s.resourceTable.Apply(ctx, rowKey, mut); err != nil {
		return fmt.Errorf("failed to update object: %w", err)
	}

	obj.SetResourceVersion(strconv.FormatInt(rv, 10))

	if s.broadcaster != nil {
		s.broadcaster.Broadcast(storage.ResourceEvent{
			Type:               storage.EventModified,
			Object:             obj.DeepCopyObject().(client.Object),
			ResourceVersion:    strconv.FormatInt(rv, 10),
			ContextFilterValue: filterValue,
		})
	}

	return nil
}

// Delete implements storage.ResourceStore.
func (s *BigtableStore) Delete(ctx context.Context, namespace, name string) error {
	filterValue, err := s.contextFilterValue(ctx)
	if err != nil {
		return err
	}

	obj, err := s.Get(ctx, namespace, name)
	if err != nil {
		return err
	}

	rv, err := s.nextResourceVersion(ctx)
	if err != nil {
		return err
	}

	rowKey := buildRowKey(filterValue, namespace, name)

	mut := bigtable.NewMutation()
	mut.DeleteRow()

	if err := s.resourceTable.Apply(ctx, rowKey, mut); err != nil {
		return fmt.Errorf("failed to delete object: %w", err)
	}

	if s.broadcaster != nil {
		s.broadcaster.Broadcast(storage.ResourceEvent{
			Type:               storage.EventDeleted,
			Object:             obj,
			ResourceVersion:    strconv.FormatInt(rv, 10),
			ContextFilterValue: filterValue,
		})
	}

	return nil
}

// Watch implements storage.ResourceStore.
func (s *BigtableStore) Watch(ctx context.Context, opts storage.ListOptions, resourceVersion string) (<-chan storage.ResourceEvent, func(), error) {
	filterValue, err := s.contextFilterValue(ctx)
	if err != nil {
		return nil, nil, err
	}

	if s.broadcaster == nil {
		return nil, nil, fmt.Errorf("broadcaster not configured")
	}

	eventCh, stopSubscription, err := s.broadcaster.Subscribe(resourceVersion)
	if err != nil {
		return nil, nil, err
	}

	outCh := make(chan storage.ResourceEvent, 100)
	stopCh := make(chan struct{})

	go func() {
		defer close(outCh)
		defer stopSubscription()

		var labelSelector labels.Selector
		if opts.LabelSelector != "" {
			var parseErr error
			labelSelector, parseErr = labels.Parse(opts.LabelSelector)
			if parseErr != nil {
				labelSelector = nil
			}
		}

		for {
			select {
			case <-stopCh:
				return
			case event, ok := <-eventCh:
				if !ok {
					return
				}

				if s.contextFilterKey != nil && event.ContextFilterValue != filterValue {
					continue
				}

				clientObj, ok := event.Object.(client.Object)
				if !ok {
					continue
				}

				if opts.Namespace != "" && clientObj.GetNamespace() != opts.Namespace {
					continue
				}

				if labelSelector != nil && !labelSelector.Matches(labels.Set(clientObj.GetLabels())) {
					continue
				}

				if opts.ShardSelector != nil {
					matches, matchErr := storage.MatchesShard(clientObj, opts.ShardSelector)
					if matchErr != nil || !matches {
						continue
					}
				}

				if len(opts.FieldFilters) > 0 && !matchesFieldFilters(clientObj, opts.FieldFilters) {
					continue
				}

				select {
				case outCh <- event:
				case <-stopCh:
					return
				}
			}
		}
	}()

	stopFunc := func() {
		close(stopCh)
	}

	return outCh, stopFunc, nil
}

// --- helpers ---

func buildRowKey(filterValue, namespace, name string) string {
	if filterValue != "" {
		return filterValue + "\x00" + namespace + "\x00" + name
	}
	return namespace + "\x00" + name
}

func buildPrefixForList(filterValue, namespace string) string {
	if filterValue != "" {
		if namespace != "" {
			return filterValue + "\x00" + namespace + "\x00"
		}
		return filterValue + "\x00"
	}
	if namespace != "" {
		return namespace + "\x00"
	}
	return ""
}

// prefixSuccessor returns the first key that is lexicographically after all keys
// with the given prefix. Returns "" if no successor exists (prefix is all 0xFF bytes).
func prefixSuccessor(prefix string) string {
	p := []byte(prefix)
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] < 0xFF {
			p[i]++
			return string(p[:i+1])
		}
	}
	return ""
}

func int64ToBytes(v int64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, uint64(v))
	return b
}

func bytesToInt64(b []byte) int64 {
	if len(b) < 8 {
		return 0
	}
	return int64(binary.BigEndian.Uint64(b))
}

func marshalData(obj client.Object) ([]byte, error) {
	rv := obj.GetResourceVersion()
	obj.SetResourceVersion("")
	data, err := json.Marshal(obj)
	obj.SetResourceVersion(rv)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal object: %w", err)
	}
	return data, nil
}

func unmarshalData(data []byte, rv int64) (*unstructured.Unstructured, error) {
	obj := &unstructured.Unstructured{}
	if err := json.Unmarshal(data, obj); err != nil {
		return nil, fmt.Errorf("failed to unmarshal object: %w", err)
	}
	obj.SetResourceVersion(strconv.FormatInt(rv, 10))
	return obj, nil
}

func rowToObject(row bigtable.Row) (client.Object, error) {
	cells := row[familyData]
	var data []byte
	var rv int64
	for _, cell := range cells {
		switch cell.Column {
		case familyData + ":" + colJSON:
			data = cell.Value
		case familyData + ":" + colRV:
			rv = bytesToInt64(cell.Value)
		}
	}
	if data == nil {
		return nil, fmt.Errorf("no data column found in row")
	}
	return unmarshalData(data, rv)
}

func parseResourceRow(row bigtable.Row) (*unstructured.Unstructured, int64, error) {
	cells := row[familyData]
	var data []byte
	var rv int64
	for _, cell := range cells {
		switch cell.Column {
		case familyData + ":" + colJSON:
			data = cell.Value
		case familyData + ":" + colRV:
			rv = bytesToInt64(cell.Value)
		}
	}
	if data == nil {
		return nil, 0, fmt.Errorf("no data column found in row")
	}
	obj, err := unmarshalData(data, rv)
	if err != nil {
		return nil, 0, err
	}
	return obj, rv, nil
}

func padResourceVersion(rv int64) string {
	return fmt.Sprintf("%020d", rv)
}

func matchesFieldFilters(obj client.Object, filters map[string]string) bool {
	data, err := json.Marshal(obj)
	if err != nil {
		return false
	}
	var objMap map[string]interface{}
	if err := json.Unmarshal(data, &objMap); err != nil {
		return false
	}
	for path, expected := range filters {
		if fieldValueFromMap(objMap, path) != expected {
			return false
		}
	}
	return true
}

func fieldValueFromMap(m map[string]interface{}, path string) string {
	parts := strings.Split(path, ".")
	current := interface{}(m)
	for _, part := range parts {
		cm, ok := current.(map[string]interface{})
		if !ok {
			return ""
		}
		current = cm[part]
	}
	s, _ := current.(string)
	return s
}

var _ storage.ResourceStore = (*BigtableStore)(nil)
