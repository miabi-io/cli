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

	"github.com/miabi-io/cli/internal/api"
	"github.com/miabi-io/cli/internal/ui"
	"github.com/spf13/cobra"
)

// appLifecycleCmd builds an `apps <action> [app]` subcommand (action before the
// name, like `apps create`). The app is positional or the one bound by
// `miabi use`; it tab-completes app handles.
func appLifecycleCmd(use, short, done string, fn func(*api.Client, context.Context, string, string) error) *cobra.Command {
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
			appRef, err := resolveAppRef(eff, appArg(args))
			if err != nil {
				return err
			}
			if err := fn(c, ctx, ws, appRef); err != nil {
				return err
			}
			ui.Success("%s %s", done, ui.Bold(appRef))
			return nil
		},
	}
}

var (
	appStartCmd = appLifecycleCmd("start", "Start an application's container", "Started",
		func(c *api.Client, ctx context.Context, ws, app string) error { return c.StartApp(ctx, ws, app) })
	appStopCmd = appLifecycleCmd("stop", "Stop an application's container", "Stopped",
		func(c *api.Client, ctx context.Context, ws, app string) error { return c.StopApp(ctx, ws, app) })
	appRestartCmd = appLifecycleCmd("restart", "Restart an application's container", "Restarting",
		func(c *api.Client, ctx context.Context, ws, app string) error { return c.RestartApp(ctx, ws, app) })
)
