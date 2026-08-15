package cmd

import (
	"context"
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
)

func init() {
	f := deployCmd.Flags()
	f.StringVar(&deployTag, "tag", "", "image tag to deploy (e.g. the git SHA)")
	f.StringVar(&deployStrategy, "strategy", "", "deploy strategy: recreate | rolling | canary")
	f.BoolVar(&deployWait, "wait", false, "block until the deployment is terminal; non-zero exit on failure")
	f.DurationVar(&deployTimeout, "timeout", 10*time.Minute, "max time to wait with --wait")
	deployCmd.ValidArgsFunction = completeApps
	appCmd.AddCommand(deployCmd)
}

var deployCmd = &cobra.Command{
	Use:   "deploy [app] [--tag <tag>] [--wait]",
	Short: "Deploy an application (optionally waiting for the result)",
	Long: "Triggers a deployment of the app (positional, or the app bound by `miabi use`).\n" +
		"With --tag, deploys that image tag (the common CI flow). With --wait, blocks\n" +
		"until the deployment finishes and exits non-zero if it failed — a CI gate.",
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

		dep, err := c.Deploy(ctx, ws, appID, api.DeployRequest{Tag: deployTag, Strategy: deployStrategy})
		if err != nil {
			return err
		}
		if !deployWait {
			if structured() {
				return emit(dep)
			}
			ui.Success("Deployment #%d of %s started (%s)", dep.Number, ui.Bold(appRef), ui.Status(dep.Status))
			ui.Info("Follow it: miabi apps logs %s --deployment %d", appRef, dep.Number)
			return nil
		}

		wctx, cancel := context.WithTimeout(ctx, deployTimeout)
		defer cancel()

		sp := ui.NewSpinner(fmt.Sprintf("Deployment #%d: %s", dep.Number, dep.Status))
		sp.Start()
		onUpdate := func(status string) {
			sp.Update(fmt.Sprintf("Deployment #%d: %s", dep.Number, status))
		}
		final, err := c.WaitForDeploy(wctx, ws, appID, dep.ID, onUpdate)
		sp.Stop()
		if err != nil {
			return fmt.Errorf("waiting for deployment #%d: %w", dep.Number, err)
		}
		if structured() {
			_ = emit(final)
		}
		if api.IsFailure(final.Status) {
			if final.Error != "" {
				ui.Fail("Deployment #%d failed: %s", final.Number, final.Error)
			}
			// Non-zero exit so CI fails the step.
			return fmt.Errorf("deployment #%d failed", final.Number)
		}
		if !structured() {
			ui.Success("Deployment #%d succeeded", final.Number)
		}
		return nil
	},
}
