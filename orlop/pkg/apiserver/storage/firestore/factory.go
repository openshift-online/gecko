package firestore

import (
	"context"
	"fmt"
	"strings"

	"cloud.google.com/go/firestore"
	"github.com/openshift-online/gecko/orlop/pkg/apiserver/storage"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type StorageFactoryConfig struct {
	ProjectID        string
	Client           *firestore.Client
	CollectionPrefix string
	Context          context.Context
}

func NewStorageFactory(config StorageFactoryConfig) func(string, *runtime.Scheme, schema.GroupVersionKind) (storage.ResourceStore, error) {
	return func(resourceType string, scheme *runtime.Scheme, gvk schema.GroupVersionKind) (storage.ResourceStore, error) {
		ctx := config.Context
		if ctx == nil {
			ctx = context.Background()
		}

		fsClient := config.Client
		var err error

		if fsClient == nil {
			fsClient, err = firestore.NewClient(ctx, config.ProjectID)
			if err != nil {
				return nil, fmt.Errorf("failed to create firestore client: %w", err)
			}
		}

		prefix := config.CollectionPrefix
		safeType := sanitizeCollectionName(resourceType)
		resourcesColl := prefix + "resources_" + safeType
		eventLogColl := prefix + "eventlog_" + safeType
		countersColl := prefix + "counters"

		broadcaster, err := NewFirestoreBroadcaster(ctx, FirestoreBroadcasterConfig{
			Client:       fsClient,
			EventLogColl: eventLogColl,
			Scheme:       scheme,
			GVK:          gvk,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create broadcaster: %w", err)
		}

		store, err := NewFirestoreStore(ctx, FirestoreStoreConfig{
			Client:        fsClient,
			ResourceType:  resourceType,
			Scheme:        scheme,
			GVK:           gvk,
			Broadcaster:   broadcaster,
			ResourcesColl: resourcesColl,
			CountersColl:  countersColl,
		})
		if err != nil {
			broadcaster.Close()
			return nil, fmt.Errorf("failed to create store: %w", err)
		}

		return store, nil
	}
}

func sanitizeCollectionName(name string) string {
	return strings.ReplaceAll(name, ".", "-")
}
