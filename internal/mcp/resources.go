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

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Resource URIs include the workspace segment so they stay unambiguous across workspaces.
const uriScheme = "miabi://"

func appURI(ws, app string) string {
	return fmt.Sprintf("%sworkspaces/%s/apps/%s", uriScheme, ws, app)
}

type parsedURI struct {
	workspace  string
	app        string
	deployment int
	isDeploy   bool
}

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

// handleResourcesList returns an empty list without a workspace; agents can still construct URIs.
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
