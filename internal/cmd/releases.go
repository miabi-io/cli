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
		appRef, err := resolveAppRef(eff, appArg(args))
		if err != nil {
			return err
		}
		rels, err := c.Releases(ctx, ws, appRef)
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
