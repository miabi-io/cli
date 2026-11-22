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
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/miabi-io/cli/internal/api"
	"github.com/miabi-io/cli/internal/ui"
	"github.com/spf13/cobra"
)

var (
	regKeep      int
	regOlderThan string
	regDryRun    bool
	regYes       bool
)

func init() {
	regTagsRmCmd.Flags().IntVar(&regKeep, "keep", 0, "keep the newest N images and delete the rest")
	regTagsRmCmd.Flags().StringVar(&regOlderThan, "older-than", "", "delete images older than this, e.g. 30d or 2w")
	regTagsRmCmd.Flags().BoolVar(&regDryRun, "dry-run", false, "show what would be deleted, delete nothing")
	regTagsRmCmd.Flags().BoolVarP(&regYes, "yes", "y", false, "skip the confirmation prompt")
	regRmCmd.Flags().BoolVarP(&regYes, "yes", "y", false, "skip the confirmation prompt")

	regTagsCmd.AddCommand(regTagsLsCmd, regTagsRmCmd)
	registryCmd.AddCommand(regLsCmd, regUsageCmd, regTagsCmd, regRmCmd)
	rootCmd.AddCommand(registryCmd)
}

var registryCmd = &cobra.Command{
	Use:   "registry",
	Short: "Browse and clean the workspace's container registry",
	Long: "List repositories and tags in the workspace's built-in registry, see the storage they\n" +
		"use, and delete tags. A registry deletes images, not tags: every tag on the same image\n" +
		"goes with it, so each delete is previewed first and names those tags.",
	Example: "  miabi registry ls\n" +
		"  miabi registry usage\n" +
		"  miabi registry tags ls api\n" +
		"  miabi registry tags rm api v1.2.0 v1.3.0\n" +
		"  miabi registry tags rm api --keep 5\n" +
		"  miabi registry tags rm api --older-than 30d --dry-run\n" +
		"  miabi registry rm old-api",
}

var regLsCmd = &cobra.Command{
	Use:     "ls",
	Aliases: []string{"list"},
	Short:   "List repositories with their tag count and size",
	RunE: func(_ *cobra.Command, _ []string) error {
		ctx, c, ws, err := configContext()
		if err != nil {
			return err
		}
		repos, err := c.RegistryRepositories(ctx, ws)
		if err != nil {
			return err
		}
		if structured() {
			return emit(repos)
		}
		if len(repos) == 0 {
			ui.Info("No images in this workspace's registry yet.")
			return nil
		}
		sizes := map[string]int64{}
		if u, err := c.RegistryUsage(ctx, ws); err == nil {
			for _, r := range u.Repositories {
				sizes[r.Name] = r.Bytes
			}
		}
		t := ui.NewTable("REPOSITORY", "TAGS", "SIZE")
		for _, r := range repos {
			t.Row(r.Name, strconv.Itoa(r.TagCount), sizeOrDash(sizes[r.Name]))
		}
		t.Print()
		return nil
	},
}

var regUsageCmd = &cobra.Command{
	Use:   "usage",
	Short: "Show the storage the workspace's registry uses",
	RunE: func(_ *cobra.Command, _ []string) error {
		ctx, c, ws, err := configContext()
		if err != nil {
			return err
		}
		u, err := c.RegistryUsage(ctx, ws)
		if err != nil {
			return err
		}
		if structured() {
			return emit(u)
		}
		used := humanBytes(u.TotalBytes)
		if u.QuotaBytes > 0 {
			fmt.Printf("%s %s of %s (%d%%)\n", ui.Bold("Used"), used, humanBytes(u.QuotaBytes), u.TotalBytes*100/u.QuotaBytes)
		} else {
			fmt.Printf("%s %s %s\n", ui.Bold("Used"), used, ui.Dim("(no quota)"))
		}
		if u.Stale {
			ui.Detail("Being recounted after a recent push or delete.")
		}
		if len(u.Repositories) > 0 {
			fmt.Println()
			t := ui.NewTable("REPOSITORY", "TAGS", "SIZE", "FREED IF DELETED")
			for _, r := range u.Repositories {
				t.Row(r.Name, strconv.Itoa(r.Tags), humanBytes(r.Bytes), sizeOrDash(r.ExclusiveBytes))
			}
			t.Print()
		}
		ui.Detail("Deleted tags free their space at the next nightly cleanup.")
		return nil
	},
}

