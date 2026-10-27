package catalog

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/openshift-online/gecko/controllers/util/setup"
	privatev1 "github.com/openshift-online/gecko/platform-api/api/private/v1"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestResolve(t *testing.T) {
	apiClient := fake.NewClientBuilder().WithScheme(setup.NewScheme()).WithObjects(
		&privatev1.Version{ObjectMeta: metav1.ObjectMeta{Name: "5.0.0-ec.6"},
			Spec: privatev1.VersionSpec{ReleaseImage: "ci-image", ChannelGroups: []string{"nightly", "dev-preview"}}},
	).Build()
	for _, tc := range []struct {
		name    string
		version string
		group   string
		reason  string
	}{
		{name: "CI release", version: "5.0.0-ec.6", group: "dev-preview"},
		{name: "other published group", version: "5.0.0-ec.6", group: "nightly"},
		{name: "missing", version: "4.22.0", group: "stable", reason: "VersionNotFoundInCatalog"},
		{name: "wrong group", version: "5.0.0-ec.6", group: "stable", reason: "VersionNotInChannelGroup"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			image, err := Resolve(context.Background(), apiClient, tc.version, tc.group)
			if tc.reason == "" {
				require.NoError(t, err)
				require.Equal(t, "ci-image", image)
				return
			}
			var unavailable *UnavailableError
			require.ErrorAs(t, err, &unavailable)
			require.Equal(t, tc.reason, unavailable.Reason)
			require.Contains(t, unavailable.Message, tc.version)
			require.Empty(t, image)
		})
	}
}

type failingReader struct {
	client.Reader
	err error
}

func (r failingReader) Get(context.Context, client.ObjectKey, client.Object, ...client.GetOption) error {
	return r.err
}

func TestResolveInfrastructureFailures(t *testing.T) {
	for _, lookupErr := range []error{
		apierrors.NewServiceUnavailable("API unavailable"),
		apierrors.NewForbidden(schema.GroupResource{Resource: "versions"}, "5.0.0-ec.6", errors.New("denied")),
		&cache.ErrCacheNotStarted{},
		context.DeadlineExceeded,
	} {
		t.Run(lookupErr.Error(), func(t *testing.T) {
			image, err := Resolve(context.Background(), failingReader{err: lookupErr}, "5.0.0-ec.6", "dev-preview")
			require.ErrorIs(t, err, lookupErr)
			var unavailable *UnavailableError
			require.False(t, errors.As(err, &unavailable))
			require.Empty(t, image)
		})
	}
}
