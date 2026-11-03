package setupgradepolicy

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// ---------------------------------------------------------------------------
// Minimal mock client
// ---------------------------------------------------------------------------

type mockClient struct {
	// Apply path
	appliedObj map[string]any
	applyErr   error

	// Patch path (used by --clear)
	patchedObj  ctrlclient.Object
	patchedData []byte
	patchErr    error
}

func (m *mockClient) Apply(_ context.Context, obj runtime.ApplyConfiguration, _ ...ctrlclient.ApplyOption) error {
	if m.applyErr != nil {
		return m.applyErr
	}
	b, err := json.Marshal(obj)
	if err != nil {
		return err
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return err
	}
	m.appliedObj = out
	return nil
}

func (m *mockClient) Patch(_ context.Context, obj ctrlclient.Object, p ctrlclient.Patch, _ ...ctrlclient.PatchOption) error {
	if m.patchErr != nil {
		return m.patchErr
	}
	m.patchedObj = obj
	m.patchedData, _ = p.Data(obj)
	return nil
}

// Remaining methods satisfy the client.Client interface.

func (m *mockClient) Get(_ context.Context, _ ctrlclient.ObjectKey, _ ctrlclient.Object, _ ...ctrlclient.GetOption) error {
	return nil
}
func (m *mockClient) Create(_ context.Context, _ ctrlclient.Object, _ ...ctrlclient.CreateOption) error {
	return nil
}
func (m *mockClient) Delete(_ context.Context, _ ctrlclient.Object, _ ...ctrlclient.DeleteOption) error {
	return nil
}
func (m *mockClient) DeleteAllOf(_ context.Context, _ ctrlclient.Object, _ ...ctrlclient.DeleteAllOfOption) error {
	return nil
}
func (m *mockClient) List(_ context.Context, _ ctrlclient.ObjectList, _ ...ctrlclient.ListOption) error {
	return nil
}
func (m *mockClient) Update(_ context.Context, _ ctrlclient.Object, _ ...ctrlclient.UpdateOption) error {
	return nil
}
func (m *mockClient) SubResource(_ string) ctrlclient.SubResourceClient { return nil }
func (m *mockClient) Status() ctrlclient.SubResourceWriter              { return nil }
func (m *mockClient) Scheme() *runtime.Scheme                           { return nil }
func (m *mockClient) RESTMapper() meta.RESTMapper                       { return nil }
func (m *mockClient) GroupVersionKindFor(_ runtime.Object) (schema.GroupVersionKind, error) {
	return schema.GroupVersionKind{}, nil
}
func (m *mockClient) IsObjectNamespaced(_ runtime.Object) (bool, error) { return true, nil }

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// specMaintenance extracts spec.maintenance from the applied object map.
// Returns (nil, false) if the key is absent, (nil, true) if explicitly null.
func specMaintenance(t *testing.T, obj map[string]any) (map[string]any, bool) {
	t.Helper()
	spec, ok := obj["spec"].(map[string]any)
	require.True(t, ok, "spec should be a map")
	val, present := spec["maintenance"]
	if !present {
		return nil, false
	}
	if val == nil {
		return nil, true // explicitly null
	}
	m, ok := val.(map[string]any)
	require.True(t, ok, "maintenance should be a map")
	return m, true
}

// ---------------------------------------------------------------------------
// parseDaysOfWeek
// ---------------------------------------------------------------------------

