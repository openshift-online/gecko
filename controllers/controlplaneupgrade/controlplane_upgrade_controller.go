// Package controlplaneupgrade selects and observes control-plane upgrades.
package controlplaneupgrade

import (
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilversion "k8s.io/apimachinery/pkg/util/version"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	privatev1 "github.com/openshift-online/gecko/platform-api/api/private/v1"

	"github.com/openshift-online/gecko/controllers/util/logger"
)

const (
	conditionAvailable   = "ControlPlaneUpgradeAvailable"
	conditionProgressing = "ControlPlaneUpgradeProgressing"
	conditionDegraded    = "ControlPlaneUpgradeDegraded"

	requeueObserving = 15 * time.Second
	requeueStable    = 5 * time.Minute

	// PoC safety boundary: this controller can mutate only this development
	// Cluster. The target is selected from its advertised z-stream updates.
	pocClusterNamespace = "gcp-hcp-dev-customer-pvasanth"
	pocClusterName      = "upgrade-poc"
)

// Reconciler selects an advertised update by changing Cluster.spec.release.version,
// then observes the HC-owned version feedback until HyperShift completes it.
type Reconciler struct {
	client client.Client
	log    logger.Logger
}

// NewReconciler creates a control-plane upgrade reconciler.
func NewReconciler(c client.Client, log logger.Logger) *Reconciler {
	return &Reconciler{client: c, log: log}
}

// Reconcile evaluates one explicitly configured development Cluster.
func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	if req.Namespace != pocClusterNamespace || req.Name != pocClusterName {
		return reconcile.Result{}, nil
	}

	var cluster privatev1.Cluster
	if err := r.client.Get(ctx, req.NamespacedName, &cluster); err != nil {
		if apierrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, fmt.Errorf("control-plane upgrade: get cluster %s: %w", req.NamespacedName, err)
	}
	if !cluster.DeletionTimestamp.IsZero() {
		return reconcile.Result{}, nil
	}

	observed := cluster.Status.HostedClusterResult
	if observed == nil || observed.Version == "" || observed.DesiredVersion == "" {
		err := r.setConditions(ctx, &cluster,
			newCondition(conditionAvailable, metav1.ConditionUnknown, "WaitingForHostedClusterFeedback", "Waiting for completed and desired HostedCluster versions"),
			newCondition(conditionProgressing, metav1.ConditionFalse, "NoUpgradeRequested", "No control-plane upgrade has been requested"),
			newCondition(conditionDegraded, metav1.ConditionFalse, "AsExpected", ""),
		)
		return reconcile.Result{RequeueAfter: requeueObserving}, err
	}

	// A spec/observed mismatch means an upgrade has already been requested.
	// Observe it without selecting or requesting another target.
	if cluster.Spec.Release.Version != observed.Version || observed.DesiredVersion != observed.Version {
		target := cluster.Spec.Release.Version
		if observed.DesiredVersion != observed.Version {
			target = observed.DesiredVersion
		}
		if reason, message, failed := rolloutFailure(cluster.Status.Conditions); failed {
			err := r.setConditions(ctx, &cluster,
				newCondition(conditionAvailable, metav1.ConditionFalse, "UpgradeInProgress", fmt.Sprintf("Control-plane target is %s", target)),
				newCondition(conditionProgressing, metav1.ConditionFalse, "UpgradeFailed", fmt.Sprintf("Control-plane upgrade to %s is not progressing", target)),
				newCondition(conditionDegraded, metav1.ConditionTrue, reason, message),
			)
			return reconcile.Result{RequeueAfter: requeueStable}, err
		}

		reason := "WaitingForHostedCluster"
		message := fmt.Sprintf("Waiting for HostedCluster to accept control-plane version %s", target)
		if observed.DesiredVersion == target {
			reason = "HostedClusterProgressing"
			message = fmt.Sprintf("HostedCluster is upgrading the control plane from %s to %s", observed.Version, target)
		}
		err := r.setConditions(ctx, &cluster,
			newCondition(conditionAvailable, metav1.ConditionFalse, "UpgradeInProgress", fmt.Sprintf("Control-plane target is %s", target)),
			newCondition(conditionProgressing, metav1.ConditionTrue, reason, message),
			newCondition(conditionDegraded, metav1.ConditionFalse, "AsExpected", ""),
		)
		return reconcile.Result{RequeueAfter: requeueObserving}, err
	}

	target, err := selectTarget(observed.Version, observed.AvailableVersions)
	if err != nil {
		statusErr := r.setConditions(ctx, &cluster,
			newCondition(conditionAvailable, metav1.ConditionFalse, "TargetNotEligible", err.Error()),
			newCondition(conditionProgressing, metav1.ConditionFalse, "NoUpgradeRequested", "No control-plane upgrade has been requested"),
			newCondition(conditionDegraded, metav1.ConditionTrue, "TargetSelectionFailed", err.Error()),
		)
		return reconcile.Result{RequeueAfter: requeueStable}, statusErr
	}
	if target == "" {
		progressReason := "NoUpgradeRequested"
		progressMessage := "No control-plane upgrade has been requested"
		if meta.IsStatusConditionTrue(cluster.Status.Conditions, conditionProgressing) {
			progressReason = "UpgradeCompleted"
			progressMessage = fmt.Sprintf("Control-plane upgrade to %s completed", observed.Version)
		}
		err := r.setConditions(ctx, &cluster,
			newCondition(conditionAvailable, metav1.ConditionFalse, "NoRecommendedUpdate", fmt.Sprintf("No newer z-stream update is advertised for %s", observed.Version)),
			newCondition(conditionProgressing, metav1.ConditionFalse, progressReason, progressMessage),
			newCondition(conditionDegraded, metav1.ConditionFalse, "AsExpected", ""),
		)
		return reconcile.Result{RequeueAfter: requeueStable}, err
	}

	if !meta.IsStatusConditionTrue(cluster.Status.Conditions, "HostedClusterAvailable") {
		err := r.setConditions(ctx, &cluster,
			newCondition(conditionAvailable, metav1.ConditionFalse, "HostedClusterUnavailable", "HostedCluster must be available before an upgrade can begin"),
			newCondition(conditionProgressing, metav1.ConditionFalse, "UpgradeBlocked", "Control-plane upgrade is waiting for HostedCluster availability"),
			newCondition(conditionDegraded, metav1.ConditionFalse, "AsExpected", ""),
		)
		return reconcile.Result{RequeueAfter: requeueObserving}, err
	}
	if !meta.IsStatusConditionTrue(cluster.Status.Conditions, "ClusterVersionUpgradeable") {
		message := "HostedCluster has not reported ClusterVersionUpgradeable=True"
		if condition := meta.FindStatusCondition(cluster.Status.Conditions, "ClusterVersionUpgradeable"); condition != nil && condition.Message != "" {
			message = condition.Message
		}
		err := r.setConditions(ctx, &cluster,
			newCondition(conditionAvailable, metav1.ConditionFalse, "ClusterVersionNotUpgradeable", message),
			newCondition(conditionProgressing, metav1.ConditionFalse, "UpgradeBlocked", fmt.Sprintf("Control-plane upgrade to %s is blocked", target)),
			newCondition(conditionDegraded, metav1.ConditionFalse, "AsExpected", ""),
		)
		return reconcile.Result{RequeueAfter: requeueObserving}, err
	}
	if reason, message, failed := rolloutFailure(cluster.Status.Conditions); failed {
		err := r.setConditions(ctx, &cluster,
			newCondition(conditionAvailable, metav1.ConditionFalse, "HostedClusterUnhealthy", message),
			newCondition(conditionProgressing, metav1.ConditionFalse, "UpgradeBlocked", fmt.Sprintf("Control-plane upgrade to %s is blocked", target)),
			newCondition(conditionDegraded, metav1.ConditionTrue, reason, message),
		)
		return reconcile.Result{RequeueAfter: requeueStable}, err
	}

	before := cluster.DeepCopy()
	cluster.Spec.Release.Version = target
	if err := r.client.Patch(ctx, &cluster, client.MergeFrom(before)); err != nil {
		return reconcile.Result{}, fmt.Errorf("control-plane upgrade: request version %s for cluster %s: %w", target, cluster.Name, err)
	}
	r.log.Infof(ctx, "control-plane upgrade: cluster %s requested version %s", cluster.Name, target)

	err = r.setConditions(ctx, &cluster,
		newCondition(conditionAvailable, metav1.ConditionTrue, "UpdateSelected", fmt.Sprintf("Selected advertised control-plane update %s", target)),
		newCondition(conditionProgressing, metav1.ConditionTrue, "UpgradeRequested", fmt.Sprintf("Requested control-plane upgrade from %s to %s", observed.Version, target)),
		newCondition(conditionDegraded, metav1.ConditionFalse, "AsExpected", ""),
	)
	return reconcile.Result{RequeueAfter: requeueObserving}, err
}

