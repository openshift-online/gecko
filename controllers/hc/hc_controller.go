// Package hc implements the hc-controller reconciler for managing HostedClusters via kube-applier-gcp.
package hc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/openshift-online/gecko/controllers/client/transport"
	"github.com/openshift-online/gecko/controllers/hc/manifest"
	"github.com/openshift-online/gecko/controllers/util/constants"
	"github.com/openshift-online/gecko/controllers/util/logger"
	privatev1 "github.com/openshift-online/gecko/platform-api/api/private/v1"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	adapterName            = "hc-controller"
	NodePoolClusterIDField = "spec.clusterID"

	requeuePending = 15 * time.Second
	// requeueCorrected is the short delay after a recorded etcd backend is
	// corrected, so the next reconcile applies the right type without waiting.
	requeueCorrected = time.Second
	requeueStable    = 5 * time.Minute
)

// Reconciler implements the hc-controller reconcile loop.
type Reconciler struct {
	transport           transport.Client
	log                 logger.Logger
	client              client.Client
	customerLabels      map[string]string
	pullSecretStoreName string
	pullSecretGCPKey    string
	spannerEtcd         bool
}

// Option customizes a Reconciler.
type Option func(*Reconciler)

// WithSpannerEtcd makes socketcd's Spanner-backed Unmanaged etcd the backend of
// clusters the reconciler sees for the first time. HyperShift does not allow the
// etcd management type to change after creation, so each Cluster's backend is
// recorded in AnnotationEtcdBackend when it is first reconciled and never
// changes afterwards: the setting never affects a cluster that has already been
// recorded, or one whose HostedCluster already exists.
func WithSpannerEtcd(enabled bool) Option {
	return func(r *Reconciler) { r.spannerEtcd = enabled }
}

