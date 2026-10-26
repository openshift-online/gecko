package versionresolution

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveReleaseImage(t *testing.T) {
	const (
		version      = "4.22.0-ec.4"
		releaseImage = "quay.io/openshift-release-dev/ocp-release:4.22.0-ec.4-x86_64"
	)

	t.Run("returns the catalog release image", func(t *testing.T) {
		store := &mockStoreClient{version: catalogVersion(version, releaseImage, "candidate", "fast")}

		got, err := ResolveReleaseImage(context.Background(), store, version, "fast")

		require.NoError(t, err)
		require.Equal(t, releaseImage, got)
	})

	t.Run("reports a missing catalog entry", func(t *testing.T) {
		store := &mockStoreClient{}

		_, err := ResolveReleaseImage(context.Background(), store, version, "candidate")

		var resolveErr *ResolveError
		require.ErrorAs(t, err, &resolveErr)
		require.Equal(t, ReasonVersionNotInCatalog, resolveErr.Reason)
		require.Contains(t, resolveErr.Message, version)
	})

	t.Run("reports a channel group the release is not published in", func(t *testing.T) {
		store := &mockStoreClient{version: catalogVersion(version, releaseImage, "candidate")}

		_, err := ResolveReleaseImage(context.Background(), store, version, "stable")

		var resolveErr *ResolveError
		require.ErrorAs(t, err, &resolveErr)
		require.Equal(t, ReasonVersionNotInChannelGroup, resolveErr.Reason)
		require.Contains(t, resolveErr.Message, "stable")
		require.Contains(t, resolveErr.Message, "candidate")
	})

	t.Run("propagates infra failures as retryable errors", func(t *testing.T) {
		store := &mockStoreClient{versionGetErr: errors.New("etcd timeout")}

		_, err := ResolveReleaseImage(context.Background(), store, version, "candidate")

		require.Error(t, err)
		var resolveErr *ResolveError
		require.NotErrorAs(t, err, &resolveErr, "infra failures must stay retryable")
		require.Contains(t, err.Error(), "etcd timeout")
	})

	t.Run("refuses to pin an empty release image", func(t *testing.T) {
		store := &mockStoreClient{version: catalogVersion(version, "", "candidate")}

		_, err := ResolveReleaseImage(context.Background(), store, version, "candidate")

		require.Error(t, err)
		var resolveErr *ResolveError
		require.NotErrorAs(t, err, &resolveErr, "an empty image is a catalog defect, not a terminal request error")
		require.Contains(t, err.Error(), "no release image")
	})

	t.Run("accepts any channel group the catalog publishes", func(t *testing.T) {
		for _, group := range []string{"stable", "fast", "candidate", "eus", "nightly", "dev-preview"} {
			store := &mockStoreClient{version: catalogVersion(version, releaseImage, group)}

			got, err := ResolveReleaseImage(context.Background(), store, version, group)

			require.NoError(t, err, "channel group %s", group)
			require.Equal(t, releaseImage, got)
		}
	})
}
