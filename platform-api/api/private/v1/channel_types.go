package v1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
// Channel provides clients with the default version for cluster installation
// and the minor version approved for automatic fleet upgrades. Channel resources
// are managed by the platform and are read-only to end users.
type Channel struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +orlop:public
	// +required
	Spec ChannelSpec `json:"spec"`
}

// +kubebuilder:object:root=true
type ChannelList struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ListMeta `json:"metadata,omitempty"`

	// +orlop:public
	Items []Channel `json:"items"`
}

// ChannelSpec contains platform-managed controls for a channel group.
type ChannelSpec struct {
	// InstallDefaultVersion is the exact release selected when a client does
	// not provide a version during cluster creation.
	// +orlop:public
	// +required
	// +kubebuilder:validation:MinLength=1
	InstallDefaultVersion string `json:"installDefaultVersion"`

	// FleetMinorVersion is the major.minor release line approved for automatic
	// fleet upgrades.
	// +orlop:public
	// +required
	// +kubebuilder:validation:Pattern=`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`
	FleetMinorVersion string `json:"fleetMinorVersion"`
}

func init() { register(&Channel{}, &ChannelList{}) }
