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
		appRef, err := resolveAppRef(eff, appArg(args))
		if err != nil {
			return err
		}
		deps, err := c.Deployments(ctx, ws, appRef)
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
