package versionresolution

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/openshift-online/gecko/controllers/util/setup"
)

func TestCommandRequiresDefaultVersion(t *testing.T) {
	cmd := NewCommand(&setup.RootFlags{})
	cmd.SetArgs(nil)

	err := cmd.Execute()

	require.EqualError(t, err, "--default-version is required")
}

func TestCommandRejectsDefaultBelowMinimum(t *testing.T) {
	cmd := NewCommand(&setup.RootFlags{})
	cmd.SetArgs([]string{"--default-version=4.21.9"})

	err := cmd.Execute()

	require.ErrorContains(t, err, "unsupported version")
}

func TestCommandRejectsMinimumBelowGCPHCPFloor(t *testing.T) {
	cmd := NewCommand(&setup.RootFlags{})
	cmd.SetArgs([]string{
		"--default-version=4.21.9",
		"--minimum-supported-version=4.0.0",
	})

	err := cmd.Execute()

	require.ErrorContains(t, err, "cannot be below the GCP HCP floor 4.22.0")
}

func TestCommandMinimumSupportedVersionDefault(t *testing.T) {
	cmd := NewCommand(&setup.RootFlags{})
	flag := cmd.Flags().Lookup("minimum-supported-version")

	require.NotNil(t, flag)
	require.Equal(t, "4.22.0", flag.DefValue)
}
