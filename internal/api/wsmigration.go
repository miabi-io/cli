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
	"time"
)

// MigrationImportTimeout bounds POST /migrations and /migrations/probe, which reach the source before
// they answer.
const MigrationImportTimeout = 2 * time.Minute

// MigrationStartRequest is the body of POST .../migration/start.
type MigrationStartRequest struct {
	ExpiresInHours int `json:"expires_in_hours,omitempty"`
}

// MigrationStarted is an open migration as started; Code is shown only once.
type MigrationStarted struct {
	Code      string    `json:"code"`
	SourceURL string    `json:"source_url,omitempty"`
	Workspace string    `json:"workspace,omitempty"`
	ExpiresAt time.Time `json:"expires_at"`
}

// MigrationWindow is an open migration: while it lasts, its code lets another Miabi pull.
type MigrationWindow struct {
	StartedAt  time.Time  `json:"started_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
}

// MigrationExport is one pull of a workspace by another Miabi, as the source lists it.
type MigrationExport struct {
	ID         uint      `json:"id"`
	Workspace  string    `json:"workspace"`
	Status     string    `json:"status"`
	Phase      string    `json:"phase,omitempty"`
	Error      string    `json:"error,omitempty"`
	Freeze     bool      `json:"freeze"`
	Data       bool      `json:"data"`
	Completed  bool      `json:"completed"`
	ImportedBy string    `json:"imported_by,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// MigrationOverview is a workspace's migration state on the source, or the install's for a platform admin.
type MigrationOverview struct {
	Enabled      bool              `json:"enabled"`
	Window       *MigrationWindow  `json:"window"`
	Frozen       bool              `json:"frozen"`
	FrozenReason string            `json:"frozen_reason,omitempty"`
	Exports      []MigrationExport `json:"exports"`
	URL          string            `json:"url,omitempty"`
}

// MigrationStatus is GET /migrations/status: whether MIABI_MIGRATION_ENABLED is set on a server.
type MigrationStatus struct {
	Enabled bool   `json:"enabled"`
	URL     string `json:"url,omitempty"`
}

// MigrationSource is a migration code and how the target trusts the source it names. CABundle is PEM
// text, not a path.
type MigrationSource struct {
	Code        string `json:"code"`
	InsecureTLS bool   `json:"insecure_skip_tls_verify"`
	CABundle    string `json:"ca_bundle,omitempty"`
}

// MigrationWorkspace is one workspace a migration code opens, with what moving it involves.
type MigrationWorkspace struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name,omitempty"`
	Frozen      bool   `json:"frozen,omitempty"`
	Apps        int    `json:"apps"`
	Databases   int    `json:"databases"`
	Volumes     int    `json:"volumes"`
	SizeBytes   int64  `json:"size_bytes"`
}

// MigrationProbe is what POST /migrations/probe learns from the source a code names.
type MigrationProbe struct {
	InstallID string `json:"install_id"`
	Version   string `json:"version"`
	Schema    int    `json:"schema"`
	// CodeWorkspace is the workspace a workspace code was started for; empty for an instance code.
	CodeWorkspace string               `json:"code_workspace,omitempty"`
	Workspaces    []MigrationWorkspace `json:"workspaces"`
}

// MigrationImport is the body of POST /migrations.
type MigrationImport struct {
	MigrationSource
	SourceWorkspace string `json:"source_workspace,omitempty"`
	NewWorkspace    string `json:"new_workspace,omitempty"`
	RestoreData     bool   `json:"restore_data"`
	DeployApps      bool   `json:"deploy_apps"`
	Freeze          bool   `json:"freeze"`
	Rehearse        bool   `json:"rehearse"`
	RetireSource    bool   `json:"retire_source"`
}

// MigrationReportItem is one resource's outcome in a migration report.
type MigrationReportItem struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Action string `json:"action"`
	Detail string `json:"detail,omitempty"`
}

