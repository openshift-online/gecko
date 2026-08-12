package placement

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	secretmanagerpb "cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"github.com/spf13/cobra"
	"google.golang.org/api/iterator"
	ctrl "sigs.k8s.io/controller-runtime"

	privatev1 "github.com/openshift-online/gecko/platform-api/api/private/v1"

	placement "github.com/openshift-online/gecko/controllers/placement"
	"github.com/openshift-online/gecko/controllers/util/setup"
)

func envOr(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

// readGoogPartnerSolution reads goog-partner-solution from the argocd-cluster
// Secret Manager secret. Returns "" if smProject is empty or the label is absent.
func readGoogPartnerSolution(ctx context.Context, smClient *secretmanager.Client, smProject string) string {
	if smProject == "" {
		return ""
	}

	// List argocd-cluster secrets filtered by infra-type:region label.
	it := smClient.ListSecrets(ctx, &secretmanagerpb.ListSecretsRequest{
		Parent: fmt.Sprintf("projects/%s", smProject),
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

// NewCommand returns the placement subcommand.
func NewCommand(rf *setup.RootFlags) *cobra.Command {
	var candidateNames, baseDomains []string
	var smProject, maestroHTTPAddr string

	cmd := &cobra.Command{
		Use:   "placement",
		Short: "Run the placement controller",
		RunE: func(cmd *cobra.Command, args []string) error {
			if v := envOr("SECRETMANAGER_PROJECT", ""); v != "" && !cmd.Flags().Changed("secretmanager-project") {
				smProject = v
			}
			if v := envOr("MAESTRO_HTTP_ADDR", ""); v != "" && !cmd.Flags().Changed("maestro-http-addr") {
				maestroHTTPAddr = v
			}

			ctx := cmd.Context()

			log, err := rf.NewLogger("placement-controller")
			if err != nil {
				return fmt.Errorf("create logger: %w", err)
			}

			var selector placement.Selector
			var candidates []placement.Candidate
			var googPartnerSolution string

			if smProject != "" {
				smClient, err := secretmanager.NewClient(ctx)
				if err != nil {
					return fmt.Errorf("create secret manager client: %w", err)
				}
				defer smClient.Close() //nolint:errcheck
				selector = placement.NewDynamicSelector(smClient, smProject, maestroHTTPAddr)
				googPartnerSolution = readGoogPartnerSolution(ctx, smClient, smProject)
			} else {
				candidates = make([]placement.Candidate, 0, len(candidateNames))
				for i, name := range candidateNames {
					c := placement.Candidate{Name: name}
					if i < len(baseDomains) {
						c.BaseDomains = []string{baseDomains[i]}
					}
					candidates = append(candidates, c)
				}
				selector = placement.NewRoundRobinSelector()
			}

			scheme := setup.NewScheme()
			mgr, err := rf.NewManager(scheme, log)
			if err != nil {
				return fmt.Errorf("create manager: %w", err)
			}

			rec := placement.NewReconciler(selector, candidates, log, mgr.GetClient(), googPartnerSolution)

			if err := ctrl.NewControllerManagedBy(mgr).
				For(&privatev1.Cluster{}).
				WithOptions(rf.ControllerOpts()).
				Complete(rec); err != nil {
				return fmt.Errorf("setup controller: %w", err)
			}

			return mgr.Start(ctx)
		},
	}

	cmd.Flags().StringSliceVar(&candidateNames, "candidates", nil, "MC names (comma-separated); ignored when --secretmanager-project is set")
	cmd.Flags().StringSliceVar(&baseDomains, "base-domains", nil, "Base domains per MC, paired with --candidates")
	cmd.Flags().StringVar(&smProject, "secretmanager-project", "", "GCP project for Secret Manager MC/DNS discovery [$SECRETMANAGER_PROJECT]; enables dynamic selector")
	cmd.Flags().StringVar(&maestroHTTPAddr, "maestro-http-addr", "http://maestro.hyperfleet.svc.cluster.local:8000", "Maestro HTTP API URL for consumer discovery [$MAESTRO_HTTP_ADDR]")

	return cmd
}
