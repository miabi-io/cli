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
	"fmt"
	"path/filepath"
	"time"

	"github.com/miabi-io/cli/internal/host"
	"github.com/miabi-io/cli/internal/ui"
	"github.com/miabi-io/miabi/pkg/stack/stackcmd"
	"github.com/spf13/cobra"
)

func newBackupCmd() *cobra.Command {
	var file, dir string
	c := &cobra.Command{
		Use:   "backup",
		Short: "Dump the platform database to a local file",
		Long: "Writes a pg_dump of the platform database to backups/ beside the manifest. It needs no\n" +
			"running panel, only Postgres.\n\n" +
			"This is the database only — not volumes, certificates or the manifest. The dump's\n" +
			"secrets decrypt only with the encryption key in the manifest, so back that file up\n" +
			"too. For off-host disaster recovery use Platform backup in the admin panel.",
		Example:       "  sudo miabi stack backup\n  sudo miabi stack backup --dir /mnt/backups",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			sess, err := openHost(file)
			if err != nil {
				return err
			}
			defer func() { _ = sess.Close() }()
			ctx, cancel := stackCtx()
			defer cancel()
			_, err = stackcmd.Backup(ctx, sess.Svc, sess.Manifest, stackcmd.BackupOptions{Dir: dir}, cliUI{})
			return err
		},
	}
	c.Flags().StringVar(&dir, "dir", "", "directory to write to (default: backups/ beside the manifest)")
	c.Flags().StringVarP(&file, "file", "f", "", "manifest path")
	return c
}

func newBackupsCmd() *cobra.Command {
	var file, dir string
	c := &cobra.Command{
		Use:           "backups",
		Short:         "List the local database dumps",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			if dir == "" {
				path := host.ManifestPath(file)
				if err := host.RequireReadable(path); err != nil {
					return err
				}
				dir = stackcmd.BackupDir(path)
			}
			all, err := stackcmd.ListBackups(dir)
			if err != nil {
				return err
			}
			if len(all) == 0 {
				ui.Info("No database dumps in %s.", dir)
				return nil
			}
			fmt.Printf("%s\n\n", ui.Dim(dir))
			t := ui.NewTable("NAME", "VERSION", "LABEL", "TAKEN", "SIZE")
			for _, b := range all {
				t.Row(filepath.Base(b.Path), b.Version, orDash(b.Label), b.Taken.Local().Format(time.DateTime), humanBytes(b.Size))
			}
			t.Print()
			return nil
		},
	}
	c.Flags().StringVar(&dir, "dir", "", "directory to list (default: backups/ beside the manifest)")
	c.Flags().StringVarP(&file, "file", "f", "", "manifest path")
	return c
}

func newRestoreCmd() *cobra.Command {
	var file string
	var yes, force bool
	c := &cobra.Command{
		Use:   "restore <dump>",
		Short: "Replace the platform database with a local dump",
		Long: "Stops the control plane, loads the dump into a scratch database, swaps it in only once\n" +
			"it loaded cleanly, and starts the control plane again. The current database is dumped\n" +
			"first, so a restore can itself be undone.\n\n" +
			"The dump is a path, or a name from `miabi stack backups`. A dump taken by a newer Miabi\n" +
			"than the one running is refused: the control plane only migrates forward.",
		Example:       "  sudo miabi stack backups\n  sudo miabi stack restore miabi-1.8.0-20261017T120000Z.dump",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(_ *cobra.Command, args []string) error {
			sess, err := openHost(file)
			if err != nil {
				return err
			}
			defer func() { _ = sess.Close() }()
			ctx, cancel := stackCtx()
			defer cancel()
			return stackcmd.Restore(ctx, sess.Svc, sess.Manifest, stackcmd.RestoreOptions{
				File: args[0], Yes: yes, Force: force,
			}, cliUI{})
		},
	}
	c.Flags().BoolVar(&force, "force", false, "restore a dump taken by a newer Miabi than the one running")
	c.Flags().StringVarP(&file, "file", "f", "", "manifest path")
	c.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return c
}
