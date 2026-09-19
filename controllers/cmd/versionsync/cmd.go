package versionsync

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/openshift-online/gecko/controllers/util/setup"
	"github.com/openshift-online/gecko/controllers/versionresolution"
	versionsynccontroller "github.com/openshift-online/gecko/controllers/versionsync"
)

// NewCommand returns the version-sync subcommand.
func NewCommand(rootFlags *setup.RootFlags) *cobra.Command {
	var cincinnatiURL string
	var architecture string

	command := &cobra.Command{
		Use:   "version-sync",
		Short: "Synchronize OpenShift versions from Cincinnati",
		RunE: func(command *cobra.Command, args []string) error {
			ctx := command.Context()

			log, err := rootFlags.NewLogger("version-sync-controller")
			if err != nil {
				return fmt.Errorf("create logger: %w", err)
			}

			scheme := setup.NewScheme()
			manager, err := rootFlags.NewManager(scheme, log)
			if err != nil {
				return fmt.Errorf("create manager: %w", err)
			}

			cincinnatiClient := versionresolution.NewCincinnatiClient(
				cincinnatiURL,
				architecture,
			)

			controller := versionsynccontroller.NewController(
				cincinnatiClient,
				log,
				manager.GetClient(),
			)

			if err := manager.Add(controller); err != nil {
				return fmt.Errorf("add version-sync controller: %w", err)
			}

			return manager.Start(ctx)
		},
	}

	command.Flags().StringVar(
		&cincinnatiURL,
		"cincinnati-url",
		"https://api.openshift.com/api/upgrades_info/v1/graph",
		"Cincinnati API URL",
	)
	command.Flags().StringVar(
		&architecture,
		"arch",
		"amd64",
		"CPU architecture for Cincinnati queries",
	)

	return command
}
