package setupgradepolicy

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/openshift-online/gecko/controllers/util/setup"
	privatev1 "github.com/openshift-online/gecko/platform-api/api/private/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const fieldManager = "gecko-set-upgrade-policy"

// validDays is the set of recognized lowercase day names.
var validDays = map[string]bool{
	"monday": true, "tuesday": true, "wednesday": true,
	"thursday": true, "friday": true, "saturday": true, "sunday": true,
}

// parseDaysOfWeek splits and validates a comma-separated day list.
func parseDaysOfWeek(csv string) ([]string, error) {
	var days []string
	for _, d := range strings.Split(csv, ",") {
		d = strings.TrimSpace(strings.ToLower(d))
		if d == "" {
			continue
		}
		if !validDays[d] {
			return nil, fmt.Errorf("invalid day %q: must be one of monday, tuesday, wednesday, thursday, friday, saturday, sunday", d)
		}
		days = append(days, d)
	}
	if len(days) == 0 {
		return nil, fmt.Errorf("at least one day of the week is required")
	}
	return days, nil
}

// clearPolicyPatch is the JSON merge patch body that removes spec.maintenance
// unconditionally, regardless of which field manager previously owned it.
// A merge patch sends the raw JSON bytes over the wire without going through
// typed-object serialization, so the `omitempty` tag on
// ClusterSpec.Maintenance cannot suppress the explicit null.
var clearPolicyPatch = []byte(`{"spec":{"maintenance":null}}`)

// buildApplyConfig constructs the unstructured SSA apply configuration for the
// spec.maintenance field. Only fields explicitly managed by this command are
// included; all other Cluster fields are left untouched by server-side apply.
//
// Modes:
//   - clearWindow: sets spec.maintenance to an empty object {}, so the server
//     clears the window sub-field (previously owned by this manager) while
//     preserving any other inline policy fields.
//   - otherwise: sets spec.maintenance with the supplied window.
//
// The --clear (clearPolicy) path is handled separately via a JSON merge patch;
// see setUpgradePolicy.
func buildApplyConfig(
	namespace, name string,
	startTime time.Time,
	durationSeconds int32,
	daysOfWeek []string,
	clearWindow bool,
) runtime.ApplyConfiguration {
	base := map[string]any{
		"apiVersion": privatev1.GroupVersion.String(),
		"kind":       "Cluster",
		"metadata": map[string]any{
			"name":      name,
			"namespace": namespace,
		},
	}

	if clearWindow {
		// Empty maintenance object: the window sub-field is absent, so the server
		// clears it when this manager previously set it.
		base["spec"] = map[string]any{
			"maintenance": map[string]any{},
		}
	} else {
		base["spec"] = map[string]any{
			"maintenance": map[string]any{
				"window": map[string]any{
					"startTime":       metav1.NewTime(startTime).UTC().Format(time.RFC3339),
					"durationSeconds": int64(durationSeconds),
					"recurrence": map[string]any{
						"frequency":  "weekly",
						"daysOfWeek": daysOfWeekAsAny(daysOfWeek),
					},
				},
			},
		}
	}

	u := &unstructured.Unstructured{Object: base}
	return ctrlclient.ApplyConfigurationFromUnstructured(u)
}

// daysOfWeekAsAny converts []string to []any for unstructured embedding.
func daysOfWeekAsAny(days []string) []any {
	out := make([]any, len(days))
	for i, d := range days {
		out[i] = d
	}
	return out
}

