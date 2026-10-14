package v1

import (
	"context"
	"errors"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// errQuotaExceeded is a test-only sentinel error returned by quota hook stubs.
var errQuotaExceeded = errors.New("quota exceeded")

// ─── condition helpers (test-only) ───────────────────────────────────────────

func setApproved(s *QuotaRequestStatus, limit int32, auto bool) {
	s.ApprovedLimit = &limit
	s.AutoApproved = &auto
	meta.SetStatusCondition(&s.Conditions, metav1.Condition{
		Type:               QuotaRequestConditionApproved,
		Status:             metav1.ConditionTrue,
		LastTransitionTime: metav1.Now(),
		Reason:             QuotaRequestReasonAutoApproved,
		Message:            "auto-approved",
	})
}

func setDenied(s *QuotaRequestStatus, msg string) {
	meta.SetStatusCondition(&s.Conditions, metav1.Condition{
		Type:               QuotaRequestConditionApproved,
		Status:             metav1.ConditionFalse,
		LastTransitionTime: metav1.Now(),
		Reason:             QuotaRequestReasonDenied,
		Message:            msg,
	})
}

func setSuperseded(s *QuotaRequestStatus, newerName string) {
	meta.SetStatusCondition(&s.Conditions, metav1.Condition{
		Type:               QuotaRequestConditionSuperseded,
		Status:             metav1.ConditionTrue,
		LastTransitionTime: metav1.Now(),
		Reason:             QuotaRequestReasonSuperseded,
		Message:            "superseded by " + newerName,
	})
	meta.SetStatusCondition(&s.Conditions, metav1.Condition{
		Type:               QuotaRequestConditionApproved,
		Status:             metav1.ConditionUnknown,
		LastTransitionTime: metav1.Now(),
		Reason:             QuotaRequestReasonSuperseded,
		Message:            "superseded by " + newerName,
	})
}

// ─── QuotaRequest.ValidateDelete ─────────────────────────────────────────────

func approvedQR() *QuotaRequest {
	qr := &QuotaRequest{}
	qr.Name = "req-1"
	setApproved(&qr.Status, 100, true)
	return qr
}

func TestQuotaRequestValidateDelete_ApprovedIsRejected(t *testing.T) {
	if err := approvedQR().ValidateDelete(context.Background()); err == nil {
		t.Error("expected error deleting Approved QuotaRequest, got nil")
	}
}

func TestQuotaRequestValidateDelete_ApprovedReturnsConflict(t *testing.T) {
	err := approvedQR().ValidateDelete(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !apierrors.IsConflict(err) {
		t.Errorf("expected Conflict StatusError (HTTP 409), got %T: %v", err, err)
	}
}

func TestQuotaRequestValidateDelete_PendingIsAllowed(t *testing.T) {
	// No conditions set → IsPending() == true
	qr := &QuotaRequest{}
	if err := qr.ValidateDelete(context.Background()); err != nil {
		t.Errorf("expected no error deleting Pending QuotaRequest, got: %v", err)
	}
}

func TestQuotaRequestValidateDelete_DeniedIsAllowed(t *testing.T) {
	qr := &QuotaRequest{}
	setDenied(&qr.Status, "not approved")
	if err := qr.ValidateDelete(context.Background()); err != nil {
		t.Errorf("expected no error deleting Denied QuotaRequest, got: %v", err)
	}
}

func TestQuotaRequestValidateDelete_SupersededIsAllowed(t *testing.T) {
	qr := &QuotaRequest{}
	setSuperseded(&qr.Status, "newer-req")
	if err := qr.ValidateDelete(context.Background()); err != nil {
		t.Errorf("expected no error deleting Superseded QuotaRequest, got: %v", err)
	}
}

func TestQuotaRequestValidateDelete_NewRequestIsAllowed(t *testing.T) {
	// A newly-created QuotaRequest with no conditions is Pending and deletable.
	qr := &QuotaRequest{}
	if err := qr.ValidateDelete(context.Background()); err != nil {
		t.Errorf("expected no error deleting new QuotaRequest with no conditions, got: %v", err)
	}
}

// ─── QuotaRequest.ValidateCreate / ValidateUpdate ────────────────────────────

func TestQuotaRequestValidateCreate_AlwaysSucceeds(t *testing.T) {
	qr := &QuotaRequest{}
	if err := qr.ValidateCreate(context.Background()); err != nil {
		t.Errorf("ValidateCreate should always succeed, got: %v", err)
	}
}

func TestQuotaRequestValidateUpdate_AlwaysSucceeds(t *testing.T) {
	qr := &QuotaRequest{}
	if err := qr.ValidateUpdate(context.Background(), &QuotaRequest{}); err != nil {
		t.Errorf("ValidateUpdate should always succeed, got: %v", err)
	}
}

// ─── Cluster/NodePool quota hook ─────────────────────────────────────────────

func TestClusterValidateCreate_QuotaHookCalled(t *testing.T) {
	called := false
	SetQuotaCheckFunc(func(_ context.Context, ns, resource string) error {
		called = true
		if ns != "my-ns" {
			t.Errorf("namespace: got %q, want %q", ns, "my-ns")
		}
		if resource != "hostedclusters" {
			t.Errorf("resource: got %q, want %q", resource, "hostedclusters")
		}
		return nil
	})
	t.Cleanup(func() { SetQuotaCheckFunc(nil) })

	c := &Cluster{}
	c.Name = "c1"
	c.Namespace = "my-ns"
	c.UID = "uid-1"
	c.Spec.SafeName = DefaultSafeName(c.Name, c.UID)

	if err := c.ValidateCreate(context.Background()); err != nil {
		t.Errorf("ValidateCreate: %v", err)
	}
	if !called {
		t.Error("quota check was not called")
	}
}

func TestClusterValidateCreate_QuotaErrorPropagated(t *testing.T) {
	SetQuotaCheckFunc(func(_ context.Context, _, _ string) error {
		return errQuotaExceeded
	})
	t.Cleanup(func() { SetQuotaCheckFunc(nil) })

	c := &Cluster{}
	c.Name = "c1"
	c.Namespace = "ns"
	c.UID = "uid-1"
	c.Spec.SafeName = DefaultSafeName(c.Name, c.UID)

	if err := c.ValidateCreate(context.Background()); err == nil {
		t.Error("expected quota error to be returned, got nil")
	}
}

func TestClusterValidateCreate_NoHookIsNoop(t *testing.T) {
	SetQuotaCheckFunc(nil)

	c := &Cluster{}
	c.Name = "c1"
	c.Namespace = "ns"
	c.UID = "uid-1"
	c.Spec.SafeName = DefaultSafeName(c.Name, c.UID)

	if err := c.ValidateCreate(context.Background()); err != nil {
		t.Errorf("expected no error with nil quota hook, got: %v", err)
	}
}

func TestNodePoolValidateCreate_QuotaHookCalled(t *testing.T) {
	called := false
	SetQuotaCheckFunc(func(_ context.Context, ns, resource string) error {
		called = true
		if resource != "nodepools" {
			t.Errorf("resource: got %q, want %q", resource, "nodepools")
		}
		return nil
	})
	t.Cleanup(func() { SetQuotaCheckFunc(nil) })

	np := &NodePool{}
	np.Namespace = "my-ns"

	if err := np.ValidateCreate(context.Background()); err != nil {
		t.Errorf("ValidateCreate: %v", err)
	}
	if !called {
		t.Error("quota check was not called for NodePool")
	}
}

func TestNodePoolValidateCreate_QuotaErrorPropagated(t *testing.T) {
	SetQuotaCheckFunc(func(_ context.Context, _, _ string) error {
		return errQuotaExceeded
	})
	t.Cleanup(func() { SetQuotaCheckFunc(nil) })

	np := &NodePool{}
	if err := np.ValidateCreate(context.Background()); err == nil {
		t.Error("expected quota error to be returned, got nil")
	}
}

func TestNodePoolValidateUpdate_ClusterIDImmutable(t *testing.T) {
	old := &NodePool{}
	old.Spec.ClusterID = "cluster-a"

	updated := &NodePool{}
	updated.Spec.ClusterID = "cluster-b"

	if err := updated.ValidateUpdate(context.Background(), old); err == nil {
		t.Error("expected error when clusterID changes, got nil")
	}
}

func TestNodePoolValidateUpdate_ClusterIDUnchanged(t *testing.T) {
	old := &NodePool{}
	old.Spec.ClusterID = "cluster-a"

	updated := &NodePool{}
	updated.Spec.ClusterID = "cluster-a"

	if err := updated.ValidateUpdate(context.Background(), old); err != nil {
		t.Errorf("expected no error when clusterID unchanged, got: %v", err)
	}
}
