package hc

import (
	"context"
	"fmt"

	"github.com/openshift-online/gecko/controllers/client/transport"
	"github.com/openshift-online/gecko/controllers/util/constants"
	"github.com/openshift-online/gecko/controllers/util/logger"
	privatev1 "github.com/openshift-online/gecko/platform-api/api/private/v1"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// conditionEtcdBackendReady reports whether the socketcd Spanner backend of an
// opted-in cluster is ready to serve etcd.
const conditionEtcdBackendReady = "EtcdBackendReady"

// observeBackend registers the SpannerEtcd of a cluster that uses the socketcd
// backend as a read-only target. Other clusters have nothing to observe.
func (r *Reconciler) observeBackend(ctx context.Context, cluster *privatev1.Cluster, managementCluster, groupKey string) error {
	if !usesSpannerEtcd(cluster) {
		return nil
	}
	if err := r.transport.Observe(ctx, managementCluster, groupKey, []transport.ResourceRef{spannerEtcdRef(cluster)}); err != nil {
		return fmt.Errorf("%s: observe spanner etcd: %w", adapterName, err)
	}
	return nil
}

// spannerEtcdRef returns the SpannerEtcd that socketcd creates next to a
// HostedCluster, with the same namespace and name.
func spannerEtcdRef(cluster *privatev1.Cluster) transport.ResourceRef {
	return transport.ResourceRef{
		Group:     constants.SocketCDGroup,
		Version:   constants.SocketCDVersion,
		Resource:  constants.SocketCDResource,
		Namespace: hostedClusterNamespace(cluster),
		Name:      hostedClusterName(cluster),
	}
}

// hostedClusterNamespace returns the management cluster namespace of the
// Cluster's HostedCluster.
func hostedClusterNamespace(cluster *privatev1.Cluster) string {
	return fmt.Sprintf("clusters-%s", cluster.UID)
}

// hostedClusterName returns the name of the Cluster's HostedCluster.
func hostedClusterName(cluster *privatev1.Cluster) string {
	if cluster.Spec.SafeName != "" {
		return cluster.Spec.SafeName
	}
	return privatev1.DefaultSafeName(cluster.Name, cluster.UID)
}

// ensureEtcdBackend selects and records the Cluster's etcd backend the first
// time the Cluster is seen; usesSpannerEtcd then reports it.
//
// The selection is a one-time decision, written before anything is applied so a
// later change of the flag, a retry, or a crash cannot select a different type
// for the HostedCluster. A Cluster whose HostedCluster already exists predates
// the setting and necessarily has Managed etcd, which is recognized from its
// status and, failing that, from the resources already dispatched for it.
func (r *Reconciler) ensureEtcdBackend(ctx context.Context, cluster *privatev1.Cluster) error {
	switch backend := cluster.Annotations[constants.AnnotationEtcdBackend]; backend {
	case constants.EtcdBackendSpanner, constants.EtcdBackendManaged:
		return nil
	case "":
	default:
		return fmt.Errorf("%s: unknown etcd backend %q in annotation %s", adapterName, backend, constants.AnnotationEtcdBackend)
	}

	backend := constants.EtcdBackendManaged
	if r.spannerEtcd && !hostedClusterMayExist(cluster) {
		dispatched, err := r.resourcesDispatched(ctx, cluster)
		if err != nil {
			return err
		}
		if !dispatched {
			backend = constants.EtcdBackendSpanner
		}
	}
	if err := r.recordEtcdBackend(ctx, cluster, backend); err != nil {
		return fmt.Errorf("%s: record etcd backend: %w", adapterName, err)
	}
	return nil
}

// recordEtcdBackend writes the Cluster's etcd backend to its annotation.
func (r *Reconciler) recordEtcdBackend(ctx context.Context, cluster *privatev1.Cluster, backend string) error {
	if cluster.Annotations == nil {
		cluster.Annotations = map[string]string{}
	}
	cluster.Annotations[constants.AnnotationEtcdBackend] = backend
	return r.client.Update(ctx, cluster)
}

// resourcesDispatched reports whether resources were already dispatched to the
// Cluster's management cluster. An earlier controller may have applied the
// HostedCluster without any of it being reflected in the Cluster's status yet,
// so the transport is the authority. Nothing can have been dispatched before
// placement. A failed query is an error rather than a guess, because guessing
// wrong selects the wrong etcd type for a HostedCluster that cannot change it.
func (r *Reconciler) resourcesDispatched(ctx context.Context, cluster *privatev1.Cluster) (bool, error) {
	placement := cluster.Status.PlacementResult
	if placement == nil || placement.ManagementClusterName == "" {
		return false, nil
	}
	groupKey, err := transport.ClusterGroupKey(cluster.Namespace, cluster.Name)
	if err != nil {
		return false, fmt.Errorf("%s: build group key: %w", adapterName, err)
	}
	status, err := r.transport.GetDeleteStatus(ctx, placement.ManagementClusterName, groupKey)
	if err != nil {
		return false, fmt.Errorf("%s: check for dispatched resources: %w", adapterName, err)
	}
	return status != nil && (status.ApplyDesiresCount > 0 || status.TotalCount > 0), nil
}

// correctEtcdBackend aligns the recorded etcd backend with the live
// HostedCluster, whose etcd management type HyperShift does not allow to change.
// The record normally matches it by construction. It can differ when a Cluster
// was recorded wrongly, for example one whose HostedCluster an earlier
// controller applied in a way no status or desire revealed. Left alone, every
// apply of such a Cluster would be rejected, so the record is corrected and the
// next reconcile applies the type the HostedCluster really has.
//
// Only a readable, recognized type is acted on. Missing, empty or unrecognized
// feedback says nothing about the HostedCluster and never changes the record.
// Unmanaged etcd is read as the socketcd backend only when the HostedCluster
// carries the socketcd opt-in annotation, so an Unmanaged etcd that Gecko did
// not set up for socketcd is left alone rather than guessed at. Returns true if
// the record was changed.
func (r *Reconciler) correctEtcdBackend(ctx context.Context, cluster *privatev1.Cluster, managementCluster, groupKey string, mwStatus *transport.Status, log logger.Logger) (bool, error) {
	if mwStatus == nil {
		return false, nil
	}
	feedback := mwStatus.ResourceStatuses[transport.ResourceKey(constants.HyperShiftGroup, constants.HyperShiftVersion,
		"hostedclusters", hostedClusterNamespace(cluster), hostedClusterName(cluster))]
	var want string
	switch {
	case feedback["etcdManagementType"] == "Managed":
		want = constants.EtcdBackendManaged
	case feedback["etcdManagementType"] == "Unmanaged" && feedback["spannerEnabled"] == "true":
		want = constants.EtcdBackendSpanner
	default:
		return false, nil
	}
	recorded := cluster.Annotations[constants.AnnotationEtcdBackend]
	if recorded == want {
		return false, nil
	}

	log.Warnf(ctx, "%s: cluster %s is recorded with etcd backend %q but its HostedCluster uses %q etcd; correcting the record",
		adapterName, cluster.Name, recorded, feedback["etcdManagementType"])
	// The SpannerEtcd target registered for a mistaken Spanner record is released
	// first, so a failure here is retried instead of being forgotten.
	if want == constants.EtcdBackendManaged {
		if err := r.transport.ReleaseObserved(ctx, managementCluster, groupKey); err != nil {
			return false, fmt.Errorf("%s: release observed resources: %w", adapterName, err)
		}
	}
	if err := r.recordEtcdBackend(ctx, cluster, want); err != nil {
		return false, fmt.Errorf("%s: correct etcd backend: %w", adapterName, err)
	}
	return true, nil
}

// hostedClusterMayExist reports whether the Cluster's HostedCluster has been
// applied or observed. Such a Cluster was created before it was possible to
// select a backend.
func hostedClusterMayExist(cluster *privatev1.Cluster) bool {
	return cluster.Status.HostedClusterResult != nil ||
		meta.IsStatusConditionTrue(cluster.Status.Conditions, "ResourcesApplied")
}

// usesSpannerEtcd reports whether the recorded backend of the Cluster is
// socketcd's Spanner-backed etcd. Observation, status and gating follow it
// rather than the flag, so they keep working for a cluster created under the
// flag after the flag is turned off.
func usesSpannerEtcd(cluster *privatev1.Cluster) bool {
	return cluster.Annotations[constants.AnnotationEtcdBackend] == constants.EtcdBackendSpanner
}

// observedBackendCondition returns the EtcdBackendReady condition for the
// reconcile, or nil when the cluster does not use the socketcd backend.
func observedBackendCondition(cluster *privatev1.Cluster, mwStatus *transport.Status, clusterNS, safeName string, gen int64) *metav1.Condition {
	if !usesSpannerEtcd(cluster) {
		return nil
	}
	key := transport.ResourceKey(constants.SocketCDGroup, constants.SocketCDVersion, constants.SocketCDResource, clusterNS, safeName)
	return etcdBackendCondition(mwStatus.ResourceStatuses[key], gen)
}

// backendNotFoundCondition returns the EtcdBackendReady condition used before
// any resources have been applied, or nil when the cluster does not use the
// socketcd backend.
func backendNotFoundCondition(cluster *privatev1.Cluster, gen int64) *metav1.Condition {
	if !usesSpannerEtcd(cluster) {
		return nil
	}
	return &metav1.Condition{
		Type:               conditionEtcdBackendReady,
		Status:             metav1.ConditionFalse,
		Reason:             "ResourcesNotFound",
		Message:            "Resources have not been applied yet",
		ObservedGeneration: gen,
	}
}

// applyBackendCondition writes the backend condition, or removes a leftover one
// when the cluster does not use the backend because nothing observes it. Returns
// true if the conditions changed.
func applyBackendCondition(cluster *privatev1.Cluster, backend *metav1.Condition) bool {
	if backend == nil {
		return meta.RemoveStatusCondition(&cluster.Status.Conditions, conditionEtcdBackendReady)
	}
	return meta.SetStatusCondition(&cluster.Status.Conditions, *backend)
}

// backendBlocksAvailability reports whether the backend reported a failure.
// Unknown does not block: it covers both a backend that has not reported yet
// and one that is still provisioning, neither of which is a failure.
func backendBlocksAvailability(backend *metav1.Condition) bool {
	return backend != nil && backend.Status == metav1.ConditionFalse
}

// etcdBackendCondition derives EtcdBackendReady from the SpannerEtcd feedback.
// socketcd's Ready condition names the first stage that is not ready in its
// reason and message, so a failure is reported as it was observed. An empty
// feedback map means the object has not been read yet, which is not evidence
// about the backend either way.
func etcdBackendCondition(feedback map[string]string, gen int64) *metav1.Condition {
	status, ok := feedback["readyCondition"]
	if !ok {
		return &metav1.Condition{
			Type:               conditionEtcdBackendReady,
			Status:             metav1.ConditionUnknown,
			Reason:             "BackendNotReported",
			Message:            "socketcd has not reported the Spanner etcd backend status yet",
			ObservedGeneration: gen,
		}
	}
	if status == string(metav1.ConditionTrue) {
		return &metav1.Condition{
			Type:               conditionEtcdBackendReady,
			Status:             metav1.ConditionTrue,
			Reason:             "BackendReady",
			Message:            "The Spanner etcd backend is ready",
			ObservedGeneration: gen,
		}
	}
	condition := &metav1.Condition{
		Type:               conditionEtcdBackendReady,
		Status:             metav1.ConditionFalse,
		Reason:             feedback["readyConditionReason"],
		Message:            feedback["readyConditionMessage"],
		ObservedGeneration: gen,
	}
	if status == string(metav1.ConditionUnknown) {
		condition.Status = metav1.ConditionUnknown
	}
	if condition.Reason == "" {
		condition.Reason = "BackendNotReady"
	}
	return condition
}