// NewCommand returns the set-upgrade-policy subcommand.
//
// set-upgrade-policy applies the upgrade maintenance policy on an existing
// Cluster using server-side apply. Only spec.maintenance is managed by this
// command; all other Cluster fields are left untouched. Running the command
// multiple times with the same arguments is idempotent.
func NewCommand(rf *setup.RootFlags) *cobra.Command {
	var (
		namespace       string
		clusterName     string
		startTimeStr    string
		durationSeconds int32
		daysOfWeekCSV   string
		clearWindow     bool
		clearPolicy     bool
	)

	cmd := &cobra.Command{
		Use:   "set-upgrade-policy",
		Short: "Configure the upgrade maintenance policy on an existing cluster",
		Long: `set-upgrade-policy applies spec.maintenance on an existing Cluster using
server-side apply. Only the maintenance fields are managed; all other Cluster
fields are left untouched. Running the command multiple times with the same
arguments is idempotent.

To configure a weekly recurring maintenance window:

  gecko-controllers set-upgrade-policy \
    --cluster my-cluster \
    --namespace my-namespace \
    --start-time 2026-01-05T02:00:00Z \
    --duration-seconds 7200 \
    --days-of-week monday,wednesday

To remove the maintenance window (allow upgrades at any time):

  gecko-controllers set-upgrade-policy \
    --cluster my-cluster \
    --namespace my-namespace \
    --clear-window

To remove the entire maintenance policy:

  gecko-controllers set-upgrade-policy \
    --cluster my-cluster \
    --namespace my-namespace \
    --clear`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			if clusterName == "" {
				return fmt.Errorf("--cluster is required")
			}
			if namespace == "" {
				return fmt.Errorf("--namespace is required")
			}

			// Mutually exclusive options.
			if clearPolicy && clearWindow {
				return fmt.Errorf("--clear and --clear-window are mutually exclusive")
			}
			if clearPolicy && startTimeStr != "" {
				return fmt.Errorf("--clear cannot be combined with --start-time")
			}
			if clearWindow && startTimeStr != "" {
				return fmt.Errorf("--clear-window cannot be combined with --start-time")
			}

			var (
				startTime  time.Time
				daysOfWeek []string
			)

			if !clearPolicy && !clearWindow {
				if startTimeStr == "" {
					return fmt.Errorf("--start-time is required (use --clear-window to remove the window, or --clear to remove the entire policy)")
				}
				var err error
				startTime, err = time.Parse(time.RFC3339, startTimeStr)
				if err != nil {
					return fmt.Errorf("parse --start-time: %w", err)
				}

				if durationSeconds < 60 || durationSeconds > 86400 {
					return fmt.Errorf("--duration-seconds must be between 60 and 86400")
				}

				if daysOfWeekCSV == "" {
					return fmt.Errorf("--days-of-week is required when setting a maintenance window")
				}
				daysOfWeek, err = parseDaysOfWeek(daysOfWeekCSV)
				if err != nil {
					return fmt.Errorf("parse --days-of-week: %w", err)
				}
			}

			scheme := setup.NewScheme()
			c, err := rf.NewClient(scheme)
			if err != nil {
				return fmt.Errorf("create client: %w", err)
			}

			return setUpgradePolicy(ctx, c, namespace, clusterName, clearPolicy, startTime, durationSeconds, daysOfWeek, clearWindow)
		},
	}

	cmd.Flags().StringVar(&clusterName, "cluster", "", "Name of the Cluster to configure (required)")
	cmd.Flags().StringVar(&namespace, "namespace", "", "Namespace of the Cluster (required)")
	cmd.Flags().StringVar(&startTimeStr, "start-time", "", "RFC3339 start time of the first maintenance window occurrence (e.g. 2026-01-05T02:00:00Z)")
	cmd.Flags().Int32Var(&durationSeconds, "duration-seconds", 3600, "Duration of each maintenance window occurrence in seconds (60–86400)")
	cmd.Flags().StringVar(&daysOfWeekCSV, "days-of-week", "", "Comma-separated days on which the window may start (e.g. monday,wednesday)")
	cmd.Flags().BoolVar(&clearWindow, "clear-window", false, "Remove the maintenance window, allowing upgrades at any time (preserves other policy fields)")
	cmd.Flags().BoolVar(&clearPolicy, "clear", false, "Remove the entire maintenance policy from the cluster spec")

	return cmd
}

// setUpgradePolicy configures the maintenance policy on the named cluster.
//
// --clear uses a JSON merge patch so that the literal `"maintenance":null` is
// sent on the wire. This bypasses typed-object serialization and the
// `omitempty` tag on ClusterSpec.Maintenance, ensuring the field is removed
// from the store regardless of which field manager previously owned it.
//
// All other modes (set window, clear window) use server-side apply so that
// only spec.maintenance is managed; all other Cluster fields are untouched.
func setUpgradePolicy(
	ctx context.Context,
	c ctrlclient.Client,
	namespace, name string,
	clearPolicy bool,
	startTime time.Time,
	durationSeconds int32,
	daysOfWeek []string,
	clearWindow bool,
) error {
	if clearPolicy {
		// Use a JSON merge patch to guarantee the null reaches the server.
		// SSA with a typed object would lose the null via omitempty during
		// Orlop's typed-object conversion step.
		target := &unstructured.Unstructured{}
		target.SetGroupVersionKind(privatev1.GroupVersion.WithKind("Cluster"))
		target.SetNamespace(namespace)
		target.SetName(name)

		if err := c.Patch(ctx, target,
			ctrlclient.RawPatch(types.MergePatchType, clearPolicyPatch),
		); err != nil {
			return fmt.Errorf("clear upgrade policy on cluster %s/%s: %w", namespace, name, err)
		}
		return nil
	}

	cfg := buildApplyConfig(namespace, name, startTime, durationSeconds, daysOfWeek, clearWindow)

	if err := c.Apply(ctx, cfg, ctrlclient.FieldOwner(fieldManager), ctrlclient.ForceOwnership); err != nil {
		return fmt.Errorf("apply upgrade policy on cluster %s/%s: %w", namespace, name, err)
	}

	return nil
}
