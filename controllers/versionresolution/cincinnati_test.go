package versionresolution

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestCincinnatiClientListReleases(t *testing.T) {
	client := NewCincinnatiClient("https://cincinnati.example.test/graph", "amd64")
	client.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, "stable-4.24", req.URL.Query().Get("channel"))
		require.Equal(t, "amd64", req.URL.Query().Get("arch"))
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(`{
				"nodes": [
					{"version": "4.24.1", "payload": "quay.io/ocp:4.24.1"},
					{"version": "4.24.2", "payload": "quay.io/ocp:4.24.2"}
				]
			}`)),
			Header: make(http.Header),
		}, nil
	})}

	releases, err := client.ListReleases(context.Background(), "stable-4.24")

	require.NoError(t, err)
	require.Equal(t, []ReleaseInfo{
		{Version: "4.24.1", Payload: "quay.io/ocp:4.24.1"},
		{Version: "4.24.2", Payload: "quay.io/ocp:4.24.2"},
	}, releases)
}

func TestCincinnatiClientResolveUsesReleaseList(t *testing.T) {
	client := NewCincinnatiClient("https://cincinnati.example.test/graph", "amd64")
	client.httpClient = &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(`{
				"nodes": [
					{"version": "4.24.1", "payload": "quay.io/ocp:4.24.1"},
					{"version": "4.24.2", "payload": "quay.io/ocp:4.24.2"}
				]
			}`)),
			Header: make(http.Header),
		}, nil
	})}

	release, err := client.Resolve(context.Background(), "4.24.2", "stable-4.24")

	require.NoError(t, err)
	require.Equal(t, &ReleaseInfo{Version: "4.24.2", Payload: "quay.io/ocp:4.24.2"}, release)
}

func TestCincinnatiClientListReleasesErrors(t *testing.T) {
	tests := []struct {
		name      string
		transport roundTripFunc
		contains  string
	}{
		{
			name: "request failure",
			transport: func(_ *http.Request) (*http.Response, error) {
				return nil, errors.New("request timed out")
			},
			contains: "request timed out",
		},
		{
			name: "non-success response",
			transport: func(_ *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusBadGateway,
					Body:       io.NopCloser(strings.NewReader("upstream unavailable")),
					Header:     make(http.Header),
				}, nil
			},
			contains: "returned 502",
		},
		{
			name: "malformed response",
			transport: func(_ *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader("not-json")),
					Header:     make(http.Header),
				}, nil
			},
			contains: "unmarshal response",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := NewCincinnatiClient("https://cincinnati.example.test/graph", "amd64")
			client.httpClient = &http.Client{Transport: test.transport}

			_, err := client.ListReleases(context.Background(), "stable-4.24")

			require.ErrorContains(t, err, test.contains)
		})
	}
}
