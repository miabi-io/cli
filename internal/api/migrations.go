package api

import (
	"context"
	"fmt"
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

func (c *Client) PlanMigration(ctx context.Context, ws string, appID uint, body StartMigration) (*MigrationPlan, error) {
	var p MigrationPlan
	return &p, c.post(ctx, fmt.Sprintf("/api/v1/workspaces/%s/apps/%d/migrations/plan", ws, appID), body, &p)
}

func (c *Client) StartMigration(ctx context.Context, ws string, appID uint, body StartMigration) (*Migration, error) {
	var m Migration
	return &m, c.post(ctx, fmt.Sprintf("/api/v1/workspaces/%s/apps/%d/migrations", ws, appID), body, &m)
}

func (c *Client) AppMigrations(ctx context.Context, ws string, appID uint) ([]Migration, error) {
	var out []Migration
	return out, c.get(ctx, fmt.Sprintf("/api/v1/workspaces/%s/apps/%d/migrations", ws, appID), &out)
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
