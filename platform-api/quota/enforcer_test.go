package quota

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/openshift-online/gecko/orlop/pkg/apiserver/storage"
	"github.com/openshift-online/gecko/orlop/pkg/apiserver/storage/memory"
	privatev1 "github.com/openshift-online/gecko/platform-api/api/private/v1"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

// testScheme is built once for the test binary; runtime.Scheme is safe to
// share across goroutines after construction.
var testScheme = func() *runtime.Scheme {
	s := runtime.NewScheme()
	if err := privatev1.AddToScheme(s); err != nil {
		panic(err)
	}
	return s
}()

func quotaStore(t *testing.T) *memory.MemoryStore {
	t.Helper()
	return memory.NewMemoryStore("quotas", testScheme,
		privatev1.GroupVersion.WithKind("Quota"))
}

func clusterStore(t *testing.T) *memory.MemoryStore {
	t.Helper()
	return memory.NewMemoryStore("clusters", testScheme,
		privatev1.GroupVersion.WithKind("Cluster"))
}

func nodepoolStore(t *testing.T) *memory.MemoryStore {
	t.Helper()
	return memory.NewMemoryStore("nodepools", testScheme,
		privatev1.GroupVersion.WithKind("NodePool"))
}

func mustCreate(t *testing.T, store *memory.MemoryStore, obj client.Object) {
	t.Helper()
	if err := store.Create(context.Background(), obj); err != nil {
		t.Fatalf("store.Create: %v", err)
	}
}

func newQuota(ns string, clusterLimit, nodepoolLimit int32) *privatev1.Quota {
	return &privatev1.Quota{
		TypeMeta:   metav1.TypeMeta{APIVersion: "gcp.managed.openshift.io/v1", Kind: "Quota"},
		ObjectMeta: metav1.ObjectMeta{Name: QuotaName, Namespace: ns},
		Spec: privatev1.QuotaSpec{
			Resources: []privatev1.QuotaResourceSpec{
				{Resource: "hostedclusters", Limit: clusterLimit},
				{Resource: "nodepools", Limit: nodepoolLimit},
			},
		},
	}
}

func newCluster(ns, name string) *privatev1.Cluster {
	return &privatev1.Cluster{
		TypeMeta:   metav1.TypeMeta{APIVersion: "gcp.managed.openshift.io/v1", Kind: "Cluster"},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
	}
}

func newNodePool(ns, name string) *privatev1.NodePool {
	return &privatev1.NodePool{
		TypeMeta:   metav1.TypeMeta{APIVersion: "gcp.managed.openshift.io/v1", Kind: "NodePool"},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
	}
}

// ─── EffectiveLimit ───────────────────────────────────────────────────────────

func TestEffectiveLimit_ReturnsSpecLimit(t *testing.T) {
	q := newQuota("ns", 30, 200)
	if got := EffectiveLimit(q, "hostedclusters"); got != 30 {
		t.Errorf("hostedclusters limit: got %d, want 30", got)
	}
	if got := EffectiveLimit(q, "nodepools"); got != 200 {
		t.Errorf("nodepools limit: got %d, want 200", got)
	}
}

func TestEffectiveLimit_FallsBackToDefault(t *testing.T) {
	q := &privatev1.Quota{}
	if got := EffectiveLimit(q, "hostedclusters"); got != DefaultClusterLimit {
		t.Errorf("default cluster limit: got %d, want %d", got, DefaultClusterLimit)
	}
	if got := EffectiveLimit(q, "nodepools"); got != DefaultNodePoolLimit {
		t.Errorf("default nodepool limit: got %d, want %d", got, DefaultNodePoolLimit)
	}
}

// ─── DefaultLimit ─────────────────────────────────────────────────────────────

func TestDefaultLimit_KnownResources(t *testing.T) {
	if got := DefaultLimit("hostedclusters"); got != DefaultClusterLimit {
		t.Errorf("got %d, want %d", got, DefaultClusterLimit)
	}
	if got := DefaultLimit("nodepools"); got != DefaultNodePoolLimit {
		t.Errorf("got %d, want %d", got, DefaultNodePoolLimit)
	}
}

