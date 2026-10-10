package authz

import (
	_ "embed"
	"fmt"

	"github.com/cedar-policy/cedar-go"
)

const policyAuthenticatedCatalogRead = "authenticated-catalog-read"

//go:embed policies/authenticated-catalog-read.cedar
var authenticatedCatalogReadPolicySource string

func loadPlatformPolicySets() (map[string]*cedar.PolicySet, error) {
	sources := map[string]string{
		policyAuthenticatedCatalogRead: authenticatedCatalogReadPolicySource,
	}
	sets := make(map[string]*cedar.PolicySet, len(sources))
	for ref, source := range sources {
		var policy cedar.Policy
		if err := policy.UnmarshalCedar([]byte(source)); err != nil {
			return nil, fmt.Errorf("parse platform policy %q: %w", ref, err)
		}
		set := cedar.NewPolicySet()
		set.Add(cedar.PolicyID("platform:"+ref), &policy)
		sets[ref] = set
	}
	return sets, nil
}
