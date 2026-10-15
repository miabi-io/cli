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

var statusDeployment int

func init() {
	statusCmd.Flags().IntVar(&statusDeployment, "deployment", 0, "show a specific deployment's status by its number")
	statusCmd.ValidArgsFunction = completeApps
	appCmd.AddCommand(statusCmd)
}

var statusCmd = &cobra.Command{
	Use:     "status [app] [--deployment <number>]",
	Short:   "Show an application's status, or a specific deployment's status",
	Example: "  miabi apps status web\n  miabi apps status web --deployment 7",
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

		if statusDeployment != 0 {
			dep, err := c.DeploymentByNumber(ctx, ws, appRef, statusDeployment)
			if err != nil {
				return err
			}
			if structured() {
				return emit(dep)
			}
			ui.Info("Deployment #%d: %s", dep.Number, ui.Status(dep.Status))
			if dep.Image != "" {
				ui.Info("Image: %s", dep.Image)
			}
			if dep.Error != "" {
				ui.Fail("Error: %s", dep.Error)
			}
			return nil
		}

		app, err := c.App(ctx, ws, appRef)
		if err != nil {
			return err
		}
		if structured() {
			return emit(app)
		}
		ui.Info("%s (%s): %s", ui.Bold(app.Name), appRef, ui.Status(app.Status))
		ui.Info("Image: %s:%s", app.Image, app.Tag)
		return nil
	},
}
