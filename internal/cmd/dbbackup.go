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
	"time"

	"github.com/miabi-io/cli/internal/api"
	"github.com/miabi-io/cli/internal/ui"
	"github.com/spf13/cobra"
)

var (
	dbBackupDatabase string
	dbBackupComment  string
	dbBackupWait     bool
	dbBackupTimeout  time.Duration
)

func init() {
	f := dbBackupCmd.Flags()
	f.StringVar(&dbBackupDatabase, "database", "", "logical database to back up (default: the instance's only one)")
	f.StringVar(&dbBackupComment, "comment", "", "note to attach, e.g. why it was taken")
	f.BoolVar(&dbBackupWait, "wait", false, "block until the backup finishes, and exit non-zero if it fails")
	f.DurationVar(&dbBackupTimeout, "timeout", time.Hour, "max time to wait with --wait")
	dbBackupCmd.ValidArgsFunction = completeDatabases
	dbCmd.AddCommand(dbBackupCmd)
}

var dbBackupCmd = &cobra.Command{
	Use:   "backup <db> [--database <name>] [--wait]",
	Short: "Back up a database now",
	Long: "Starts a backup of one logical database to the workspace's backup destination.\n" +
		"The panel runs it in the background, so this returns while it is still\n" +
		"running; --wait polls until it settles and exits non-zero on failure, which is\n" +
		"what a step before a migration wants.",
	Example: "  miabi db backup shop-db\n" +
		"  miabi db backup shop-db --database orders --comment \"before v2 migration\" --wait",
	Args: cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		ctx := context.Background()
		c, ws, id, err := dbConn(ctx, args[0])
		if err != nil {
			return err
		}
		dbID, err := c.ResolveLogicalDatabaseID(ctx, ws, id, dbBackupDatabase)
		if err != nil {
			return err
		}
		b, err := c.RunBackup(ctx, ws, id, dbID, dbBackupComment)
		if err != nil {
			return err
		}
		if !dbBackupWait || api.IsBackupTerminal(b.Status) {
			// An older server runs the backup inline and answers with it finished.
			return reportDBBackup(args[0], b, !api.IsBackupTerminal(b.Status))
		}
		wctx, cancel := context.WithTimeout(ctx, dbBackupTimeout)
		defer cancel()
		sp := ui.NewSpinner(fmt.Sprintf("Backup #%d: %s", b.Number, b.Status))
		sp.Start()
		final, err := c.WaitForBackup(wctx, ws, id, dbID, b.ID, func(status string) {
			sp.Update(fmt.Sprintf("Backup #%d: %s", b.Number, status))
		})
		sp.Stop()
		if err != nil {
			return fmt.Errorf("waiting for backup #%d: %w", b.Number, err)
		}
		return reportDBBackup(args[0], final, false)
	},
}

func reportDBBackup(db string, b *api.Backup, started bool) error {
	if structured() {
		if err := emit(b); err != nil {
			return err
		}
	}
	switch {
	case api.IsBackupFailure(b.Status):
		if !structured() && b.Error != "" {
			ui.Fail("Backup #%d failed: %s", b.Number, b.Error)
		}
		// Non-zero exit so CI fails the step.
		return fmt.Errorf("backup #%d failed", b.Number)
	case structured():
	case started:
		ui.Success("Backup #%d of %s started (%s)", b.Number, ui.Bold(db), ui.Status(b.Status))
		ui.Info("You'll get a notification when it finishes; add --wait to block until then.")
	default:
		ui.Success("Backup #%d of %s completed %s", b.Number, ui.Bold(db), ui.Dim("("+humanBytes(b.SizeBytes)+")"))
	}
	return nil
}
