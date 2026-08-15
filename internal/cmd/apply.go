package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/miabi-io/cli/internal/api"
	"github.com/spf13/cobra"
)

var (
	applyFiles  []string
	applyPrune  bool
	applyDryRun bool
)

func init() {
	f := applyCmd.Flags()
	f.StringArrayVarP(&applyFiles, "file", "f", nil, "manifest file(s); repeat for several, or '-' for stdin (required)")
	f.BoolVar(&applyPrune, "prune", false, "delete managed resources absent from the bundle")
	f.BoolVar(&applyDryRun, "dry-run", false, "show the plan without applying")
	_ = applyCmd.MarkFlagRequired("file")
	rootCmd.AddCommand(applyCmd)
}

var applyCmd = &cobra.Command{
	Use:   "apply -f stack.yaml [--prune] [--dry-run]",
	Short: "Converge the workspace to a bundle of miabi.io/v1 manifests",
	Long: "Reads one or more miabi.io/v1 YAML manifests and converges the workspace to\n" +
		"them via the apply API. --dry-run prints the plan; --prune deletes managed\n" +
		"resources that are absent from the bundle. Exits non-zero if any resource\n" +
		"fails to converge.",
	RunE: func(cmd *cobra.Command, _ []string) error {
		ctx := context.Background()
		bundle, err := readManifests(applyFiles)
		if err != nil {
			return err
		}
		c, eff, err := newClient()
		if err != nil {
			return err
		}
		ws, err := workspaceRef(ctx, c, eff)
		if err != nil {
			return err
		}

		if applyDryRun {
			plan, err := c.PlanApply(ctx, ws, bundle, applyPrune)
			if err != nil {
				return err
			}
			if structured() {
				return emit(plan)
			}
			printChanges(plan.Changes)
			fmt.Println("\n(dry run — nothing was applied)")
			return nil
		}

		res, err := c.Apply(ctx, ws, bundle, applyPrune)
		if err != nil {
			return err
		}
		if structured() {
			_ = emit(res)
		} else {
			if res.Plan != nil {
				printChanges(res.Plan.Changes)
			}
			fmt.Printf("\nApplied %d resource(s).\n", res.Applied)
			for _, f := range res.Failures {
				fmt.Fprintf(os.Stderr, "  ✗ %s/%s (%s): %s\n", f.Kind, f.Name, f.Action, f.Error)
			}
		}
		if len(res.Failures) > 0 {
			return fmt.Errorf("%d resource(s) failed to apply", len(res.Failures))
		}
		return nil
	},
}

// readManifests concatenates the given files into one multi-document bundle,
// separating them with the YAML document marker. "-" reads stdin.
func readManifests(files []string) (string, error) {
	var docs []string
	for _, name := range files {
		var (
			data []byte
			err  error
		)
		if name == "-" {
			data, err = io.ReadAll(os.Stdin)
		} else {
			data, err = os.ReadFile(name)
		}
		if err != nil {
			return "", fmt.Errorf("read %s: %w", name, err)
		}
		docs = append(docs, strings.TrimSpace(string(data)))
	}
	return strings.Join(docs, "\n---\n"), nil
}

// printChanges renders a plan as a table, skipping no-op entries.
func printChanges(changes []api.Change) {
	var shown int
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "ACTION\tKIND\tNAME\tREASON")
	for _, ch := range changes {
		if ch.Action == "noop" {
			continue
		}
		shown++
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", symbolFor(ch.Action)+ch.Action, ch.Kind, ch.Name, ch.Reason)
	}
	_ = tw.Flush()
	if shown == 0 {
		fmt.Println("No changes — the workspace already matches the bundle.")
	}
}

func symbolFor(action string) string {
	switch action {
	case "create":
		return "+ "
	case "delete":
		return "- "
	case "update":
		return "~ "
	default:
		return "  "
	}
}
