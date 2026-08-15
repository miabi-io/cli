package mcp

import (
	"context"
	"fmt"

	"github.com/miabi-io/cli/internal/api"
)

// tool is one MCP tool: its advertised definition, whether it only reads, and
// the handler that runs it against the API.
type tool struct {
	def      toolDef
	readOnly bool
	handler  func(ctx context.Context, s *Server, args map[string]any) (any, error)
}

// object is a small helper for building a JSON Schema object literal.
func object(props map[string]any, required ...string) map[string]any {
	schema := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

// wsProp is the shared optional "workspace" argument.
var wsProp = map[string]any{"type": "string", "description": "workspace name, UID, or id (default: the CLI's active workspace)"}

// register adds a tool to the catalog. Write tools are only registered when the
// server is in read-write mode, so read-only servers never advertise them.
func (s *Server) register(t tool) {
	if !t.readOnly && !s.allowWrite {
		return
	}
	if t.readOnly {
		if t.def.Annotations == nil {
			t.def.Annotations = &toolAnnotations{}
		}
		t.def.Annotations.ReadOnlyHint = true
	}
	s.tools[t.def.Name] = t
	s.order = append(s.order, t.def.Name)
}

func (s *Server) toolDefs() []toolDef {
	defs := make([]toolDef, 0, len(s.order))
	for _, name := range s.order {
		defs = append(defs, s.tools[name].def)
	}
	return defs
}

// registerTools installs the standard catalog: read tools always, write tools
// only under --allow-write.
func (s *Server) registerTools() {
	// identity & workspaces
	s.register(tool{
		def: toolDef{
			Name:        "whoami",
			Description: "Return the authenticated user and their default workspace.",
			InputSchema: object(map[string]any{}),
		},
		readOnly: true,
		handler: func(ctx context.Context, s *Server, _ map[string]any) (any, error) {
			return s.client.Me(ctx)
		},
	})
	s.register(tool{
		def: toolDef{
			Name:        "list_workspaces",
			Description: "List the workspaces the token can access.",
			InputSchema: object(map[string]any{}),
		},
		readOnly: true,
		handler: func(ctx context.Context, s *Server, _ map[string]any) (any, error) {
			return s.client.Workspaces(ctx)
		},
	})

	// applications
	s.register(tool{
		def: toolDef{
			Name:        "list_apps",
			Description: "List applications in a workspace with their status.",
			InputSchema: object(map[string]any{"workspace": wsProp}),
		},
		readOnly: true,
		handler: func(ctx context.Context, s *Server, args map[string]any) (any, error) {
			ws, err := s.resolveWS(ctx, args)
			if err != nil {
				return nil, err
			}
			return s.client.Apps(ctx, ws)
		},
	})
	s.register(tool{
		def: toolDef{
			Name:        "get_app",
			Description: "Get one application (status, image, tag, current release) by handle or id.",
			InputSchema: object(map[string]any{"workspace": wsProp, "app": str("application handle or numeric id")}, "app"),
		},
		readOnly: true,
		handler: func(ctx context.Context, s *Server, args map[string]any) (any, error) {
			ws, appID, err := s.resolveApp(ctx, args)
			if err != nil {
				return nil, err
			}
			return s.client.App(ctx, ws, appID)
		},
	})
	s.register(tool{
		def: toolDef{
			Name:        "list_deployments",
			Description: "List an application's deployment history (most recent first).",
			InputSchema: object(map[string]any{"workspace": wsProp, "app": str("application handle or numeric id")}, "app"),
		},
		readOnly: true,
		handler: func(ctx context.Context, s *Server, args map[string]any) (any, error) {
			ws, appID, err := s.resolveApp(ctx, args)
			if err != nil {
				return nil, err
			}
			return s.client.Deployments(ctx, ws, appID)
		},
	})
	s.register(tool{
		def: toolDef{
			Name:        "get_deployment",
			Description: "Get one deployment by its per-app number (from list_deployments).",
			InputSchema: object(map[string]any{
				"workspace": wsProp,
				"app":       str("application handle or numeric id"),
				"number":    map[string]any{"type": "integer", "description": "the deployment number"},
			}, "app", "number"),
		},
		readOnly: true,
		handler: func(ctx context.Context, s *Server, args map[string]any) (any, error) {
			ws, appID, err := s.resolveApp(ctx, args)
			if err != nil {
				return nil, err
			}
			n, err := argInt(args, "number")
			if err != nil {
				return nil, err
			}
			return s.client.DeploymentByNumber(ctx, ws, appID, n)
		},
	})
	s.register(tool{
		def: toolDef{
			Name:        "list_releases",
			Description: "List an application's releases (rollback targets).",
			InputSchema: object(map[string]any{"workspace": wsProp, "app": str("application handle or numeric id")}, "app"),
		},
		readOnly: true,
		handler: func(ctx context.Context, s *Server, args map[string]any) (any, error) {
			ws, appID, err := s.resolveApp(ctx, args)
			if err != nil {
				return nil, err
			}
			return s.client.Releases(ctx, ws, appID)
		},
	})

	//  databases
	s.register(tool{
		def: toolDef{
			Name:        "list_databases",
			Description: "List database instances in a workspace.",
			InputSchema: object(map[string]any{"workspace": wsProp}),
		},
		readOnly: true,
		handler: func(ctx context.Context, s *Server, args map[string]any) (any, error) {
			ws, err := s.resolveWS(ctx, args)
			if err != nil {
				return nil, err
			}
			return s.client.Databases(ctx, ws)
		},
	})
	s.register(tool{
		def: toolDef{
			Name:        "get_database",
			Description: "Get one database instance by handle or id. Credentials are NOT returned.",
			InputSchema: object(map[string]any{"workspace": wsProp, "database": str("database handle or numeric id")}, "database"),
		},
		readOnly: true,
		handler: func(ctx context.Context, s *Server, args map[string]any) (any, error) {
			ws, err := s.resolveWS(ctx, args)
			if err != nil {
				return nil, err
			}
			ref, err := argString(args, "database")
			if err != nil {
				return nil, err
			}
			id, err := s.client.ResolveDatabaseID(ctx, ws, ref)
			if err != nil {
				return nil, err
			}
			return s.client.Database(ctx, ws, id)
		},
	})

	// secrets
	s.register(tool{
		def: toolDef{
			Name:        "list_secrets",
			Description: "List secret names and metadata in a workspace. Values are never returned.",
			InputSchema: object(map[string]any{"workspace": wsProp}),
		},
		readOnly: true,
		handler: func(ctx context.Context, s *Server, args map[string]any) (any, error) {
			ws, err := s.resolveWS(ctx, args)
			if err != nil {
				return nil, err
			}
			return s.client.Secrets(ctx, ws)
		},
	})

	// mutating tools (only under --allow-write)
	s.register(tool{
		def: toolDef{
			Name:        "deploy_app",
			Description: "Deploy an application, optionally pinning a new image tag. Returns the deployment.",
			InputSchema: object(map[string]any{
				"workspace": wsProp,
				"app":       str("application handle or numeric id"),
				"tag":       str("image tag to deploy (optional; keeps the current tag if omitted)"),
			}, "app"),
			Annotations: &toolAnnotations{Title: "Deploy application"},
		},
		handler: func(ctx context.Context, s *Server, args map[string]any) (any, error) {
			ws, appID, err := s.resolveApp(ctx, args)
			if err != nil {
				return nil, err
			}
			return s.client.Deploy(ctx, ws, appID, api.DeployRequest{Tag: optString(args, "tag")})
		},
	})
	s.registerAction("restart_app", "Restart an application.", true, (*api.Client).RestartApp)
	s.registerAction("start_app", "Start a stopped application.", false, (*api.Client).StartApp)
	s.registerAction("stop_app", "Stop a running application.", true, (*api.Client).StopApp)
	s.register(tool{
		def: toolDef{
			Name:        "rollback_app",
			Description: "Roll an application back to a prior release (get release ids from list_releases).",
			InputSchema: object(map[string]any{
				"workspace":  wsProp,
				"app":        str("application handle or numeric id"),
				"release_id": map[string]any{"type": "integer", "description": "the release id to roll back to"},
			}, "app", "release_id"),
			Annotations: &toolAnnotations{Title: "Roll back application", DestructiveHint: true},
		},
		handler: func(ctx context.Context, s *Server, args map[string]any) (any, error) {
			ws, appID, err := s.resolveApp(ctx, args)
			if err != nil {
				return nil, err
			}
			rid, err := argInt(args, "release_id")
			if err != nil {
				return nil, err
			}
			return s.client.Rollback(ctx, ws, appID, api.RollbackRequest{ReleaseID: uint(rid)})
		},
	})
}

// registerAction registers a mutating app-lifecycle tool whose handler is one of
// the client's action methods (Start/Stop/RestartApp). destructive tags the tool
// so clients prompt before calling it.
func (s *Server) registerAction(name, desc string, destructive bool, fn func(*api.Client, context.Context, string, uint) error) {
	s.register(tool{
		def: toolDef{
			Name:        name,
			Description: desc,
			InputSchema: object(map[string]any{"workspace": wsProp, "app": str("application handle or numeric id")}, "app"),
			Annotations: &toolAnnotations{DestructiveHint: destructive},
		},
		handler: func(ctx context.Context, s *Server, args map[string]any) (any, error) {
			ws, appID, err := s.resolveApp(ctx, args)
			if err != nil {
				return nil, err
			}
			if err := fn(s.client, ctx, ws, appID); err != nil {
				return nil, err
			}
			return map[string]any{"ok": true, "app_id": appID}, nil
		},
	})
}

// resolveWS resolves the target workspace from the optional "workspace" argument,
// falling back to the CLI's active workspace.
func (s *Server) resolveWS(ctx context.Context, args map[string]any) (string, error) {
	return s.client.ResolveWorkspaceName(ctx, optString(args, "workspace"), s.fallbackWS)
}

// resolveApp resolves both the workspace and the numeric app id for an
// app-scoped tool call.
func (s *Server) resolveApp(ctx context.Context, args map[string]any) (string, uint, error) {
	ws, err := s.resolveWS(ctx, args)
	if err != nil {
		return "", 0, err
	}
	ref, err := argString(args, "app")
	if err != nil {
		return "", 0, err
	}
	id, err := s.client.ResolveAppID(ctx, ws, ref)
	if err != nil {
		return "", 0, err
	}
	return ws, id, nil
}

// optString returns a string argument, or "" if absent.
func optString(args map[string]any, key string) string {
	if v, ok := args[key].(string); ok {
		return v
	}
	return ""
}

// argString returns a required non-empty string argument.
func argString(args map[string]any, key string) (string, error) {
	if v, ok := args[key].(string); ok && v != "" {
		return v, nil
	}
	return "", fmt.Errorf("missing required argument %q", key)
}

// argInt returns a required integer argument. JSON numbers decode to float64, so
// accept that and a plain int.
func argInt(args map[string]any, key string) (int, error) {
	switch v := args[key].(type) {
	case float64:
		return int(v), nil
	case int:
		return v, nil
	default:
		return 0, fmt.Errorf("missing or non-numeric argument %q", key)
	}
}