// New creates a new Reconciler.
func New(transport transport.Client, log logger.Logger, c client.Client, customerLabels map[string]string, pullSecretStoreName, pullSecretGCPKey string, opts ...Option) *Reconciler {
	r := &Reconciler{
		transport:           transport,
		log:                 log,
		client:              c,
		customerLabels:      customerLabels,
		pullSecretStoreName: pullSecretStoreName,
		pullSecretGCPKey:    pullSecretGCPKey,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Reconcile runs the hc-controller loop for one cluster event.
func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	groupKey, err := transport.ClusterGroupKey(req.Namespace, req.Name)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("build group key: %w", err)
	}
	log := r.log.With("controller", adapterName).With("cluster_id", req.Name)

	var cluster privatev1.Cluster
	if err := r.client.Get(ctx, req.NamespacedName, &cluster); err != nil {
		if apierrors.IsNotFound(err) {
			log.Infof(ctx, "cluster not found, skipping")
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, fmt.Errorf("%s: get cluster: %w", adapterName, err)
	}

	// Handle deletion.
	if !cluster.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, &cluster, log)
	}

	// Ensure finalizer is present.
	if !controllerutil.ContainsFinalizer(&cluster, constants.FinalizerCluster) {
		controllerutil.AddFinalizer(&cluster, constants.FinalizerCluster)
		if err := r.client.Update(ctx, &cluster); err != nil {
			return reconcile.Result{}, fmt.Errorf("%s: add finalizer: %w", adapterName, err)
		}
		return reconcile.Result{}, nil // re-reconcile with finalizer in place
	}

	spanner, err := r.ensureEtcdBackend(ctx, &cluster)
	if err != nil {
		return reconcile.Result{}, err
	}

	ready, err := r.prerequisitesReady(ctx, &cluster, log)
	if err != nil {
		return reconcile.Result{}, err
	}
	if !ready {
		return reconcile.Result{RequeueAfter: requeuePending}, nil
	}

	placement := cluster.Status.PlacementResult
	vr := cluster.Status.VersionResolution
	safeName := cluster.Spec.SafeName
	if safeName == "" {
		safeName = privatev1.DefaultSafeName(cluster.Name, cluster.UID)
	}

	// Extract platform fields.
	var gcpProjectID, gcpRegion, gcpNetwork, gcpSubnet, gcpEndpointAccess string
	var wifProjectNumber, wifPoolID, wifProviderID string
	var nodePoolEmail, controlPlaneEmail, cloudControllerEmail string
	var storageEmail, imageRegistryEmail, networkEmail, ingressEmail string
	if gcp := cluster.Spec.Platform.GCP; gcp != nil {
		gcpProjectID = gcp.ProjectID
		gcpRegion = gcp.Region
		gcpNetwork = gcp.Network
		gcpSubnet = gcp.Subnet
		gcpEndpointAccess = gcp.EndpointAccess
		wif := gcp.WorkloadIdentity
		wifProjectNumber = wif.ProjectNumber
		wifPoolID = wif.PoolID
		wifProviderID = wif.ProviderID
		if sa := wif.ServiceAccountsRef; sa != nil {
			nodePoolEmail = sa.NodePoolEmail
			controlPlaneEmail = sa.ControlPlaneEmail
			cloudControllerEmail = sa.CloudControllerEmail
			storageEmail = sa.StorageEmail
			imageRegistryEmail = sa.ImageRegistryEmail
			networkEmail = sa.NetworkEmail
			if sa.IngressEmail != nil {
				ingressEmail = *sa.IngressEmail
			}
		}
	}

	// Build manifests. The ingress default certificate is staged: request it
	// once HyperShift has published the ACME delegation CNAME, then reference it
	// once cert-manager has produced the serving secret.
	enableIngress := ingressEmail != ""
	includeIngressCertificate := enableIngress && ingressCertificateRequested(&cluster)
	includeDefaultIngressCert := includeIngressCertificate &&
		(ingressCertificateReady(&cluster) || ingressDefaultCertificateApplied(&cluster))

	// TODO: CreatedBy is not yet in the orlop ClusterSpec.
	mwInput := manifest.Input{
		ClusterID:                 string(cluster.UID),
		ClusterName:               safeName,
		Generation:                cluster.Generation,
		CreatedBy:                 cluster.Annotations[constants.AnnotationCreatedBy],
		InfraID:                   cluster.Spec.InfraID,
		IssuerURL:                 cluster.Spec.IssuerURL,
		GCPProjectID:              gcpProjectID,
		GCPRegion:                 gcpRegion,
		GCPNetwork:                gcpNetwork,
		GCPSubnet:                 gcpSubnet,
		GCPEndpointAccess:         gcpEndpointAccess,
		WIFProjectNumber:          wifProjectNumber,
		WIFPoolID:                 wifPoolID,
		WIFProviderID:             wifProviderID,
		NodePoolEmail:             nodePoolEmail,
		ControlPlaneEmail:         controlPlaneEmail,
		CloudControllerEmail:      cloudControllerEmail,
		StorageEmail:              storageEmail,
		ImageRegistryEmail:        imageRegistryEmail,
		NetworkEmail:              networkEmail,
		IngressEmail:              ingressEmail,
		IncludeIngressCertificate: includeIngressCertificate,
		IncludeDefaultIngressCert: includeDefaultIngressCert,
		ReleaseImage:              vr.ReleaseImage,
		ReleaseChannel:            vr.CincinnatiChannel,
		BaseDomain:                placement.BaseDomain,
		PullSecretStoreName:       r.pullSecretStoreName,
		PullSecretGCPKey:          r.pullSecretGCPKey,
		ResourceLabels:            r.customerLabels,
		SpannerEtcd:               spanner,
	}

	manifests, err := manifest.Build(mwInput)
	if err != nil {
		return reconcile.Result{}, r.preparationFailure(ctx, &cluster, err)
	}

	if err := r.observeBackend(ctx, &cluster, spanner, placement.ManagementClusterName, groupKey, safeName); err != nil {
		return reconcile.Result{}, err
	}

	mwStatus, err := r.applyResources(ctx, &cluster, placement.ManagementClusterName, groupKey, manifests)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("%s: apply resources: %w", adapterName, err)
	}

	// If status is stale, skip condition updates and requeue quickly.
	if mwStatus != nil && mwStatus.Stale {
		log.Infof(ctx, "hc-controller: cluster %s status is stale, requeueing after %s", req.Name, requeuePending)
		return reconcile.Result{RequeueAfter: requeuePending}, nil
	}

	// The live HostedCluster has the final say on its etcd type.
	corrected, err := r.correctEtcdBackend(ctx, &cluster, placement.ManagementClusterName, groupKey, mwStatus, log)
	if err != nil {
		return reconcile.Result{}, err
	}
	if corrected {
		return reconcile.Result{RequeueAfter: requeueCorrected}, nil
	}

	// Write status conditions — only update if something changed.
	statusChanged, err := r.applyStatusConditions(&cluster, mwStatus, enableIngress, includeIngressCertificate, includeDefaultIngressCert)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("%s: apply status feedback: %w", adapterName, err)
	}
	if statusChanged {
		if err := r.client.Status().Update(ctx, &cluster); err != nil {
			if apierrors.IsConflict(err) {
				return reconcile.Result{}, nil
			}
			return reconcile.Result{}, fmt.Errorf("%s: update cluster status: %w", adapterName, err)
		}
	}

	if !meta.IsStatusConditionTrue(cluster.Status.Conditions, "ResourcesApplied") {
		log.Infof(ctx, "hc-controller: cluster %s resources not yet applied, requeueing after %s", req.Name, requeuePending)
		return reconcile.Result{RequeueAfter: requeuePending}, nil
	}

	if !meta.IsStatusConditionTrue(cluster.Status.Conditions, "HostedClusterAvailable") {
		log.Infof(ctx, "hc-controller: cluster %s not yet available, requeueing after %s", req.Name, requeuePending)
		return reconcile.Result{RequeueAfter: requeuePending}, nil
	}

	log.Infof(ctx, "hc-controller: cluster %s reconciled, requeueing after %s", req.Name, requeueStable)
	return reconcile.Result{RequeueAfter: requeueStable}, nil
}

