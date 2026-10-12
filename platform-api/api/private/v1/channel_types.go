package v1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
// +orlop:public-verbs: get,list
// +orlop:authorization-policy: get=authenticated-catalog-read,list=authenticated-catalog-read
// Channel provides clients with the default version for cluster installation
// and platform controls for supported releases and automatic fleet upgrades.
// Channel resources are managed by the platform and are read-only to end users.
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
	// MinimumSupportedVersion is the oldest major.minor release line included
	// in this Channel's version catalog, for example 4.22 or 4.24. It is independent
	// of fleet upgrade authorization and the pinned installation default.
	// +required
	// +kubebuilder:validation:Pattern=`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`
	MinimumSupportedVersion string `json:"minimumSupportedVersion"`

	// InstallDefaultVersion is the exact release selected when a client does
	// not provide a version during cluster creation.
	// +orlop:public
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
