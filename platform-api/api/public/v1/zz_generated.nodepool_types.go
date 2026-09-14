package v1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Available",type=string,JSONPath=`.status.conditions[?(@.type=="NodePoolAvailable")].status`
// +kubebuilder:printcolumn:name="Healthy",type=string,JSONPath=`.status.conditions[?(@.type=="NodePoolHealthy")].status`
// +kubebuilder:printcolumn:name="Progressing",type=string,JSONPath=`.status.conditions[?(@.type=="NodePoolProgressing")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type NodePool struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +optional
	Spec NodePoolSpec `json:"spec,omitempty"`

	// +optional
	Status NodePoolStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type NodePoolList struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []NodePool `json:"items"`
}

// +kubebuilder:validation:XValidation:rule="!has(self.nodeCount) || !has(self.autoscaling)",message="nodeCount and autoscaling are mutually exclusive"
type NodePoolSpec struct {

	// +required
	ClusterID string `json:"clusterID"`

	// +required
	Platform NodePoolPlatformSpec `json:"platform"`

	// +required
	Release ReleaseSpec `json:"release"`

	// +optional
	// +kubebuilder:validation:Minimum=0
	NodeCount *int32 `json:"nodeCount,omitempty"`

	// +optional
	Autoscaling *AutoscalingSpec `json:"autoscaling,omitempty"`

	// +optional
	NodeLabels map[string]string `json:"nodeLabels,omitempty"`

	// +optional
	// +listType=map
	// +listMapKey=key
	Taints []TaintSpec `json:"taints,omitempty"`
}

type NodePoolPlatformSpec struct {

	// +required
	// +kubebuilder:validation:Enum=GCP
	Type string `json:"type"`

	// +optional
	GCP *GCPNodePoolPlatform `json:"gcp,omitempty"`
}

type GCPNodePoolPlatform struct {

	// +optional
	MachineType string `json:"machineType,omitempty"`

	// +optional
	// +kubebuilder:validation:Minimum=20
	DiskSizeGB int64 `json:"diskSizeGB,omitempty"`

	// +optional
	// +kubebuilder:validation:Enum=pd-standard;pd-ssd;pd-balanced
	DiskType string `json:"diskType,omitempty"`

	// +optional
	Zone string `json:"zone,omitempty"`

	// +optional
	Subnet string `json:"subnet,omitempty"`

	// +optional
	// +kubebuilder:validation:Enum=Standard;Spot;Preemptible
	ProvisioningModel string `json:"provisioningModel,omitempty"`

	// +optional
	// +kubebuilder:validation:Enum=MIGRATE;TERMINATE
	OnHostMaintenance string `json:"onHostMaintenance,omitempty"`

	// +optional
	// +listType=map
	// +listMapKey=key
	ResourceLabels []GCPResourceLabel `json:"resourceLabels,omitempty"`

	// +optional
	// +listType=atomic
	NetworkTags []string `json:"networkTags,omitempty"`
}

type TaintSpec struct {

	// +required
	Key string `json:"key"`

	// +optional
	Value string `json:"value,omitempty"`

	// +required
	// +kubebuilder:validation:Enum=NoSchedule;PreferNoSchedule;NoExecute
	Effect string `json:"effect"`
}

// +kubebuilder:validation:XValidation:rule="self.max >= self.min",message="max must be greater than or equal to min"
type AutoscalingSpec struct {

	// +optional
	// +kubebuilder:validation:Minimum=0
	Min *int32 `json:"min,omitempty"`

	// +required
	// +kubebuilder:validation:Minimum=1
	Max int32 `json:"max"`
}

// NodePoolStatus is written by controllers only.
type NodePoolStatus struct {

	// +optional
	// +listType=map
	// +listMapKey=type
	// +patchStrategy=merge
	// +patchMergeKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

func init() { register(&NodePool{}, &NodePoolList{}) }