// prerequisitesReady records why a Cluster cannot yet be applied to its HostedCluster.
func (r *Reconciler) prerequisitesReady(ctx context.Context, cluster *privatev1.Cluster, log logger.Logger) (bool, error) {
	if cluster.Status.PlacementResult == nil || cluster.Status.PlacementResult.ManagementClusterName == "" {
		if r.setWaitingConditions(cluster, "PlacementNotReady", "Waiting for placement to select a management cluster") {
			if err := r.client.Status().Update(ctx, cluster); err != nil && !apierrors.IsConflict(err) {
				return false, fmt.Errorf("%s: update cluster status: %w", adapterName, err)
			}
		}
		log.Infof(ctx, "placement not ready, requeueing after %s", requeuePending)
		return false, nil
	}

	if cluster.Status.VersionResolution == nil {
		if r.setWaitingConditions(cluster, "VersionResolutionNotReady", "Waiting for version resolution") {
			if err := r.client.Status().Update(ctx, cluster); err != nil && !apierrors.IsConflict(err) {
				return false, fmt.Errorf("%s: update cluster status: %w", adapterName, err)
			}
		}
		log.Infof(ctx, "version resolution not ready, requeueing after %s", requeuePending)
		return false, nil
	}

	if cluster.Status.VersionResolution.ReleaseVersion != privatev1.ClusterInstallationVersion(cluster) {
		msg := fmt.Sprintf("VR version %q does not match spec version %q",
			cluster.Status.VersionResolution.ReleaseVersion, privatev1.ClusterInstallationVersion(cluster))
		if r.setWaitingConditions(cluster, "VersionMismatch", msg) {
			if err := r.client.Status().Update(ctx, cluster); err != nil && !apierrors.IsConflict(err) {
				return false, fmt.Errorf("%s: update cluster status: %w", adapterName, err)
			}
		}
		log.Infof(ctx, "vr version %q does not match spec version %q, requeueing after %s",
			cluster.Status.VersionResolution.ReleaseVersion, privatev1.ClusterInstallationVersion(cluster), requeuePending)
		return false, nil
	}

	return true, nil
}

// handleDeletion cleans up management-cluster resources and removes the finalizer.
// Deletion flow:
// 1. Call transport.Delete to enqueue DeleteDesires (async)
// 2. Requeue, wait for kube-applier-gcp to process (poll GetDeleteStatus)
// 3. Once all DeleteDesires report Successful=True, cleanup DeleteDesires
// 4. Remove finalizer
func (r *Reconciler) handleDeletion(ctx context.Context, cluster *privatev1.Cluster, log logger.Logger) (reconcile.Result, error) {
	if !controllerutil.ContainsFinalizer(cluster, constants.FinalizerCluster) {
		return reconcile.Result{}, nil
	}

	var nodePools privatev1.NodePoolList
	if err := r.client.List(ctx, &nodePools,
		client.InNamespace(cluster.Namespace),
		client.MatchingFields{NodePoolClusterIDField: cluster.Name},
	); err != nil {
		return reconcile.Result{}, fmt.Errorf("%s: list nodepools: %w", adapterName, err)
	}

	// Start child deletion before cleaning up Cluster resources. Both cleanup paths
	// may progress together, but the Cluster finalizer waits for every NodePool.
	nodePoolsRemain := false
	var deleteErrors []error
	for i := range nodePools.Items {
		nodePool := &nodePools.Items[i]
		nodePoolsRemain = true
		if !nodePool.DeletionTimestamp.IsZero() {
			continue
		}
		if err := r.client.Delete(ctx, nodePool); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			deleteErrors = append(deleteErrors, fmt.Errorf("%s: delete nodepool %s: %w", adapterName, nodePool.Name, err))
			continue
		}
		log.Infof(ctx, "%s: deleting nodepool %s for cluster %s", adapterName, nodePool.Name, cluster.Name)
	}
	if err := errors.Join(deleteErrors...); err != nil {
		return reconcile.Result{}, err
	}

	// nodePoolWait appends the child nodepool wait to a deletion message.
	nodePoolWait := func(msg string) string {
		if nodePoolsRemain {
			msg += fmt.Sprintf("; waiting for %d nodepools to be deleted", len(nodePools.Items))
		}
		return msg
	}

	// Only call transport.Delete if resources were applied to an MC.
	if meta.FindStatusCondition(cluster.Status.Conditions, "ResourcesApplied") != nil &&
		cluster.Status.PlacementResult != nil && cluster.Status.PlacementResult.ManagementClusterName != "" {
		mcName := cluster.Status.PlacementResult.ManagementClusterName

		groupKey, err := transport.ClusterGroupKey(cluster.Namespace, cluster.Name)
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("%s: build group key: %w", adapterName, err)
		}

		// Check if deletion already in progress by querying delete status first.
		deleteStatus, err := r.transport.GetDeleteStatus(ctx, mcName, groupKey)
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("%s: get delete status: %w", adapterName, err)
		}

		if deleteStatus.TotalCount == 0 {
			// No DeleteDesires exist.
			if deleteStatus.ApplyDesiresCount > 0 {
				// ApplyDesires still present → deletion never started, call Delete().
				log.Infof(ctx, "%s: deleting resources for cluster %s from %s", adapterName, cluster.Name, mcName)
				if err := r.transport.Delete(ctx, mcName, groupKey); err != nil {
					return reconcile.Result{}, fmt.Errorf("%s: delete resources: %w", adapterName, err)
				}
				if err := r.setDeletionProgressing(ctx, cluster, "DeletingResources",
					nodePoolWait(transport.DeleteInitiatedMessage(deleteStatus.ApplyDesiresCount))); err != nil {
					return reconcile.Result{}, err
				}
				log.Infof(ctx, "%s: delete initiated for cluster %s, requeueing to poll status", adapterName, cluster.Name)
				return reconcile.Result{RequeueAfter: requeuePending}, nil
			}
			// TotalCount=0 and ApplyDesiresCount=0 → deletion already complete (no-op), proceed to finalizer.
		}

		if !deleteStatus.AllSuccessful {
			// Deletion in progress — wait for completion.
			reason := "DeletionInProgress"
			if deleteStatus.FailedCount > 0 {
				reason = "DeletionBlocked"
			}
			if err := r.setDeletionProgressing(ctx, cluster, reason,
				nodePoolWait(transport.DeletePendingMessage(deleteStatus))); err != nil {
				return reconcile.Result{}, err
			}
			log.Infof(ctx, "%s: deletion in progress for cluster %s (%d/%d pending), requeueing",
				adapterName, cluster.Name, deleteStatus.PendingCount, deleteStatus.TotalCount)
			return reconcile.Result{RequeueAfter: requeuePending}, nil
		}

		// All DeleteDesires successful — cleanup before removing finalizer.
		log.Infof(ctx, "%s: deletion complete for cluster %s, cleaning up %d DeleteDesires",
			adapterName, cluster.Name, deleteStatus.TotalCount)
		if err := r.transport.CleanupDeleteDesires(ctx, mcName, groupKey); err != nil {
			return reconcile.Result{}, fmt.Errorf("%s: cleanup delete desires: %w", adapterName, err)
		}
	}

	if nodePoolsRemain {
		if err := r.setDeletionProgressing(ctx, cluster, "DeletionInProgress",
			fmt.Sprintf("Waiting for %d nodepools to be deleted", len(nodePools.Items))); err != nil {
			return reconcile.Result{}, err
		}
		log.Infof(ctx, "%s: waiting for nodepools to be deleted for cluster %s, requeueing", adapterName, cluster.Name)
		return reconcile.Result{RequeueAfter: requeuePending}, nil
	}

	// Observed read targets are registered before anything is applied, so they can
	// exist without the ResourcesApplied condition that gates the Delete above.
	// Release them here, and keep the finalizer if that fails, so they cannot
	// outlive the cluster and be picked up by a new cluster with the same name.
	if placement := cluster.Status.PlacementResult; placement != nil && placement.ManagementClusterName != "" {
		groupKey, err := transport.ClusterGroupKey(cluster.Namespace, cluster.Name)
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("%s: build group key: %w", adapterName, err)
		}
		if err := r.transport.ReleaseObserved(ctx, placement.ManagementClusterName, groupKey); err != nil {
			return reconcile.Result{}, fmt.Errorf("%s: release observed resources: %w", adapterName, err)
		}
	}

	controllerutil.RemoveFinalizer(cluster, constants.FinalizerCluster)
	if err := r.client.Update(ctx, cluster); err != nil {
		return reconcile.Result{}, fmt.Errorf("%s: remove finalizer: %w", adapterName, err)
	}

	log.Infof(ctx, "%s: finalizer removed for cluster %s", adapterName, cluster.Name)
	return reconcile.Result{}, nil
}

