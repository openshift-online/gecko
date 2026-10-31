package v1_test

import (
	"testing"
	"time"

	"github.com/openshift-online/gecko/orlop/pkg/apiserver/conversion"
	privatev1 "github.com/openshift-online/gecko/platform-api/api/private/v1"
	publicv1 "github.com/openshift-online/gecko/platform-api/api/public/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// clusterForConversionTest returns a minimal valid private Cluster with a
// maintenance window and one exclusion.
func clusterForConversionTest() privatev1.Cluster {
	return privatev1.Cluster{
		TypeMeta:   metav1.TypeMeta{APIVersion: privatev1.GroupVersion.String(), Kind: "Cluster"},
		ObjectMeta: metav1.ObjectMeta{Name: "cluster-1", Namespace: "hyperfleet"},
		Spec: privatev1.ClusterSpec{
			Maintenance: &privatev1.ClusterMaintenanceSpec{
				Exclusions: []privatev1.ControlPlaneMaintenanceExclusion{
					{
						Name:      "holiday-freeze",
						StartTime: metav1.NewTime(time.Date(2026, time.December, 20, 0, 0, 0, 0, time.UTC)),
						EndTime:   metav1.NewTime(time.Date(2027, time.January, 3, 0, 0, 0, 0, time.UTC)),
					},
				},
			},
		},
	}
}

func newClusterConverter(t *testing.T) *conversion.Converter {
	t.Helper()
	privateScheme, publicScheme := runtime.NewScheme(), runtime.NewScheme()
	if err := privatev1.AddToScheme(privateScheme); err != nil {
		t.Fatal(err)
	}
	if err := publicv1.AddToScheme(publicScheme); err != nil {
		t.Fatal(err)
	}
	return conversion.NewConverter(publicScheme, privateScheme, "")
}

// TestClusterMaintenanceExclusionsClearedViaPublicAPI verifies that a public
// PUT that explicitly sends an empty exclusions list clears the stored exclusions.
// This is the regression test for the omitempty removal on Exclusions: without
// that fix the key is absent from the JSON and the old exclusions survive.
func TestClusterMaintenanceExclusionsClearedViaPublicAPI(t *testing.T) {
	converter := newClusterConverter(t)
	existing := clusterForConversionTest()

	// Confirm the existing private cluster has one exclusion.
	if len(existing.Spec.Maintenance.Exclusions) != 1 {
		t.Fatalf("test setup: expected 1 exclusion, got %d", len(existing.Spec.Maintenance.Exclusions))
	}

	// Simulate a public GET: convert to public.
	pub, err := converter.PrivateToPublic(&existing)
	if err != nil {
		t.Fatalf("PrivateToPublic: %v", err)
	}
	publicCluster := pub.(*publicv1.Cluster)

	// Client clears the exclusion list.
	publicCluster.Spec.Maintenance.Exclusions = []publicv1.ControlPlaneMaintenanceExclusion{}

	// Simulate a public PUT: convert back to private, seeded from the existing object.
	updated, err := converter.PublicToPrivate(publicCluster, &existing)
	if err != nil {
		t.Fatalf("PublicToPrivate: %v", err)
	}
	updatedCluster := updated.(*privatev1.Cluster)

	if len(updatedCluster.Spec.Maintenance.Exclusions) != 0 {
		t.Fatalf("expected exclusions to be cleared, got %d exclusion(s): %+v",
			len(updatedCluster.Spec.Maintenance.Exclusions),
			updatedCluster.Spec.Maintenance.Exclusions)
	}
}

// TestClusterMaintenanceExclusionsPreservedWhenMaintenanceOmitted verifies
// that omitting the entire maintenance field in a public PUT preserves the
// existing maintenance configuration, including exclusions and criticalUpgrade.
func TestClusterMaintenanceExclusionsPreservedWhenMaintenanceOmitted(t *testing.T) {
	converter := newClusterConverter(t)
	existing := clusterForConversionTest()
	existing.Spec.Maintenance.CriticalUpgrade = "4.15.1"

	// Public PUT that touches only release, not maintenance.
	publicCluster := &publicv1.Cluster{
		TypeMeta:   metav1.TypeMeta{APIVersion: publicv1.GroupVersion.String(), Kind: "Cluster"},
		ObjectMeta: existing.ObjectMeta,
		Spec: publicv1.ClusterSpec{
			Maintenance: nil, // client omits maintenance entirely
		},
	}

	updated, err := converter.PublicToPrivate(publicCluster, &existing)
	if err != nil {
		t.Fatalf("PublicToPrivate: %v", err)
	}
	updatedCluster := updated.(*privatev1.Cluster)

	if len(updatedCluster.Spec.Maintenance.Exclusions) != 1 {
		t.Fatalf("expected exclusions to be preserved, got %d", len(updatedCluster.Spec.Maintenance.Exclusions))
	}
	if updatedCluster.Spec.Maintenance.CriticalUpgrade != "4.15.1" {
		t.Fatalf("expected criticalUpgrade to be preserved, got %q", updatedCluster.Spec.Maintenance.CriticalUpgrade)
	}
}

// TestClusterMaintenanceCriticalUpgradeNotExposedViaPublicAPI verifies that
// the private criticalUpgrade field is not visible to public clients — a public
// GET must not return it, and a public PUT must not be able to set it.
func TestClusterMaintenanceCriticalUpgradeNotExposedViaPublicAPI(t *testing.T) {
	converter := newClusterConverter(t)
	existing := clusterForConversionTest()
	existing.Spec.Maintenance.CriticalUpgrade = "4.15.1"

	// Public GET: criticalUpgrade must be absent.
	pub, err := converter.PrivateToPublic(&existing)
	if err != nil {
		t.Fatalf("PrivateToPublic: %v", err)
	}
	publicCluster := pub.(*publicv1.Cluster)
	if publicCluster.Spec.Maintenance == nil {
		t.Fatal("expected public Maintenance to be non-nil")
	}
	// publicv1.ClusterMaintenanceSpec has no CriticalUpgrade field — compile-time
	// proof that it is stripped. We only check the round-trip does not overwrite.

	// Public PUT: a client-forged criticalUpgrade in the JSON body must not
	// overwrite the stored value.
	updated, err := converter.PublicToPrivate(publicCluster, &existing)
	if err != nil {
		t.Fatalf("PublicToPrivate: %v", err)
	}
	updatedCluster := updated.(*privatev1.Cluster)
	if updatedCluster.Spec.Maintenance.CriticalUpgrade != "4.15.1" {
		t.Fatalf("criticalUpgrade was overwritten: got %q, want %q",
			updatedCluster.Spec.Maintenance.CriticalUpgrade, "4.15.1")
	}
}
