package authz

import (
	"net/http"
	"testing"
)

func TestParseClusterChildRequestPath(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		plural string
		named  bool
	}{
		{name: "cluster item", path: "/apis/gcp.managed.openshift.io/v1/namespaces/customer/clusters/example", plural: "clusters", named: true},
		{name: "nodepool collection", path: "/apis/gcp.managed.openshift.io/v1/namespaces/customer/clusters/example/nodepools", plural: "nodepools"},
		{name: "upgrade policy collection", path: "/apis/gcp.managed.openshift.io/v1/namespaces/customer/clusters/example/controlplaneupgradepolicies", plural: "controlplaneupgradepolicies"},
		{name: "upgrade request collection", path: "/apis/gcp.managed.openshift.io/v1/namespaces/customer/clusters/example/controlplaneupgraderequests", plural: "controlplaneupgraderequests"},
		{name: "upgrade request item", path: "/apis/gcp.managed.openshift.io/v1/namespaces/customer/clusters/example/controlplaneupgraderequests/request-1", plural: "controlplaneupgraderequests", named: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseRequestPath(tt.path)
			if err != nil {
				t.Fatal(err)
			}
			if got.namespace != "customer" || got.plural != tt.plural || got.named != tt.named {
				t.Fatalf("parsed request = %+v, want namespace customer, plural %q, named %t", got, tt.plural, tt.named)
			}
		})
	}
}

func TestUnknownClusterChildRequestIsDenied(t *testing.T) {
	parsed, err := parseRequestPath("/apis/gcp.managed.openshift.io/v1/namespaces/customer/clusters/example/unknown")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := actionForRequest(http.MethodGet, parsed.plural, parsed.named); err == nil {
		t.Fatal("expected authorization to reject an unknown child resource")
	}
}