// MigrationRun is one workspace migration into a server, as GET /migrations/{id} returns it.
type MigrationRun struct {
	ID                uint   `json:"id"`
	WorkspaceID       uint   `json:"workspace_id"`
	TargetWorkspaceID uint   `json:"target_workspace_id,omitempty"`
	Ref               string `json:"ref,omitempty"`
	Status            string `json:"status"`
	Phase             string `json:"phase,omitempty"`
	SourceWorkspace   string `json:"source_workspace,omitempty"`
	RestoreData       bool   `json:"restore_data"`
	DeployApps        bool   `json:"deploy_apps"`
	Artifacts         int    `json:"artifacts"`
	SizeBytes         int64  `json:"size_bytes"`
	Report            struct {
		Items []MigrationReportItem `json:"items,omitempty"`
		Notes []string              `json:"notes,omitempty"`
	} `json:"report"`
	Migration *struct {
		SourceURL      string `json:"source_url,omitempty"`
		SourceVersion  string `json:"source_version,omitempty"`
		Freeze         bool   `json:"freeze,omitempty"`
		Rehearse       bool   `json:"rehearse,omitempty"`
		RetireSource   bool   `json:"retire_source,omitempty"`
		SourceRetired  bool   `json:"source_retired,omitempty"`
		SourceUnfrozen bool   `json:"source_unfrozen,omitempty"`
		BatchID        string `json:"batch_id,omitempty"`
	} `json:"migration,omitempty"`
	Error      string     `json:"error,omitempty"`
	StartedAt  *time.Time `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
	CreatedAt  time.Time  `json:"created_at"`
}

// MigrationRunDone reports whether a workspace migration has ended.
func MigrationRunDone(status string) bool {
	return status == "completed" || status == "partial" || status == "failed"
}

func migrationPath(ws string) string {
	if ws == "" {
		return "/api/v1/admin/migration"
	}
	return fmt.Sprintf("/api/v1/workspaces/%s/migration", ws)
}

// StartWorkspaceMigration opens a migration of ws and returns its code; ws "" opens the install (platform admin).
// Starting again replaces the open migration's code.
func (c *Client) StartWorkspaceMigration(ctx context.Context, ws string, body MigrationStartRequest) (*MigrationStarted, error) {
	var m MigrationStarted
	return &m, c.post(ctx, migrationPath(ws)+"/start", body, &m)
}

// WorkspaceMigration returns ws's migration state on the source; ws "" returns the install's.
func (c *Client) WorkspaceMigration(ctx context.Context, ws string) (*MigrationOverview, error) {
	var o MigrationOverview
	return &o, c.get(ctx, migrationPath(ws), &o)
}

// CancelWorkspaceMigration closes ws's open migration, so its code stops working; ws "" closes the install's.
func (c *Client) CancelWorkspaceMigration(ctx context.Context, ws string) error {
	return c.del(ctx, migrationPath(ws), nil)
}

// UnfreezeWorkspace lifts the freeze a migration put on ws, rolling the migration back on the source.
func (c *Client) UnfreezeWorkspace(ctx context.Context, ws string) error {
	return c.post(ctx, fmt.Sprintf("/api/v1/workspaces/%s/migration/unfreeze", ws), nil, nil)
}

func (c *Client) MigrationStatus(ctx context.Context) (*MigrationStatus, error) {
	var s MigrationStatus
	return &s, c.get(ctx, "/api/v1/migrations/status", &s)
}

// ProbeMigration has this server check the source a code names and list the workspaces it opens.
func (c *Client) ProbeMigration(ctx context.Context, src MigrationSource) (*MigrationProbe, error) {
	var p MigrationProbe
	return &p, c.postLong(ctx, "/api/v1/migrations/probe", src, &p, MigrationImportTimeout)
}

// ImportWorkspace starts pulling a workspace from another Miabi into a new workspace on this server.
func (c *Client) ImportWorkspace(ctx context.Context, body MigrationImport) (*MigrationRun, error) {
	var r MigrationRun
	return &r, c.postLong(ctx, "/api/v1/migrations", body, &r, MigrationImportTimeout)
}

func (c *Client) MigrationRun(ctx context.Context, id uint) (*MigrationRun, error) {
	var r MigrationRun
	return &r, c.get(ctx, fmt.Sprintf("/api/v1/migrations/%d", id), &r)
}

// WaitForMigrationRun polls every interval until the run ends, reporting each status or phase change.
func (c *Client) WaitForMigrationRun(ctx context.Context, id uint, every time.Duration, onChange func(*MigrationRun)) (*MigrationRun, error) {
	last := ""
	for {
		r, err := c.MigrationRun(ctx, id)
		if err != nil {
			return nil, err
		}
		if key := r.Status + "/" + r.Phase; key != last {
			last = key
			if onChange != nil {
				onChange(r)
			}
		}
		if MigrationRunDone(r.Status) {
			return r, nil
		}
		select {
		case <-ctx.Done():
			return r, ctx.Err()
		case <-time.After(every):
		}
	}
}
