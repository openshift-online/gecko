package bigtable

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	bt "cloud.google.com/go/bigtable"
	"github.com/openshift-online/gecko/orlop/pkg/apiserver/storage"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type objectOption func(*unstructured.Unstructured)

func newTestObject(opts ...objectOption) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "test.example.com/v1",
			"kind":       "TestObject",
			"metadata": map[string]any{
				"name":      "test",
				"namespace": "default",
			},
		},
	}
	for _, opt := range opts {
		opt(obj)
	}
	return obj
}

func withName(name string) objectOption {
	return func(obj *unstructured.Unstructured) { obj.SetName(name) }
}

func withNamespace(namespace string) objectOption {
	return func(obj *unstructured.Unstructured) { obj.SetNamespace(namespace) }
}

func withLabels(labels map[string]string) objectOption {
	return func(obj *unstructured.Unstructured) { obj.SetLabels(labels) }
}

func withSpec(spec map[string]any) objectOption {
	return func(obj *unstructured.Unstructured) { obj.Object["spec"] = spec }
}

func withGenerateName(generateName string) objectOption {
	return func(obj *unstructured.Unstructured) {
		obj.SetGenerateName(generateName)
		obj.SetName("")
	}
}

func testScheme() (*runtime.Scheme, schema.GroupVersionKind) {
	scheme := runtime.NewScheme()
	gv := schema.GroupVersion{Group: "test.example.com", Version: "v1"}
	scheme.AddKnownTypeWithName(gv.WithKind("TestObject"), &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(gv.WithKind("TestObjectList"), &unstructured.UnstructuredList{})
	gvk := schema.GroupVersionKind{Group: "test.example.com", Version: "v1", Kind: "TestObject"}
	return scheme, gvk
}

func setupTestStore(t *testing.T) (*BigtableStore, func()) {
	t.Helper()

	emulatorHost := os.Getenv("BIGTABLE_EMULATOR_HOST")
	if emulatorHost == "" {
		t.Skip("BIGTABLE_EMULATOR_HOST not set, skipping bigtable integration test")
	}

	ctx := context.Background()
	project := "test-project"
	instance := "test-instance"

	adminClient, err := bt.NewAdminClient(ctx, project, instance)
	if err != nil {
		t.Fatalf("Failed to create admin client: %v", err)
	}

	dataClient, err := bt.NewClient(ctx, project, instance)
	if err != nil {
		adminClient.Close()
		t.Fatalf("Failed to create data client: %v", err)
	}

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	resourceTable := "resources_test_" + suffix
	counterTable := "counters_test_" + suffix

	for tbl, families := range map[string]map[string]bt.GCPolicy{
		resourceTable: {familyData: bt.MaxVersionsPolicy(1)},
		counterTable:  {familyCounter: bt.MaxVersionsPolicy(1)},
	} {
		if err := ensureTable(ctx, adminClient, tbl, families); err != nil {
			adminClient.Close()
			dataClient.Close()
			t.Fatalf("Failed to ensure table %s: %v", tbl, err)
		}
	}

	scheme, gvk := testScheme()

	store, err := NewBigtableStore(ctx, BigtableStoreConfig{
		Client:        dataClient,
		ResourceType:  "testobjects",
		Scheme:        scheme,
		GVK:           gvk,
		ResourceTable: resourceTable,
		CounterTable:  counterTable,
	})
	if err != nil {
		adminClient.Close()
		dataClient.Close()
		t.Fatalf("Failed to create store: %v", err)
	}

	cleanup := func() {
		adminClient.DeleteTable(ctx, resourceTable)
		adminClient.DeleteTable(ctx, counterTable)
		adminClient.Close()
		dataClient.Close()
	}

	return store, cleanup
}

func TestBigtableStore_Create(t *testing.T) {
	t.Run("creates new object with resourceVersion", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		obj := newTestObject(withName("test-obj"), withNamespace("default"))

		err := store.Create(context.Background(), obj)
		if err != nil {
			t.Fatalf("Create() failed: %v", err)
		}

		retrieved, err := store.Get(context.Background(), "default", "test-obj")
		if err != nil {
			t.Fatalf("Get() failed: %v", err)
		}

		if retrieved.GetResourceVersion() == "" {
			t.Error("Created object missing resourceVersion")
		}
		if _, err := strconv.ParseInt(retrieved.GetResourceVersion(), 10, 64); err != nil {
			t.Errorf("resourceVersion is not a valid integer: %s", retrieved.GetResourceVersion())
		}
	})

	t.Run("returns error for duplicate", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		obj := newTestObject(withName("duplicate"), withNamespace("default"))

		store.Create(context.Background(), obj)
		err := store.Create(context.Background(), obj)

		if err == nil {
			t.Error("Expected error for duplicate object, got nil")
		}
	})

	t.Run("creates in different namespaces", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		obj1 := newTestObject(withName("obj"), withNamespace("ns1"))
		obj2 := newTestObject(withName("obj"), withNamespace("ns2"))

		if err := store.Create(context.Background(), obj1); err != nil {
			t.Errorf("Create in ns1 failed: %v", err)
		}
		if err := store.Create(context.Background(), obj2); err != nil {
			t.Errorf("Create in ns2 failed: %v", err)
		}

		if _, err := store.Get(context.Background(), "ns1", "obj"); err != nil {
			t.Error("Object in ns1 not found")
		}
		if _, err := store.Get(context.Background(), "ns2", "obj"); err != nil {
			t.Error("Object in ns2 not found")
		}
	})

	t.Run("generateName produces a unique name", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		obj := newTestObject(withGenerateName("gen-"), withNamespace("default"))

		err := store.Create(context.Background(), obj)
		if err != nil {
			t.Fatalf("Create() with generateName failed: %v", err)
		}

		name := obj.GetName()
		if name == "" {
			t.Fatal("Name was not set after Create with generateName")
		}
		if len(name) < len("gen-")+5 {
			t.Errorf("Generated name too short: %q", name)
		}

		retrieved, err := store.Get(context.Background(), "default", name)
		if err != nil {
			t.Fatalf("Get() by generated name failed: %v", err)
		}
		if retrieved.GetName() != name {
			t.Errorf("Retrieved name %q != generated name %q", retrieved.GetName(), name)
		}
	})

	t.Run("generateName creates distinct names", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		seen := make(map[string]bool)

		for range 20 {
			obj := newTestObject(withGenerateName("multi-"), withNamespace("default"))
			if err := store.Create(context.Background(), obj); err != nil {
				t.Fatalf("Create() failed: %v", err)
			}
			name := obj.GetName()
			if seen[name] {
				t.Fatalf("Duplicate generated name: %q", name)
			}
			seen[name] = true
		}

		listObj, _ := store.List(context.Background(), storage.ListOptions{Namespace: "default"})
		list := listObj.(*unstructured.UnstructuredList)
		if len(list.Items) != 20 {
			t.Errorf("Expected 20 objects, got %d", len(list.Items))
		}
	})

	t.Run("sets creation timestamp", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		obj := newTestObject(withName("test"), withNamespace("default"))
		if err := store.Create(context.Background(), obj); err != nil {
			t.Fatalf("Create() failed: %v", err)
		}

		retrieved, err := store.Get(context.Background(), "default", "test")
		if err != nil {
			t.Fatalf("Get() failed: %v", err)
		}
		creationTime := retrieved.GetCreationTimestamp()
		if creationTime.IsZero() {
			t.Error("Creation timestamp not set")
		}
	})
}

