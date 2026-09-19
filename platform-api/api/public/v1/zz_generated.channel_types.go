package v1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
type Channel struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +required
	Spec ChannelSpec `json:"spec"`
}

// +kubebuilder:object:root=true
type ChannelList struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []Channel `json:"items"`
}

// ChannelSpec contains platform-managed controls for a channel group.
type ChannelSpec struct {
	// InstallDefaultVersion is the exact release selected when a client does
	// not provide a version during cluster creation.

	// +required
	// +kubebuilder:validation:MinLength=1
	InstallDefaultVersion string `json:"installDefaultVersion"`

	// FleetMinorVersion is the major.minor release line approved for automatic
	// fleet upgrades.

	// +required
	// +kubebuilder:validation:Pattern=`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`
	FleetMinorVersion string `json:"fleetMinorVersion"`
}

func init() { register(&Channel{}, &ChannelList{}) }
