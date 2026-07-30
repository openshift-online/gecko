package bigtable

import (
	"context"
	"fmt"
	"strings"
	"time"

	bt "cloud.google.com/go/bigtable"
	btpb "cloud.google.com/go/bigtable/apiv2/bigtablepb"
	"github.com/openshift-online/gecko/orlop/pkg/apiserver/storage"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/oauth"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"golang.org/x/oauth2/google"
)

// StorageFactoryConfig configures the Bigtable storage factory.
type StorageFactoryConfig struct {
	ProjectID     string
	InstanceID    string
	Client        *bt.Client
	AdminClient   *bt.AdminClient
	GRPCClient    btpb.BigtableClient
	ClientOptions []option.ClientOption
	Context       context.Context
	TablePrefix   string
}

// NewStorageFactory creates a storage factory that uses Bigtable for all resources.
func NewStorageFactory(config StorageFactoryConfig) func(string, *runtime.Scheme, schema.GroupVersionKind) (storage.ResourceStore, error) {
	return func(resourceType string, scheme *runtime.Scheme, gvk schema.GroupVersionKind) (storage.ResourceStore, error) {
		ctx := config.Context
		if ctx == nil {
			ctx = context.Background()
		}

		client := config.Client
		adminClient := config.AdminClient
		grpcClient := config.GRPCClient
		var err error

		if client == nil {
			client, err = bt.NewClient(ctx, config.ProjectID, config.InstanceID, config.ClientOptions...)
			if err != nil {
				return nil, fmt.Errorf("failed to create bigtable client: %w", err)
			}
		}

		if adminClient == nil {
			adminClient, err = bt.NewAdminClient(ctx, config.ProjectID, config.InstanceID, config.ClientOptions...)
			if err != nil {
				return nil, fmt.Errorf("failed to create bigtable admin client: %w", err)
			}
		}

		if grpcClient == nil {
			grpcClient, err = newBigtableGRPCClient(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to create bigtable gRPC client: %w", err)
			}
		}

		prefix := config.TablePrefix
		safeType := sanitizeTableName(resourceType)
		resourceTableName := prefix + "resources_" + safeType
		eventLogTableName := prefix + "eventlog_" + safeType
		counterTableName := prefix + "counters"

		if err := ensureTable(ctx, adminClient, resourceTableName, map[string]bt.GCPolicy{
			familyData: bt.MaxVersionsPolicy(1),
		}); err != nil {
			return nil, fmt.Errorf("failed to ensure resource table %s: %w", resourceTableName, err)
		}

		if err := ensureTable(ctx, adminClient, eventLogTableName, map[string]bt.GCPolicy{
			familyEvent: bt.MaxAgePolicy(7 * 24 * time.Hour),
		}); err != nil {
			return nil, fmt.Errorf("failed to ensure event log table %s: %w", eventLogTableName, err)
		}

		if err := ensureTable(ctx, adminClient, counterTableName, map[string]bt.GCPolicy{
			familyCounter: bt.MaxVersionsPolicy(1),
		}); err != nil {
			return nil, fmt.Errorf("failed to ensure counter table %s: %w", counterTableName, err)
		}

		tablePath := fmt.Sprintf("projects/%s/instances/%s/tables/%s",
			config.ProjectID, config.InstanceID, eventLogTableName)

		broadcaster, err := NewBigtableBroadcaster(ctx, BigtableBroadcasterConfig{
			DataClient:    client,
			GRPCClient:    grpcClient,
			EventLogTable: eventLogTableName,
			TablePath:     tablePath,
			Scheme:        scheme,
			GVK:           gvk,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create broadcaster: %w", err)
		}

		store, err := NewBigtableStore(ctx, BigtableStoreConfig{
			Client:        client,
			ResourceType:  resourceType,
			Scheme:        scheme,
			GVK:           gvk,
			Broadcaster:   broadcaster,
			ResourceTable: resourceTableName,
			CounterTable:  counterTableName,
		})
		if err != nil {
			broadcaster.Close()
			return nil, fmt.Errorf("failed to create store: %w", err)
		}

		return store, nil
	}
}

func newBigtableGRPCClient(ctx context.Context) (btpb.BigtableClient, error) {
	creds, err := google.FindDefaultCredentials(ctx, "https://www.googleapis.com/auth/bigtable.data")
	if err != nil {
		return nil, fmt.Errorf("failed to find default credentials: %w", err)
	}

	conn, err := grpc.NewClient(
		"bigtable.googleapis.com:443",
		grpc.WithPerRPCCredentials(oauth.TokenSource{TokenSource: creds.TokenSource}),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create gRPC connection: %w", err)
	}

	return btpb.NewBigtableClient(conn), nil
}

func ensureTable(ctx context.Context, admin *bt.AdminClient, tableName string, families map[string]bt.GCPolicy) error {
	tables, err := admin.Tables(ctx)
	if err != nil {
		return fmt.Errorf("failed to list tables: %w", err)
	}

	exists := false
	for _, t := range tables {
		if t == tableName {
			exists = true
			break
		}
	}

	if !exists {
		if err := admin.CreateTable(ctx, tableName); err != nil {
			return fmt.Errorf("failed to create table %s: %w", tableName, err)
		}
	}

	tableInfo, err := admin.TableInfo(ctx, tableName)
	if err != nil {
		return fmt.Errorf("failed to get table info for %s: %w", tableName, err)
	}

	existingFamilies := make(map[string]bool)
	for _, fi := range tableInfo.FamilyInfos {
		existingFamilies[fi.Name] = true
	}

	for family, gcPolicy := range families {
		if existingFamilies[family] {
			continue
		}
		if err := admin.CreateColumnFamily(ctx, tableName, family); err != nil {
			return fmt.Errorf("failed to create column family %s on table %s: %w", family, tableName, err)
		}
		if err := admin.SetGCPolicy(ctx, tableName, family, gcPolicy); err != nil {
			return fmt.Errorf("failed to set GC policy on %s:%s: %w", tableName, family, err)
		}
	}

	return nil
}

func sanitizeTableName(name string) string {
	return strings.ReplaceAll(name, ".", "-")
}