func TestBigtableStore_Get(t *testing.T) {
	t.Run("gets existing object", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		obj := newTestObject(withName("test"), withNamespace("default"))
		store.Create(context.Background(), obj)

		retrieved, err := store.Get(context.Background(), "default", "test")
		if err != nil {
			t.Fatalf("Get() failed: %v", err)
		}
		if retrieved.GetName() != "test" {
			t.Errorf("Got wrong object: %s", retrieved.GetName())
		}
	})

	t.Run("returns error for non-existent object", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		_, err := store.Get(context.Background(), "default", "missing")
		if err == nil {
			t.Error("Expected error for missing object, got nil")
		}
	})

	t.Run("returns error for wrong namespace", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		obj := newTestObject(withName("test"), withNamespace("default"))
		store.Create(context.Background(), obj)

		_, err := store.Get(context.Background(), "kube-system", "test")
		if err == nil {
			t.Error("Expected error for wrong namespace, got nil")
		}
	})
}

func TestBigtableStore_List(t *testing.T) {
	t.Run("lists objects in namespace", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		store.Create(context.Background(), newTestObject(withName("obj1"), withNamespace("default")))
		store.Create(context.Background(), newTestObject(withName("obj2"), withNamespace("default")))
		store.Create(context.Background(), newTestObject(withName("obj3"), withNamespace("kube-system")))

		listObj, err := store.List(context.Background(), storage.ListOptions{Namespace: "default"})
		if err != nil {
			t.Fatalf("List() failed: %v", err)
		}

		list := listObj.(*unstructured.UnstructuredList)
		if len(list.Items) != 2 {
			t.Errorf("Expected 2 objects, got %d", len(list.Items))
		}
	})

	t.Run("lists all namespaces", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		store.Create(context.Background(), newTestObject(withName("obj1"), withNamespace("default")))
		store.Create(context.Background(), newTestObject(withName("obj2"), withNamespace("kube-system")))
		store.Create(context.Background(), newTestObject(withName("obj3"), withNamespace("kube-public")))

		listObj, err := store.List(context.Background(), storage.ListOptions{})
		if err != nil {
			t.Fatalf("List() failed: %v", err)
		}

		list := listObj.(*unstructured.UnstructuredList)
		if len(list.Items) != 3 {
			t.Errorf("Expected 3 objects, got %d", len(list.Items))
		}
	})

	t.Run("returns empty list for empty store", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		listObj, err := store.List(context.Background(), storage.ListOptions{Namespace: "default"})
		if err != nil {
			t.Fatalf("List() failed: %v", err)
		}

		list := listObj.(*unstructured.UnstructuredList)
		if len(list.Items) != 0 {
			t.Errorf("Expected empty list, got %d objects", len(list.Items))
		}
	})

	t.Run("sets resourceVersion on list", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		store.Create(context.Background(), newTestObject(withName("obj1"), withNamespace("default")))
		store.Create(context.Background(), newTestObject(withName("obj2"), withNamespace("default")))

		listObj, _ := store.List(context.Background(), storage.ListOptions{Namespace: "default"})
		list := listObj.(*unstructured.UnstructuredList)

		if list.GetResourceVersion() == "" {
			t.Error("List resourceVersion not set")
		}
	})

	t.Run("filters by label selector", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		store.Create(context.Background(), newTestObject(withName("a"), withNamespace("default"), withLabels(map[string]string{"env": "prod"})))
		store.Create(context.Background(), newTestObject(withName("b"), withNamespace("default"), withLabels(map[string]string{"env": "dev"})))
		store.Create(context.Background(), newTestObject(withName("c"), withNamespace("default"), withLabels(map[string]string{"env": "prod"})))

		opts := storage.ListOptions{Namespace: "default"}
		opts.LabelSelector = "env=prod"
		filteredObj, err := store.List(context.Background(), opts)
		if err != nil {
			t.Fatalf("List() with label selector failed: %v", err)
		}

		filtered := filteredObj.(*unstructured.UnstructuredList)
		if len(filtered.Items) != 2 {
			t.Errorf("Expected 2 objects with env=prod, got %d", len(filtered.Items))
		}
	})

	t.Run("pagination with limit and continue", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		for i := range 5 {
			store.Create(context.Background(), newTestObject(
				withName(fmt.Sprintf("obj-%02d", i)),
				withNamespace("default"),
			))
		}

		opts := storage.ListOptions{Namespace: "default"}
		opts.Limit = 2
		page1, err := store.List(context.Background(), opts)
		if err != nil {
			t.Fatalf("List page 1 failed: %v", err)
		}

		list1 := page1.(*unstructured.UnstructuredList)
		if len(list1.Items) != 2 {
			t.Fatalf("Expected 2 items in page 1, got %d", len(list1.Items))
		}

		continueToken := list1.GetContinue()
		if continueToken == "" {
			t.Fatal("Expected continue token for page 1")
		}

		opts2 := storage.ListOptions{Namespace: "default"}
		opts2.Limit = 2
		opts2.Continue = continueToken
		page2, err := store.List(context.Background(), opts2)
		if err != nil {
			t.Fatalf("List page 2 failed: %v", err)
		}

		list2 := page2.(*unstructured.UnstructuredList)
		if len(list2.Items) != 2 {
			t.Fatalf("Expected 2 items in page 2, got %d", len(list2.Items))
		}

		// Page 2 items should be different from page 1
		for _, item1 := range list1.Items {
			for _, item2 := range list2.Items {
				if item1.GetName() == item2.GetName() {
					t.Errorf("Page 2 contains item from page 1: %s", item1.GetName())
				}
			}
		}
	})
}

