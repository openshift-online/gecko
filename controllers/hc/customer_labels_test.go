package hc

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadCustomerLabels(t *testing.T) {
	t.Run("valid JSON file: returns label map", func(t *testing.T) {
		f := writeTemp(t, `{"goog-partner-solution":"isol_psn_abc","env":"prod"}`)
		labels, err := LoadCustomerLabels(f)
		require.NoError(t, err)
		assert.Equal(t, map[string]string{
			"goog-partner-solution": "isol_psn_abc",
			"env":                   "prod",
		}, labels)
	})

	t.Run("single label: returns map with one entry", func(t *testing.T) {
		f := writeTemp(t, `{"goog-partner-solution":"isol_psn_abc"}`)
		labels, err := LoadCustomerLabels(f)
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"goog-partner-solution": "isol_psn_abc"}, labels)
	})

	t.Run("empty JSON object: returns empty map", func(t *testing.T) {
		f := writeTemp(t, `{}`)
		labels, err := LoadCustomerLabels(f)
		require.NoError(t, err)
		assert.Empty(t, labels)
	})

	t.Run("file does not exist: returns error", func(t *testing.T) {
		_, err := LoadCustomerLabels(filepath.Join(t.TempDir(), "nonexistent.json"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "read customer labels file")
	})

	t.Run("invalid JSON: returns error", func(t *testing.T) {
		f := writeTemp(t, `not-json`)
		_, err := LoadCustomerLabels(f)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "parse customer labels")
	})
}

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "labels-*.json")
	require.NoError(t, err)
	_, err = f.WriteString(content)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	return f.Name()
}
