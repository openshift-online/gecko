package main

import (
	"testing"
)

func TestGetPublicResourcesCatalogPolicyRefs(t *testing.T) {
	wantPolicyRef := map[string]string{
		"Channel": "authenticated-catalog-read",
		"Version": "authenticated-catalog-read",
	}
	seen := make(map[string]bool)
	for _, resource := range getPublicResources() {
		if policyRef, found := wantPolicyRef[resource.GVK.Kind]; found {
			seen[resource.GVK.Kind] = true
			if resource.AuthorizationPolicyRefs["get"] != policyRef || resource.AuthorizationPolicyRefs["list"] != policyRef {
				t.Errorf("%s policy refs = %#v, want get/list=%q", resource.GVK.Kind, resource.AuthorizationPolicyRefs, policyRef)
			}
			if resource.Namespaced {
				t.Errorf("%s is namespaced, want cluster-scoped", resource.GVK.Kind)
			}
			if !resource.VerbAllowed("get") || !resource.VerbAllowed("list") {
				t.Errorf("%s must allow get and list verbs", resource.GVK.Kind)
			}
			for _, verb := range []string{"create", "update", "patch", "delete", "watch"} {
				if resource.VerbAllowed(verb) {
					t.Errorf("%s unexpectedly allows %s", resource.GVK.Kind, verb)
				}
			}
			continue
		}
		if len(resource.AuthorizationPolicyRefs) != 0 {
			t.Errorf("%s unexpectedly has authorization policy refs: %#v", resource.GVK.Kind, resource.AuthorizationPolicyRefs)
		}
	}
	for kind := range wantPolicyRef {
		if !seen[kind] {
			t.Errorf("public resource %s not found", kind)
		}
	}
}
