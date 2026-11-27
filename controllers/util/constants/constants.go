package constants

// GCP HCP Kubernetes Resource Annotations and Labels
// These constants define standard annotations and labels used across GCP HCP resources
// for tracking, management, and identification purposes.

const (
	// AnnotationGeneration is the annotation key for tracking resource generation.
	// This is used to track changes and ensure resources are updated with the correct generation.
	// Format: "gcp.managed.openshift.io/generation"
	// Example value: "5" (integer as string)
	AnnotationGeneration = "gcp.managed.openshift.io/generation"

	// LabelGeneration is the label key for tracking resource generation.
	// Used for label-based filtering and selection of resources by generation.
	// Format: "gcp.managed.openshift.io/generation"
	// Example value: "5" (integer as string)
	LabelGeneration = "gcp.managed.openshift.io/generation"

	// AnnotationClusterID is the annotation key for cluster identification.
	// Links resources to their target cluster.
	// Format: "gcp.managed.openshift.io/cluster-id"
	AnnotationClusterID = "gcp.managed.openshift.io/cluster-id"

	// LabelClusterID is the label key for cluster identification.
	// Used for label-based filtering and selection of resources by cluster.
	// Format: "gcp.managed.openshift.io/cluster-id"
	LabelClusterID = "gcp.managed.openshift.io/cluster-id"

	// AnnotationController is the annotation key for controller identification.
	// Identifies which controller created or manages the resource.
	// Format: "gcp.managed.openshift.io/controller"
	AnnotationController = "gcp.managed.openshift.io/controller"

	// LabelController is the label key for controller identification.
	// Used for label-based filtering and selection of resources by controller.
	// Format: "gcp.managed.openshift.io/controller"
	LabelController = "gcp.managed.openshift.io/controller"

	// AnnotationManagedBy identifies the entity managing the resource.
	// Format: "gcp.managed.openshift.io/managed-by"
	// Example value: "gecko-controllers"
	AnnotationManagedBy = "gcp.managed.openshift.io/managed-by"

	// LabelManagedBy is the label key for managed-by identification.
	// Format: "gcp.managed.openshift.io/managed-by"
	LabelManagedBy = "gcp.managed.openshift.io/managed-by"

	// AnnotationCreatedBy identifies the authenticated user who created the resource.
	// Uses the private prefix so it is automatically stripped from the public API
	// and cannot be set or modified by external clients.
	// Format: "private.orlop.gcp.managed.openshift.io/created-by"
	// Example value: "user@example.com"
	AnnotationCreatedBy = "private.orlop.gcp.managed.openshift.io/created-by"

	// AnnotationEtcdBackend records the etcd backend the hc-controller selected
	// for a Cluster the first time it reconciled it. HyperShift does not allow a
	// HostedCluster's etcd management type to change after creation, so the
	// selection is made once and never revisited. Uses the private prefix so it is
	// stripped from the public API and cannot be set or modified by external
	// clients.
	// Format: "private.orlop.gcp.managed.openshift.io/etcd-backend"
	// Example values: EtcdBackendManaged, EtcdBackendSpanner
	AnnotationEtcdBackend = "private.orlop.gcp.managed.openshift.io/etcd-backend"
)

// Values of AnnotationEtcdBackend.
const (
	// EtcdBackendManaged is HyperShift-managed etcd on persistent volumes.
	EtcdBackendManaged = "managed"

	// EtcdBackendSpanner is socketcd's Spanner-backed Unmanaged etcd.
	EtcdBackendSpanner = "spanner"
)

// Finalizer constants for deletion handling.
const (
	// FinalizerCluster is the finalizer added by the hc-controller to ensure
	// management-cluster resources are cleaned up before a Cluster CR is deleted.
	FinalizerCluster = "gcp.managed.openshift.io/cluster-finalizer"

	// FinalizerNodePool is the finalizer added by the nodepool-controller to ensure
	// management-cluster resources are cleaned up before a NodePool CR is deleted.
	FinalizerNodePool = "gcp.managed.openshift.io/nodepool-finalizer"
)

// HyperShift API constants
const (
	// HyperShiftGroup is the API group for HyperShift resources (HostedCluster, NodePool, etc.).
	HyperShiftGroup = "hypershift.openshift.io"

	// HyperShiftVersion is the API version for HyperShift resources.
	HyperShiftVersion = "v1beta1"
)

// socketcd API and HostedCluster contract constants. socketcd provisions the
// Spanner-backed etcd for HostedClusters that opt in at creation time.
const (
	// SocketCDGroup is the API group of the socketcd SpannerEtcd resource.
	SocketCDGroup = "etcd.gcp.managed.openshift.io"

	// SocketCDVersion is the API version of the socketcd SpannerEtcd resource.
	SocketCDVersion = "v1alpha1"

	// SocketCDResource is the plural resource name of SpannerEtcd.
	SocketCDResource = "spanneretcds"

	// AnnotationSpannerEnabled is the HostedCluster annotation that opts the
	// cluster in to socketcd. It cannot change after creation.
	AnnotationSpannerEnabled = "gcp.managed.openshift.io/spanner-enabled"

	// SpannerEtcdServiceName is the Service in the control plane namespace that
	// socketcd's adapter serves the etcd API on.
	SpannerEtcdServiceName = "spanner-etcd"

	// SpannerEtcdPort is the port of the SpannerEtcdServiceName Service.
	SpannerEtcdPort = 2379

	// SpannerEtcdClientSecretSuffix is appended to the HostedCluster name to form
	// the client Secret that socketcd writes and HyperShift reads.
	SpannerEtcdClientSecretSuffix = "-spanner-etcd-client"
)
