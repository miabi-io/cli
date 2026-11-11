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
	"strings"

	"github.com/miabi-io/cli/internal/api"
	"github.com/miabi-io/cli/internal/ui"
	"github.com/spf13/cobra"
)

var (
	versionSetScheme      string
	versionSetSeries      string
	versionSetBump        string
	versionSetStart       string
	versionSetTemplate    string
	versionSetRelease     []string
	versionSetOthers      string
	versionSetFloating    []string
	versionSetFromCommits bool
	versionHistorySize    int
)

func init() {
	sf := versioningSetCmd.Flags()
	sf.StringVar(&versionSetScheme, "scheme", "", "semver, calver or run")
	sf.StringVar(&versionSetSeries, "series", "", "semver version line, e.g. 1.x.x or 1.4.x")
	sf.StringVar(&versionSetBump, "bump", "", "default bump per build: patch, minor or major")
	sf.StringVar(&versionSetStart, "start", "", "first version of a series that has none")
	sf.StringVar(&versionSetTemplate, "template", "", "image tag template, e.g. v{version}")
	sf.StringSliceVar(&versionSetRelease, "release-branches", nil, "branches that produce real versions (empty: all)")
	sf.StringVar(&versionSetOthers, "other-branches", "", "prerelease or run-tag")
	sf.StringSliceVar(&versionSetFloating, "floating-tags", nil, "tags moved to the newest version, e.g. latest,v{major}")
	sf.BoolVar(&versionSetFromCommits, "bump-from-commits", false, "derive the bump from Conventional Commits")

	versioningHistoryCmd.Flags().IntVar(&versionHistorySize, "limit", 20, "how many versions to show")

	versioningCmd.AddCommand(versioningGetCmd, versioningHistoryCmd, versioningSetCmd, versioningSwitchCmd)
	rootCmd.AddCommand(versioningCmd)
}

var versioningCmd = &cobra.Command{
	Use:     "versioning",
	Aliases: []string{"version-policy"},
	Short:   "Manage how pipeline builds are versioned",
	Long: "A version policy decides the version, and image tag, each pipeline build gets: v1.4.2,\n" +
		"2026.10.3 or run-17. Policies are addressed by the pipeline that builds the image; an\n" +
		"application's pipelines share one policy.",
	Example: "  miabi versioning get web\n" +
		"  miabi versioning set web --bump minor --release-branches main\n" +
		"  miabi versioning switch web 2.x.x\n" +
		"  miabi versioning history web",
}

func versioningPolicy(ctx context.Context, pipeline string) (*api.Client, string, *api.VersionPolicy, error) {
	c, eff, err := newClient()
	if err != nil {
		return nil, "", nil, err
	}
	ws, err := workspaceRef(ctx, c, eff)
	if err != nil {
		return nil, "", nil, err
	}
	p, err := c.PipelineVersioning(ctx, ws, pipeline)
	if err != nil {
		return nil, "", nil, err
	}
	return c, ws, p, nil
}

var versioningGetCmd = &cobra.Command{
	Use:   "get <pipeline>",
	Short: "Show the version policy of a pipeline's images",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		_, _, p, err := versioningPolicy(context.Background(), args[0])
		if err != nil {
			return err
		}
		if structured() {
			return emit(p)
		}
		printPolicy(p)
		return nil
	},
}

func printPolicy(p *api.VersionPolicy) {
	t := ui.NewTable("FIELD", "VALUE")
	t.Row("repository", fmt.Sprintf("%s (%s)", p.Repository, p.OwnerKind))
	t.Row("scheme", p.Scheme)
	switch p.Scheme {
	case "semver":
		t.Row("series", p.Series)
		t.Row("bump", p.Bump)
	case "calver":
		t.Row("format", p.CalverFormat+".N")
	}
	if p.Scheme != "run" {
		t.Row("template", p.Template)
		t.Row("release branches", orAll(p.ReleaseBranches))
		t.Row("other branches", p.OtherBranches)
		if len(p.FloatingTags) > 0 {
			t.Row("floating tags", strings.Join(p.FloatingTags, ", "))
		}
	}
	t.Row("latest", orDash(p.Current))
	t.Row("next", orDash(p.Next))
	if p.ManagedBy != "" {
		t.Row("managed by", "pipeline "+p.ManagedBy+" (versioning: block)")
	}
	t.Print()
}

func orAll(v []string) string {
	if len(v) == 0 {
		return "all"
	}
	return strings.Join(v, ", ")
}

var versioningHistoryCmd = &cobra.Command{
	Use:   "history <pipeline>",
	Short: "List the versions a pipeline's images were given",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		c, ws, p, err := versioningPolicy(ctx, args[0])
		if err != nil {
			return err
		}
		out, err := c.VersionAllocations(ctx, ws, p.UID, versionHistorySize)
		if err != nil {
			return err
		}
		if structured() {
			return emit(out)
		}
		t := ui.NewTable("TAG", "STATUS", "RUN", "BRANCH", "AGE")
		for _, a := range out {
			run := "—"
			if a.RunNumber > 0 {
				run = fmt.Sprintf("#%d", a.RunNumber)
			}
			t.Row(a.Tag, a.Status, run, orDash(a.Branch), ui.Age(a.CreatedAt))
		}
		t.Print()
		return nil
	},
}

var versioningSetCmd = &cobra.Command{
	Use:   "set <pipeline> [flags]",
	Short: "Change a version policy's settings",
	Long: "Updates only the settings given as flags. A policy whose settings come from a\n" +
		"pipeline's versioning: block is changed in that file instead.",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		c, ws, p, err := versioningPolicy(ctx, args[0])
		if err != nil {
			return err
		}
		s := p.Settings()
		f := cmd.Flags()
		if f.Changed("scheme") {
			s.Scheme = versionSetScheme
		}
		if f.Changed("series") {
			s.Series = versionSetSeries
		}
		if f.Changed("bump") {
			s.Bump = versionSetBump
		}
		if f.Changed("start") {
			s.Start = versionSetStart
		}
		if f.Changed("template") {
			s.Template = versionSetTemplate
		}
		if f.Changed("release-branches") {
			s.Branches.Release = versionSetRelease
		}
		if f.Changed("other-branches") {
			s.Branches.Others = versionSetOthers
		}
		if f.Changed("floating-tags") {
			s.FloatingTags = versionSetFloating
		}
		if f.Changed("bump-from-commits") {
			s.BumpFromCommits = versionSetFromCommits
		}
		updated, err := c.UpdateVersionPolicy(ctx, ws, p.UID, s)
		if err != nil {
			return err
		}
		if structured() {
			return emit(updated)
		}
		ui.Success("Version policy updated; next build: %s", ui.Bold(orDash(updated.Next)))
		return nil
	},
}

var versioningSwitchCmd = &cobra.Command{
	Use:   "switch <pipeline> <series>",
	Short: "Release on another version line, e.g. 2.x.x",
	Long: "Moves a semver policy to another series. A new series starts at its first version;\n" +
		"switching back to an older one continues where it stopped, so a hotfix line never\n" +
		"repeats a version.",
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		c, ws, p, err := versioningPolicy(ctx, args[0])
		if err != nil {
			return err
		}
		updated, err := c.SwitchVersionSeries(ctx, ws, p.UID, args[1])
		if err != nil {
			return err
		}
		if structured() {
			return emit(updated)
		}
		ui.Success("Now releasing on %s; next build: %s", ui.Bold(updated.Series), ui.Bold(orDash(updated.Next)))
		return nil
	},
}
