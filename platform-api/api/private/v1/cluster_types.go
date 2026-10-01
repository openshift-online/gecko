package v1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Available",type=string,JSONPath=`.status.conditions[?(@.type=="HostedClusterAvailable")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type Cluster struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +orlop:public
	// +optional
	Spec ClusterSpec `json:"spec,omitempty"`
	// +orlop:public
	// +optional
	Status ClusterStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type ClusterList struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ListMeta `json:"metadata,omitempty"`

	// +orlop:public
	Items []Cluster `json:"items"`
}

// ClusterSpec is user-defined input only.
type ClusterSpec struct {
	// SafeName is a private, immutable, DNS-safe name used for downstream HyperShift resources.
	// +optional
	// +kubebuilder:validation:MaxLength=17
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	SafeName string `json:"safeName,omitempty"`
	// +orlop:public
	// +optional
	InfraID string `json:"infraID,omitempty"`
	// +orlop:public
	// +optional
	IssuerURL string `json:"issuerURL,omitempty"`
	// +orlop:public
	// +required
	Platform ClusterPlatformSpec `json:"platform"`
	// +orlop:public
	// +required
	Release ReleaseSpec `json:"release"`
	// +orlop:public
	// +required
	Networking NetworkingSpec `json:"networking"`
	// +orlop:public
	// +optional
	DNS *DNSSpec `json:"dns,omitempty"`
}

type ClusterPlatformSpec struct {
	// +orlop:public
	// +required
	// +kubebuilder:validation:Enum=GCP
	Type string `json:"type"`
	// +orlop:public
	// +optional
	GCP *GCPClusterPlatform `json:"gcp,omitempty"`
}

type GCPClusterPlatform struct {
	// +orlop:public
	// +optional
	ProjectID string `json:"projectID,omitempty"`
	// +orlop:public
	// +optional
	Region string `json:"region,omitempty"`
	// +orlop:public
	// +optional
	Network string `json:"network,omitempty"`
	// +orlop:public
	// +optional
	Subnet string `json:"subnet,omitempty"`
	// +orlop:public
	// +optional
	// +kubebuilder:validation:Enum=PublicAndPrivate;Private
	EndpointAccess string `json:"endpointAccess,omitempty"`
	// +orlop:public
	// +required
	WorkloadIdentity WorkloadIdentitySpec `json:"workloadIdentity"`
	// +orlop:public
	// +optional
	// +listType=map
	// +listMapKey=key
	ResourceLabels []GCPResourceLabel `json:"resourceLabels,omitempty"`
}

type WorkloadIdentitySpec struct {
	// +orlop:public
	// +optional
	PoolID string `json:"poolID,omitempty"`
	// +orlop:public
	// +optional
	ProjectNumber string `json:"projectNumber,omitempty"`
	// +orlop:public
	// +optional
	ProviderID string `json:"providerID,omitempty"`
	// +orlop:public
	// +optional
	ServiceAccountsRef *ServiceAccountsRef `json:"serviceAccountsRef,omitempty"`
}

type ServiceAccountsRef struct {
	// +orlop:public
	// +optional
	NodePoolEmail string `json:"nodePoolEmail,omitempty"`
	// +orlop:public
	// +optional
	ControlPlaneEmail string `json:"controlPlaneEmail,omitempty"`
	// +orlop:public
	// +optional
	CloudControllerEmail string `json:"cloudControllerEmail,omitempty"`
	// +orlop:public
	// +optional
	StorageEmail string `json:"storageEmail,omitempty"`
	// +orlop:public
	// +optional
	ImageRegistryEmail string `json:"imageRegistryEmail,omitempty"`
	// +orlop:public
	// +optional
	NetworkEmail string `json:"networkEmail,omitempty"`
}

// GCPResourceLabel is a label applied to GCP resources created for the cluster.
type GCPResourceLabel struct {
	// +orlop:public
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Key string `json:"key"`
	// +orlop:public
	// +optional
	// +kubebuilder:validation:MaxLength=63
	Value string `json:"value,omitempty"`
}

// ReleaseSpec defines the target OCP release version.
// The version-resolution adapter resolves Version+ChannelGroup to a release image pullspec.
type ReleaseSpec struct {
	// +orlop:public
	// +required
	// +kubebuilder:validation:MinLength=1
	Version string `json:"version"`
	// +orlop:public
	// +required
	// +kubebuilder:validation:MinLength=1
	ChannelGroup string `json:"channelGroup"`
}

type NetworkingSpec struct {
	// +orlop:public
	// +optional
	// +listType=atomic
	MachineNetwork []MachineNetworkEntry `json:"machineNetwork,omitempty"`
	// +orlop:public
	// +optional
	// +listType=atomic
	ClusterNetwork []ClusterNetworkEntry `json:"clusterNetwork,omitempty"`
	// +orlop:public
	// +optional
	// +listType=atomic
	ServiceNetwork []string `json:"serviceNetwork,omitempty"`
	// +orlop:public
	// +optional
	// +kubebuilder:validation:Enum=OVNKubernetes;Other
	// +default="OVNKubernetes"
	NetworkType string `json:"networkType,omitempty"`
}

type MachineNetworkEntry struct {
	// +orlop:public
	// +required
	CIDR string `json:"cidr"`
}

type ClusterNetworkEntry struct {
	// +orlop:public
	// +optional
	CIDR string `json:"cidr,omitempty"`
	// +orlop:public
	// +optional
	HostPrefix int32 `json:"hostPrefix,omitempty"`
}

