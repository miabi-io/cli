package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Resources expose apps and their deployments under a stable miabi:// URI so an
// agent can attach one as context (an "@-mention"). The scheme is:
//
//	miabi://workspaces/{ws}/apps/{app}
//	miabi://workspaces/{ws}/apps/{app}/deployments/{number}
//
// The workspace segment keeps a URI unambiguous across workspaces; resources/list
// fills it with the CLI's active workspace.

const uriScheme = "miabi://"

// appURI builds the canonical URI for an app.
func appURI(ws, app string) string {
	return fmt.Sprintf("%sworkspaces/%s/apps/%s", uriScheme, ws, app)
}

// parsedURI is a decoded miabi:// resource reference.
type parsedURI struct {
	workspace  string
	app        string
	deployment int
	isDeploy   bool
}

// parseURI decodes a miabi:// app/deployment URI.
func parseURI(uri string) (parsedURI, error) {
	var p parsedURI
	rest, ok := strings.CutPrefix(uri, uriScheme)
	if !ok {
		return p, fmt.Errorf("unsupported URI %q (want %sworkspaces/{ws}/apps/{app}[/deployments/{n}])", uri, uriScheme)
	}
	seg := strings.Split(strings.Trim(rest, "/"), "/")
	if (len(seg) != 4 && len(seg) != 6) || seg[0] != "workspaces" || seg[2] != "apps" {
		return p, fmt.Errorf("malformed URI %q", uri)
	}
	p.workspace, p.app = seg[1], seg[3]
	if p.workspace == "" || p.app == "" {
		return p, fmt.Errorf("malformed URI %q: empty workspace or app", uri)
	}
	if len(seg) == 6 {
		if seg[4] != "deployments" {
			return p, fmt.Errorf("malformed URI %q", uri)
		}
		n, err := strconv.Atoi(seg[5])
		if err != nil || n <= 0 {
			return p, fmt.Errorf("malformed URI %q: deployment number must be a positive integer", uri)
		}
		p.isDeploy, p.deployment = true, n
	}
	return p, nil
}

// resourceTemplates advertises the parameterized URIs an agent can construct. The
// concrete app list comes from resources/list; deployments are per-app and better
// expressed as a template than enumerated.
func resourceTemplates() []resourceTemplate {
	return []resourceTemplate{
		{
			URITemplate: uriScheme + "workspaces/{workspace}/apps/{app}",
			Name:        "app",
			Title:       "Application",
			Description: "An application's current state (status, image, tag, release).",
			MIMEType:    "application/json",
		},
		{
			URITemplate: uriScheme + "workspaces/{workspace}/apps/{app}/deployments/{number}",
			Name:        "deployment",
			Title:       "Deployment",
			Description: "One deployment of an application, addressed by its per-app number.",
			MIMEType:    "application/json",
		},
	}
}

// handleResourcesList enumerates the apps in the active workspace as resources.
// With no resolvable workspace it returns an empty list (not an error) — an agent
// can still read a resource by constructing its URI.
func (s *Server) handleResourcesList(ctx context.Context) (*resourcesListResult, *rpcError) {
	ws, err := s.client.ResolveWorkspaceName(ctx, "", s.fallbackWS)
	if err != nil {
		return &resourcesListResult{Resources: []resourceDef{}}, nil
	}
	apps, err := s.client.Apps(ctx, ws)
	if err != nil {
		return nil, &rpcError{Code: codeInternal, Message: err.Error()}
	}
	out := make([]resourceDef, 0, len(apps))
	for _, a := range apps {
		out = append(out, resourceDef{
			URI:         appURI(ws, a.Name),
			Name:        a.Name,
			Title:       a.DisplayName,
			Description: fmt.Sprintf("app %q — status %s, image %s:%s", a.Name, a.Status, a.Image, a.Tag),
			MIMEType:    "application/json",
		})
	}
	return &resourcesListResult{Resources: out}, nil
}

// handleResourceRead fetches the app or deployment a URI names and returns it as
// JSON text.
func (s *Server) handleResourceRead(ctx context.Context, raw json.RawMessage) (*readResourceResult, *rpcError) {
	var p readResourceParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: "invalid params: " + err.Error()}
	}
	u, err := parseURI(p.URI)
	if err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}
	ws, err := s.client.ResolveWorkspaceName(ctx, u.workspace, s.fallbackWS)
	if err != nil {
		return nil, &rpcError{Code: codeInternal, Message: err.Error()}
	}
	var payload any
	if u.isDeploy {
		payload, err = s.client.DeploymentByNumber(ctx, ws, u.app, u.deployment)
	} else {
		payload, err = s.client.App(ctx, ws, u.app)
	}
	if err != nil {
		return nil, &rpcError{Code: codeInternal, Message: err.Error()}
	}
	return &readResourceResult{Contents: []resourceContents{{
		URI:      p.URI,
		MIMEType: "application/json",
		Text:     jsonString(payload),
	}}}, nil
}
