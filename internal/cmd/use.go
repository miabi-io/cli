// Copyright (c) 2026 Jonas Kaninda.
//
// This file is part of Miabi.
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

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
			if cur := f.Selected(flagContext); cur != nil && cur.App != nil {
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
			cur := f.Selected(flagContext)
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
		app, err := c.App(ctx, ws, args[0])
		if err != nil {
			return err
		}
		cur, err := bindContext(f, eff)
		if err != nil {
			return err
		}
		cur.App = &config.AppRef{ID: app.ID, Name: app.Name, DisplayName: app.DisplayName}
		if err := config.Save(f); err != nil {
			return err
		}
		ui.Success("Now using %s %s", ui.Bold(app.Name), ui.Dim(fmt.Sprintf("(%s)", app.DisplayName)))
		return nil
	},
}
