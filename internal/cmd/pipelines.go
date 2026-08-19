package cmd

import (
	"context"
	"fmt"
	"strconv"

	"github.com/miabi-io/cli/internal/api"
	"github.com/miabi-io/cli/internal/ui"
	"github.com/spf13/cobra"
)

var (
	pipelineRunBranch    string
	pipelineRunCommit    string
	pipelineRunNoCache   bool
	pipelineRerunNoCache bool
)

func init() {
	rf := pipelineRunCmd.Flags()
	rf.StringVar(&pipelineRunBranch, "branch", "", "branch to build (default: the app's tracked ref)")
	rf.StringVar(&pipelineRunCommit, "commit", "", "commit to build (default: the branch head)")
	rf.BoolVar(&pipelineRunNoCache, "no-cache", false, "rebuild every layer, ignoring the build cache")

	pipelineRerunCmd.Flags().BoolVar(&pipelineRerunNoCache, "no-cache", false, "rebuild every layer, ignoring the build cache")

	pipelineCmd.AddCommand(pipelineLsCmd, pipelineRunCmd, pipelineRerunCmd)
	rootCmd.AddCommand(pipelineCmd)
}

var pipelineCmd = &cobra.Command{
	Use:     "pipeline",
	Aliases: []string{"pipelines"},
	Short:   "Run CI/CD pipelines",
	Long: "Lists the workspace's pipelines and starts runs. A run builds a commit into an\n" +
		"image and deploys that exact artifact, so it is the CI entry point for an app\n" +
		"whose repository carries pipeline-as-code.",
	Example: "  miabi pipeline ls\n" +
		"  miabi pipeline run web --branch main\n" +
		"  miabi pipeline run web --no-cache\n" +
		"  miabi pipeline rerun 42 --no-cache",
}

var pipelineLsCmd = &cobra.Command{
	Use:     "ls",
	Aliases: []string{"list"},
	Short:   "List pipelines in the workspace",
	RunE: func(cmd *cobra.Command, _ []string) error {
		ctx := context.Background()
		c, eff, err := newClient()
		if err != nil {
			return err
		}
		ws, err := workspaceRef(ctx, c, eff)
		if err != nil {
			return err
		}
		ps, err := c.Pipelines(ctx, ws)
		if err != nil {
			return err
		}
		if structured() {
			return emit(ps)
		}
		t := ui.NewTable("ID", "NAME", "SOURCE", "ENABLED", "AGE")
		for _, p := range ps {
			source := p.Source
			if source == "" {
				source = "manual"
			}
			t.Row(strconv.FormatUint(uint64(p.ID), 10), p.Name, source, boolLabel(p.Enabled), ui.Age(p.CreatedAt))
		}
		t.Print()
		return nil
	},
}

var pipelineRunCmd = &cobra.Command{
	Use:   "run <pipeline> [--branch <ref>] [--no-cache]",
	Short: "Start a pipeline run",
	Long: "Triggers a manual run of the pipeline (by name, uid, or numeric id).\n" +
		"--no-cache rebuilds every layer for this run only, which is the pipeline-side\n" +
		"replacement for editing an ARG CACHEBUST line into the Dockerfile.",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		c, eff, err := newClient()
		if err != nil {
			return err
		}
		ws, err := workspaceRef(ctx, c, eff)
		if err != nil {
			return err
		}
		run, err := c.TriggerPipeline(ctx, ws, args[0], api.TriggerPipelineRequest{
			Branch: pipelineRunBranch, Commit: pipelineRunCommit, NoCache: pipelineRunNoCache,
		})
		if err != nil {
			return err
		}
		return reportRun(run, args[0])
	},
}

var pipelineRerunCmd = &cobra.Command{
	Use:   "rerun <run-id> [--no-cache]",
	Short: "Re-run a pipeline run at the same commit",
	Long: "Starts a new run of the same pipeline at the ref and commit the earlier run\n" +
		"built — the same code again, not whatever has landed since.",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		runID, err := strconv.ParseUint(args[0], 10, 64)
		if err != nil || runID == 0 {
			return fmt.Errorf("invalid run id %q", args[0])
		}
		ctx := context.Background()
		c, eff, err := newClient()
		if err != nil {
			return err
		}
		ws, err := workspaceRef(ctx, c, eff)
		if err != nil {
			return err
		}
		run, err := c.RerunPipelineRun(ctx, ws, uint(runID), api.RerunPipelineRequest{NoCache: pipelineRerunNoCache})
		if err != nil {
			return err
		}
		return reportRun(run, args[0])
	},
}

// reportRun prints (or emits) a freshly queued run. A cold build is called out because it is the
// difference between a run that takes seconds and one that takes minutes.
func reportRun(run *api.PipelineRun, ref string) error {
	if structured() {
		return emit(run)
	}
	ui.Success("Run #%d of %s queued (%s)", run.Number, ui.Bold(ref), ui.Status(run.Status))
	if run.NoCache {
		ui.Info("Building without cache — expect a slower run.")
	}
	return nil
}

func boolLabel(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}
