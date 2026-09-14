package versionresolution

import (
	"fmt"

	"github.com/spf13/cobra"
	utilversion "k8s.io/apimachinery/pkg/util/version"
	ctrl "sigs.k8s.io/controller-runtime"

	privatev1 "github.com/openshift-online/gecko/platform-api/api/private/v1"

	"github.com/openshift-online/gecko/controllers/util/setup"
	"github.com/openshift-online/gecko/controllers/versionresolution"
)

const gcpHCPVersionFloor = "4.22.0"

// NewCommand returns the version-resolution subcommand.
func NewCommand(rf *setup.RootFlags) *cobra.Command {
	var cincinnatiURL, arch, defaultVersion, minimumSupportedVersion string

	cmd := &cobra.Command{
		Use:   "version-resolution",
		Short: "Run the version-resolution controller",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if defaultVersion == "" {
				return fmt.Errorf("--default-version is required")
			}
			if err := validateMinimumSupportedVersion(minimumSupportedVersion); err != nil {
				return err
			}
			cinClient := versionresolution.NewCincinnatiClient(cincinnatiURL, arch)
			service, err := versionresolution.NewVersionService(cinClient, minimumSupportedVersion)
			if err != nil {
				return err
			}
			if err := service.Validate(defaultVersion); err != nil {
				return fmt.Errorf("validate --default-version: %w", err)
			}

			log, err := rf.NewLogger("version-resolution-controller")
			if err != nil {
				return fmt.Errorf("create logger: %w", err)
			}

			scheme := setup.NewScheme()
			mgr, err := rf.NewManager(scheme, log)
			if err != nil {
				return fmt.Errorf("create manager: %w", err)
			}

			rec := versionresolution.NewReconciler(service, defaultVersion, log, mgr.GetClient())

			if err := ctrl.NewControllerManagedBy(mgr).
				For(&privatev1.Cluster{}).
				WithOptions(rf.ControllerOpts()).
				Complete(rec); err != nil {
				return fmt.Errorf("setup controller: %w", err)
			}

			return mgr.Start(ctx)
		},
	}

	cmd.Flags().StringVar(&cincinnatiURL, "cincinnati-url", "https://api.openshift.com/api/upgrades_info/v1/graph", "Cincinnati API URL")
	cmd.Flags().StringVar(&arch, "arch", "amd64", "CPU architecture for Cincinnati query")
	cmd.Flags().StringVar(&defaultVersion, "default-version", "", "Exact OpenShift version reported as the platform default (required)")
	cmd.Flags().StringVar(&minimumSupportedVersion, "minimum-supported-version", gcpHCPVersionFloor, "Minimum OpenShift version supported by GCP HCP")

	return cmd
}

func validateMinimumSupportedVersion(minimum string) error {
	configured, err := utilversion.ParseSemantic(minimum)
	if err != nil {
		return fmt.Errorf("parse --minimum-supported-version %q: %w", minimum, err)
	}
	floor := utilversion.MustParseSemantic(gcpHCPVersionFloor)
	if !configured.AtLeast(floor) {
		return fmt.Errorf("--minimum-supported-version %q cannot be below the GCP HCP floor %s", minimum, floor)
	}
	return nil
}