func TestDefaultLimit_UnknownResourceUsesClusterDefault(t *testing.T) {
	if got := DefaultLimit("unknown"); got != DefaultClusterLimit {
		t.Errorf("got %d, want %d", got, DefaultClusterLimit)
	}
}

// ─── QuotaExceededError ───────────────────────────────────────────────────────

func TestQuotaExceededError_ContainsExpectedFields(t *testing.T) {
	err := QuotaExceededError("acme-prod", "hostedclusters", 50, 50)
	if err == nil {
		t.Fatal("expected non-nil error")
	}
	msg := err.Error()
	for _, want := range []string{"acme-prod", "50", "hostedclusters", "QuotaRequest"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message %q missing %q", msg, want)
		}
	}
}

func TestQuotaExceededError_IsForbidden(t *testing.T) {
	err := QuotaExceededError("acme-prod", "hostedclusters", 50, 50)
	if !apierrors.IsForbidden(err) {
		t.Errorf("QuotaExceededError must be a Forbidden StatusError; got %T: %v", err, err)
	}
}

func TestQuotaExceededError_HasReasonAndStructuredDetails(t *testing.T) {
	err := QuotaExceededError("acme-prod", "hostedclusters", 12, 50)
	se, ok := err.(*apierrors.StatusError)
	if !ok {
		t.Fatalf("expected *apierrors.StatusError, got %T", err)
	}

	if se.ErrStatus.Reason != "QuotaExceeded" {
		t.Errorf("Reason: got %q, want %q", se.ErrStatus.Reason, "QuotaExceeded")
	}
	if se.ErrStatus.Code != 403 {
		t.Errorf("Code: got %d, want 403", se.ErrStatus.Code)
	}

	if se.ErrStatus.Details == nil {
		t.Fatal("Details must not be nil")
	}
	if se.ErrStatus.Details.Kind != "hostedclusters" {
		t.Errorf("Details.Kind: got %q, want %q", se.ErrStatus.Details.Kind, "hostedclusters")
	}

	if len(se.ErrStatus.Details.Causes) == 0 {
		t.Fatal("Details.Causes must not be empty")
	}
	// Both causes must mention the namespace.
	for _, cause := range se.ErrStatus.Details.Causes {
		if !strings.Contains(cause.Message, "acme-prod") {
			t.Errorf("Cause.Message %q missing namespace %q", cause.Message, "acme-prod")
		}
	}
}

// ─── StoreEnforcer.CheckQuota — fail-closed storage error ────────────────────

// errorStore is a storage.ResourceStore whose Get always returns a non-NotFound
// error, simulating a transient storage failure.
// List, Create, Update, Delete, and Watch are interface-satisfaction stubs;
// only Get is exercised by the fail-closed test.
type errorStore struct {
	err error
}

func (s *errorStore) Get(_ context.Context, _, _ string) (client.Object, error) {
	return nil, s.err
}
func (s *errorStore) List(_ context.Context, _ storage.ListOptions) (client.ObjectList, error) {
	return nil, s.err
}
func (s *errorStore) Create(_ context.Context, _ client.Object) error { return s.err }
func (s *errorStore) Update(_ context.Context, _ client.Object) error { return s.err }
func (s *errorStore) Delete(_ context.Context, _, _ string) error     { return s.err }
func (s *errorStore) Watch(_ context.Context, _ storage.ListOptions, _ string) (<-chan storage.ResourceEvent, func(), error) {
	return nil, func() {}, s.err
}

func TestCheckQuota_FailsClosedOnStorageError(t *testing.T) {
	// When the quota store returns a non-NotFound error, CheckQuota must
	// fail closed: reject the creation request rather than fall back to
	// the default limit.
	ctx := context.Background()

	storageErr := fmt.Errorf("connection refused")
	e := NewStoreEnforcer(&errorStore{err: storageErr}, clusterStore(t), nodepoolStore(t))

	err := e.CheckQuota(ctx, "ns", "hostedclusters")
	if err == nil {
		t.Fatal("expected error when quota store is unavailable, got nil")
	}
	if strings.Contains(err.Error(), "connection refused") || strings.Contains(err.Error(), "quota check failed") {
		return // correct — fail-closed
	}
	t.Errorf("unexpected error message: %v", err)
}

// ─── StoreEnforcer.CheckQuota ─────────────────────────────────────────────────

