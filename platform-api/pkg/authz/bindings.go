package authz

import (
	"context"
	"fmt"

	"github.com/cedar-policy/cedar-go"

	"github.com/openshift-online/gecko/orlop/pkg/apiserver/storage"
	privatev1 "github.com/openshift-online/gecko/platform-api/api/private/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// authorizationBinding is the common authorization view of both binding kinds.
// The resource kind, rather than the role's contents, determines its scope.
type authorizationBinding struct {
	metav1.ObjectMeta
	Spec     privatev1.RoleBindingSpec
	platform bool
}

func (b authorizationBinding) roleUID() cedar.EntityUID {
	if b.platform {
		return cedar.NewEntityUID("PlatformRoleBinding", cedar.String(b.Spec.RoleRef.Name+"/"+b.Name))
	}
	return cedar.NewEntityUID("NamespaceRole", cedar.String(fmt.Sprintf("%s/%s/%s", b.Namespace, b.Spec.RoleRef.Name, b.Name)))
}

func scopeResource(namespace string) cedar.EntityUID {
	if namespace == "" {
		return cedar.NewEntityUID("Platform", "default")
	}
	return cedar.NewEntityUID("Namespace", cedar.String(namespace))
}

// resourceForAction rejects scope mismatches even for direct authorizer calls.
// Request routing additionally checks the existing registered resource scope.
func resourceForAction(action Action, namespace string) (cedar.EntityUID, bool) {
	if isPlatformAction(action) != (namespace == "") {
		return cedar.EntityUID{}, false
	}
	return scopeResource(namespace), true
}

func listBindings(ctx context.Context, stores Stores, platform bool, options storage.ListOptions) ([]authorizationBinding, error) {
	store, kind := stores.RoleBindings, "RoleBinding"
	if platform {
		store, kind = stores.PlatformRoleBindings, "PlatformRoleBinding"
	}
	objects, err := listObjects(ctx, store, options, stores.Scheme, privatev1.GroupVersion.WithKind(kind))
	if err != nil {
		return nil, err
	}
	bindings := make([]authorizationBinding, 0, len(objects))
	for _, object := range objects {
		var binding authorizationBinding
		switch typed := object.(type) {
		case *privatev1.RoleBinding:
			binding = authorizationBinding{ObjectMeta: typed.ObjectMeta, Spec: typed.Spec}
		case *privatev1.PlatformRoleBinding:
			binding = authorizationBinding{ObjectMeta: typed.ObjectMeta, Spec: typed.Spec, platform: true}
		default:
			return nil, fmt.Errorf("binding store returned %T", object)
		}
		// Malformed stored objects cannot acquire the other binding kind's scope.
		if binding.platform == (binding.Namespace == "") {
			bindings = append(bindings, binding)
		}
	}
	return bindings, nil
}

func bindingsForUser(ctx context.Context, stores Stores, email string, platform bool) ([]authorizationBinding, error) {
	options := storage.ListOptions{}
	if !platform {
		options.FieldFilters = map[string]string{"spec.subject": email}
	}
	bindings, err := listBindings(ctx, stores, platform, options)
	if err != nil {
		return nil, fmt.Errorf("list bindings for principal: %w", err)
	}
	matched := make([]authorizationBinding, 0, len(bindings))
	for _, binding := range bindings {
		if binding.Spec.Subject == email || (platform && email != "" && binding.Spec.Subject == "*") {
			matched = append(matched, binding)
		}
	}
	return matched, nil
}