// setDeletionProgressing records the DeletionProgressing condition on a
// cluster being deleted. The condition status is always True while deletion
// is underway; the reason encodes the phase (DeletingResources,
// DeletionInProgress, DeletionBlocked) and the message summarizes what is
// holding the deletion up. Writes are change-gated and conflicts are
// tolerated, mirroring the status-update pattern in Reconcile.
func (r *Reconciler) setDeletionProgressing(ctx context.Context, cluster *privatev1.Cluster, reason, message string) error {
	if !meta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
		Type:               "DeletionProgressing",
		Status:             metav1.ConditionTrue,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: cluster.Generation,
	}) {
		return nil
	}
	if err := r.client.Status().Update(ctx, cluster); err != nil && !apierrors.IsConflict(err) {
		return fmt.Errorf("%s: record deletion progress: %w", adapterName, err)
	}
	return nil
}

// setWaitingConditions records preparation readiness without overwriting the
// availability last observed from a running HostedCluster.
func (r *Reconciler) setWaitingConditions(cluster *privatev1.Cluster, reason, message string) bool {
	gen := cluster.Generation
	a := meta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
		Type:               "ResourcesApplied",
		Status:             metav1.ConditionUnknown,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: gen,
	})
	if hc := cluster.Status.HostedClusterResult; hc != nil && hc.Version != "" {
		return a
	}
	b := meta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
		Type:               "HostedClusterAvailable",
		Status:             metav1.ConditionUnknown,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: gen,
	})
	return a || b
}