func TestBigtableStore_Update(t *testing.T) {
	t.Run("updates existing object", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		obj := newTestObject(
			withName("test"),
			withNamespace("default"),
			withSpec(map[string]any{"field": "original"}),
		)
		store.Create(context.Background(), obj)

		retrieved, _ := store.Get(context.Background(), "default", "test")
		updated := retrieved.DeepCopyObject().(client.Object)
		u := updated.(*unstructured.Unstructured)
		u.Object["spec"] = map[string]any{"field": "updated"}

		err := store.Update(context.Background(), updated)
		if err != nil {
			t.Fatalf("Update() failed: %v", err)
		}

		final, _ := store.Get(context.Background(), "default", "test")
		finalU := final.(*unstructured.Unstructured)
		spec := finalU.Object["spec"].(map[string]any)
		if spec["field"] != "updated" {
			t.Errorf("Update did not persist: got %v", spec["field"])
		}
	})

	t.Run("increments resourceVersion", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		obj := newTestObject(withName("test"), withNamespace("default"))
		store.Create(context.Background(), obj)

		retrieved, _ := store.Get(context.Background(), "default", "test")
		initialRV := retrieved.GetResourceVersion()

		store.Update(context.Background(), retrieved)

		updated, _ := store.Get(context.Background(), "default", "test")
		if updated.GetResourceVersion() == initialRV {
			t.Error("ResourceVersion not incremented after update")
		}
	})

	t.Run("returns error for non-existent object", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		obj := newTestObject(withName("missing"), withNamespace("default"))

		err := store.Update(context.Background(), obj)
		if err == nil {
			t.Error("Expected error for missing object, got nil")
		}
	})
}

