package cmd

import (
	"context"

	"github.com/miabi-io/cli/internal/ui"
	"github.com/spf13/cobra"
)

func init() {
	releasesCmd.ValidArgsFunction = completeApps
	appCmd.AddCommand(releasesCmd)
}

var releasesCmd = &cobra.Command{
	Use:     "releases [app]",
	Short:   "List an application's releases (newest first)",
	Example: "  miabi apps releases web\n  miabi apps releases       # the bound app",
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
		rels, err := c.Releases(ctx, ws, appID)
		if err != nil {
			return err
		}
		if structured() {
			return emit(rels)
		}
		t := ui.NewTable("VERSION", "IMAGE", "ACTIVE", "AGE")
		for _, r := range rels {
			active := ""
			if r.Active {
				active = ui.Green("✓")
			}
			t.Row("v"+itoa(r.Version), r.Image, active, ui.Age(r.CreatedAt))
		}
		t.Print()
		return nil
	},
}