// applyStatusConditions derives conditions from the resource status and writes them to the cluster.
// Returns true if any condition changed.
func (r *Reconciler) applyStatusConditions(cluster *privatev1.Cluster, mwStatus *transport.Status, enableIngress, ingressCertRequested, ingressCertApplied bool) (bool, error) {
	gen := cluster.Generation

	if mwStatus == nil {
		a := meta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
			Type:               "ResourcesApplied",
			Status:             metav1.ConditionFalse,
			Reason:             "ResourcesNotFound",
			Message:            "Resources have not been applied yet",
			ObservedGeneration: gen,
		})
		b := meta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
			Type:               "HostedClusterAvailable",
			Status:             metav1.ConditionFalse,
			Reason:             "ResourcesNotFound",
			Message:            "Resources have not been applied yet",
			ObservedGeneration: gen,
		})
		c := meta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
			Type:               "ApiCertificateReady",
			Status:             metav1.ConditionFalse,
			Reason:             "ResourcesNotFound",
			Message:            "Resources have not been applied yet",
			ObservedGeneration: gen,
		})
		e, f := false, false
		if enableIngress {
			e = meta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
				Type:               "IngressCredentialsReady",
				Status:             metav1.ConditionFalse,
				Reason:             "ResourcesNotFound",
				Message:            "Resources have not been applied yet",
				ObservedGeneration: gen,
			})
		}
		if ingressCertRequested {
			f = meta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
				Type:               "IngressCertificateReady",
				Status:             metav1.ConditionFalse,
				Reason:             "ResourcesNotFound",
				Message:            "Resources have not been applied yet",
				ObservedGeneration: gen,
			})
		}
		g := r.applyBackendCondition(cluster, backendNotFoundCondition(cluster, gen))
		return a || b || c || e || f || g, nil
	}

	// Derive ResourcesApplied from top-level conditions.
	appliedStatus, appliedReason, appliedMessage := firstCondition(mwStatus.Conditions, "Applied")

	// Derive HostedClusterAvailable and HostedClusterResult fields from HC resource status.
	clusterNS := fmt.Sprintf("clusters-%s", cluster.UID)
	safeName := cluster.Spec.SafeName
	if safeName == "" {
		safeName = privatev1.DefaultSafeName(cluster.Name, cluster.UID)
	}
	hcKey := transport.ResourceKey(constants.HyperShiftGroup, constants.HyperShiftVersion, "hostedclusters",
		clusterNS, safeName)
	hcFeedback := mwStatus.ResourceStatuses[hcKey]
	// The map key can be present but empty: extractResourceStatuses inserts an empty
	// map when the HostedCluster's live content has not synced yet (KubeContent nil),
	// which is a different condition than the "read status document never arrived"
	// case mwStatus.Stale already covers. Treat an empty map the same as no feedback —
	// key presence alone is not proof this is fresh data. extractHCFields always sets
	// at least availableVersions and versionConditions when it actually runs, so a
	// non-empty map reliably means live content was read.
	hasHCFeedback := len(hcFeedback) > 0
	availableStatus := string(metav1.ConditionFalse)
	if v, ok := hcFeedback["availableCondition"]; ok {
		availableStatus = v
	}

	// HostedClusterResult (including the observed HyperShift conditions) is only
	// touched once something has been observed at least once. Before that, there
	// is nothing meaningful to report and the field stays nil.
	resultChanged := false
	if hasHCFeedback || cluster.Status.HostedClusterResult != nil {
		var err error
		resultChanged, err = r.applyHostedClusterResult(cluster, hasHCFeedback, hcFeedback, gen)
		if err != nil {
			return false, fmt.Errorf("%s: apply hosted cluster result: %w", adapterName, err)
		}
	}

	// Derive ApiCertificateReady from Certificate resource status.
	certStatus, certReason, certMessage := certificateReadyCondition(mwStatus.ResourceStatuses,
		transport.ResourceKey("cert-manager.io", "v1", "certificates", clusterNS, "external-api-cert"))

	// Ingress readiness is only reported for clusters that enable Ingress.
	var ingressCredentials, ingressDefaultCert *metav1.Condition
	if enableIngress && cluster.Status.HostedClusterResult != nil {
		ingressCredentials = meta.FindStatusCondition(cluster.Status.HostedClusterResult.ObservedConditions, "IngressCredentialsFound")
		ingressDefaultCert = meta.FindStatusCondition(cluster.Status.HostedClusterResult.ObservedConditions, "IngressDefaultCertificateSynced")
	}

	backend := observedBackendCondition(cluster, mwStatus, clusterNS, safeName, gen)

	// Gate availability on backend and ingress readiness. HyperShift treats a
	// missing ingress certificate as non-fatal, but Gecko does not report such a
	// cluster as available. An explicitly reported backend failure also keeps the
	// cluster from being reported as usable. Unknown, including a backend that has
	// not reported yet, does not: HostedCluster availability already reflects an
	// etcd that cannot serve.
	availableReason := "HostedClusterNotAvailable"
	if availableStatus == "True" {
		availableReason = "HostedClusterAvailable"
	}
	availableMessage := ""
	if availableStatus == "True" {
		switch {
		case backendBlocksAvailability(backend):
			availableStatus = string(metav1.ConditionFalse)
			availableReason = backend.Reason
			availableMessage = backend.Message
		case ingressCredentials != nil && ingressCredentials.Status == metav1.ConditionFalse:
			availableStatus = string(metav1.ConditionFalse)
			availableReason = ingressCredentials.Reason
			availableMessage = ingressCredentials.Message
		case ingressDefaultCert != nil && ingressDefaultCert.Status == metav1.ConditionFalse:
			availableStatus = string(metav1.ConditionFalse)
			availableReason = ingressDefaultCert.Reason
			availableMessage = ingressDefaultCert.Message
		}
	}

	a := meta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
		Type:               "ResourcesApplied",
		Status:             metav1.ConditionStatus(appliedStatus),
		Reason:             appliedReason,
		Message:            appliedMessage,
		ObservedGeneration: gen,
	})
	b := meta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
		Type:               "HostedClusterAvailable",
		Status:             metav1.ConditionStatus(availableStatus),
		Reason:             availableReason,
		Message:            availableMessage,
		ObservedGeneration: gen,
	})
	d := meta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
		Type:               "ApiCertificateReady",
		Status:             metav1.ConditionStatus(certStatus),
		Reason:             certReason,
		Message:            certMessage,
		ObservedGeneration: gen,
	})

	changed := a || b || d || resultChanged
	changed = r.applyBackendCondition(cluster, backend) || changed
	changed = applyIngressConditions(cluster, mwStatus, ingressCredentials, ingressDefaultCert,
		ingressCertRequested, ingressCertApplied, hasHCFeedback, clusterNS, gen) || changed

	return changed, nil
}

