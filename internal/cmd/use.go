package cmd

import (
	"context"
	"fmt"

	"github.com/miabi-io/cli/internal/config"
	"github.com/miabi-io/cli/internal/ui"
	"github.com/spf13/cobra"
)

var useClear bool

func init() {
	useCmd.Flags().BoolVar(&useClear, "clear", false, "unbind the current app")
	useCmd.ValidArgsFunction = completeApps
	rootCmd.AddCommand(useCmd)
}

var useCmd = &cobra.Command{
	Use:   "use [app]",
	Short: "Bind a default application for app-scoped commands",
	Long: "Persists a default app in ~/.miabi/config.yaml so commands like `apps deploy`,\n" +
		"`apps logs`, and `apps status` need no app argument. The binding is cleared when\n" +
		"you switch workspaces. Run with no argument to show the current binding.",
	Example: "  miabi use web       # bind\n  miabi use           # show current\n  miabi use --clear   # unbind",
	Args:    cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		f, err := config.Load()
		if err != nil {
			return err
		}

		if useClear {
			if cur := f.CurrentContext(); cur != nil && cur.App != nil {
				cur.App = nil
				if err := config.Save(f); err != nil {
					return err
				}
			}
			ui.Success("Cleared the current app")
			return nil
		}

		// No arg: report the current binding.
		if len(args) == 0 {
			cur := f.CurrentContext()
			if cur == nil || cur.App == nil {
				ui.Info("No app bound. Bind one with `miabi use <app>`.")
				return nil
			}
			if structured() {
				return emit(cur.App)
			}
			ui.Info("Current app: %s", ui.Bold(cur.App.Name))
			return nil
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
		id, err := c.ResolveAppID(ctx, ws, args[0])
		if err != nil {
			return err
		}
		app, err := c.App(ctx, ws, id)
		if err != nil {
			return err
		}
		f.EnsureCurrent().App = &config.AppRef{ID: app.ID, Name: app.Name, DisplayName: app.DisplayName}
		if err := config.Save(f); err != nil {
			return err
		}
		ui.Success("Now using %s %s", ui.Bold(app.Name), ui.Dim(fmt.Sprintf("(%s)", app.DisplayName)))
		return nil
	},
}
