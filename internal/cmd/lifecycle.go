package cmd

import (
	"context"

	"github.com/miabi-io/cli/internal/api"
	"github.com/miabi-io/cli/internal/ui"
	"github.com/spf13/cobra"
)

// appLifecycleCmd builds an `apps <action> [app]` subcommand (action before the
// name, like `apps create`). The app is positional or the one bound by
// `miabi use`; it tab-completes app handles.
func appLifecycleCmd(use, short, done string, fn func(*api.Client, context.Context, string, uint) error) *cobra.Command {
	return &cobra.Command{
		Use:               use + " [app]",
		Short:             short,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeApps,
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
			if err := fn(c, ctx, ws, appID); err != nil {
				return err
			}
			ui.Success("%s %s", done, ui.Bold(appRef))
			return nil
		},
	}
}

var (
	appStartCmd = appLifecycleCmd("start", "Start an application's container", "Started",
		func(c *api.Client, ctx context.Context, ws string, id uint) error { return c.StartApp(ctx, ws, id) })
	appStopCmd = appLifecycleCmd("stop", "Stop an application's container", "Stopped",
		func(c *api.Client, ctx context.Context, ws string, id uint) error { return c.StopApp(ctx, ws, id) })
	appRestartCmd = appLifecycleCmd("restart", "Restart an application's container", "Restarting",
		func(c *api.Client, ctx context.Context, ws string, id uint) error { return c.RestartApp(ctx, ws, id) })
)
