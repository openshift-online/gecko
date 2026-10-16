package versionresolution

import (
	"context"
	"fmt"
	"slices"
	"strings"

	privatev1 "github.com/openshift-online/gecko/platform-api/api/private/v1"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Condition reasons reported when a release cannot be resolved from the catalog.
const (
	ReasonVersionNotInCatalog      = "VersionNotInCatalog"
	ReasonVersionNotInChannelGroup = "VersionNotInChannelGroup"
)

// ResolveError is returned when the catalog does not offer the requested
// release. It carries the condition reason and message to surface on the
// resource, and signals that the request is not retryable on its own: the
// catalog has to change first.
type ResolveError struct {
	Reason  string
	Message string
}

func (e *ResolveError) Error() string { return e.Message }

// ResolveReleaseImage returns the release image pullspec the Version catalog
// records for the given version and channel group.
//
// Version resources are maintained by the version-sync controller, so whichever
// source populated the catalog decides which releases exist. Resolution is a
// cluster-local lookup and performs no external calls.
//
// A *ResolveError is returned when the version is absent from the catalog or is
// not published in the requested channel group. Any other error is an infra
// failure and should be retried.
func ResolveReleaseImage(ctx context.Context, c client.Client, version, channelGroup string) (string, error) {
	// Version resources are cluster-scoped and named after the release version.
	var catalogVersion privatev1.Version
	if err := c.Get(ctx, client.ObjectKey{Name: version}, &catalogVersion); err != nil {
		if apierrors.IsNotFound(err) {
			return "", &ResolveError{
				Reason:  ReasonVersionNotInCatalog,
				Message: fmt.Sprintf("Version %s is not in the version catalog", version),
			}
		}
		return "", fmt.Errorf("get version %s: %w", version, err)
	}

	if !slices.Contains(catalogVersion.Spec.ChannelGroups, channelGroup) {
		return "", &ResolveError{
			Reason: ReasonVersionNotInChannelGroup,
			Message: fmt.Sprintf("Version %s is not published in channel group %s (published in: %s)",
				version, channelGroup, strings.Join(catalogVersion.Spec.ChannelGroups, ", ")),
		}
	}

	// The API requires a non-empty release image. Guard anyway: resolution pins
	// its result, so an empty image would stick to the resource permanently.
	if catalogVersion.Spec.ReleaseImage == "" {
		return "", fmt.Errorf("version %s has no release image in the catalog", version)
	}

	return catalogVersion.Spec.ReleaseImage, nil
}
