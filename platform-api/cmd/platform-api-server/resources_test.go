package main

import (
	"testing"
)

func TestGetPublicResourcesReadOnlyPlatformCatalog(t *testing.T) {
	wantCatalog := map[string]bool{
		"Channel": true,
		"Version": true,
	}
	seen := make(map[string]bool)
	for _, resource := range getPublicResources() {
		if _, found := wantCatalog[resource.GVK.Kind]; found {
			seen[resource.GVK.Kind] = true
			if resource.Namespaced {
				t.Errorf("%s is namespaced, want platform-scoped", resource.GVK.Kind)
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
	}
	for kind := range wantCatalog {
		if !seen[kind] {
			t.Errorf("public resource %s not found", kind)
		}
	}
}

func TestPlatformRoleBindingIsPrivatePlatformResource(t *testing.T) {
	found := false
	for _, resource := range getPrivateResources() {
		if resource.GVK.Kind == "PlatformRoleBinding" {
			found = true
			if resource.Namespaced || resource.Plural != "platformrolebindings" {
				t.Fatalf("unexpected PlatformRoleBinding registration: %+v", resource)
			}
		}
	}
	if !found {
		t.Fatal("PlatformRoleBinding missing from private API")
	}
	for _, resource := range getPublicResources() {
		if resource.GVK.Kind == "PlatformRoleBinding" {
			t.Fatal("PlatformRoleBinding exposed through public API")
		}
	}
}