func TestBigtableStore_Delete(t *testing.T) {
	t.Run("deletes existing object", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		obj := newTestObject(withName("test"), withNamespace("default"))
		store.Create(context.Background(), obj)

		err := store.Delete(context.Background(), "default", "test")
		if err != nil {
			t.Fatalf("Delete() failed: %v", err)
		}

		_, err = store.Get(context.Background(), "default", "test")
		if err == nil {
			t.Error("Object still exists after delete")
		}
	})

	t.Run("returns error for non-existent object", func(t *testing.T) {
		store, cleanup := setupTestStore(t)
		defer cleanup()

		err := store.Delete(context.Background(), "default", "missing")
		if err == nil {
			t.Error("Expected error for missing object, got nil")
		}
	})
}

func TestBigtableStore_ResourceVersionIncrement(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	parseRV := func(obj client.Object) int64 {
		rv, err := strconv.ParseInt(obj.GetResourceVersion(), 10, 64)
		if err != nil {
			t.Fatalf("invalid resourceVersion %q: %v", obj.GetResourceVersion(), err)
		}
		return rv
	}

	obj1 := newTestObject(withName("obj1"), withNamespace("default"))
	store.Create(context.Background(), obj1)

	retrieved1, _ := store.Get(context.Background(), "default", "obj1")
	rv1 := parseRV(retrieved1)
	if rv1 <= 0 {
		t.Errorf("After first create, rv = %d, want > 0", rv1)
	}

	obj2 := newTestObject(withName("obj2"), withNamespace("default"))
	store.Create(context.Background(), obj2)

	retrieved2, _ := store.Get(context.Background(), "default", "obj2")
	rv2 := parseRV(retrieved2)
	if rv2 <= rv1 {
		t.Errorf("After second create, rv = %d, want > %d", rv2, rv1)
	}

	store.Update(context.Background(), retrieved1)

	retrievedUpdated, _ := store.Get(context.Background(), "default", "obj1")
	rv3 := parseRV(retrievedUpdated)
	if rv3 <= rv2 {
		t.Errorf("After update, rv = %d, want > %d", rv3, rv2)
	}
}