const (
	// conditionEtcdBackendReady reports whether the socketcd Spanner backend of
	// an opted-in cluster is ready to serve etcd.
	conditionEtcdBackendReady = "EtcdBackendReady"

	etcdBackendNotReported = "BackendNotReported"
)

// observeBackend registers the SpannerEtcd of a cluster that uses the socketcd
// backend as a read-only target. Other clusters have nothing to observe.
func (r *Reconciler) observeBackend(ctx context.Context, cluster *privatev1.Cluster, spanner bool, managementCluster, groupKey, safeName string) error {
	if !spanner {
		return nil
	}
	ref := spannerEtcdRef(cluster, safeName)
	if err := r.transport.Observe(ctx, managementCluster, groupKey, []transport.ResourceRef{ref}); err != nil {
		return fmt.Errorf("%s: observe spanner etcd: %w", adapterName, err)
	}
	return nil
}

// spannerEtcdRef returns the SpannerEtcd that socketcd creates next to a
// HostedCluster, with the same namespace and name.
func spannerEtcdRef(cluster *privatev1.Cluster, safeName string) transport.ResourceRef {
	return transport.ResourceRef{
		Group:     constants.SocketCDGroup,
		Version:   constants.SocketCDVersion,
		Resource:  constants.SocketCDResource,
		Namespace: fmt.Sprintf("clusters-%s", cluster.UID),
		Name:      safeName,
	}
}

// ensureEtcdBackend returns whether the Cluster uses socketcd's Spanner-backed
// etcd, selecting and recording the backend the first time the Cluster is seen.
//
// The selection is a one-time decision, written before anything is applied so a
// later change of the flag, a retry, or a crash cannot select a different type
// for the HostedCluster. A Cluster whose HostedCluster already exists predates
// the setting and necessarily has Managed etcd, which is recognized from its
// status and, failing that, from the resources already dispatched for it.
func (r *Reconciler) ensureEtcdBackend(ctx context.Context, cluster *privatev1.Cluster) (bool, error) {
	switch backend := cluster.Annotations[constants.AnnotationEtcdBackend]; backend {
	case constants.EtcdBackendSpanner:
		return true, nil
	case constants.EtcdBackendManaged:
		return false, nil
	case "":
	default:
		return false, fmt.Errorf("%s: unknown etcd backend %q in annotation %s", adapterName, backend, constants.AnnotationEtcdBackend)
	}

	spanner := false
	if r.spannerEtcd && !hostedClusterMayExist(cluster) {
		dispatched, err := r.resourcesDispatched(ctx, cluster)
		if err != nil {
			return false, err
		}
		spanner = !dispatched
	}
	backend := constants.EtcdBackendManaged
	if spanner {
		backend = constants.EtcdBackendSpanner
	}
	if cluster.Annotations == nil {
		cluster.Annotations = map[string]string{}
	}
	cluster.Annotations[constants.AnnotationEtcdBackend] = backend
	if err := r.client.Update(ctx, cluster); err != nil {
		return false, fmt.Errorf("%s: record etcd backend: %w", adapterName, err)
	}
	return spanner, nil
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
// Gecko only creates Unmanaged etcd for the socketcd backend, so Unmanaged is
// read as Spanner. Returns true if the record was changed.
func (r *Reconciler) correctEtcdBackend(ctx context.Context, cluster *privatev1.Cluster, managementCluster, groupKey string, mwStatus *transport.Status, log logger.Logger) (bool, error) {
	if mwStatus == nil {
		return false, nil
	}
	safeName := cluster.Spec.SafeName
	if safeName == "" {
		safeName = privatev1.DefaultSafeName(cluster.Name, cluster.UID)
	}
	hcKey := transport.ResourceKey(constants.HyperShiftGroup, constants.HyperShiftVersion, "hostedclusters",
		fmt.Sprintf("clusters-%s", cluster.UID), safeName)

	var want string
	switch live := mwStatus.ResourceStatuses[hcKey]["etcdManagementType"]; live {
	case "Managed":
		want = constants.EtcdBackendManaged
	case "Unmanaged":
		want = constants.EtcdBackendSpanner
	default:
		return false, nil
	}
	recorded := cluster.Annotations[constants.AnnotationEtcdBackend]
	if recorded == want {
		return false, nil
	}

	log.Warnf(ctx, "%s: cluster %s is recorded with etcd backend %q but its HostedCluster uses %q etcd; correcting the record",
		adapterName, cluster.Name, recorded, mwStatus.ResourceStatuses[hcKey]["etcdManagementType"])
	// The SpannerEtcd target registered for a mistaken Spanner record is released
	// first, so a failure here is retried instead of being forgotten.
	if want == constants.EtcdBackendManaged {
		if err := r.transport.ReleaseObserved(ctx, managementCluster, groupKey); err != nil {
			return false, fmt.Errorf("%s: release observed resources: %w", adapterName, err)
		}
	}
	cluster.Annotations[constants.AnnotationEtcdBackend] = want
	if err := r.client.Update(ctx, cluster); err != nil {
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
func (r *Reconciler) applyBackendCondition(cluster *privatev1.Cluster, backend *metav1.Condition) bool {
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
			Reason:             etcdBackendNotReported,
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

// applyIngressConditions writes the Gecko-owned ingress conditions: the
// HyperShift-observed credentials and default-certificate sync, the
// cert-manager certificate readiness, and the locally-owned reference marker.
func applyIngressConditions(cluster *privatev1.Cluster, mwStatus *transport.Status, ingressCredentials, ingressDefaultCert *metav1.Condition,
	ingressCertRequested, ingressCertApplied, hasHCFeedback bool, clusterNS string, gen int64) bool {
	changed := false
	if ingressCredentials != nil {
		changed = meta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
			Type:               "IngressCredentialsReady",
			Status:             ingressCredentials.Status,
			Reason:             ingressCredentials.Reason,
			Message:            ingressCredentials.Message,
			ObservedGeneration: gen,
		}) || changed
	}
	if ingressDefaultCert != nil {
		changed = meta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
			Type:               "IngressDefaultCertificateReady",
			Status:             ingressDefaultCert.Status,
			Reason:             ingressDefaultCert.Reason,
			Message:            ingressDefaultCert.Message,
			ObservedGeneration: gen,
		}) || changed
	}
	if ingressCertRequested {
		ingressCertStatus, ingressCertReason, ingressCertMessage := certificateReadyCondition(mwStatus.ResourceStatuses,
			transport.ResourceKey("cert-manager.io", "v1", "certificates", clusterNS, "ingress-default-cert"))
		changed = meta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
			Type:               "IngressCertificateReady",
			Status:             metav1.ConditionStatus(ingressCertStatus),
			Reason:             ingressCertReason,
			Message:            ingressCertMessage,
			ObservedGeneration: gen,
		}) || changed
	}
	// The reference marker is owned by Gecko, not derived from HyperShift
	// feedback: HyperShift can only report IngressDefaultCertificateSynced
	// after the reference has been applied, so a feedback-derived marker could
	// not cover the window between the first apply and the first report.
	if ingressCertApplied {
		changed = meta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
			Type:               "IngressDefaultCertificateApplied",
			Status:             metav1.ConditionTrue,
			Reason:             "ReferenceApplied",
			Message:            "HostedCluster references the ingress default certificate",
			ObservedGeneration: gen,
		}) || changed
	}
	// When fresh HostedCluster feedback omits an ingress observation that was
	// previously reported, mark the derived condition Unknown rather than
	// leaving the last value in place: conditions are consumed by automations
	// and must not go stale. A missing feedback document (empty read) is not
	// evidence the observation disappeared, so the reset only applies to fresh
	// feedback.
	if hasHCFeedback {
		if ingressCredentials == nil {
			changed = resetConditionToUnknown(cluster, "IngressCredentialsReady", "IngressCredentialsFound", gen) || changed
		}
		if ingressDefaultCert == nil {
			changed = resetConditionToUnknown(cluster, "IngressDefaultCertificateReady", "IngressDefaultCertificateSynced", gen) || changed
		}
	}
	return changed
}

