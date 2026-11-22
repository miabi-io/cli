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

package api

import (
	"context"
	"fmt"
	"net/url"
	"time"
)

type RegistryRepository struct {
	Name     string   `json:"name"`
	TagCount int      `json:"tag_count"`
	Tags     []string `json:"tags"`
}

type RegistryTag struct {
	Name      string     `json:"name"`
	Digest    string     `json:"digest,omitempty"`
	SizeBytes int64      `json:"size_bytes,omitempty"`
	InUse     bool       `json:"in_use"`
	BuiltAt   *time.Time `json:"built_at,omitempty"`
	Commit    string     `json:"commit,omitempty"`
}

type RegistryRepoUsage struct {
	Name           string `json:"name"`
	Bytes          int64  `json:"bytes"`
	ExclusiveBytes int64  `json:"exclusive_bytes"`
	Tags           int    `json:"tags"`
}

type RegistryUsage struct {
	TotalBytes   int64               `json:"total_bytes"`
	QuotaBytes   int64               `json:"quota_bytes"`
	Repositories []RegistryRepoUsage `json:"repositories"`
	ComputedAt   *time.Time          `json:"computed_at,omitempty"`
	Stale        bool                `json:"stale"`
}

type TagOutcome struct {
	Tag      string   `json:"tag"`
	Result   string   `json:"result"`
	Siblings []string `json:"siblings,omitempty"`
	Error    string   `json:"error,omitempty"`
}

type TagDeleteResult struct {
	DryRun     bool         `json:"dry_run"`
	Tags       []TagOutcome `json:"tags"`
	Removed    []string     `json:"removed"`
	FreedBytes int64        `json:"freed_bytes"`
}

type TagSelection struct {
	Tags       []string `json:"tags"`
	Images     int      `json:"images"`
	Matched    int      `json:"matched"`
	Truncated  bool     `json:"truncated"`
	InUse      []string `json:"in_use"`
	UnknownAge []string `json:"unknown_age"`
}

func (c *Client) RegistryRepositories(ctx context.Context, ws string) ([]RegistryRepository, error) {
	var out []RegistryRepository
	return out, c.get(ctx, fmt.Sprintf("/api/v1/workspaces/%s/registry/repositories?page=0&size=500&tag_limit=0", ws), &out)
}

// RegistryTags pages through every tag; a repository with thousands of builds still lists in a few requests.
func (c *Client) RegistryTags(ctx context.Context, ws, repo string) ([]RegistryTag, error) {
	var all []RegistryTag
	for page := 0; ; page++ {
		var batch []RegistryTag
		path := fmt.Sprintf("/api/v1/workspaces/%s/registry/repository/tags?name=%s&page=%d&size=100", ws, url.QueryEscape(repo), page)
		if err := c.get(ctx, path, &batch); err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if len(batch) < 100 {
			return all, nil
		}
	}
}

func (c *Client) RegistryUsage(ctx context.Context, ws string) (*RegistryUsage, error) {
	var out RegistryUsage
	return &out, c.get(ctx, fmt.Sprintf("/api/v1/workspaces/%s/registry/usage", ws), &out)
}

func (c *Client) DeleteRegistryTags(ctx context.Context, ws, repo string, tags []string, dryRun bool) (*TagDeleteResult, error) {
	var out TagDeleteResult
	body := map[string]any{"repository": repo, "tags": tags, "dry_run": dryRun}
	return &out, c.post(ctx, fmt.Sprintf("/api/v1/workspaces/%s/registry/repository/tags/delete", ws), body, &out)
}

// SelectRegistryTags picks by rule and deletes nothing.
func (c *Client) SelectRegistryTags(ctx context.Context, ws, repo string, keepNewest, olderThanDays int) (*TagSelection, error) {
	var out TagSelection
	body := map[string]any{"repository": repo, "keep_newest": keepNewest, "older_than_days": olderThanDays}
	return &out, c.post(ctx, fmt.Sprintf("/api/v1/workspaces/%s/registry/repository/tags/select", ws), body, &out)
}

func (c *Client) DeleteRegistryRepository(ctx context.Context, ws, repo string) (*TagDeleteResult, error) {
	var out TagDeleteResult
	return &out, c.del(ctx, fmt.Sprintf("/api/v1/workspaces/%s/registry/repository?name=%s", ws, url.QueryEscape(repo)), &out)
}
