package v1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// Quota is the authoritative per-namespace record of resource limits and live
// consumption for HostedCluster and NodePool objects. Each namespace has exactly
// one Quota object, named "default", created and maintained by the platform.
// Users have read-only access via the public API; operators manage limits and
// auto-approval thresholds via the private API.
//
// +orlop:public-verbs: list,get
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:subresource:status
type Quota struct {
	metav1.TypeMeta `json:",inline"`
	// metadata is the standard object metadata.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec contains the configured resource limits for this namespace.
	// +orlop:public
	// +optional
	Spec QuotaSpec `json:"spec,omitempty"`

	// status contains live consumption data observed by the platform.
	// +orlop:public
	// +optional
	Status QuotaStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type QuotaList struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ListMeta `json:"metadata,omitempty"`

	// items is the list of Quota objects.
	// +orlop:public
	Items []Quota `json:"items"`
}

// QuotaSpec contains the configured resource limits for a namespace.
type QuotaSpec struct {
	// resources contains the per-resource quota configurations.
	// +orlop:public
	// +optional
	// +listType=map
	// +listMapKey=resource
	Resources []QuotaResourceSpec `json:"resources,omitempty"`
}

// QuotaResourceSpec contains the quota configuration for a single resource kind.
type QuotaResourceSpec struct {
	// resource is the plural lowercase resource name, e.g. "hostedclusters" or "nodepools".
	// +orlop:public
	// +required
	// +kubebuilder:validation:Enum=hostedclusters;nodepools
	Resource string `json:"resource"`

	// limit is the effective enforced limit for this resource in the namespace.
	// It reflects the base limit, raised by any approved QuotaRequest.
	// Operators may set this directly via the private API to lower or raise the
	// ceiling independently of the QuotaRequest workflow.
	// +orlop:public
	// +required
	// +kubebuilder:validation:Minimum=0
	Limit int32 `json:"limit"`

	// autoApproveThreshold is the maximum requestedLimit that the QuotaRequest
	// controller will approve automatically without operator intervention.
	// This field is internal and not exposed on the public API.
	// +optional
	// +kubebuilder:validation:Minimum=0
	AutoApproveThreshold *int32 `json:"autoApproveThreshold,omitempty"`
}

const (
	// QuotaConditionHighUsage is set to True when at least one tracked resource
	// has reached or exceeded 80% of its effective limit. It transitions back to
	// False once all resources drop below the threshold. The condition signals
	// that a QuotaRequest should be submitted before the limit is exhausted.
	QuotaConditionHighUsage = "HighUsage"
)

// QuotaStatus contains live consumption data for a namespace.
type QuotaStatus struct {
	// conditions represents the latest observed state of the Quota object.
	// The HighUsage condition is True when at least one resource has reached
	// 80% of its effective limit.
	// +orlop:public
	// +optional
	// +listType=map
	// +listMapKey=type
	// +patchStrategy=merge
	// +patchMergeKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// resources contains per-resource consumption data.
	// +orlop:public
	// +optional
	// +listType=map
	// +listMapKey=resource
	Resources []QuotaResourceStatus `json:"resources,omitempty"`
}

// QuotaResourceStatus contains live consumption data for a single resource kind.
type QuotaResourceStatus struct {
	// resource is the plural lowercase resource name, e.g. "hostedclusters" or "nodepools".
	// +orlop:public
	// +required
	// +kubebuilder:validation:Enum=hostedclusters;nodepools
	Resource string `json:"resource"`

	// current is the number of objects of this resource kind currently in the namespace.
	// +orlop:public
	// +required
	// +kubebuilder:validation:Minimum=0
	Current int32 `json:"current"`

	// quotaReachedTime is the time at which the limit was first reached for this
	// resource. Null when the limit has not been reached.
	// +orlop:public
	// +optional
	QuotaReachedTime *metav1.Time `json:"quotaReachedTime,omitempty"`
}

// QuotaRequest allows users to request a quota limit increase for a namespace.
// Users submit a QuotaRequest with a justification; the QuotaRequest controller
// automatically approves requests that fall within the namespace's
// autoApproveThreshold. Requests that exceed the threshold remain Pending for
// operator review.
//
// QuotaRequest objects are immutable after creation. Approved requests cannot
// be deleted via the public API, preserving them as an immutable audit record.
//
// +orlop:public-verbs: create,get,list,delete
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:subresource:status
type QuotaRequest struct {
	metav1.TypeMeta `json:",inline"`
	// metadata is the standard object metadata.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec defines the requested quota increase.
	// +orlop:public
	// +required
	Spec QuotaRequestSpec `json:"spec"`

	// status reflects the current state of the request.
	// +orlop:public
	// +optional
	Status QuotaRequestStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type QuotaRequestList struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ListMeta `json:"metadata,omitempty"`

	// items is the list of QuotaRequests.
	// +orlop:public
	Items []QuotaRequest `json:"items"`
}

