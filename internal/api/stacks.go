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
	"strconv"
	"time"
)

type Stack struct {
	ID          uint      `json:"id"`
	UID         string    `json:"uid,omitempty"`
	Name        string    `json:"name"`
	DisplayName string    `json:"display_name"`
	Description string    `json:"description,omitempty"`
	ClusterID   uint      `json:"cluster_id"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	Apps        []App     `json:"apps,omitempty"`
}

type StackStatusCounts struct {
	Total   int `json:"total"`
	Running int `json:"running"`
	Stopped int `json:"stopped"`
	Failed  int `json:"failed"`
	Other   int `json:"other"`
}

type StackListItem struct {
	Stack
	AppCount int64             `json:"app_count"`
	Status   StackStatusCounts `json:"status"`
}

type ImportStackRequest struct {
	Name    string `json:"name"`
	Compose string `json:"compose"`
	// Env is the .env content: it fills ${VAR} references and is what env_file: .env loads.
	Env      string `json:"env,omitempty"`
	Location string `json:"location,omitempty"`
}

type StackImportSkip struct {
	Service string `json:"service"`
	Reason  string `json:"reason"`
}

type StackPortConflict struct {
	Service  string `json:"service"`
	HostPort int    `json:"host_port"`
	Protocol string `json:"protocol"`
	UsedBy   string `json:"used_by"`
}

type StackImportResult struct {
	Stack         *Stack              `json:"stack"`
	Created       []string            `json:"created"`
	Volumes       []string            `json:"volumes"`
	PortRequests  int                 `json:"port_requests"`
	PortConflicts []StackPortConflict `json:"port_conflicts"`
	Skipped       []StackImportSkip   `json:"skipped"`
	Warnings      []string            `json:"warnings"`
}

// StackDeployResult is one member app's outcome; Status is queued or failed.
type StackDeployResult struct {
	AppID        uint   `json:"app_id"`
	AppName      string `json:"app_name"`
	Status       string `json:"status"`
	DeploymentID uint   `json:"deployment_id,omitempty"`
	Error        string `json:"error,omitempty"`
}

func (c *Client) Stacks(ctx context.Context, ws string) ([]StackListItem, error) {
	var out []StackListItem
	return out, c.get(ctx, fmt.Sprintf("/api/v1/workspaces/%s/stacks", ws), &out)
}

// Stack returns one stack with its member apps; the API addresses stacks by id or uid, not name.
func (c *Client) Stack(ctx context.Context, ws string, id uint) (*Stack, error) {
	var st Stack
	return &st, c.get(ctx, fmt.Sprintf("/api/v1/workspaces/%s/stacks/%d", ws, id), &st)
}

func (c *Client) ImportStack(ctx context.Context, ws string, req ImportStackRequest) (*StackImportResult, error) {
	var res StackImportResult
	return &res, c.postLong(ctx, fmt.Sprintf("/api/v1/workspaces/%s/stacks/import", ws), req, &res, 5*time.Minute)
}

func (c *Client) DeployStack(ctx context.Context, ws string, id uint, outdatedOnly bool) ([]StackDeployResult, error) {
	path := "deploy"
	if outdatedOnly {
		path = "deploy-outdated"
	}
	var out []StackDeployResult
	return out, c.post(ctx, fmt.Sprintf("/api/v1/workspaces/%s/stacks/%d/%s", ws, id, path), nil, &out)
}

func (c *Client) ResolveStackID(ctx context.Context, ws, ref string) (uint, error) {
	if id, err := strconv.ParseUint(ref, 10, 64); err == nil {
		return uint(id), nil
	}
	stacks, err := c.Stacks(ctx, ws)
	if err != nil {
		return 0, err
	}
	for _, st := range stacks {
		if st.Name == ref || st.UID == ref {
			return st.ID, nil
		}
	}
	return 0, fmt.Errorf("stack %q not found", ref)
}
