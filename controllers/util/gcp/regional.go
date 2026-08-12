package gcp

import (
	"context"
	"encoding/json"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	secretmanagerpb "cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"google.golang.org/api/iterator"
)

// ReadGoogPartnerSolution reads goog-partner-solution from the argocd-cluster
// Secret Manager secret in the given project. Returns "" if project is empty
// or the label is absent (non-fatal).
//
// This label is a per-region constant identifying the GCP Marketplace partner
// solution for billing/tracking purposes. It is needed by multiple components
// (placement, PSC creation, storage, load balancers, etc.) so it's provided
// as a shared utility.
func ReadGoogPartnerSolution(ctx context.Context, smClient *secretmanager.Client, project string) string {
	if project == "" || smClient == nil {
		return ""
	}

	// List argocd-cluster secrets filtered by infra-type:region label.
	it := smClient.ListSecrets(ctx, &secretmanagerpb.ListSecretsRequest{
		Parent: "projects/" + project,
		Filter: `labels.infra-type:region name:argocd-cluster`,
	})

	var secretName string
	for {
		secret, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return ""
		}
		secretName = secret.Name
		break
	}

	if secretName == "" {
		return ""
	}

	// Access the latest version of the secret.
	result, err := smClient.AccessSecretVersion(ctx, &secretmanagerpb.AccessSecretVersionRequest{
		Name: secretName + "/versions/latest",
	})
	if err != nil {
		return ""
	}

	// Unmarshal the payload to extract meta_common_labels.
	var payload struct {
		MetaCommonLabels string `json:"meta_common_labels"`
	}
	if err := json.Unmarshal(result.Payload.Data, &payload); err != nil {
		return ""
	}

	// Parse meta_common_labels (JSON-encoded string) and extract goog-partner-solution.
	if payload.MetaCommonLabels == "" {
		return ""
	}

	var commonLabels map[string]string
	if err := json.Unmarshal([]byte(payload.MetaCommonLabels), &commonLabels); err != nil {
		return ""
	}

	return commonLabels["goog-partner-solution"]
}
