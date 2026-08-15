package cmd

import (
	"context"

	"github.com/miabi-io/cli/internal/ui"
	"github.com/spf13/cobra"
)

func init() {
	deploymentsCmd.ValidArgsFunction = completeApps
	appCmd.AddCommand(deploymentsCmd)
}

var deploymentsCmd = &cobra.Command{
	Use:     "deployments [app]",
	Aliases: []string{"deploys"},
	Short:   "List an application's deployments (newest first)",
	Long: "Lists the app's deploy history. The NUMBER column is the per-app deployment\n" +
		"number you pass to `miabi apps logs --deployment` and `miabi apps status --deployment`.",
	Example: "  miabi apps deployments web\n  miabi apps deployments    # the bound app",
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
		appID, _, err := resolveAppRef(ctx, c, eff, ws, appArg(args))
		if err != nil {
			return err
		}
		deps, err := c.Deployments(ctx, ws, appID)
		if err != nil {
			return err
		}
		if structured() {
			return emit(deps)
		}
		t := ui.NewTable("NUMBER", "STATUS", "TRIGGER", "IMAGE", "AGE")
		for _, d := range deps {
			num := "#" + itoa(d.Number)
			if d.Current {
				num += " " + ui.Green("(current)")
			}
			t.Row(num, ui.Status(d.Status), d.Trigger, d.Image, ui.Age(d.CreatedAt))
		}
		t.Print()
		return nil
	},
}
