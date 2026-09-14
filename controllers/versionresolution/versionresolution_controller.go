package versionresolution

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	privatev1 "github.com/openshift-online/gecko/platform-api/api/private/v1"

	"github.com/openshift-online/gecko/controllers/util/logger"
)

const (
	adapterName         = "version-resolution-controller"
	defaultChannelGroup = "candidate"
	requeueStable       = 5 * time.Minute
)

// Reconciler resolves the OCP release image for a cluster via Cincinnati.
type Reconciler struct {
	service        *VersionService
	defaultVersion string
	log            logger.Logger
	client         client.Client
}

// NewReconciler creates a new version-resolution Reconciler.
func NewReconciler(service *VersionService, defaultVersion string, log logger.Logger, c client.Client) *Reconciler {
	return &Reconciler{
		service:        service,
		defaultVersion: defaultVersion,
		log:            log,
		client:         c,
	}
}

// Reconcile runs the version-resolution loop for one cluster event.
func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	clusterID := req.Name

	var cluster privatev1.Cluster
	if err := r.client.Get(ctx, req.NamespacedName, &cluster); err != nil {
		if apierrors.IsNotFound(err) {
			r.log.Infof(ctx, "vr: cluster %s not found, skipping", clusterID)
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, fmt.Errorf("vr: get cluster %s: %w", clusterID, err)
	}

	if cluster.Spec.Release.Version == "" {
		r.log.Warnf(ctx, "vr: cluster %s: release version is required", clusterID)
		if err := r.setVersionCondition(ctx, &cluster, metav1.ConditionFalse, "ReleaseVersionNotSet", "Release version is required"); err != nil {
			return reconcile.Result{}, err
		}
		return reconcile.Result{}, nil
	}
	version := cluster.Spec.Release.Version

	channelGroup := defaultChannelGroup
	if cluster.Spec.Release.ChannelGroup != "" {
		channelGroup = cluster.Spec.Release.ChannelGroup
	}
	r.log.Infof(ctx, "vr: cluster %s: resolving version %s via channel group %s", clusterID, version, channelGroup)

	resolution, err := r.service.Resolve(ctx, version, channelGroup)
	if err != nil {
		status := metav1.ConditionFalse
		reason := "VersionNotFoundInCincinnati"
		message := err.Error()
		switch {
		case errors.Is(err, ErrInvalidVersion):
			reason = "InvalidVersion"
		case errors.Is(err, ErrUnsupportedVersion):
			reason = "UnsupportedVersion"
		case errors.Is(err, ErrVersionNotFound):
			reason = "VersionNotFoundInCincinnati"
		case errors.Is(err, ErrNoValidReleases):
			status = metav1.ConditionUnknown
			reason = "CincinnatiDataInvalid"
			message = "Cincinnati returned no valid releases"
		default:
			status = metav1.ConditionUnknown
			reason = "CincinnatiUnavailable"
			message = "Unable to retrieve valid release data from Cincinnati"
		}
		r.log.Warnf(ctx, "vr: cluster %s: %v", clusterID, err)
		if updateErr := r.setVersionCondition(ctx, &cluster, status, reason, message); updateErr != nil {
			return reconcile.Result{}, updateErr
		}
		if status == metav1.ConditionUnknown {
			return reconcile.Result{}, fmt.Errorf("vr: resolve version for cluster %s: %w", clusterID, err)
		}
		return reconcile.Result{}, nil
	}

	desired := &privatev1.VersionResolutionResult{
		ReleaseImage:      resolution.ReleaseImage,
		ReleaseVersion:    resolution.Version,
		DefaultVersion:    r.defaultVersion,
		LatestVersion:     resolution.LatestVersion,
		CincinnatiChannel: resolution.Channel,
		ChannelGroup:      channelGroup,
	}
	reason := "VersionResolved"
	message := fmt.Sprintf("Version %s resolved to image %s", version, resolution.ReleaseImage)
	condition := meta.FindStatusCondition(cluster.Status.Conditions, "VersionResolved")
	if reflect.DeepEqual(cluster.Status.VersionResolution, desired) &&
		condition != nil && condition.Status == metav1.ConditionTrue && condition.Reason == reason &&
		condition.ObservedGeneration == cluster.Generation {
		return reconcile.Result{RequeueAfter: requeueStable}, nil
	}

	cluster.Status.VersionResolution = desired
	meta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
		Type:               "VersionResolved",
		Status:             metav1.ConditionTrue,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: cluster.Generation,
	})
	if err := r.client.Status().Update(ctx, &cluster); err != nil {
		if apierrors.IsConflict(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, fmt.Errorf("vr: update cluster status %s: %w", clusterID, err)
	}

	r.log.Infof(ctx, "vr: cluster %s: resolved version %s", clusterID, version)
	return reconcile.Result{RequeueAfter: requeueStable}, nil
}

func (r *Reconciler) setVersionCondition(ctx context.Context, cluster *privatev1.Cluster, status metav1.ConditionStatus, reason, message string) error {
	resolutionChanged := cluster.Status.VersionResolution != nil
	cluster.Status.VersionResolution = nil
	conditionChanged := meta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
		Type:               "VersionResolved",
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: cluster.Generation,
	})
	if !conditionChanged && !resolutionChanged {
		return nil
	}
	if err := r.client.Status().Update(ctx, cluster); err != nil && !apierrors.IsConflict(err) {
		return fmt.Errorf("vr: update cluster status %s: %w", cluster.Name, err)
	}
	return nil
}
