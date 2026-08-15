package cmd

import (
	"context"
	"fmt"

	"github.com/miabi-io/cli/internal/api"
	"github.com/miabi-io/cli/internal/ui"
	"github.com/spf13/cobra"
)

var (
	rollbackTo         int
	rollbackToPrevious bool
	rollbackYes        bool
)

func init() {
	f := rollbackCmd.Flags()
	f.IntVar(&rollbackTo, "to", 0, "release version to roll back to (see `miabi releases`)")
	f.BoolVar(&rollbackToPrevious, "to-previous", false, "roll back to the most recent inactive release")
	f.BoolVarP(&rollbackYes, "yes", "y", false, "skip the confirmation prompt")
	rollbackCmd.ValidArgsFunction = completeApps
	appCmd.AddCommand(rollbackCmd)
}

var rollbackCmd = &cobra.Command{
	Use:     "rollback [app] (--to <version> | --to-previous)",
	Short:   "Roll an application back to a previous release",
	Example: "  miabi apps rollback web --to-previous\n  miabi apps rollback web --to 4",
	Args:    cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		if rollbackTo == 0 && !rollbackToPrevious {
			return fmt.Errorf("specify --to <version> or --to-previous")
		}
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

		// Resolve the target release id and version for display.
		var targetID uint
		var targetVersion int
		if rollbackToPrevious {
			rels, err := c.Releases(ctx, ws, appID)
			if err != nil {
				return err
			}
			// Releases are newest-first; the previous release is the highest-version
			// one that is not currently active.
			for _, r := range rels {
				if !r.Active {
					targetID, targetVersion = r.ID, r.Version
					break
				}
			}
			if targetID == 0 {
				return fmt.Errorf("no previous release to roll back to")
			}
		} else {
			rel, err := c.ReleaseByVersion(ctx, ws, appID, rollbackTo)
			if err != nil {
				return err
			}
			targetID, targetVersion = rel.ID, rel.Version
		}

		if !rollbackYes && !structured() {
			if !ui.Confirm(fmt.Sprintf("Roll %s back to release v%d?", ui.Bold(appRef), targetVersion)) {
				ui.Info("Aborted")
				return nil
			}
		}

		dep, err := c.Rollback(ctx, ws, appID, api.RollbackRequest{ReleaseID: targetID})
		if err != nil {
			return err
		}
		if structured() {
			return emit(dep)
		}
		ui.Success("Rolling %s back to v%d (deployment #%d, %s)", ui.Bold(appRef), targetVersion, dep.Number, ui.Status(dep.Status))
		return nil
	},
}
