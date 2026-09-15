package versionresolution

import (
	"context"
	"errors"
	"fmt"
	"strings"

	utilversion "k8s.io/apimachinery/pkg/util/version"
)

var (
	// ErrInvalidVersion indicates that a requested version is not valid semantic
	// version syntax.
	ErrInvalidVersion = errors.New("invalid version")
	// ErrUnsupportedVersion indicates that a requested version predates the
	// minimum version supported by GCP HCP.
	ErrUnsupportedVersion = errors.New("unsupported version")
	// ErrVersionNotFound indicates that Cincinnati does not contain the exact
	// requested version in its derived channel.
	ErrVersionNotFound = errors.New("version not found in Cincinnati")
	// ErrNoValidReleases indicates that Cincinnati returned no usable releases.
	ErrNoValidReleases = errors.New("Cincinnati returned no valid releases")
)

type releaseSource interface {
	ListReleases(ctx context.Context, channel string) ([]ReleaseInfo, error)
}

// Resolution is the stable, Cincinnati-independent result used by Gecko.
type Resolution struct {
	Version       string
	ReleaseImage  string
	Channel       string
	LatestVersion string
}

// VersionService validates and resolves OpenShift versions through Cincinnati.
type VersionService struct {
	source  releaseSource
	minimum *utilversion.Version
}

// NewVersionService creates a version service.
func NewVersionService(source releaseSource, minimum string) (*VersionService, error) {
	parsed, err := utilversion.ParseSemantic(minimum)
	if err != nil {
		return nil, fmt.Errorf("parse minimum supported version %q: %w", minimum, err)
	}
	return &VersionService{source: source, minimum: parsed}, nil
}

// Validate returns nil when version is at least the configured minimum.
func (s *VersionService) Validate(version string) error {
	parsed, err := utilversion.ParseSemantic(version)
	if err != nil {
		return fmt.Errorf("%w %q: %v", ErrInvalidVersion, version, err)
	}
	if !parsed.AtLeast(s.minimum) {
		return fmt.Errorf("%w %q: minimum supported version is %s", ErrUnsupportedVersion, version, s.minimum)
	}
	return nil
}

// Resolve validates version, derives its channel, resolves its image, and
// calculates the latest supported version in the same channel.
func (s *VersionService) Resolve(ctx context.Context, version, channelGroup string) (*Resolution, error) {
	if err := s.Validate(version); err != nil {
		return nil, err
	}

	channel, err := buildChannel(version, channelGroup)
	if err != nil {
		return nil, fmt.Errorf("build channel: %w", err)
	}
	releases, err := s.source.ListReleases(ctx, channel)
	if err != nil {
		return nil, err
	}
	if len(releases) == 0 {
		return nil, ErrNoValidReleases
	}

	var requested *ReleaseInfo
	var latest *utilversion.Version
	for i := range releases {
		if releases[i].Payload == "" {
			continue
		}
		parsed, parseErr := utilversion.ParseSemantic(releases[i].Version)
		if parseErr != nil {
			continue
		}
		if latest == nil || parsed.GreaterThan(latest) {
			latest = parsed
		}
		if releases[i].Version == version {
			requested = &releases[i]
		}
	}
	if requested == nil {
		return nil, fmt.Errorf("%w: %s in channel %s", ErrVersionNotFound, version, channel)
	}

	return &Resolution{
		Version:       requested.Version,
		ReleaseImage:  requested.Payload,
		Channel:       channel,
		LatestVersion: latest.String(),
	}, nil
}

// buildChannel constructs a Cincinnati channel from a semantic version and a
// channel group, for example 4.24.3 + stable becomes stable-4.24.
func buildChannel(version, channelGroup string) (string, error) {
	parts := strings.Split(version, ".")
	if len(parts) < 2 {
		return "", fmt.Errorf("invalid version %q: expected at least major.minor", version)
	}
	return fmt.Sprintf("%s-%s.%s", channelGroup, parts[0], parts[1]), nil
}
