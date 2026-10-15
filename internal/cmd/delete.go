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
	"os"

	"github.com/spf13/cobra"
)

var (
	deleteFiles  []string
	deleteDryRun bool
)

func init() {
	f := deleteCmd.Flags()
	f.StringArrayVarP(&deleteFiles, "file", "f", nil, "manifest file or directory; repeat for several, or '-' for stdin (required)")
	f.BoolVar(&deleteDryRun, "dry-run", false, "show what would be deleted without deleting")
	_ = deleteCmd.MarkFlagRequired("file")
	rootCmd.AddCommand(deleteCmd)
}

var deleteCmd = &cobra.Command{
	Use:   "delete -f stack.yaml [--dry-run]",
	Short: "Delete the resources a miabi.io/v1 bundle names (inverse of apply)",
	Long: "Reads one or more miabi.io/v1 manifests and deletes exactly the resources\n" +
		"they name, in dependency-safe order (dependents before their dependencies).\n" +
		"Entries that don't exist are skipped. Exits non-zero if any delete fails.",
	RunE: func(cmd *cobra.Command, _ []string) error {
		ctx := context.Background()
		bundle, err := readManifests(deleteFiles)
		if err != nil {
			return err
		}
		c, eff, err := newClient()
		if err != nil {
			return err
		}
		ws, err := workspaceRef(ctx, c, eff)
		if err != nil {
			return err
		}

		if deleteDryRun {
			plan, err := c.PlanDelete(ctx, ws, bundle)
			if err != nil {
				return err
			}
			if structured() {
				return emit(plan)
			}
			printChanges(plan.Changes)
			fmt.Println("\n(dry run — nothing was deleted)")
			return nil
		}

		res, err := c.Delete(ctx, ws, bundle)
		if err != nil {
			return err
		}
		if structured() {
			_ = emit(res)
		} else {
			if res.Plan != nil {
				printChanges(res.Plan.Changes)
			}
			fmt.Printf("\nDeleted %d resource(s).\n", res.Applied)
			for _, f := range res.Failures {
				fmt.Fprintf(os.Stderr, "  ✗ %s/%s (%s): %s\n", f.Kind, f.Name, f.Action, f.Error)
			}
		}
		if len(res.Failures) > 0 {
			return fmt.Errorf("%d resource(s) failed to delete", len(res.Failures))
		}
		return nil
	},
}
