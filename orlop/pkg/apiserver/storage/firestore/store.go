package firestore

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/openshift-online/gecko/orlop/pkg/apiserver/storage"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type FirestoreStore struct {
	client           *firestore.Client
	resourceType     string
	scheme           *runtime.Scheme
	gvk              schema.GroupVersionKind
	broadcaster      storage.EventBroadcaster
	resourcesColl    string
	countersColl     string
	counterDocID     string
	contextFilterKey any
}

type FirestoreStoreConfig struct {
	Client           *firestore.Client
	ResourceType     string
	Scheme           *runtime.Scheme
	GVK              schema.GroupVersionKind
	Broadcaster      storage.EventBroadcaster
	ResourcesColl    string
	CountersColl     string
	ContextFilterKey any
}

func NewFirestoreStore(_ context.Context, config FirestoreStoreConfig) (*FirestoreStore, error) {
	if config.Client == nil {
		return nil, fmt.Errorf("firestore client is required")
	}
	if config.ResourceType == "" {
		return nil, fmt.Errorf("resource type is required")
	}
	if config.Scheme == nil {
		return nil, fmt.Errorf("scheme is required")
	}

	resourcesColl := config.ResourcesColl
	if resourcesColl == "" {
		resourcesColl = "resources_" + config.ResourceType
	}
	countersColl := config.CountersColl
	if countersColl == "" {
		countersColl = "counters"
	}

	return &FirestoreStore{
		client:           config.Client,
		resourceType:     config.ResourceType,
		scheme:           config.Scheme,
		gvk:              config.GVK,
		broadcaster:      config.Broadcaster,
		resourcesColl:    resourcesColl,
		countersColl:     countersColl,
		counterDocID:     "rv_" + config.ResourceType,
		contextFilterKey: config.ContextFilterKey,
	}, nil
}

func (s *FirestoreStore) nextResourceVersion(ctx context.Context) (int64, error) {
	counterRef := s.client.Collection(s.countersColl).Doc(s.counterDocID)
	var rv int64

	err := s.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		snap, err := tx.Get(counterRef)
		if err != nil {
			if status.Code(err) == codes.NotFound {
				rv = 1
				return tx.Create(counterRef, map[string]any{"value": int64(1)})
			}
			return err
		}
		current, err := snap.DataAt("value")
		if err != nil {
			return fmt.Errorf("failed to read counter value: %w", err)
		}
		rv = current.(int64) + 1
		return tx.Set(counterRef, map[string]any{"value": rv})
	})
	if err != nil {
		return 0, fmt.Errorf("failed to increment resource version: %w", err)
	}
	return rv, nil
}