// resetConditionToUnknown marks an existing condition Unknown, reporting which
// HyperShift observation is no longer provided. Conditions that do not exist
// yet are left absent.
func resetConditionToUnknown(cluster *privatev1.Cluster, conditionType, observation string, gen int64) bool {
	if meta.FindStatusCondition(cluster.Status.Conditions, conditionType) == nil {
		return false
	}
	return meta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
		Type:               conditionType,
		Status:             metav1.ConditionUnknown,
		Reason:             "ObservationNotReported",
		Message:            "HyperShift is not reporting " + observation,
		ObservedGeneration: gen,
	})
}

// certificateReadyCondition derives a Ready-style condition from a cert-manager
// Certificate resource status.
func certificateReadyCondition(statuses map[string]map[string]string, key string) (status, reason, message string) {
	status, reason, message = string(metav1.ConditionFalse), "CertificateNotReady", ""
	if feedback, ok := statuses[key]; ok {
		if v, ok := feedback["readyCondition"]; ok {
			if v == string(metav1.ConditionTrue) || v == string(metav1.ConditionFalse) || v == string(metav1.ConditionUnknown) {
				status = v
				if v == string(metav1.ConditionTrue) {
					reason = "CertificateReady"
				}
			}
		}
	}
	return status, reason, message
}

// observedHCCondition returns a condition from the previous reconciliation's
// HostedCluster feedback.
func observedHCCondition(cluster *privatev1.Cluster, conditionType string) *metav1.Condition {
	if cluster.Status.HostedClusterResult == nil {
		return nil
	}
	return meta.FindStatusCondition(cluster.Status.HostedClusterResult.ObservedConditions, conditionType)
}

// ingressCertificateRequested reports whether the ingress Certificate has been
// requested. HyperShift reports GCPACMECNAMEAvailable=True once the ACME
// delegation CNAME exists. Once the Certificate has reported status, keep
// managing it even if feedback is momentarily absent.
func ingressCertificateRequested(cluster *privatev1.Cluster) bool {
	if c := observedHCCondition(cluster, "GCPACMECNAMEAvailable"); c != nil && c.Status == metav1.ConditionTrue {
		return true
	}
	return meta.FindStatusCondition(cluster.Status.Conditions, "IngressCertificateReady") != nil
}

