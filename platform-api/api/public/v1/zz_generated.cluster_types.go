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

	// +optional
	Spec ClusterSpec `json:"spec,omitempty"`

	// +optional
	Status ClusterStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type ClusterList struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []Cluster `json:"items"`
}

// ClusterSpec is user-defined input only.
type ClusterSpec struct {

	// +optional
	InfraID string `json:"infraID,omitempty"`

	// +optional
	IssuerURL string `json:"issuerURL,omitempty"`

	// +required
	Platform ClusterPlatformSpec `json:"platform"`

	// +required
	Release ReleaseSpec `json:"release"`

	// +required
	Networking NetworkingSpec `json:"networking"`

	// +optional
	DNS *DNSSpec `json:"dns,omitempty"`
}

type ClusterPlatformSpec struct {

	// +required
	// +kubebuilder:validation:Enum=GCP
	Type string `json:"type"`

	// +optional
	GCP *GCPClusterPlatform `json:"gcp,omitempty"`
}

type GCPClusterPlatform struct {

	// +optional
	ProjectID string `json:"projectID,omitempty"`

	// +optional
	Region string `json:"region,omitempty"`

	// +optional
	Network string `json:"network,omitempty"`

	// +optional
	Subnet string `json:"subnet,omitempty"`

	// +optional
	// +kubebuilder:validation:Enum=PublicAndPrivate;Private
	EndpointAccess string `json:"endpointAccess,omitempty"`

	// +required
	WorkloadIdentity WorkloadIdentitySpec `json:"workloadIdentity"`

	// +optional
	// +listType=map
	// +listMapKey=key
	ResourceLabels []GCPResourceLabel `json:"resourceLabels,omitempty"`
}

type WorkloadIdentitySpec struct {

	// +optional
	PoolID string `json:"poolID,omitempty"`

	// +optional
	ProjectNumber string `json:"projectNumber,omitempty"`

	// +optional
	ProviderID string `json:"providerID,omitempty"`

	// +optional
	ServiceAccountsRef *ServiceAccountsRef `json:"serviceAccountsRef,omitempty"`
}

type ServiceAccountsRef struct {

	// +optional
	NodePoolEmail string `json:"nodePoolEmail,omitempty"`

	// +optional
	ControlPlaneEmail string `json:"controlPlaneEmail,omitempty"`

	// +optional
	CloudControllerEmail string `json:"cloudControllerEmail,omitempty"`

	// +optional
	StorageEmail string `json:"storageEmail,omitempty"`

	// +optional
	ImageRegistryEmail string `json:"imageRegistryEmail,omitempty"`

	// +optional
	NetworkEmail string `json:"networkEmail,omitempty"`
}

// GCPResourceLabel is a label applied to GCP resources created for the cluster.
type GCPResourceLabel struct {

	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Key string `json:"key"`

	// +optional
	// +kubebuilder:validation:MaxLength=63
	Value string `json:"value,omitempty"`
}

// ReleaseSpec defines the target OCP release version.
// The version-resolution adapter resolves Version+ChannelGroup to a release image pullspec.
type ReleaseSpec struct {

	// +required
	// +kubebuilder:validation:MinLength=1
	Version string `json:"version"`

	// +required
	// +kubebuilder:validation:MinLength=1
	ChannelGroup string `json:"channelGroup"`
}

type NetworkingSpec struct {

	// +optional
	// +listType=atomic
	MachineNetwork []MachineNetworkEntry `json:"machineNetwork,omitempty"`

	// +optional
	// +listType=atomic
	ClusterNetwork []ClusterNetworkEntry `json:"clusterNetwork,omitempty"`

	// +optional
	// +listType=atomic
	ServiceNetwork []string `json:"serviceNetwork,omitempty"`

	// +optional
	// +kubebuilder:validation:Enum=OVNKubernetes;Other
	// +default="OVNKubernetes"
	NetworkType string `json:"networkType,omitempty"`
}

type MachineNetworkEntry struct {

	// +required
	CIDR string `json:"cidr"`
}

type ClusterNetworkEntry struct {

	// +optional
	CIDR string `json:"cidr,omitempty"`

	// +optional
	HostPrefix int32 `json:"hostPrefix,omitempty"`
}

type DNSSpec struct {

	// +optional
	BaseDomain string `json:"baseDomain,omitempty"`
}

// ClusterStatus is written by controllers only.
type ClusterStatus struct {

	// +optional
	// +listType=map
	// +listMapKey=type
	// +patchStrategy=merge
	// +patchMergeKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// HostedClusterResult is written by the hc-adapter.

	// +optional
	HostedClusterResult *HostedClusterResult `json:"hostedClusterResult,omitempty"`

	// ControlPlaneUpgrade is written by the control-plane-upgrade controller.

	// +optional
	ControlPlaneUpgrade *ControlPlaneUpgradeResult `json:"controlPlaneUpgrade,omitempty"`
}

// HostedClusterResult holds the hc-adapter's output from ManifestWork status feedback.
// This field is read-only — populated by the hc-adapter only.
type HostedClusterResult struct {

	// +optional
	APIEndpoint string `json:"apiEndpoint,omitempty"`

	// +optional
	Version string `json:"version,omitempty"`
}

// ControlPlaneUpgradeResult holds the control-plane-upgrade controller's decision
// state. This field is read-only — populated by the control-plane-upgrade
// controller only. It never reads or writes HostedClusterResult, and
// HostedClusterResult is never written by this controller.
type ControlPlaneUpgradeResult struct {
	// TargetVersion is the control-plane version currently selected or requested.

	// +optional
	TargetVersion string `json:"targetVersion,omitempty"`
	// TargetSource records whether TargetVersion was selected automatically or
	// requested by the customer.

	// +optional
	// +kubebuilder:validation:Enum=automatic;customer
	TargetSource string `json:"targetSource,omitempty"`
	// RequestedAt is when the control-plane-upgrade controller selected or
	// requested TargetVersion. It does not indicate that HyperShift has
	// accepted or begun rolling it out; ControlPlaneUpgradeProgressing reports
	// that separately.

	// +optional
	RequestedAt *metav1.Time `json:"requestedAt,omitempty"`
	// CompletedAt is when TargetVersion completed successfully.

	// +optional
	CompletedAt *metav1.Time `json:"completedAt,omitempty"`
	// FailureReason is set when the upgrade to TargetVersion failed or stalled.

	// +optional
	FailureReason string `json:"failureReason,omitempty"`
	// FailureMessage is set when the upgrade to TargetVersion failed or stalled.

	// +optional
	FailureMessage string `json:"failureMessage,omitempty"`
}

func init() { register(&Cluster{}, &ClusterList{}) }
