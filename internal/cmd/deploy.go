package cmd

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/miabi-io/cli/internal/api"
	"github.com/miabi-io/cli/internal/ui"
	"github.com/spf13/cobra"
)

var (
	deployTag      string
	deployStrategy string
	deployWait     bool
	deployTimeout  time.Duration
	deployNoCache  bool
)

func init() {
	f := deployCmd.Flags()
	f.StringVar(&deployTag, "tag", "", "image tag to deploy (e.g. the git SHA)")
	f.StringVar(&deployStrategy, "strategy", "", "deploy strategy: recreate | rolling | canary")
	f.BoolVar(&deployWait, "wait", false, "block until the deployment is terminal; non-zero exit on failure")
	f.BoolVar(&deployNoCache, "no-cache", false, "rebuild every layer, ignoring the build cache (git-source apps)")
	f.DurationVar(&deployTimeout, "timeout", 10*time.Minute, "max time to wait with --wait")
	deployCmd.ValidArgsFunction = completeApps
	appCmd.AddCommand(deployCmd)
}

var deployCmd = &cobra.Command{
	Use:   "deploy [app] [--tag <tag>] [--no-cache] [--wait]",
	Short: "Deploy an application (optionally waiting for the result)",
	Long: "Triggers a deployment of the app (positional, or the app bound by `miabi use`).\n" +
		"With --tag, deploys that image tag (the common CI flow). With --wait, blocks\n" +
		"until the deployment finishes and exits non-zero if it failed — a CI gate. A\n" +
		"canary counts as finished: it waits for promotion, which --wait reports.\n" +
		"An app whose repository owns a pipeline deploys by running it; --wait then\n" +
		"waits for the run.",
	Example: "  miabi apps deploy web --tag $GIT_SHA --wait\n  miabi apps deploy       # deploy the bound app",
	Args:    cobra.MaximumNArgs(1),
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
		appID, appRef, err := resolveAppRef(ctx, c, eff, ws, appArg(args))
		if err != nil {
			return err
		}

		deadline := time.Now().Add(deployTimeout)
		var wait time.Duration
		if deployWait {
			wait = deployTimeout
		}
		var res *api.DeployResult
		err = withSpinner(deployWait, fmt.Sprintf("Deploying %s", appRef), func() (err error) {
			res, err = c.Deploy(ctx, ws, appID, api.DeployRequest{Tag: deployTag, Strategy: deployStrategy, NoCache: deployNoCache}, wait)
			return err
		})
		if err != nil {
			return err
		}
		if res.Run != nil {
			return finishPipelineDeploy(ctx, c, ws, appRef, res.Run, deployWait, deadline)
		}
		dep := res.Deployment
		if !deployWait {
			if structured() {
				return emit(dep)
			}
			ui.Success("Deployment #%d of %s started (%s)", dep.Number, ui.Bold(appRef), ui.Status(dep.Status))
			ui.Info("Follow it: miabi apps logs %s --deployment %d", appRef, dep.Number)
			return nil
		}
		final, err := settleDeployment(ctx, c, ws, appID, dep, deadline)
		if err != nil {
			return err
		}
		return reportSettled(final)
	},
}

// settleDeployment returns dep once it is settled. A server that honoured ?wait
// usually hands it back settled already; an older one, or one whose wait ran out
// before our own deadline, is polled for the rest of the time.
func settleDeployment(ctx context.Context, c *api.Client, ws string, appID uint, dep *api.Deployment, deadline time.Time) (*api.Deployment, error) {
	if api.IsSettled(dep.Status) {
		return dep, nil
	}
	wctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	sp := ui.NewSpinner(fmt.Sprintf("Deployment #%d: %s", dep.Number, dep.Status))
	sp.Start()
	final, err := c.WaitForDeploy(wctx, ws, appID, dep.ID, func(status string) {
		sp.Update(fmt.Sprintf("Deployment #%d: %s", dep.Number, status))
	})
	sp.Stop()
	if errors.Is(err, context.DeadlineExceeded) {
		return nil, fmt.Errorf("timed out waiting for deployment #%d to finish", dep.Number)
	}
	if err != nil {
		return nil, fmt.Errorf("waiting for deployment #%d: %w", dep.Number, err)
	}
	return final, nil
}

// reportSettled prints the outcome of a waited-on deployment and turns a failure
// into a non-zero exit, so CI fails the step.
func reportSettled(final *api.Deployment) error {
	if structured() {
		_ = emit(final)
	}
	switch {
	case api.IsFailure(final.Status):
		if final.Error != "" {
			ui.Fail("Deployment #%d failed: %s", final.Number, final.Error)
		}
		return fmt.Errorf("deployment #%d failed", final.Number)
	case final.Status == api.StatusCanary:
		if !structured() {
			ui.Warn("Deployment #%d is running as a canary; promote it in the console to finish the rollout", final.Number)
		}
	case !structured():
		ui.Success("Deployment #%d succeeded", final.Number)
	}
	return nil
}

// finishPipelineDeploy handles an app whose deploys run its repository's
// pipeline: the server answers with the run rather than a deployment.
func finishPipelineDeploy(ctx context.Context, c *api.Client, ws, appRef string, run *api.PipelineRun, wait bool, deadline time.Time) error {
	if !wait {
		if structured() {
			return emit(run)
		}
		ui.Success("Deploying %s through its pipeline: run #%d (%s)", ui.Bold(appRef), run.Number, ui.Status(run.Status))
		return nil
	}
	final := run
	if !api.IsRunTerminal(run.Status) {
		wctx, cancel := context.WithDeadline(ctx, deadline)
		defer cancel()
		sp := ui.NewSpinner(fmt.Sprintf("Pipeline run #%d: %s", run.Number, run.Status))
		sp.Start()
		r, err := c.WaitForPipelineRun(wctx, ws, run.ID, func(status string) {
			sp.Update(fmt.Sprintf("Pipeline run #%d: %s", run.Number, status))
		})
		sp.Stop()
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("timed out waiting for pipeline run #%d to finish", run.Number)
		}
		if err != nil {
			return fmt.Errorf("waiting for pipeline run #%d: %w", run.Number, err)
		}
		final = r
	}
	if structured() {
		_ = emit(final)
	}
	if final.Status != "succeeded" {
		if final.Error != "" {
			ui.Fail("Pipeline run #%d %s: %s", final.Number, final.Status, final.Error)
		}
		return fmt.Errorf("pipeline run #%d %s", final.Number, final.Status)
	}
	if !structured() {
		ui.Success("Pipeline run #%d succeeded", final.Number)
	}
	return nil
}

// withSpinner runs fn under a spinner when show is set. A server honouring ?wait
// holds the request for the whole deploy, which would otherwise look like a hang.
func withSpinner(show bool, msg string, fn func() error) error {
	if !show {
		return fn()
	}
	sp := ui.NewSpinner(msg)
	sp.Start()
	defer sp.Stop()
	return fn()
}
