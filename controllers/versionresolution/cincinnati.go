package versionresolution

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const defaultCincinnatiBaseURL = "https://api.openshift.com/api/upgrades_info/v1/graph"

// CincinnatiClient is an HTTP client for the Cincinnati update graph API.
type CincinnatiClient struct {
	BaseURL    string
	Arch       string
	httpClient *http.Client
}

// NewCincinnatiClient creates a new CincinnatiClient with a 30s timeout.
func NewCincinnatiClient(baseURL, arch string) *CincinnatiClient {
	if baseURL == "" {
		baseURL = defaultCincinnatiBaseURL
	}
	return &CincinnatiClient{
		BaseURL: baseURL,
		Arch:    arch,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// ReleaseInfo is one node in the Cincinnati graph.
type ReleaseInfo struct {
	Version string `json:"version"`
	Payload string `json:"payload"`
}

// CincinnatiGraph is the raw response from Cincinnati.
type CincinnatiGraph struct {
	Nodes []ReleaseInfo `json:"nodes"`
}

// ListReleases fetches releases from the Cincinnati graph for the given
// channel. Cincinnati graph edges and metadata remain internal to this client.
func (c *CincinnatiClient) ListReleases(ctx context.Context, channel string) ([]ReleaseInfo, error) {
	endpoint, err := url.Parse(c.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("cincinnati: parse base URL: %w", err)
	}
	query := endpoint.Query()
	query.Set("channel", channel)
	query.Set("arch", c.Arch)
	endpoint.RawQuery = query.Encode()
	requestURL := endpoint.String()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("cincinnati: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cincinnati: GET %s: %w", requestURL, err)
	}
	defer resp.Body.Close() //nolint:errcheck

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("cincinnati: read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("cincinnati: GET %s returned %d: %s", requestURL, resp.StatusCode, string(body))
	}

	var graph CincinnatiGraph
	if err := json.Unmarshal(body, &graph); err != nil {
		return nil, fmt.Errorf("cincinnati: unmarshal response: %w", err)
	}
	return graph.Nodes, nil
}

// Resolve fetches Cincinnati releases for the given channel and returns the
// ReleaseInfo matching version. Returns nil, nil if the version is not found.
func (c *CincinnatiClient) Resolve(ctx context.Context, version, channel string) (*ReleaseInfo, error) {
	releases, err := c.ListReleases(ctx, channel)
	if err != nil {
		return nil, err
	}

	for i := range releases {
		if releases[i].Version == version {
			return &releases[i], nil
		}
	}

	// Version not found — not an error, caller handles requeue.
	return nil, nil
}