// ingressCertificateReady reports whether cert-manager produced the ingress
// serving secret.
func ingressCertificateReady(cluster *privatev1.Cluster) bool {
	c := meta.FindStatusCondition(cluster.Status.Conditions, "IngressCertificateReady")
	return c != nil && c.Status == metav1.ConditionTrue
}

// ingressDefaultCertificateApplied reports whether the HostedCluster already
// references the ingress Certificate. Once referenced, keep the reference so a
// transient feedback gap cannot remove it. The marker is set by Gecko when the
// reference is rendered, not derived from HyperShift feedback, so it also
// covers the window before HyperShift first reports the reference.
func ingressDefaultCertificateApplied(cluster *privatev1.Cluster) bool {
	c := meta.FindStatusCondition(cluster.Status.Conditions, "IngressDefaultCertificateApplied")
	return c != nil && c.Status == metav1.ConditionTrue
}

// applyHostedClusterResult writes HC-owned HostedCluster feedback to
// cluster.Status.HostedClusterResult. It owns parsing hcFeedback end to end,
// including the fresh-vs-sticky decision for each field:
//
// APIEndpoint, Version, DesiredVersion, and AvailableUpdates are sticky: when
// hasHCFeedback is false they retain their previous value instead of resetting,
// because currentVersion/targetVersion must not change while feedback is
// momentarily unavailable (e.g. a transient transport read).
//
// ObservedConditions is different: it is a verbatim mirror of whatever
// HostedCluster feedback reported this reconcile (only "Degraded" is renamed to
// "HostedClusterDegraded" to avoid colliding with a future Gecko-native condition
// of the same short name). It does not carry forward previous values and does not
// synthesize placeholder entries for types HostedCluster did not report — the
// hc-controller does not hardcode which condition types matter, that is decided by
// whichever controller reads ObservedConditions. A consumer that needs a specific
// type and finds it absent here must treat that as Unknown itself, exactly as it
// would if the type were present with Status=Unknown; either way, health/readiness
// signals must never be trusted once feedback stops arriving, even though the
// version fields above stay sticky.
//
// ObservedConditions is private HC feedback and is intentionally not exposed on the
// public API; it is the only place the hc-controller writes raw HyperShift/CVO
// conditions, including their original LastTransitionTime, which callers may use to
// tell a fresh signal apart from one that has persisted for a while. See
// ControlPlaneUpgradeResult for the customer-facing upgrade conditions the
// control-plane-upgrade controller derives from this data.
//
// Returns true if HostedClusterResult changed.
func (r *Reconciler) applyHostedClusterResult(
	cluster *privatev1.Cluster,
	hasHCFeedback bool,
	hcFeedback map[string]string,
	gen int64,
) (bool, error) {
	previous := cluster.Status.HostedClusterResult

	var apiEndpoint, version, desiredVersion string
	var availableUpdates []string
	var observed []metav1.Condition

	switch {
	case hasHCFeedback:
		apiEndpoint = hcFeedback["controlPlaneEndpoint"]
		version = hcFeedback["version"]
		desiredVersion = hcFeedback["desiredVersion"]
		if v, ok := hcFeedback["availableVersions"]; ok {
			if err := json.Unmarshal([]byte(v), &availableUpdates); err != nil {
				return false, fmt.Errorf("decode available updates: %w", err)
			}
		}
		var versionConditions []metav1.Condition
		if v, ok := hcFeedback["versionConditions"]; ok {
			if err := json.Unmarshal([]byte(v), &versionConditions); err != nil {
				return false, fmt.Errorf("decode version conditions: %w", err)
			}
		}
		observed = make([]metav1.Condition, 0, len(versionConditions))
		for _, condition := range versionConditions {
			if condition.Type == "Degraded" {
				condition.Type = "HostedClusterDegraded"
			}
			condition.ObservedGeneration = gen
			observed = append(observed, condition)
		}
	case previous != nil:
		// No fresh feedback this reconcile — identity fields stay sticky, but
		// ObservedConditions is deliberately left empty rather than carried
		// forward: we cannot currently confirm any of it.
		apiEndpoint = previous.APIEndpoint
		version = previous.Version
		desiredVersion = previous.DesiredVersion
		availableUpdates = previous.AvailableUpdates
	}
	// Empty slices are omitted from the stored status and read back as nil. Keep
	// the desired value canonical so identical empty feedback does not trigger a
	// status write on every reconciliation.
	if len(availableUpdates) == 0 {
		availableUpdates = nil
	}
	if len(observed) == 0 {
		observed = nil
	}

	desired := &privatev1.HostedClusterResult{
		APIEndpoint:        apiEndpoint,
		Version:            version,
		DesiredVersion:     desiredVersion,
		AvailableUpdates:   availableUpdates,
		ObservedConditions: observed,
	}
	if equality.Semantic.DeepEqual(previous, desired) {
		return false, nil
	}
	cluster.Status.HostedClusterResult = desired
	return true, nil
}

// firstCondition returns the status, reason, and message of the first condition matching condType.
// Defaults: status="False", reason="Unknown", message="".
func firstCondition(conds []metav1.Condition, condType string) (status, reason, message string) {
	for _, c := range conds {
		if c.Type == condType {
			return string(c.Status), c.Reason, c.Message
		}
	}
	return "False", "Unknown", ""
}
