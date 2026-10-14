package v1

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// ValidateCreate validates QuotaRequest creation.
func (qr *QuotaRequest) ValidateCreate(_ context.Context) error {
	return nil
}

// ValidateUpdate validates QuotaRequest updates.
// The spec fields are protected by XValidation immutability rules in the schema;
// this method enforces any cross-field invariants that cannot be expressed there.
func (qr *QuotaRequest) ValidateUpdate(_ context.Context, _ runtime.Object) error {
	return nil
}

// ValidateDelete validates QuotaRequest deletion.
// Approved requests are immutable audit records and cannot be deleted via the
// public API. Users may delete Pending, Denied, or Superseded requests (e.g.
// to withdraw a request, clear a rejected one, or clean up superseded ones).
//
// Returns a Conflict (HTTP 409) status error for Approved requests so that the
// public API handler surfaces the correct status code to callers.
func (qr *QuotaRequest) ValidateDelete(_ context.Context) error {
	if qr.Status.IsApproved() {
		return apierrors.NewConflict(
			schema.GroupResource{Group: GroupVersion.Group, Resource: "quotarequests"},
			qr.Name,
			fmt.Errorf("approved requests are immutable audit records and cannot be deleted"),
		)
	}
	return nil
}