var regTagsCmd = &cobra.Command{
	Use:   "tags",
	Short: "List or delete a repository's tags",
}

var regTagsLsCmd = &cobra.Command{
	Use:     "ls <repository>",
	Aliases: []string{"list"},
	Short:   "List a repository's tags, newest first",
	Args:    cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		ctx, c, ws, err := configContext()
		if err != nil {
			return err
		}
		tags, err := c.RegistryTags(ctx, ws, args[0])
		if err != nil {
			return err
		}
		if structured() {
			return emit(tags)
		}
		if len(tags) == 0 {
			ui.Info("No tags in %s.", args[0])
			return nil
		}
		t := ui.NewTable("TAG", "DIGEST", "SIZE", "BUILT", "IN USE")
		for _, tg := range tags {
			built, inUse := "-", ""
			if tg.BuiltAt != nil {
				built = ui.Age(*tg.BuiltAt)
			}
			if tg.InUse {
				inUse = "yes"
			}
			t.Row(tg.Name, shortRegistryDigest(tg.Digest), sizeOrDash(tg.SizeBytes), built, inUse)
		}
		t.Print()
		return nil
	},
}

var regTagsRmCmd = &cobra.Command{
	Use:   "rm <repository> [tag...]",
	Short: "Delete tags by name, or pick them with --keep / --older-than",
	Long: "Delete tags from a repository. Name them, or let a rule pick them: --keep N keeps the\n" +
		"newest N images, --older-than picks images older than a duration; both together must\n" +
		"both hold. Images in use and images with no known build time are never picked.\n\n" +
		"Every tag on the same image is deleted with it; the preview names those tags.",
	Args: cobra.MinimumNArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		repo, named := args[0], args[1:]
		byRule := regKeep > 0 || regOlderThan != ""
		switch {
		case byRule && len(named) > 0:
			return errors.New("name tags or use --keep / --older-than, not both")
		case !byRule && len(named) == 0:
			return errors.New("name at least one tag, or use --keep / --older-than")
		}
		ctx, c, ws, err := configContext()
		if err != nil {
			return err
		}
		tags := named
		if byRule {
			if tags, err = selectByRule(ctx, c, ws, repo); err != nil || len(tags) == 0 {
				return err
			}
		}
		preview, err := c.DeleteRegistryTags(ctx, ws, repo, tags, true)
		if err != nil {
			return err
		}
		deletable := deletableTags(preview)
		if structured() && (regDryRun || len(deletable) == 0) {
			return emit(preview)
		}
		if !structured() {
			printTagPreview(repo, preview, deletable)
		}
		if len(deletable) == 0 || regDryRun {
			return nil
		}
		if !regYes && !structured() && !ui.Confirm(fmt.Sprintf("Delete %d tag(s) from %s?", len(preview.Removed), repo)) {
			ui.Info("Aborted")
			return nil
		}
		res, err := c.DeleteRegistryTags(ctx, ws, repo, deletable, false)
		if err != nil {
			return err
		}
		if structured() {
			return emit(res)
		}
		ui.Success("Deleted %d tag(s) from %s%s", len(res.Removed), ui.Bold(repo), ui.Dim(freedNote(res.FreedBytes)))
		for _, o := range res.Tags {
			if o.Result != "deleted" {
				ui.Warn("%s: %s", o.Tag, outcomeText(o))
			}
		}
		return nil
	},
}

