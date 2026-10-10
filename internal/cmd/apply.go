package cmd

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
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
	f.StringArrayVarP(&applyFiles, "file", "f", nil, "manifest file or directory; repeat for several, or '-' for stdin (required)")
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
// separating them with the YAML document marker. "-" reads stdin, and a
// directory reads every .yaml and .yml file under it, as a Git source does.
func readManifests(files []string) (string, error) {
	var docs []string
	add := func(data []byte) {
		if doc := strings.TrimSpace(string(data)); doc != "" {
			docs = append(docs, doc)
		}
	}
	for _, name := range files {
		if name == "-" {
			data, err := io.ReadAll(os.Stdin)
			if err != nil {
				return "", fmt.Errorf("read stdin: %w", err)
			}
			add(data)
			continue
		}
		paths, err := manifestPaths(name)
		if err != nil {
			return "", err
		}
		for _, p := range paths {
			data, err := os.ReadFile(p)
			if err != nil {
				return "", fmt.Errorf("read %s: %w", p, err)
			}
			add(data)
		}
	}
	return strings.Join(docs, "\n---\n"), nil
}

// manifestPaths expands name to the files it stands for: itself, or for a
// directory every .yaml and .yml file under it in path order, the same files a
// Git source pointed at that directory reads.
func manifestPaths(name string) ([]string, error) {
	info, err := os.Stat(name)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", name, err)
	}
	if !info.IsDir() {
		return []string{name}, nil
	}
	var paths []string
	err = filepath.WalkDir(name, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		switch strings.ToLower(filepath.Ext(p)) {
		case ".yaml", ".yml":
			paths = append(paths, p)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", name, err)
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("%s: no .yaml or .yml files in the directory", name)
	}
	sort.Strings(paths)
	return paths, nil
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