func TestParseDaysOfWeek(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []string
		wantErr bool
	}{
		{"single day", "monday", []string{"monday"}, false},
		{"multiple days", "monday,friday", []string{"monday", "friday"}, false},
		{"uppercase normalised", "Monday,FRIDAY", []string{"monday", "friday"}, false},
		{"whitespace trimmed", " monday , friday ", []string{"monday", "friday"}, false},
		{"all days", "monday,tuesday,wednesday,thursday,friday,saturday,sunday",
			[]string{"monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"}, false},
		{"invalid day", "monday,funday", nil, true},
		{"empty string", "", nil, true},
		{"only commas", ",,,", nil, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseDaysOfWeek(tc.input)
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.want, got)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// buildApplyConfig
// ---------------------------------------------------------------------------

func TestBuildApplyConfig(t *testing.T) {
	startTime := time.Date(2026, 1, 5, 2, 0, 0, 0, time.UTC)
	days := []string{"monday", "wednesday"}

	marshal := func(t *testing.T, cfg interface{ IsApplyConfiguration() }) map[string]any {
		t.Helper()
		b, err := json.Marshal(cfg)
		require.NoError(t, err)
		var out map[string]any
		require.NoError(t, json.Unmarshal(b, &out))
		return out
	}

	t.Run("identity fields are always present", func(t *testing.T) {
		cfg := buildApplyConfig("ns", "c", startTime, 3600, days, false)
		obj := marshal(t, cfg.(interface{ IsApplyConfiguration() }))
		assert.Equal(t, "gcp.managed.openshift.io/v1", obj["apiVersion"])
		assert.Equal(t, "Cluster", obj["kind"])
		meta := obj["metadata"].(map[string]any)
		assert.Equal(t, "c", meta["name"])
		assert.Equal(t, "ns", meta["namespace"])
	})

	t.Run("clear window sets maintenance to empty object", func(t *testing.T) {
		cfg := buildApplyConfig("ns", "c", time.Time{}, 0, nil, true)
		obj := marshal(t, cfg.(interface{ IsApplyConfiguration() }))
		m, present := specMaintenance(t, obj)
		assert.True(t, present)
		assert.Empty(t, m, "maintenance should be an empty object")
	})

	t.Run("window fields are set correctly", func(t *testing.T) {
		cfg := buildApplyConfig("ns", "c", startTime, 7200, days, false)
		obj := marshal(t, cfg.(interface{ IsApplyConfiguration() }))
		m, present := specMaintenance(t, obj)
		require.True(t, present)
		window, ok := m["window"].(map[string]any)
		require.True(t, ok, "window should be a map")
		assert.Equal(t, "2026-01-05T02:00:00Z", window["startTime"])
		assert.Equal(t, float64(7200), window["durationSeconds"])
		rec := window["recurrence"].(map[string]any)
		assert.Equal(t, "weekly", rec["frequency"])
		dow := rec["daysOfWeek"].([]any)
		assert.Equal(t, []any{"monday", "wednesday"}, dow)
	})
}

// ---------------------------------------------------------------------------
// clearPolicyPatch content
// ---------------------------------------------------------------------------

func TestClearPolicyPatchContent(t *testing.T) {
	// Verify the literal patch bytes contain the exact JSON needed to null out
	// spec.maintenance on the wire, without going through typed-object
	// serialization (which would suppress the null via omitempty).
	var body map[string]any
	require.NoError(t, json.Unmarshal(clearPolicyPatch, &body))
	spec, ok := body["spec"].(map[string]any)
	require.True(t, ok, "spec must be an object")
	val, present := spec["maintenance"]
	assert.True(t, present, "maintenance key must be present")
	assert.Nil(t, val, "maintenance must be null")
}

// ---------------------------------------------------------------------------
// setUpgradePolicy
// ---------------------------------------------------------------------------

func TestSetUpgradePolicy(t *testing.T) {
	startTime := time.Date(2026, 1, 5, 2, 0, 0, 0, time.UTC)
	days := []string{"monday"}

	t.Run("set window: calls Apply with correct fields", func(t *testing.T) {
		mc := &mockClient{}
		err := setUpgradePolicy(context.Background(), mc, "ns", "my-cluster", false, startTime, 3600, days, false)
		require.NoError(t, err)
		require.Nil(t, mc.patchedObj, "Patch must not be called when setting a window")
		m, present := specMaintenance(t, mc.appliedObj)
		require.True(t, present)
		require.NotNil(t, m)
		window := m["window"].(map[string]any)
		assert.Equal(t, float64(3600), window["durationSeconds"])
	})

	t.Run("set window: idempotent (two calls produce identical apply configs)", func(t *testing.T) {
		mc1, mc2 := &mockClient{}, &mockClient{}
		require.NoError(t, setUpgradePolicy(context.Background(), mc1, "ns", "c", false, startTime, 3600, days, false))
		require.NoError(t, setUpgradePolicy(context.Background(), mc2, "ns", "c", false, startTime, 3600, days, false))
		assert.Equal(t, mc1.appliedObj, mc2.appliedObj)
	})

	t.Run("clear policy: uses JSON merge patch with null maintenance", func(t *testing.T) {
		mc := &mockClient{}
		err := setUpgradePolicy(context.Background(), mc, "ns", "my-cluster", true, time.Time{}, 0, nil, false)
		require.NoError(t, err)
		// Must have taken the Patch path, not Apply.
		require.Nil(t, mc.appliedObj, "Apply must not be called for --clear")
		require.NotNil(t, mc.patchedObj, "Patch must be called for --clear")
		// Patch body must contain the literal null.
		var body map[string]any
		require.NoError(t, json.Unmarshal(mc.patchedData, &body))
		spec := body["spec"].(map[string]any)
		val, present := spec["maintenance"]
		assert.True(t, present, "maintenance key must be present in patch body")
		assert.Nil(t, val, "maintenance must be null in patch body")
	})

	t.Run("clear policy: idempotent (two calls produce identical patch bodies)", func(t *testing.T) {
		mc1, mc2 := &mockClient{}, &mockClient{}
		require.NoError(t, setUpgradePolicy(context.Background(), mc1, "ns", "c", true, time.Time{}, 0, nil, false))
		require.NoError(t, setUpgradePolicy(context.Background(), mc2, "ns", "c", true, time.Time{}, 0, nil, false))
		assert.Equal(t, mc1.patchedData, mc2.patchedData)
	})

	t.Run("clear window: calls Apply with empty maintenance", func(t *testing.T) {
		mc := &mockClient{}
		err := setUpgradePolicy(context.Background(), mc, "ns", "my-cluster", false, time.Time{}, 0, nil, true)
		require.NoError(t, err)
		require.Nil(t, mc.patchedObj, "Patch must not be called for --clear-window")
		m, present := specMaintenance(t, mc.appliedObj)
		require.True(t, present)
		assert.Empty(t, m)
	})

	t.Run("apply error is propagated", func(t *testing.T) {
		mc := &mockClient{applyErr: fmt.Errorf("server error")}
		err := setUpgradePolicy(context.Background(), mc, "ns", "my-cluster", false, startTime, 3600, days, false)
		require.Error(t, err)
	})

	t.Run("patch error is propagated for --clear", func(t *testing.T) {
		mc := &mockClient{patchErr: fmt.Errorf("server error")}
		err := setUpgradePolicy(context.Background(), mc, "ns", "my-cluster", true, time.Time{}, 0, nil, false)
		require.Error(t, err)
	})
}

// ---------------------------------------------------------------------------
// Command flag validation (no API calls needed — RunE returns before Apply)
// ---------------------------------------------------------------------------

func TestCommandFlagValidation(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{
			name:    "missing --cluster",
			args:    []string{"--namespace=ns", "--start-time=2026-01-05T02:00:00Z", "--days-of-week=monday"},
			wantErr: true,
		},
		{
			name:    "missing --namespace",
			args:    []string{"--cluster=c", "--start-time=2026-01-05T02:00:00Z", "--days-of-week=monday"},
			wantErr: true,
		},
		{
			name:    "missing --start-time without clear flags",
			args:    []string{"--cluster=c", "--namespace=ns", "--days-of-week=monday"},
			wantErr: true,
		},
		{
			name:    "missing --days-of-week when setting window",
			args:    []string{"--cluster=c", "--namespace=ns", "--start-time=2026-01-05T02:00:00Z"},
			wantErr: true,
		},
		{
			name:    "invalid RFC3339 start time",
			args:    []string{"--cluster=c", "--namespace=ns", "--start-time=not-a-date", "--days-of-week=monday"},
			wantErr: true,
		},
		{
			name:    "duration-seconds too small",
			args:    []string{"--cluster=c", "--namespace=ns", "--start-time=2026-01-05T02:00:00Z", "--days-of-week=monday", "--duration-seconds=59"},
			wantErr: true,
		},
		{
			name:    "duration-seconds too large",
			args:    []string{"--cluster=c", "--namespace=ns", "--start-time=2026-01-05T02:00:00Z", "--days-of-week=monday", "--duration-seconds=86401"},
			wantErr: true,
		},
		{
			name:    "--clear and --clear-window are mutually exclusive",
			args:    []string{"--cluster=c", "--namespace=ns", "--clear", "--clear-window"},
			wantErr: true,
		},
		{
			name:    "--clear and --start-time are mutually exclusive",
			args:    []string{"--cluster=c", "--namespace=ns", "--clear", "--start-time=2026-01-05T02:00:00Z"},
			wantErr: true,
		},
		{
			name:    "--clear-window and --start-time are mutually exclusive",
			args:    []string{"--cluster=c", "--namespace=ns", "--clear-window", "--start-time=2026-01-05T02:00:00Z"},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Pass nil for RootFlags — flag validation errors are returned before
			// NewClient is called, so no actual API connection is made.
			cmd := NewCommand(nil)
			cmd.SetArgs(tc.args)
			err := cmd.Execute()
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