func selectByRule(ctx context.Context, c *api.Client, ws, repo string) ([]string, error) {
	days := 0
	if regOlderThan != "" {
		d, err := parseDays(regOlderThan)
		if err != nil {
			return nil, err
		}
		days = d
	}
	sel, err := c.SelectRegistryTags(ctx, ws, repo, regKeep, days)
	if err != nil {
		return nil, err
	}
	if !structured() {
		if len(sel.InUse) > 0 {
			ui.Detail("Left alone, in use: %s", strings.Join(sel.InUse, ", "))
		}
		if len(sel.UnknownAge) > 0 {
			ui.Detail("Left alone, no known build time: %s", strings.Join(sel.UnknownAge, ", "))
		}
		if sel.Truncated {
			ui.Detail("%d tags matched; this run takes the oldest %d. Run it again for the rest.", sel.Matched, len(sel.Tags))
		}
		if len(sel.Tags) == 0 {
			ui.Info("Nothing in %s matches the rule.", repo)
		}
	}
	return sel.Tags, nil
}

// parseDays accepts 30, 30d, 2w; anything finer than a day is no use for image age.
func parseDays(s string) (int, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	mult := 1
	switch {
	case strings.HasSuffix(s, "w"):
		mult, s = 7, strings.TrimSuffix(s, "w")
	case strings.HasSuffix(s, "d"):
		s = strings.TrimSuffix(s, "d")
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("--older-than wants a number of days or weeks, such as 30d or 2w")
	}
	return n * mult, nil
}

func deletableTags(res *api.TagDeleteResult) []string {
	var out []string
	for _, o := range res.Tags {
		if o.Result == "would_delete" {
			out = append(out, o.Tag)
		}
	}
	return out
}

func printTagPreview(repo string, res *api.TagDeleteResult, deletable []string) {
	if len(deletable) > 0 {
		fmt.Printf("%s %s: %s\n", ui.Bold("Would delete from"), repo, strings.Join(deletable, ", "))
	}
	named := map[string]bool{}
	for _, t := range deletable {
		named[t] = true
	}
	var extra []string
	for _, t := range res.Removed {
		if !named[t] {
			extra = append(extra, t)
		}
	}
	if len(extra) > 0 {
		ui.Warn("Same image, deleted too: %s", strings.Join(extra, ", "))
	}
	for _, o := range res.Tags {
		if o.Result != "would_delete" {
			ui.Detail("Left alone: %s (%s)", o.Tag, outcomeText(o))
		}
	}
	switch {
	case res.FreedBytes > 0:
		ui.Detail("Frees about %s at the next nightly cleanup.", humanBytes(res.FreedBytes))
	case len(deletable) > 0:
		ui.Detail("Frees no space: other images in the workspace still use these layers.")
	}
	if len(deletable) == 0 {
		ui.Info("Nothing to delete.")
	}
}

func outcomeText(o api.TagOutcome) string {
	switch o.Result {
	case "in_use":
		return "in use by a live deployment or a pinned release"
	case "not_found":
		return "not found"
	case "failed":
		return "failed: " + o.Error
	}
	return o.Result
}

var regRmCmd = &cobra.Command{
	Use:   "rm <repository>",
	Short: "Delete a whole repository (workspace admin)",
	Long:  "Delete every tag in a repository. Refused, deleting nothing, if any of its images is in use.",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		repo := args[0]
		ctx, c, ws, err := configContext()
		if err != nil {
			return err
		}
		if !regYes && !structured() && !ui.Confirm(fmt.Sprintf("Delete every tag in %s? This cannot be undone.", ui.Bold(repo))) {
			ui.Info("Aborted")
			return nil
		}
		res, err := c.DeleteRegistryRepository(ctx, ws, repo)
		if err != nil {
			return err
		}
		if structured() {
			return emit(res)
		}
		ui.Success("Deleted %s %s", ui.Bold(repo), ui.Dim(fmt.Sprintf("(%d tag(s))", len(res.Removed))+freedNote(res.FreedBytes)))
		return nil
	},
}

// Nothing freed means other images still use the layers, which is not worth a number.
func freedNote(n int64) string {
	if n <= 0 {
		return ""
	}
	return fmt.Sprintf(" (about %s freed at the next cleanup)", humanBytes(n))
}

func sizeOrDash(n int64) string {
	if n <= 0 {
		return "-"
	}
	return humanBytes(n)
}

func shortRegistryDigest(d string) string {
	d = strings.TrimPrefix(d, "sha256:")
	if len(d) > 12 {
		return d[:12]
	}
	if d == "" {
		return "-"
	}
	return d
}
