package mcp

import (
	"context"
	"math"

	"github.com/miabi-io/cli/internal/api"
)

var (
	databaseProp  = str("database instance handle or numeric id")
	logicalDBProp = str("database on the instance, by name or id (optional when the instance hosts exactly one)")
	alertProp     = map[string]any{"type": "integer", "description": "the alert id (from list_alerts)"}
)

// registerOpsTools installs the operational tools: health, alerts, events,
// traffic and database backups.
func (s *Server) registerOpsTools() {
	s.register(tool{
		def: toolDef{
			Name:        "workspace_overview",
			Description: "Summarize a workspace: every app's status and health, counts of running/failed apps, databases and stacks, recent events, and usage against plan limits.",
			InputSchema: object(map[string]any{"workspace": wsProp}),
		},
		readOnly: true,
		handler: func(ctx context.Context, s *Server, args map[string]any) (any, error) {
			ws, err := s.resolveWS(ctx, args)
			if err != nil {
				return nil, err
			}
			ov, err := s.client.WorkspaceOverview(ctx, ws)
			if err != nil {
				return nil, err
			}
			out := struct {
				*api.WorkspaceOverview
				Usage map[string]any `json:"usage,omitempty"`
			}{WorkspaceOverview: ov}
			// Usage is a nice-to-have; the overview alone still answers the question.
			if u, err := s.client.WorkspaceUsage(ctx, ws); err == nil {
				out.Usage = u
			}
			return out, nil
		},
	})
	s.register(tool{
		def: toolDef{
			Name:        "list_alerts",
			Description: "List a workspace's active alerts (firing or acknowledged). Set all=true to include resolved ones.",
			InputSchema: object(map[string]any{
				"workspace": wsProp,
				"all":       map[string]any{"type": "boolean", "description": "include resolved alerts (default false)"},
			}),
		},
		readOnly: true,
		handler: func(ctx context.Context, s *Server, args map[string]any) (any, error) {
			ws, err := s.resolveWS(ctx, args)
			if err != nil {
				return nil, err
			}
			return s.client.Alerts(ctx, ws, !optBool(args, "all"))
		},
	})
	s.register(tool{
		def: toolDef{
			Name:        "list_events",
			Description: "List recent events, newest first: across the workspace, or for one application when app is given.",
			InputSchema: object(map[string]any{
				"workspace": wsProp,
				"app":       str("application handle or numeric id (optional; omit for the whole workspace)"),
				"limit":     map[string]any{"type": "integer", "description": "maximum events to return (default 20, max 100)"},
			}),
		},
		readOnly: true,
		handler: func(ctx context.Context, s *Server, args map[string]any) (any, error) {
			limit := optInt(args, "limit", 20)
			if optString(args, "app") == "" {
				ws, err := s.resolveWS(ctx, args)
				if err != nil {
					return nil, err
				}
				return s.client.WorkspaceEvents(ctx, ws, limit)
			}
			ws, appID, err := s.resolveApp(ctx, args)
			if err != nil {
				return nil, err
			}
			return s.client.AppEvents(ctx, ws, appID, min(max(limit, 1), api.MaxEventPage))
		},
	})
	s.register(tool{
		def: toolDef{
			Name:        "get_traffic",
			Description: "Get an application's HTTP traffic over a window: request count, error rate, latency percentiles and status-class counts.",
			InputSchema: object(map[string]any{
				"workspace": wsProp,
				"app":       str("application handle or numeric id"),
				"range":     map[string]any{"type": "string", "enum": []string{"15m", "1h", "24h", "7d"}, "description": "time window (default 1h)"},
			}, "app"),
		},
		readOnly: true,
		handler: func(ctx context.Context, s *Server, args map[string]any) (any, error) {
			ws, appID, err := s.resolveApp(ctx, args)
			if err != nil {
				return nil, err
			}
			rng := optString(args, "range")
			if rng == "" {
				rng = "1h"
			}
			t, err := s.client.TrafficSummary(ctx, ws, appID, rng)
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"app_id":             appID,
				"range":              rng,
				"requests":           t.Totals.Requests,
				"error_rate":         t.Totals.ErrorRate,
				"error_rate_percent": math.Round(t.Totals.ErrorRate*10000) / 100,
				"avg_latency_ms":     t.Totals.AvgLatencyMS,
				"p95_latency_ms":     t.Totals.P95LatencyMS,
				"p99_latency_ms":     t.Totals.P99LatencyMS,
				"status": map[string]int64{
					"2xx": t.Status.S2xx, "3xx": t.Status.S3xx, "4xx": t.Status.S4xx, "5xx": t.Status.S5xx,
				},
			}, nil
		},
	})
	s.register(tool{
		def: toolDef{
			Name:        "list_backups",
			Description: "List the backup history of a database hosted on a database instance.",
			InputSchema: object(map[string]any{"workspace": wsProp, "database": databaseProp, "db": logicalDBProp}, "database"),
		},
		readOnly: true,
		handler: func(ctx context.Context, s *Server, args map[string]any) (any, error) {
			ws, id, dbID, err := s.resolveLogicalDB(ctx, args)
			if err != nil {
				return nil, err
			}
			return s.client.Backups(ctx, ws, id, dbID)
		},
	})

	s.registerAlertAction("ack_alert", "Acknowledge an alert: it stays active but stops re-notifying while you work on it.", (*api.Client).AckAlert)
	s.registerAlertAction("resolve_alert", "Resolve (close) an alert, e.g. after fixing its cause.", (*api.Client).ResolveAlert)
	s.register(tool{
		def: toolDef{
			Name:        "run_backup",
			Description: "Back up a database now, to the workspace's configured destination. Blocks until the backup finishes and returns it; check its status.",
			InputSchema: object(map[string]any{
				"workspace": wsProp,
				"database":  databaseProp,
				"db":        logicalDBProp,
				"comment":   str("note to attach to the backup, e.g. why it was taken (optional)"),
			}, "database"),
			Annotations: &toolAnnotations{Title: "Run database backup"},
		},
		handler: func(ctx context.Context, s *Server, args map[string]any) (any, error) {
			ws, id, dbID, err := s.resolveLogicalDB(ctx, args)
			if err != nil {
				return nil, err
			}
			return s.client.RunBackup(ctx, ws, id, dbID, optString(args, "comment"))
		},
	})
}