func (s *FirestoreStore) contextFilterValue(ctx context.Context) (string, error) {
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

func (s *FirestoreStore) Create(ctx context.Context, obj client.Object) error {
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

		creationTime := obj.GetCreationTimestamp()
		if creationTime.IsZero() {
			obj.SetCreationTimestamp(metav1.NewTime(time.Now()))
		}

		rv, err := s.nextResourceVersion(ctx)
		if err != nil {
			return err
		}

		data, err := marshalData(obj)
		if err != nil {
			return err
		}

		docID := buildDocID(filterValue, namespace, name)
		docRef := s.client.Collection(s.resourcesColl).Doc(docID)

		doc := map[string]any{
			"data":          data,
			"rv":            rv,
			"labels":        obj.GetLabels(),
			"namespace":     namespace,
			"name":          name,
			"contextFilter": filterValue,
			"createdAt":     time.Now(),
		}

		_, err = docRef.Create(ctx, doc)
		if err != nil {
			if status.Code(err) == codes.AlreadyExists {
				if useGenerateName && attempt < maxAttempts-1 {
					continue
				}
				return errors.NewAlreadyExists(
					schema.GroupResource{Resource: s.resourceType},
					name,
				)
			}
			return fmt.Errorf("failed to create document: %w", err)
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

func (s *FirestoreStore) Get(ctx context.Context, namespace, name string) (client.Object, error) {
	filterValue, err := s.contextFilterValue(ctx)
	if err != nil {
		return nil, err
	}

	docID := buildDocID(filterValue, namespace, name)
	snap, err := s.client.Collection(s.resourcesColl).Doc(docID).Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, errors.NewNotFound(
				schema.GroupResource{Resource: s.resourceType},
				name,
			)
		}
		return nil, fmt.Errorf("failed to get document: %w", err)
	}

	return snapToObject(snap)
}

func (s *FirestoreStore) List(ctx context.Context, opts storage.ListOptions) (client.ObjectList, error) {
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

	q := s.client.Collection(s.resourcesColl).Query

	if filterValue != "" {
		q = q.Where("contextFilter", "==", filterValue)
	}

	if opts.Namespace != "" {
		q = q.Where("namespace", "==", opts.Namespace)
	}

	q = q.OrderBy("namespace", firestore.Asc).OrderBy("name", firestore.Asc)

	if continueToken != nil {
		q = q.StartAfter(continueToken.Namespace, continueToken.Name)
	}

	if opts.Limit > 0 {
		q = q.Limit(int(opts.Limit) + 1)
	}

	docs, err := q.Documents(ctx).GetAll()
	if err != nil {
		return nil, fmt.Errorf("failed to list documents: %w", err)
	}

	var items []unstructured.Unstructured
	var maxRV int64
	limit := opts.Limit
	hasMore := false

	for _, snap := range docs {
		obj, rv, parseErr := parseResourceSnap(snap)
		if parseErr != nil {
			continue
		}

		if labelSelector != nil && !labelSelector.Matches(labels.Set(obj.GetLabels())) {
			continue
		}

		if opts.ShardSelector != nil {
			matches, matchErr := storage.MatchesShard(obj, opts.ShardSelector)
			if matchErr != nil || !matches {
				continue
			}
		}

		if len(opts.FieldFilters) > 0 && !matchesFieldFilters(obj, opts.FieldFilters) {
			continue
		}

		if limit > 0 && int64(len(items)) >= limit {
			hasMore = true
			break
		}

		items = append(items, *obj)
		if rv > maxRV {
			maxRV = rv
		}
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

func (s *FirestoreStore) Update(ctx context.Context, obj client.Object) error {
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

	docID := buildDocID(filterValue, namespace, name)
	docRef := s.client.Collection(s.resourcesColl).Doc(docID)

	_, err = docRef.Set(ctx, map[string]any{
		"data":          data,
		"rv":            rv,
		"labels":        obj.GetLabels(),
		"namespace":     namespace,
		"name":          name,
		"contextFilter": filterValue,
		"createdAt":     time.Now(),
	})
	if err != nil {
		return fmt.Errorf("failed to update document: %w", err)
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

func (s *FirestoreStore) Delete(ctx context.Context, namespace, name string) error {
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

	docID := buildDocID(filterValue, namespace, name)
	docRef := s.client.Collection(s.resourcesColl).Doc(docID)

	_, err = docRef.Delete(ctx)
	if err != nil {
		return fmt.Errorf("failed to delete document: %w", err)
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

func (s *FirestoreStore) Watch(ctx context.Context, opts storage.ListOptions, resourceVersion string) (<-chan storage.ResourceEvent, func(), error) {
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

func buildDocID(filterValue, namespace, name string) string {
	if filterValue != "" {
		return filterValue + "_" + namespace + "_" + name
	}
	return namespace + "_" + name
}

func marshalData(obj client.Object) (map[string]any, error) {
	rv := obj.GetResourceVersion()
	obj.SetResourceVersion("")
	data, err := json.Marshal(obj)
	obj.SetResourceVersion(rv)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal object: %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("failed to unmarshal to map: %w", err)
	}
	return m, nil
}

func unmarshalData(data map[string]any, rv int64) (*unstructured.Unstructured, error) {
	obj := &unstructured.Unstructured{Object: data}
	obj.SetResourceVersion(strconv.FormatInt(rv, 10))
	return obj, nil
}

func snapToObject(snap *firestore.DocumentSnapshot) (client.Object, error) {
	docData := snap.Data()
	rawData, ok := docData["data"]
	if !ok {
		return nil, fmt.Errorf("no data field in document")
	}

	dataMap, ok := rawData.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("data field is not a map")
	}

	var rv int64
	if rvVal, ok := docData["rv"]; ok {
		rv = toInt64(rvVal)
	}

	return unmarshalData(dataMap, rv)
}

func parseResourceSnap(snap *firestore.DocumentSnapshot) (*unstructured.Unstructured, int64, error) {
	docData := snap.Data()
	rawData, ok := docData["data"]
	if !ok {
		return nil, 0, fmt.Errorf("no data field in document")
	}

	dataMap, ok := rawData.(map[string]any)
	if !ok {
		return nil, 0, fmt.Errorf("data field is not a map")
	}

	var rv int64
	if rvVal, ok := docData["rv"]; ok {
		rv = toInt64(rvVal)
	}

	obj, err := unmarshalData(dataMap, rv)
	if err != nil {
		return nil, 0, err
	}
	return obj, rv, nil
}

func toInt64(v any) int64 {
	switch val := v.(type) {
	case int64:
		return val
	case float64:
		return int64(val)
	case int:
		return int64(val)
	default:
		return 0
	}
}

func padResourceVersion(rv int64) string {
	return fmt.Sprintf("%020d", rv)
}

func matchesFieldFilters(obj client.Object, filters map[string]string) bool {
	data, err := json.Marshal(obj)
	if err != nil {
		return false
	}
	var objMap map[string]any
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

func fieldValueFromMap(m map[string]any, path string) string {
	parts := strings.Split(path, ".")
	current := any(m)
	for _, part := range parts {
		cm, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current = cm[part]
	}
	s, _ := current.(string)
	return s
}

var _ storage.ResourceStore = (*FirestoreStore)(nil)
