package v1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
// +orlop:authorization-policy: get=authenticated-catalog-read,list=authenticated-catalog-read
// Channel provides clients with the default version for cluster installation
// and platform controls for supported releases and automatic fleet upgrades.
// Channel resources are managed by the platform and are read-only to end users.
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
}

func init() { register(&Channel{}, &ChannelList{}) }