func (s *Server) registerAlertAction(name, desc string, fn func(*api.Client, context.Context, string, uint) (*api.Alert, error)) {
	s.register(tool{
		def: toolDef{
			Name:        name,
			Description: desc,
			InputSchema: object(map[string]any{"workspace": wsProp, "alert": alertProp}, "alert"),
		},
		handler: func(ctx context.Context, s *Server, args map[string]any) (any, error) {
			ws, err := s.resolveWS(ctx, args)
			if err != nil {
				return nil, err
			}
			id, err := argInt(args, "alert")
			if err != nil {
				return nil, err
			}
			return fn(s.client, ctx, ws, uint(id))
		},
	})
}

// resolveLogicalDB resolves the workspace, the instance named by "database" and
// the logical database named by the optional "db".
func (s *Server) resolveLogicalDB(ctx context.Context, args map[string]any) (ws string, id, dbID uint, err error) {
	if ws, err = s.resolveWS(ctx, args); err != nil {
		return
	}
	ref, err := argString(args, "database")
	if err != nil {
		return
	}
	if id, err = s.client.ResolveDatabaseID(ctx, ws, ref); err != nil {
		return
	}
	dbID, err = s.client.ResolveLogicalDatabaseID(ctx, ws, id, optString(args, "db"))
	return
}

// optBool returns a boolean argument, or false if absent.
func optBool(args map[string]any, key string) bool {
	v, _ := args[key].(bool)
	return v
}

// optInt returns an integer argument, or def if absent or not a number.
func optInt(args map[string]any, key string, def int) int {
	if n, err := argInt(args, key); err == nil {
		return n
	}
	return def
}