func (r *Reconciler) setConditions(ctx context.Context, cluster *privatev1.Cluster, conditions ...metav1.Condition) error {
	changed := false
	for _, condition := range conditions {
		condition.ObservedGeneration = cluster.Generation
		changed = meta.SetStatusCondition(&cluster.Status.Conditions, condition) || changed
	}
	if !changed {
		return nil
	}
	if err := r.client.Status().Update(ctx, cluster); err != nil {
		if apierrors.IsConflict(err) {
			return nil
		}
		return fmt.Errorf("control-plane upgrade: update cluster %s status: %w", cluster.Name, err)
	}
	return nil
}

func newCondition(conditionType string, status metav1.ConditionStatus, reason, message string) metav1.Condition {
	return metav1.Condition{Type: conditionType, Status: status, Reason: reason, Message: message}
}

// selectTarget selects only z-stream updates. Minor-version maintenance policy
// evaluation is intentionally left for the production controller refinement.
func selectTarget(current string, advertised []string) (string, error) {
	currentVersion, err := utilversion.ParseSemantic(current)
	if err != nil {
		return "", fmt.Errorf("parse current version %q: %w", current, err)
	}

	var selected string
	var selectedVersion *utilversion.Version
	for _, candidate := range advertised {
		candidateVersion, err := utilversion.ParseSemantic(candidate)
		if err != nil {
			continue
		}
		if candidateVersion.Major() != currentVersion.Major() || candidateVersion.Minor() != currentVersion.Minor() {
			continue
		}
		if !currentVersion.LessThan(candidateVersion) {
			continue
		}
		if selectedVersion == nil || selectedVersion.LessThan(candidateVersion) {
			selected = candidate
			selectedVersion = candidateVersion
		}
	}
	return selected, nil
}

func rolloutFailure(conditions []metav1.Condition) (reason, message string, failed bool) {
	if condition := meta.FindStatusCondition(conditions, "HostedClusterDegraded"); condition != nil && condition.Status == metav1.ConditionTrue {
		return "HostedClusterDegraded", condition.Message, true
	}
	if condition := meta.FindStatusCondition(conditions, "ClusterVersionReleaseAccepted"); condition != nil && condition.Status == metav1.ConditionFalse {
		return "ReleaseNotAccepted", condition.Message, true
	}
	if condition := meta.FindStatusCondition(conditions, "ClusterVersionSucceeding"); condition != nil && condition.Status == metav1.ConditionFalse {
		return "ClusterVersionNotSucceeding", condition.Message, true
	}
	return "", "", false
}