type DNSSpec struct {
	// +orlop:public
	// +optional
	BaseDomain string `json:"baseDomain,omitempty"`
}

// ClusterStatus is written by controllers only.
type ClusterStatus struct {
	// +orlop:public
	// +optional
	// +listType=map
	// +listMapKey=type
	// +patchStrategy=merge
	// +patchMergeKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// PlacementResult is written by the placement controller.
	// Not exposed on the public API.
	// +optional
	PlacementResult *PlacementResult `json:"placementResult,omitempty"`

	// HostedClusterResult is written by the hc-adapter.
	// +orlop:public
	// +optional
	HostedClusterResult *HostedClusterResult `json:"hostedClusterResult,omitempty"`

	// VersionResolution is written by the version-resolution controller.
	// Not exposed on the public API.
	// +optional
	VersionResolution *VersionResolutionResult `json:"versionResolution,omitempty"`

	// ControlPlaneUpgrade is written by the control-plane-upgrade controller.
	// +orlop:public
	// +optional
	ControlPlaneUpgrade *ControlPlaneUpgradeResult `json:"controlPlaneUpgrade,omitempty"`
}

// PlacementResult holds the placement controller's output.
type PlacementResult struct {
	// +optional
	ManagementClusterName string `json:"managementClusterName,omitempty"`
	// +optional
	BaseDomain string `json:"baseDomain,omitempty"`
}

// VersionResolutionResult holds the VR controller's output.
type VersionResolutionResult struct {
	// ReleaseImage is the resolved OCP release pullspec (e.g. quay.io/openshift-release-dev/ocp-release@sha256:...).
	// +optional
	ReleaseImage string `json:"releaseImage,omitempty"`
	// ReleaseVersion is the resolved OCP version string (e.g. "4.16.3").
	// +optional
	ReleaseVersion string `json:"releaseVersion,omitempty"`
	// CincinnatiChannel is the channel string used to query Cincinnati (e.g. "stable-4.16").
	// Derived from ChannelGroup and the major.minor of ReleaseVersion.
	// +optional
	CincinnatiChannel string `json:"cincinnatiChannel,omitempty"`
	// ChannelGroup is the raw channel group from spec.release.channelGroup (e.g. "stable", "candidate", "fast").
	// Stored to detect channel group changes without reconstructing the Cincinnati channel string.
	// +optional
	ChannelGroup string `json:"channelGroup,omitempty"`
}

// HostedClusterResult holds the hc-adapter's output from ManifestWork status feedback.
// This field is read-only — populated by the hc-adapter only.
type HostedClusterResult struct {
	// +orlop:public
	// +optional
	APIEndpoint string `json:"apiEndpoint,omitempty"`
	// +orlop:public
	// +optional
	Version string `json:"version,omitempty"`
	// DesiredVersion is the release currently requested by the HostedCluster.
	// This is private HC feedback used by Gecko controllers.
	// +optional
	DesiredVersion string `json:"desiredVersion,omitempty"`
	// AvailableUpdates are the update targets advertised by HyperShift for the
	// HostedCluster's configured release channel. This is private HC feedback
	// used by Gecko controllers.
	// +optional
	AvailableUpdates []string `json:"availableUpdates,omitempty"`
	// ObservedConditions are HyperShift/CVO upgrade-readiness conditions normalized
	// by the hc-controller from raw HostedCluster feedback. This field is recalculated
	// in full on every reconciliation; a condition type absent from HostedCluster
	// feedback is reported as Unknown with reason HostedClusterFeedbackMissing rather
	// than left stale. This is private HC feedback used by Gecko controllers and is
	// intentionally not exposed on the public API — see ControlPlaneUpgradeResult for
	// the customer-facing upgrade conditions derived from this data.
	// +optional
	// +listType=map
	// +listMapKey=type
	ObservedConditions []metav1.Condition `json:"observedConditions,omitempty"`
}

// ControlPlaneUpgradeResult holds the control-plane-upgrade controller's decision
// state. This field is read-only — populated by the control-plane-upgrade
// controller only. It never reads or writes HostedClusterResult, and
// HostedClusterResult is never written by this controller.
type ControlPlaneUpgradeResult struct {
	// TargetVersion is the control-plane version currently selected or requested.
	// +orlop:public
	// +optional
	TargetVersion string `json:"targetVersion,omitempty"`
	// TargetSource records whether TargetVersion was selected automatically or
	// requested by the customer.
	// +orlop:public
	// +optional
	// +kubebuilder:validation:Enum=automatic;customer
	TargetSource string `json:"targetSource,omitempty"`
	// RequestedAt is when the control-plane-upgrade controller selected or
	// requested TargetVersion. It does not indicate that HyperShift has
	// accepted or begun rolling it out; ControlPlaneUpgradeProgressing reports
	// that separately.
	// +orlop:public
	// +optional
	RequestedAt *metav1.Time `json:"requestedAt,omitempty"`
	// CompletedAt is when TargetVersion completed successfully.
	// +orlop:public
	// +optional
	CompletedAt *metav1.Time `json:"completedAt,omitempty"`
	// FailureReason is set when the upgrade to TargetVersion failed or stalled.
	// +orlop:public
	// +optional
	FailureReason string `json:"failureReason,omitempty"`
	// FailureMessage is set when the upgrade to TargetVersion failed or stalled.
	// +orlop:public
	// +optional
	FailureMessage string `json:"failureMessage,omitempty"`
}

func init() { register(&Cluster{}, &ClusterList{}) }
