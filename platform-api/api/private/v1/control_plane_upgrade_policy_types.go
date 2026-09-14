package v1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// ControlPlaneUpgradePolicy defines customer timing preferences for mandatory
// platform-managed control-plane upgrades. An omitted policy permits eligible
// upgrades to begin at any time.
type ControlPlaneUpgradePolicy struct {
	// MaintenanceWindow is the recurring window in which an automatic minor
	// version upgrade may begin.
	// +orlop:public
	MaintenanceWindow *RecurringMaintenanceWindow `json:"maintenanceWindow,omitempty"`

	// MaintenanceExclusions are one-off blackout periods. An active exclusion
	// takes precedence over the recurring maintenance window.
	// +orlop:public
	// +kubebuilder:validation:MaxItems=3
	MaintenanceExclusions []MaintenanceExclusion `json:"maintenanceExclusions,omitempty"`
}

// RecurringMaintenanceWindow defines an RFC 5545 recurring maintenance window.
type RecurringMaintenanceWindow struct {
	// Start anchors the first occurrence of the window.
	// +orlop:public
	Start metav1.Time `json:"start"`

	// Duration is the amount of time each occurrence remains open. The API
	// validation layer requires a minimum duration of four hours.
	// +orlop:public
	Duration metav1.Duration `json:"duration"`

	// Recurrence is an RFC 5545 recurrence rule, for example
	// FREQ=WEEKLY;BYDAY=SA.
	// +orlop:public
	// +kubebuilder:validation:MinLength=1
	Recurrence string `json:"recurrence"`

	// TimeZone is an IANA time-zone name used to evaluate local recurrence.
	// +orlop:public
	// +kubebuilder:default=UTC
	TimeZone string `json:"timeZone,omitempty"`
}

// MaintenanceExclusion defines a non-recurring period during which automatic
// control-plane minor version upgrades must not begin.
type MaintenanceExclusion struct {
	// Name identifies the exclusion within the Cluster policy.
	// +orlop:public
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Name string `json:"name"`

	// Start is the beginning of the blackout period.
	// +orlop:public
	Start metav1.Time `json:"start"`

	// End is the end of the blackout period and must be after Start.
	// +orlop:public
	End metav1.Time `json:"end"`
}
