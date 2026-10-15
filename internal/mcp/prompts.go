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
	"encoding/json"
	"fmt"
	"strings"
)

// prompt is a ready-made, parameterized instruction an agent can invoke. render
// turns the caller's arguments into the user message guiding the agent through
// the relevant tools. Prompts never call the API themselves — they only shape the
// conversation.
type prompt struct {
	def    promptDef
	render func(args map[string]string) string
}

// wsClause renders an optional "(in workspace X)" fragment plus the argument hint
// telling the agent which workspace to pass to tools.
func wsClause(args map[string]string) string {
	if ws := args["workspace"]; ws != "" {
		return fmt.Sprintf(" in workspace %q (pass workspace=%q to every tool)", ws, ws)
	}
	return " (using the active workspace)"
}

// registerPrompts installs the diagnostic prompt catalog.
func (s *Server) registerPrompts() {
	appArg := promptArg{Name: "app", Description: "application handle or numeric id", Required: true}
	wsArg := promptArg{Name: "workspace", Description: "workspace name (default: the active workspace)"}

	s.prompts = []prompt{
		{
			def: promptDef{
				Name:        "diagnose_deployment",
				Title:       "Diagnose a failed deployment",
				Description: "Investigate why an application's latest deployment failed or is unhealthy and propose a fix.",
				Arguments:   []promptArg{appArg, wsArg},
			},
			render: func(a map[string]string) string {
				return fmt.Sprintf(
					"The application %q%s is failing or its latest deployment did not succeed.\n\n"+
						"Investigate and report:\n"+
						"1. Call get_app for %q to read its status and current release.\n"+
						"2. Call list_deployments, then get_deployment on the most recent one, to see how it ended.\n"+
						"3. Explain the most likely root cause in plain language.\n"+
						"4. Recommend a concrete next step. If a rollback is warranted, identify the target release "+
						"with list_releases and state the release id — but do NOT roll back without my confirmation.",
					a["app"], wsClause(a), a["app"])
			},
		},
		{
			def: promptDef{
				Name:        "app_health",
				Title:       "Assess application health",
				Description: "Summarize an application's current health and recent deployment activity.",
				Arguments:   []promptArg{appArg, wsArg},
			},
			render: func(a map[string]string) string {
				return fmt.Sprintf(
					"Give me a health summary of application %q%s.\n\n"+
						"Use get_app for its live status and current release, and list_deployments for recent "+
						"activity and their outcomes. Report: current status, the last successful deployment, "+
						"any recent failures, and whether anything needs attention.",
					a["app"], wsClause(a))
			},
		},
		{
			def: promptDef{
				Name:        "workspace_overview",
				Title:       "Workspace overview",
				Description: "Summarize the apps and databases in a workspace and flag anything unhealthy.",
				Arguments:   []promptArg{wsArg},
			},
			render: func(a map[string]string) string {
				return fmt.Sprintf(
					"Give me an overview of this workspace%s.\n\n"+
						"Call list_apps and list_databases. Summarize what's running, group apps by status, "+
						"and call out anything stopped, errored, or otherwise needing attention.",
					wsClause(a))
			},
		},
	}
}

func (s *Server) promptDefs() []promptDef {
	defs := make([]promptDef, 0, len(s.prompts))
	for _, p := range s.prompts {
		defs = append(defs, p.def)
	}
	return defs
}

// handlePromptGet validates the requested prompt's required arguments and renders
// it into a single user message.
func (s *Server) handlePromptGet(raw json.RawMessage) (*getPromptResult, *rpcError) {
	var p getPromptParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: "invalid params: " + err.Error()}
	}
	var found *prompt
	for i := range s.prompts {
		if s.prompts[i].def.Name == p.Name {
			found = &s.prompts[i]
			break
		}
	}
	if found == nil {
		return nil, &rpcError{Code: codeMethodNotFound, Message: "unknown prompt: " + p.Name}
	}
	var missing []string
	for _, arg := range found.def.Arguments {
		if arg.Required && strings.TrimSpace(p.Arguments[arg.Name]) == "" {
			missing = append(missing, arg.Name)
		}
	}
	if len(missing) > 0 {
		return nil, &rpcError{Code: codeInvalidParams, Message: "missing required argument(s): " + strings.Join(missing, ", ")}
	}
	return &getPromptResult{
		Description: found.def.Description,
		Messages: []promptMessage{{
			Role:    "user",
			Content: contentBlock{Type: "text", Text: found.render(p.Arguments)},
		}},
	}, nil
}
