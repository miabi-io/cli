package cmd

import (
	"context"
	"fmt"
	"strconv"

	"github.com/miabi-io/cli/internal/config"
	"github.com/miabi-io/cli/internal/ui"
	"github.com/spf13/cobra"
)

func init() {
	workspaceCmd.AddCommand(workspaceListCmd, workspaceShowCmd, workspaceSwitchCmd)
	rootCmd.AddCommand(workspaceCmd)
}

var workspaceCmd = &cobra.Command{
	Use:     "workspace",
	Aliases: []string{"ws"},
	Short:   "Manage the active workspace context",
}

var workspaceListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List the workspaces you can access",
	RunE: func(cmd *cobra.Command, _ []string) error {
		c, _, err := newClient()
		if err != nil {
			return err
		}
		ws, err := c.Workspaces(context.Background())
		if err != nil {
			return err
		}
		if structured() {
			return emit(ws)
		}
		active := ""
		if f, ferr := config.Load(); ferr == nil {
			if cur := f.CurrentContext(); cur != nil && cur.Workspace != nil {
				active = cur.Workspace.Name
			}
		}
		t := ui.NewTable("", "NAME", "DISPLAY NAME", "ROLE")
		for _, w := range ws {
			marker := " "
			if w.Name == active {
				marker = ui.Cyan("→")
			}
			t.Row(marker, w.Name, w.DisplayName, w.Role)
		}
		t.Print()
		return nil
	},
}

var workspaceShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show the active workspace (from config)",
	RunE: func(cmd *cobra.Command, _ []string) error {
		f, err := config.Load()
		if err != nil {
			return err
		}
		cur := f.CurrentContext()
		if cur == nil || cur.Workspace == nil {
			fmt.Println("No active workspace. Set one with `miabi workspace switch <name>`.")
			return nil
		}
		if structured() {
			return emit(cur.Workspace)
		}
		label := cur.Workspace.Name
		if cur.Workspace.DisplayName != "" {
			label = fmt.Sprintf("%s (%s)", cur.Workspace.DisplayName, cur.Workspace.Name)
		}
		ui.Info("Active workspace: %s", ui.Bold(label))
		if cur.App != nil {
			ui.Info("Current app: %s", ui.Bold(cur.App.Name))
		}
		return nil
	},
}

var workspaceSwitchCmd = &cobra.Command{
	Use:   "switch <name-or-id>",
	Short: "Set the active workspace, persisting it to config",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, _, err := newClient()
		if err != nil {
			return err
		}
		ws, err := c.Workspaces(context.Background())
		if err != nil {
			return err
		}
		ref := args[0]
		idMatch, _ := strconv.ParseUint(ref, 10, 64)
		for _, w := range ws {
			if w.Name == ref || w.UID == ref || (idMatch != 0 && w.ID == uint(idMatch)) {
				f, err := config.Load()
				if err != nil {
					return err
				}
				cur := f.EnsureCurrent()
				cur.Workspace = &config.WorkspaceRef{ID: w.ID, Name: w.Name, DisplayName: w.DisplayName}
				// The bound app belongs to the old workspace — clear it so a stale
				// app never leaks across a workspace switch.
				cur.App = nil
				if err := config.Save(f); err != nil {
					return err
				}
				label := w.Name
				if w.DisplayName != "" {
					label = fmt.Sprintf("%s (%s)", w.DisplayName, w.Name)
				}
				fmt.Printf("Active workspace is now %s [id %d]\n", label, w.ID)
				return nil
			}
		}
		return fmt.Errorf("workspace %q not found among your workspaces", ref)
	},
}