func TestCheckQuota_AllowedWhenBelowLimit(t *testing.T) {
	ctx := context.Background()
	qs, cs, ns := quotaStore(t), clusterStore(t), nodepoolStore(t)

	mustCreate(t, qs, newQuota("ns", 5, 10))
	mustCreate(t, cs, newCluster("ns", "c1"))
	mustCreate(t, cs, newCluster("ns", "c2"))

	if err := NewStoreEnforcer(qs, cs, ns).CheckQuota(ctx, "ns", "hostedclusters"); err != nil {
		t.Errorf("expected no error below limit, got: %v", err)
	}
}

func TestCheckQuota_RejectedWhenAtLimit(t *testing.T) {
	ctx := context.Background()
	qs, cs, ns := quotaStore(t), clusterStore(t), nodepoolStore(t)

	mustCreate(t, qs, newQuota("ns", 2, 10))
	mustCreate(t, cs, newCluster("ns", "c1"))
	mustCreate(t, cs, newCluster("ns", "c2"))

	if err := NewStoreEnforcer(qs, cs, ns).CheckQuota(ctx, "ns", "hostedclusters"); err == nil {
		t.Error("expected quota exceeded error at limit, got nil")
	}
}

func TestCheckQuota_UsesDefaultWhenNoQuotaObject(t *testing.T) {
	ctx := context.Background()
	// No quota object in the store — effectiveLimit falls back to DefaultClusterLimit.
	qs, cs, ns := quotaStore(t), clusterStore(t), nodepoolStore(t)

	// Populate fewer clusters than the default limit — should be allowed.
	for i := 0; i < 3; i++ {
		mustCreate(t, cs, newCluster("ns", fmt.Sprintf("c%d", i)))
	}

	if err := NewStoreEnforcer(qs, cs, ns).CheckQuota(ctx, "ns", "hostedclusters"); err != nil {
		t.Errorf("expected no error with counts below default limit, got: %v", err)
	}
}

func TestCheckQuota_NodePoolsUseSeparateStore(t *testing.T) {
	ctx := context.Background()
	qs, cs, ns := quotaStore(t), clusterStore(t), nodepoolStore(t)

	mustCreate(t, qs, newQuota("ns", 50, 2))
	mustCreate(t, ns, newNodePool("ns", "np1"))
	mustCreate(t, ns, newNodePool("ns", "np2"))

	e := NewStoreEnforcer(qs, cs, ns)
	// nodepools at limit → rejected.
	if err := e.CheckQuota(ctx, "ns", "nodepools"); err == nil {
		t.Error("expected nodepools quota exceeded, got nil")
	}
	// hostedclusters unaffected — 0 clusters, limit=50.
	if err := e.CheckQuota(ctx, "ns", "hostedclusters"); err != nil {
		t.Errorf("expected hostedclusters allowed, got: %v", err)
	}
}

func TestCheckQuota_NamespaceIsolated(t *testing.T) {
	ctx := context.Background()
	qs, cs, ns := quotaStore(t), clusterStore(t), nodepoolStore(t)

	// ns-a has a tight limit and 2 clusters.
	mustCreate(t, qs, newQuota("ns-a", 2, 10))
	mustCreate(t, cs, newCluster("ns-a", "c1"))
	mustCreate(t, cs, newCluster("ns-a", "c2"))

	// ns-b is empty with its own quota.
	mustCreate(t, qs, newQuota("ns-b", 10, 10))

	e := NewStoreEnforcer(qs, cs, ns)
	if err := e.CheckQuota(ctx, "ns-a", "hostedclusters"); err == nil {
		t.Error("expected ns-a to be at limit")
	}
	if err := e.CheckQuota(ctx, "ns-b", "hostedclusters"); err != nil {
		t.Errorf("expected ns-b to have room, got: %v", err)
	}
}

func TestCheckQuota_UnknownResource(t *testing.T) {
	ctx := context.Background()
	qs, cs, ns := quotaStore(t), clusterStore(t), nodepoolStore(t)
	if err := NewStoreEnforcer(qs, cs, ns).CheckQuota(ctx, "ns", "widgets"); err == nil {
		t.Error("expected error for unknown resource, got nil")
	}
}
