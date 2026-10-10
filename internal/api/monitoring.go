package api

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"time"
)

// WorkspaceOverview returns app health and resource counts for a workspace.
func (c *Client) WorkspaceOverview(ctx context.Context, ws string) (*WorkspaceOverview, error) {
	var o WorkspaceOverview
	return &o, c.get(ctx, fmt.Sprintf("/api/v1/workspaces/%s/overview", ws), &o)
}

// WorkspaceUsage returns resource usage against the plan's limits. It is passed
// through as-is: the shape grows with every new plan limit.
func (c *Client) WorkspaceUsage(ctx context.Context, ws string) (map[string]any, error) {
	var u map[string]any
	return u, c.get(ctx, fmt.Sprintf("/api/v1/workspaces/%s/usage", ws), &u)
}

// Alerts lists a workspace's alerts; activeOnly keeps the firing and
// acknowledged ones.
func (c *Client) Alerts(ctx context.Context, ws string, activeOnly bool) ([]Alert, error) {
	path := fmt.Sprintf("/api/v1/workspaces/%s/alerts", ws)
	if activeOnly {
		path += "?active=true"
	}
	var as []Alert
	return as, c.get(ctx, path, &as)
}

// AckAlert acknowledges an alert: it stays active but stops re-notifying.
func (c *Client) AckAlert(ctx context.Context, ws string, id uint) (*Alert, error) {
	var a Alert
	return &a, c.post(ctx, fmt.Sprintf("/api/v1/workspaces/%s/alerts/%d/ack", ws, id), nil, &a)
}

// ResolveAlert closes an alert.
func (c *Client) ResolveAlert(ctx context.Context, ws string, id uint) (*Alert, error) {
	var a Alert
	return &a, c.post(ctx, fmt.Sprintf("/api/v1/workspaces/%s/alerts/%d/resolve", ws, id), nil, &a)
}

// MaxEventPage is the largest page the workspace events feed serves.
const MaxEventPage = 100

// WorkspaceEvents returns the newest events across the workspace. Pages are
// zero-based on the server.
func (c *Client) WorkspaceEvents(ctx context.Context, ws string, limit int) ([]Event, error) {
	limit = min(max(limit, 1), MaxEventPage)
	var es []Event
	return es, c.get(ctx, fmt.Sprintf("/api/v1/workspaces/%s/events?page=0&size=%d&order=desc", ws, limit), &es)
}

// AppEvents returns an application's newest events.
func (c *Client) AppEvents(ctx context.Context, ws string, app string, limit int) ([]Event, error) {
	path := fmt.Sprintf("/api/v1/workspaces/%s/apps/%s/events", ws, url.PathEscape(app))
	if limit > 0 {
		path += "?limit=" + strconv.Itoa(limit)
	}
	var es []Event
	return es, c.get(ctx, path, &es)
}

// TrafficSummary returns HTTP traffic totals for one app over rng (15m, 1h,
// 24h, 7d…; empty means the server default of 24h).
func (c *Client) TrafficSummary(ctx context.Context, ws string, appID uint, rng string) (*TrafficSummary, error) {
	q := url.Values{"app": {strconv.FormatUint(uint64(appID), 10)}}
	if rng != "" {
		q.Set("range", rng)
	}
	var t TrafficSummary
	return &t, c.get(ctx, fmt.Sprintf("/api/v1/workspaces/%s/analytics/summary?%s", ws, q.Encode()), &t)
}

// BackupRunTimeout bounds a manual database backup, which the server runs
// inline in the request.
const BackupRunTimeout = 30 * time.Minute

// Backups lists a logical database's backup history.
func (c *Client) Backups(ctx context.Context, ws string, id, dbID uint) ([]Backup, error) {
	var bs []Backup
	return bs, c.get(ctx, fmt.Sprintf("/api/v1/workspaces/%s/databases/%d/databases/%d/backups", ws, id, dbID), &bs)
}

// RunBackup takes a manual backup to the workspace's configured destination and
// returns the finished record; its status says whether it succeeded.
func (c *Client) RunBackup(ctx context.Context, ws string, id, dbID uint, comment string) (*Backup, error) {
	var b Backup
	body := map[string]string{"comment": comment}
	return &b, c.postLong(ctx, fmt.Sprintf("/api/v1/workspaces/%s/databases/%d/databases/%d/backups", ws, id, dbID), body, &b, BackupRunTimeout)
}

// ResolveLogicalDatabaseID picks a database hosted on instance id by name or
// numeric id. An empty ref selects the instance's only database.
func (c *Client) ResolveLogicalDatabaseID(ctx context.Context, ws string, id uint, ref string) (uint, error) {
	dbs, err := c.LogicalDatabases(ctx, ws, id)
	if err != nil {
		return 0, err
	}
	if ref == "" {
		switch len(dbs) {
		case 1:
			return dbs[0].ID, nil
		case 0:
			return 0, fmt.Errorf("the instance hosts no databases")
		default:
			names := make([]string, len(dbs))
			for i, d := range dbs {
				names[i] = d.Name
			}
			return 0, fmt.Errorf("the instance hosts %d databases %v; name one", len(dbs), names)
		}
	}
	n, _ := strconv.ParseUint(ref, 10, 64)
	for _, d := range dbs {
		if d.Name == ref || (n != 0 && d.ID == uint(n)) {
			return d.ID, nil
		}
	}
	return 0, fmt.Errorf("database %q not found on this instance", ref)
}
