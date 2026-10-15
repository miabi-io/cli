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

// MigrationIssue is a blocker or warning a migration plan raised.
type MigrationIssue struct {
	Code     string `json:"code"`
	Message  string `json:"message"`
	Resource string `json:"resource,omitempty"`
}

// MigrationPlan is POST /apps/{id}/migrations/plan: what moving an app would do.
type MigrationPlan struct {
	Location      string `json:"location"`
	LocationLabel string `json:"location_label"`
	Volumes       []struct {
		VolumeID     uint   `json:"volume_id"`
		Name         string `json:"name"`
		Action       string `json:"action"`
		UsedBytes    int64  `json:"used_bytes"`
		StorageClass string `json:"storage_class"`
	} `json:"volumes"`
	Databases []MigrationDBItem `json:"databases"`
	Blockers  []MigrationIssue  `json:"blockers"`
	Warnings  []MigrationIssue  `json:"warnings"`
	CopyBytes int64             `json:"copy_bytes"`
}

// MigrationDBItem is one database instance in a plan.
type MigrationDBItem struct {
	InstanceID   uint     `json:"instance_id"`
	InstanceName string   `json:"instance_name"`
	Engine       string   `json:"engine"`
	Version      string   `json:"version"`
	Exclusive    bool     `json:"exclusive"`
	Strategies   []string `json:"strategies"`
	Strategy     string   `json:"strategy"`
}

// MigrationDBChoice picks a strategy for one instance.
type MigrationDBChoice struct {
	InstanceID       uint   `json:"instance_id"`
	Strategy         string `json:"strategy"`
	TargetInstanceID uint   `json:"target_instance_id,omitempty"`
}

// StartMigration is the body of POST /apps/{id}/migrations.
type StartMigration struct {
	Location      string              `json:"location"`
	Databases     []MigrationDBChoice `json:"databases,omitempty"`
	CutoverMode   string              `json:"cutover_mode,omitempty"`
	BandwidthKBps int                 `json:"bandwidth_kbps,omitempty"`
}

// Migration is one location migration.
type Migration struct {
	ID       uint          `json:"id"`
	AppName  string        `json:"app_name"`
	Status   string        `json:"status"`
	Phase    string        `json:"phase"`
	Plan     MigrationPlan `json:"plan"`
	Progress struct {
		Items []struct {
			Kind   string `json:"kind"`
			Name   string `json:"name"`
			Status string `json:"status"`
			Bytes  int64  `json:"bytes"`
			Total  int64  `json:"total"`
		} `json:"items"`
		EstimatedDowntimeSeconds int    `json:"estimated_downtime_seconds"`
		Message                  string `json:"message"`
	} `json:"progress"`
	Report struct {
		Notes []string `json:"notes"`
	} `json:"report"`
	Error         string     `json:"error"`
	FinalizeAfter *time.Time `json:"finalize_after"`
	CreatedAt     time.Time  `json:"created_at"`
}

// MigrationSettled reports whether a migration has stopped moving on its own: it cut over, waits for a
// person, or ended.
func MigrationSettled(status string) bool { return status != "running" }

func (c *Client) PlanMigration(ctx context.Context, ws string, app string, body StartMigration) (*MigrationPlan, error) {
	var p MigrationPlan
	return &p, c.post(ctx, fmt.Sprintf("/api/v1/workspaces/%s/apps/%s/migrations/plan", ws, url.PathEscape(app)), body, &p)
}

func (c *Client) StartMigration(ctx context.Context, ws string, app string, body StartMigration) (*Migration, error) {
	var m Migration
	return &m, c.post(ctx, fmt.Sprintf("/api/v1/workspaces/%s/apps/%s/migrations", ws, url.PathEscape(app)), body, &m)
}

func (c *Client) AppMigrations(ctx context.Context, ws string, app string) ([]Migration, error) {
	var out []Migration
	return out, c.get(ctx, fmt.Sprintf("/api/v1/workspaces/%s/apps/%s/migrations", ws, url.PathEscape(app)), &out)
}

func (c *Client) Migrations(ctx context.Context, ws string) ([]Migration, error) {
	var out []Migration
	return out, c.get(ctx, fmt.Sprintf("/api/v1/workspaces/%s/migrations", ws), &out)
}

func (c *Client) Migration(ctx context.Context, ws string, id uint) (*Migration, error) {
	var m Migration
	return &m, c.get(ctx, fmt.Sprintf("/api/v1/workspaces/%s/migrations/%d", ws, id), &m)
}

// MigrationAction runs cutover, cancel, rollback or finalize.
func (c *Client) MigrationAction(ctx context.Context, ws string, id uint, action string) (*Migration, error) {
	var m Migration
	return &m, c.post(ctx, fmt.Sprintf("/api/v1/workspaces/%s/migrations/%d/%s", ws, id, action), nil, &m)
}

// WaitForMigration polls until the migration settles, reporting each phase change.
func (c *Client) WaitForMigration(ctx context.Context, ws string, id uint, onPhase func(*Migration)) (*Migration, error) {
	last := ""
	for {
		m, err := c.Migration(ctx, ws, id)
		if err != nil {
			return nil, err
		}
		if key := m.Status + "/" + m.Phase; key != last {
			last = key
			if onPhase != nil {
				onPhase(m)
			}
		}
		if MigrationSettled(m.Status) {
			return m, nil
		}
		select {
		case <-ctx.Done():
			return m, ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}