// QuotaRequestSpec contains the user's requested limit increase.
type QuotaRequestSpec struct {
	// resource is the plural lowercase resource name for which a limit increase
	// is requested, e.g. "hostedclusters" or "nodepools".
	// This field is immutable after creation.
	// +orlop:public
	// +required
	// +kubebuilder:validation:Enum=hostedclusters;nodepools
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="resource is immutable"
	Resource string `json:"resource"`

	// requestedLimit is the new limit value being requested.
	// Must be greater than the current effective limit.
	// This field is immutable after creation.
	// +orlop:public
	// +required
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="requestedLimit is immutable"
	RequestedLimit int32 `json:"requestedLimit"`

	// reason describes why the limit increase is needed.
	// This field is immutable after creation.
	// +orlop:public
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="reason is immutable"
	Reason string `json:"reason"`

	// neededBy is the date by which the additional quota is required.
	// Providing this information helps the platform pre-provision shared
	// infrastructure before the new limit is reached.
	// This field is immutable after creation.
	// +orlop:public
	// +optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="neededBy is immutable"
	NeededBy *metav1.Time `json:"neededBy,omitempty"`
}

const (
	// QuotaRequestConditionApproved tracks whether the request has been approved.
	//
	// True  — the requested limit increase was granted; reason is either
	//         "AutoApproved" (controller) or "ManuallyApproved" (operator).
	//         The message contains the approved limit value.
	// False — the request was denied by an operator; reason is "Denied".
	//         The message contains the operator's explanation.
	// Unknown — the request is still pending evaluation; reason is one of:
	//           "Pending"        — awaiting controller evaluation or operator review.
	//           "Superseded"     — a newer request for the same resource was submitted.
	//           "NotIncreasing"  — the requestedLimit does not exceed the current
	//                              effective limit; operator review required.
	QuotaRequestConditionApproved = "Approved"

	// QuotaRequestConditionSuperseded tracks whether the request has been
	// replaced by a newer one for the same resource.
	//
	// True  — a newer QuotaRequest for the same resource exists in the
	//         namespace. The message names the superseding request.
	//         Reason is "Superseded". The request may be deleted.
	// False — the request is not superseded (initial state).
	QuotaRequestConditionSuperseded = "Superseded"

	// Reason values for QuotaRequestConditionApproved.
	QuotaRequestReasonPending          = "Pending"
	QuotaRequestReasonAutoApproved     = "AutoApproved"
	QuotaRequestReasonManuallyApproved = "ManuallyApproved"
	QuotaRequestReasonDenied           = "Denied"
	// QuotaRequestReasonSuperseded is also used on QuotaRequestConditionApproved
	// (Unknown) when the request has been replaced by a newer one.
	QuotaRequestReasonSuperseded = "Superseded"
	// QuotaRequestReasonNotIncreasing is set on QuotaRequestConditionApproved
	// (Unknown) when the requestedLimit does not exceed the current effective
	// limit — for example because an operator already raised Quota.spec.limit
	// via the private API to a value at or above what was requested. The
	// request cannot be auto-approved (doing so would overwrite the current
	// limit with a lower or equal value) and is left for operator review.
	QuotaRequestReasonNotIncreasing = "NotIncreasing"
)

// QuotaRequestStatus contains the current state of a QuotaRequest expressed
// as standard Kubernetes conditions.
type QuotaRequestStatus struct {
	// conditions represents the latest observed state of the QuotaRequest.
	//
	// Condition types:
	//   Approved   — True when granted, False when denied, Unknown when pending
	//                or superseded.
	//   Superseded — True when a newer QuotaRequest for the same resource
	//                exists in the namespace.
	//
	// +orlop:public
	// +optional
	// +listType=map
	// +listMapKey=type
	// +patchStrategy=merge
	// +patchMergeKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// approvedLimit is the limit value that was granted.
	// Set only when the Approved condition is True.
	// +orlop:public
	// +optional
	// +kubebuilder:validation:Minimum=1
	ApprovedLimit *int32 `json:"approvedLimit,omitempty"`

	// autoApproved indicates whether this request was automatically approved by
	// the controller (true) or by an operator (false). Private API only.
	// +optional
	AutoApproved *bool `json:"autoApproved,omitempty"`
}

// IsApproved returns true when the QuotaRequest has been granted.
func (s *QuotaRequestStatus) IsApproved() bool {
	for _, c := range s.Conditions {
		if c.Type == QuotaRequestConditionApproved {
			return c.Status == metav1.ConditionTrue
		}
	}
	return false
}

// IsSuperseded returns true when the QuotaRequest has been replaced by a
// newer request for the same resource.
func (s *QuotaRequestStatus) IsSuperseded() bool {
	for _, c := range s.Conditions {
		if c.Type == QuotaRequestConditionSuperseded {
			return c.Status == metav1.ConditionTrue
		}
	}
	return false
}

// IsPending returns true when the request has not yet been decided and has not
// been superseded or blocked by a NotIncreasing condition.
func (s *QuotaRequestStatus) IsPending() bool {
	return !s.IsApproved() && !s.IsSuperseded() && !s.isDenied() && !s.isNotIncreasing()
}

func (s *QuotaRequestStatus) isNotIncreasing() bool {
	for _, c := range s.Conditions {
		if c.Type == QuotaRequestConditionApproved &&
			c.Status == metav1.ConditionUnknown &&
			c.Reason == QuotaRequestReasonNotIncreasing {
			return true
		}
	}
	return false
}

func (s *QuotaRequestStatus) isDenied() bool {
	for _, c := range s.Conditions {
		if c.Type == QuotaRequestConditionApproved && c.Status == metav1.ConditionFalse {
			return true
		}
	}
	return false
}

func init() {
	register(&Quota{}, &QuotaList{})
	register(&QuotaRequest{}, &QuotaRequestList{})
}
