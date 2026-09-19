package v1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
// Version provides clients with versions for cluster creation and upgrade
// validation, including their channel-group membership. Version resources are
// synchronized from Cincinnati by the version-sync controller and are read-only
// to end users.
type Version struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +orlop:public
	// +required
	Spec VersionSpec `json:"spec"`
}

// +kubebuilder:object:root=true
type VersionList struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ListMeta `json:"metadata,omitempty"`

	// +orlop:public
	Items []Version `json:"items"`
}

// VersionSpec contains release information synchronized from Cincinnati.
type VersionSpec struct {
	// ChannelGroups contains the Cincinnati channel groups containing this release.
	// +orlop:public
	// +required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:UniqueItems=true
	// +listType=set
	ChannelGroups []string `json:"channelGroups"`

	// ReleaseImage is the Cincinnati release payload used internally by Gecko.
	// It is intentionally not exposed through the public API.
	// +required
	// +kubebuilder:validation:MinLength=1
	ReleaseImage string `json:"releaseImage"`
}

func init() { register(&Version{}, &VersionList{}) }
