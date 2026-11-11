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
	"github.com/miabi-io/miabi/pkg/stack"
	"github.com/spf13/cobra"
)

func init() {
	systemCmd.AddCommand(hostCommands()...)
	systemCmd.AddCommand(newSetupCmd("setup"), newSystemConfigCmd())

	setupCmd := newSetupCmd("setup")
	setupCmd.AddCommand(newStackEnvCmd())
	rootCmd.AddCommand(systemCmd, setupCmd, newUpgradeCmd("upgrade"))
}

var systemCmd = &cobra.Command{
	Use:     "system",
	Aliases: []string{"sys"},
	Short:   "Manage the Miabi installation on this host",
	Long: "These commands drive the Docker stack on the machine they run on — not a panel over\n" +
		"HTTP. They read and write the manifest at " + stack.DefaultConfigPath + ", need root,\n" +
		"and require a reachable Docker daemon.",
	Example: "  sudo miabi setup --domain miabi.example.com\n" +
		"  sudo miabi system upgrade\n" +
		"  sudo miabi system status\n" +
		"  sudo miabi system restart miabi-gateway",

	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
}

// hostCommands builds a fresh set each call, so `system` and the `stack` shim never share a cobra parent.
func hostCommands() []*cobra.Command {
	return []*cobra.Command{
		newUpgradeCmd("upgrade"),
		newApplyCmd(),
		newRestartCmd(),
		newStatusCmd(),
		newUninstallCmd(),
		newStackEnvCmd(),
		newBackupCmd(),
		newBackupsCmd(),
		newRestoreCmd(),
	}
}

func newSystemConfigCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "config",
		Short: "Manage the installation's manifest file",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	migrate := newMigrateConfigCmd()
	migrate.Use = "migrate"
	c.AddCommand(migrate)
	return c
}

// addHostShim keeps `miabi stack <host command>` working under the stacks group until the next major.
func addHostShim(parent *cobra.Command) {
	for _, c := range hostCommands() {
		deprecate(c, "miabi system "+c.Name())
		parent.AddCommand(c)
	}
	migrate := newMigrateConfigCmd()
	deprecate(migrate, "miabi system config migrate")
	parent.AddCommand(migrate)
}

// Cobra prints the notice only for the command that runs, so every subcommand carries its own.
func deprecate(c *cobra.Command, replacement string) {
	c.Deprecated = "use '" + replacement + "'"
	for _, sub := range c.Commands() {
		deprecate(sub, replacement+" "+sub.Name())
	}
}
