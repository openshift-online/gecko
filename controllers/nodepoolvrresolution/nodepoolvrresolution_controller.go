package nodepoolvrresolution

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/openshift-online/gecko/controllers/util/logger"
	"github.com/openshift-online/gecko/controllers/versionresolution"
	privatev1 "github.com/openshift-online/gecko/platform-api/api/private/v1"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	adapterName   = "nodepool-vr-controller"
	requeueStable = 5 * time.Minute

	// NodePoolReleaseVersionField indexes NodePools by the release version they
	// request, so a Version event can enqueue the node pools waiting for it.
	NodePoolReleaseVersionField = "spec.release.version"
)

// Reconciler resolves the OCP release image for a node pool from the Version catalog.
type Reconciler struct {
	log    logger.Logger
	client client.Client
}

// NewReconciler creates a new nodepool-vr Reconciler.
func NewReconciler(log logger.Logger, c client.Client) *Reconciler {
	return &Reconciler{
		log:    log,
		client: c,
	}
}

// Reconcile runs the nodepool-vr loop for one nodepool event.
// req.Namespace = project namespace, req.Name = nodepoolID.
func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	nodepoolID := req.Name

	var np privatev1.NodePool
	if err := r.client.Get(ctx, req.NamespacedName, &np); err != nil {
		if apierrors.IsNotFound(err) {
			r.log.Infof(ctx, "nodepool-vr: nodepool %s not found, skipping", nodepoolID)
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, fmt.Errorf("nodepool-vr: get nodepool %s: %w", nodepoolID, err)
	}

	// Verify the parent cluster still exists.
	clusterID := np.Spec.ClusterID
	clusterKey := types.NamespacedName{Namespace: req.Namespace, Name: clusterID}
	var cluster privatev1.Cluster
	if err := r.client.Get(ctx, clusterKey, &cluster); err != nil {
		if apierrors.IsNotFound(err) {
			r.log.Infof(ctx, "nodepool-vr: cluster %s not found for nodepool %s, skipping", clusterID, nodepoolID)
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, fmt.Errorf("nodepool-vr: get cluster %s: %w", clusterID, err)
	}

	if np.Spec.Release.Version == "" {
		r.log.Infof(ctx, "nodepool-vr: nodepool %s: release version not set, waiting for next event", nodepoolID)
		if meta.SetStatusCondition(&np.Status.Conditions, metav1.Condition{
			Type:               "NodePoolVersionResolved",
			Status:             metav1.ConditionUnknown,
			Reason:             "ReleaseVersionNotSet",
			Message:            "Release version not set in spec",
			ObservedGeneration: np.Generation,
		}) {
			if err := r.client.Status().Update(ctx, &np); err != nil && !apierrors.IsConflict(err) {
				return reconcile.Result{}, fmt.Errorf("nodepool-vr: update nodepool status %s: %w", nodepoolID, err)
			}
		}
		return reconcile.Result{}, nil
	}
	version := np.Spec.Release.Version
	channelGroup := np.Spec.Release.ChannelGroup

	channel, err := buildChannel(version, channelGroup)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("nodepool-vr: build channel for nodepool %s: %w", nodepoolID, err)
	}

	// Check already resolved — both version and channel group must match to skip re-resolution.
	// This pins the release image for the lifetime of the request: later catalog
	// changes must not move a running node pool to a different image.
	if vr := np.Status.VersionResolution; vr != nil &&
		vr.ReleaseVersion == version &&
		vr.ChannelGroup == channelGroup {
		r.log.Infof(ctx, "nodepool-vr: nodepool %s: version %s channel group %s already resolved, waiting for next event", nodepoolID, version, channelGroup)
		return reconcile.Result{}, nil
	}
	r.log.Infof(ctx, "nodepool-vr: nodepool %s: resolving version %s in channel group %s", nodepoolID, version, channelGroup)

	releaseImage, err := versionresolution.ResolveReleaseImage(ctx, r.client, version, channelGroup)
	if err != nil {
		var resolveErr *versionresolution.ResolveError
		if !errors.As(err, &resolveErr) {
			return reconcile.Result{}, fmt.Errorf("nodepool-vr: resolve version for nodepool %s: %w", nodepoolID, err)
		}

		r.log.Warnf(ctx, "nodepool-vr: nodepool %s: %s, waiting for next event", nodepoolID, resolveErr.Message)
		if meta.SetStatusCondition(&np.Status.Conditions, metav1.Condition{
			Type:               "NodePoolVersionResolved",
			Status:             metav1.ConditionFalse,
			Reason:             resolveErr.Reason,
			Message:            resolveErr.Message,
			ObservedGeneration: np.Generation,
		}) {
			if err := r.client.Status().Update(ctx, &np); err != nil && !apierrors.IsConflict(err) {
				return reconcile.Result{}, fmt.Errorf("nodepool-vr: update nodepool status %s: %w", nodepoolID, err)
			}
		}
		return reconcile.Result{}, nil
	}

	// Write VR result and NodePoolVersionResolved condition to status.
	np.Status.VersionResolution = &privatev1.VersionResolutionResult{
		ReleaseImage:      releaseImage,
		ReleaseVersion:    version,
		CincinnatiChannel: channel,
		ChannelGroup:      channelGroup,
	}
	meta.SetStatusCondition(&np.Status.Conditions, metav1.Condition{
		Type:               "NodePoolVersionResolved",
		Status:             metav1.ConditionTrue,
		Reason:             "VersionResolved",
		Message:            fmt.Sprintf("Version %s resolved to image %s", version, releaseImage),
		ObservedGeneration: np.Generation,
	})
	if err := r.client.Status().Update(ctx, &np); err != nil {
		if apierrors.IsConflict(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, fmt.Errorf("nodepool-vr: update nodepool status %s: %w", nodepoolID, err)
	}

	r.log.Infof(ctx, "nodepool-vr: nodepool %s: resolved version %s", nodepoolID, version)
	return reconcile.Result{RequeueAfter: requeueStable}, nil
}

// buildChannel constructs the upgrade channel advertised to the hosted cluster
// from a version string and channel group.
// e.g. "4.22.0-rc.5" + "candidate" → "candidate-4.22"
func buildChannel(version, channelGroup string) (string, error) {
	parts := strings.Split(version, ".")
	if len(parts) < 2 {
		return "", fmt.Errorf("invalid version %q: expected at least major.minor", version)
	}
	return fmt.Sprintf("%s-%s.%s", channelGroup, parts[0], parts[1]), nil
}
