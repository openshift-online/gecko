package authz

import (
	"context"
	"fmt"
	"log"

	privatev1 "github.com/openshift-online/gecko/platform-api/api/private/v1"
	"github.com/openshift-online/gecko/orlop/pkg/apiserver/storage"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RunBootstrap upserts initial PlatformRoleBindings from the bootstrap config.
// This is idempotent: if a binding with the same name already exists, it is skipped.
// Uses private types because the ConvertingResourceHandler stores objects as private types.
func RunBootstrap(ctx context.Context, prbStore storage.ResourceStore, bindings []BootstrapBinding) error {
	for _, b := range bindings {
		prb := &privatev1.PlatformRoleBinding{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "gcp.managed.openshift.io/v1",
				Kind:       "PlatformRoleBinding",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: b.Name,
			},
			Spec: privatev1.PlatformRoleBindingSpec{
				Subject: b.Subject,
				RoleRef: b.RoleRef,
			},
		}

		if err := prbStore.Create(ctx, prb); err != nil {
			if errors.IsAlreadyExists(err) {
				log.Printf("Bootstrap: PlatformRoleBinding %q already exists, skipping", b.Name)
				continue
			}
			return fmt.Errorf("bootstrap: creating PlatformRoleBinding %q: %w", b.Name, err)
		}
		log.Printf("Bootstrap: created PlatformRoleBinding %q (subject=%s, roleRef=%s)", b.Name, b.Subject, b.RoleRef)
	}
	return nil
}
