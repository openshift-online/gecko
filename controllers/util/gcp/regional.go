package gcp

import (
	"context"
	"encoding/json"
	"fmt"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	secretmanagerpb "cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"google.golang.org/api/iterator"

	"github.com/openshift-online/gecko/controllers/util/logger"
)

// ReadGoogPartnerSolutionWithLogging reads goog-partner-solution from the argocd-cluster
// Secret Manager secret. Returns "" if not found or on error. Logs warnings/errors via logger.
func ReadGoogPartnerSolutionWithLogging(ctx context.Context, smClient *secretmanager.Client, project string, log logger.Logger) string {
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
			log.Warnf(ctx, "gcp: failed to list argocd-cluster secrets in project %s: %v", project, err)
			return ""
		}
		secretName = secret.Name
		break
	}

	if secretName == "" {
		log.Warnf(ctx, "gcp: no argocd-cluster secret found in project %s (label filter: infra-type:region, name:argocd-cluster)", project)
		return ""
	}

	// Access the latest version of the secret.
	result, err := smClient.AccessSecretVersion(ctx, &secretmanagerpb.AccessSecretVersionRequest{
		Name: secretName + "/versions/latest",
	})
	if err != nil {
		log.Warnf(ctx, "gcp: failed to access secret %s: %v (may be permission issue)", secretName, err)
		return ""
	}

	// Unmarshal the payload to extract meta_common_labels.
	var payload struct {
		MetaCommonLabels string `json:"meta_common_labels"`
	}
	if err := json.Unmarshal(result.Payload.Data, &payload); err != nil {
		log.Warnf(ctx, "gcp: failed to unmarshal secret payload from %s: %v", secretName, err)
		return ""
	}

	// Parse meta_common_labels (JSON-encoded string) and extract goog-partner-solution.
	if payload.MetaCommonLabels == "" {
		log.Infof(ctx, "gcp: argocd-cluster secret exists but meta_common_labels is empty in project %s", project)
		return ""
	}

	var commonLabels map[string]string
	if err := json.Unmarshal([]byte(payload.MetaCommonLabels), &commonLabels); err != nil {
		log.Warnf(ctx, "gcp: failed to unmarshal meta_common_labels JSON: %v", err)
		return ""
	}

	label := commonLabels["goog-partner-solution"]
	if label == "" {
		log.Infof(ctx, "gcp: goog-partner-solution label not found in meta_common_labels for project %s", project)
	} else {
		log.Infof(ctx, "gcp: successfully read goog-partner-solution=%s from project %s", label, project)
	}
	return label
}

// ReadGoogPartnerSolution reads goog-partner-solution from the argocd-cluster
// Secret Manager secret in the given project. Returns "" if project is empty,
// the label is absent, or any error occurs (silently).
//
// This label is a per-region constant identifying the GCP Marketplace partner
// solution for billing/tracking purposes. It is needed by multiple components
// (placement, PSC creation, storage, load balancers, etc.) so it's provided
// as a shared utility.
//
// For debugging, use ReadGoogPartnerSolutionWithLogging instead.
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
