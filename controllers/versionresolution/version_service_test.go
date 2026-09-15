package versionresolution

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

type fakeReleaseSource struct {
	mu       sync.Mutex
	releases []ReleaseInfo
	err      error
	calls    int
}

func (f *fakeReleaseSource) ListReleases(_ context.Context, _ string) ([]ReleaseInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return append([]ReleaseInfo(nil), f.releases...), f.err
}

func (f *fakeReleaseSource) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func newTestVersionService(t *testing.T, source releaseSource) *VersionService {
	t.Helper()
	service, err := NewVersionService(source, "4.24.0")
	require.NoError(t, err)
	return service
}

func TestVersionServiceValidate(t *testing.T) {
	service := newTestVersionService(t, &fakeReleaseSource{})
	tests := []struct {
		name    string
		version string
		wantErr error
	}{
		{name: "minimum release", version: "4.24.0"},
		{name: "later patch", version: "4.24.3"},
		{name: "later minor", version: "4.25.0"},
		{name: "later major", version: "5.0.0"},
		{name: "malformed version", version: "not-a-version", wantErr: ErrInvalidVersion},
		{name: "prerelease before minimum", version: "4.24.0-rc.1", wantErr: ErrUnsupportedVersion},
		{name: "older release", version: "4.23.9", wantErr: ErrUnsupportedVersion},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := service.Validate(test.version)
			if test.wantErr == nil {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, test.wantErr)
		})
	}
}

func TestNewVersionServiceRejectsInvalidMinimum(t *testing.T) {
	service, err := NewVersionService(&fakeReleaseSource{}, "4.24")

	require.Nil(t, service)
	require.Error(t, err)
}

func TestVersionServiceResolveAndLatest(t *testing.T) {
	source := &fakeReleaseSource{releases: []ReleaseInfo{
		{Version: "4.24.1", Payload: "image-1"},
		{Version: "4.24.3", Payload: "image-3"},
		{Version: "4.24.2", Payload: "image-2"},
		{Version: "4.24.4", Payload: ""},
	}}
	service := newTestVersionService(t, source)

	resolution, err := service.Resolve(context.Background(), "4.24.2", "stable")

	require.NoError(t, err)
	require.Equal(t, &Resolution{
		Version:       "4.24.2",
		ReleaseImage:  "image-2",
		Channel:       "stable-4.24",
		LatestVersion: "4.24.3",
	}, resolution)
}

func TestVersionServiceRejectsBeforeCincinnati(t *testing.T) {
	source := &fakeReleaseSource{}
	service := newTestVersionService(t, source)

	_, err := service.Resolve(context.Background(), "4.23.9", "stable")
	require.ErrorIs(t, err, ErrUnsupportedVersion)
	require.Zero(t, source.callCount())
}

func TestVersionServiceVersionNotFound(t *testing.T) {
	source := &fakeReleaseSource{releases: []ReleaseInfo{{Version: "4.24.1", Payload: "image-1"}}}
	service := newTestVersionService(t, source)

	_, err := service.Resolve(context.Background(), "4.24.2", "stable")

	require.ErrorIs(t, err, ErrVersionNotFound)
}

func TestVersionServiceEmptyResponse(t *testing.T) {
	service := newTestVersionService(t, &fakeReleaseSource{})

	_, err := service.Resolve(context.Background(), "4.24.1", "stable")

	require.ErrorIs(t, err, ErrNoValidReleases)
}

func TestVersionServiceCincinnatiError(t *testing.T) {
	source := &fakeReleaseSource{err: errors.New("Cincinnati unavailable")}
	service := newTestVersionService(t, source)

	_, err := service.Resolve(context.Background(), "4.24.1", "stable")

	require.ErrorContains(t, err, "Cincinnati unavailable")
}
