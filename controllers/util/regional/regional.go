package regional

import (
	"context"
	"encoding/json"
	"strings"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	secretmanagerpb "cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"google.golang.org/api/iterator"

	"github.com/openshift-online/gecko/controllers/util/logger"
)

// ReadGoogPartnerSolution reads goog-partner-solution from the argocd-cluster
// Secret Manager secret in the given project. Returns "" if not found or on error.
// Logs warnings/errors via logger.
//
// This label is a per-region constant identifying the GCP Marketplace partner
// solution for billing/tracking purposes. It is needed by multiple components
// (placement, PSC creation, storage, load balancers, etc.) so it's provided
// as a shared utility.
func ReadGoogPartnerSolution(ctx context.Context, smClient *secretmanager.Client, project string, log logger.Logger) string {
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
			log.Warnf(ctx, "regional: failed to list argocd-cluster secrets in project %s: %v", project, err)
			return ""
		}
		secretName = secret.Name
		break
	}

	if secretName == "" {
		log.Warnf(ctx, "regional: no argocd-cluster secret found in project %s (label filter: infra-type:region, name:argocd-cluster)", project)
		return ""
	}

	// Access the latest version of the secret.
	result, err := smClient.AccessSecretVersion(ctx, &secretmanagerpb.AccessSecretVersionRequest{
		Name: secretName + "/versions/latest",
	})
	if err != nil {
		log.Warnf(ctx, "regional: failed to access secret %s: %v (may be permission issue)", secretName, err)
		return ""
	}

	// Unmarshal the payload to extract meta_common_labels.
	var payload struct {
		MetaCommonLabels string `json:"meta_common_labels"`
	}
	if err := json.Unmarshal(result.Payload.Data, &payload); err != nil {
		log.Warnf(ctx, "regional: failed to unmarshal secret payload from %s: %v", secretName, err)
		return ""
	}

	// Parse meta_common_labels (JSON-encoded string) and extract goog-partner-solution.
	if payload.MetaCommonLabels == "" {
		log.Infof(ctx, "regional: argocd-cluster secret exists but meta_common_labels is empty in project %s", project)
		return ""
	}

	// Unescape the JSON string if it contains escaped quotes (e.g., \" stored as \\\")
	unescapedLabels := strings.ReplaceAll(payload.MetaCommonLabels, "\\\"", "\"")

	var commonLabels map[string]string
	if err := json.Unmarshal([]byte(unescapedLabels), &commonLabels); err != nil {
		log.Warnf(ctx, "regional: failed to unmarshal meta_common_labels JSON: %v", err)
		return ""
	}

	label := commonLabels["goog-partner-solution"]
	if label == "" {
		log.Infof(ctx, "regional: goog-partner-solution label not found in meta_common_labels for project %s", project)
	} else {
		log.Infof(ctx, "regional: successfully read goog-partner-solution=%s from project %s", label, project)
	}
	return label
}
