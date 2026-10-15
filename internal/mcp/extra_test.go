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
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInitializeAdvertisesCapabilities(t *testing.T) {
	resps := drive(t, false, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	var res initializeResult
	remarshal(t, resps[0].Result, &res)
	for _, cap := range []string{"tools", "resources", "prompts"} {
		if _, ok := res.Capabilities[cap]; !ok {
			t.Errorf("initialize should advertise %q capability, got %v", cap, res.Capabilities)
		}
	}
}

func TestPromptsList(t *testing.T) {
	resps := drive(t, false, `{"jsonrpc":"2.0","id":1,"method":"prompts/list","params":{}}`)
	var res promptsListResult
	remarshal(t, resps[0].Result, &res)
	got := map[string]bool{}
	for _, p := range res.Prompts {
		got[p.Name] = true
	}
	for _, want := range []string{"diagnose_deployment", "app_health", "workspace_overview"} {
		if !got[want] {
			t.Errorf("prompts/list missing %q", want)
		}
	}
}

func TestPromptGetRendersAndValidates(t *testing.T) {
	// Happy path: the app name is interpolated into the message.
	resps := drive(t, false, `{"jsonrpc":"2.0","id":1,"method":"prompts/get","params":{"name":"diagnose_deployment","arguments":{"app":"web","workspace":"prod"}}}`)
	var res getPromptResult
	remarshal(t, resps[0].Result, &res)
	if len(res.Messages) != 1 || res.Messages[0].Role != "user" {
		t.Fatalf("want one user message, got %+v", res.Messages)
	}
	text := res.Messages[0].Content.Text
	if !strings.Contains(text, `"web"`) || !strings.Contains(text, `workspace "prod"`) {
		t.Errorf("rendered prompt missing interpolated args: %q", text)
	}

	// Missing required arg → invalid params error.
	bad := drive(t, false, `{"jsonrpc":"2.0","id":2,"method":"prompts/get","params":{"name":"diagnose_deployment","arguments":{}}}`)
	if bad[0].Error == nil || bad[0].Error.Code != codeInvalidParams {
		t.Errorf("missing arg should yield invalid params, got %+v", bad[0])
	}

	// Unknown prompt → method not found.
	unknown := drive(t, false, `{"jsonrpc":"2.0","id":3,"method":"prompts/get","params":{"name":"nope","arguments":{}}}`)
	if unknown[0].Error == nil || unknown[0].Error.Code != codeMethodNotFound {
		t.Errorf("unknown prompt should yield method not found, got %+v", unknown[0])
	}
}

func TestResourceTemplatesList(t *testing.T) {
	resps := drive(t, false, `{"jsonrpc":"2.0","id":1,"method":"resources/templates/list","params":{}}`)
	var res resourceTemplatesListResult
	remarshal(t, resps[0].Result, &res)
	if len(res.ResourceTemplates) != 2 {
		t.Fatalf("want 2 templates, got %d", len(res.ResourceTemplates))
	}
}

func TestParseURI(t *testing.T) {
	tests := []struct {
		uri      string
		wantWS   string
		wantApp  string
		wantDep  int
		isDeploy bool
		wantErr  bool
	}{
		{uri: "miabi://workspaces/prod/apps/web", wantWS: "prod", wantApp: "web"},
		{uri: "miabi://workspaces/prod/apps/web/deployments/7", wantWS: "prod", wantApp: "web", wantDep: 7, isDeploy: true},
		{uri: "https://example.com/x", wantErr: true},
		{uri: "miabi://workspaces/prod/apps", wantErr: true},
		{uri: "miabi://workspaces/prod/apps/web/deployments/0", wantErr: true},
		{uri: "miabi://workspaces/prod/apps/web/releases/1", wantErr: true},
		{uri: "miabi://workspaces//apps/web", wantErr: true},
	}
	for _, tc := range tests {
		got, err := parseURI(tc.uri)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseURI(%q): want error, got %+v", tc.uri, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseURI(%q): unexpected error %v", tc.uri, err)
			continue
		}
		if got.workspace != tc.wantWS || got.app != tc.wantApp || got.deployment != tc.wantDep || got.isDeploy != tc.isDeploy {
			t.Errorf("parseURI(%q) = %+v, want ws=%s app=%s dep=%d isDeploy=%v", tc.uri, got, tc.wantWS, tc.wantApp, tc.wantDep, tc.isDeploy)
		}
	}
}

func TestAppURIRoundTrip(t *testing.T) {
	uri := appURI("prod", "web")
	p, err := parseURI(uri)
	if err != nil {
		t.Fatalf("parseURI(%q): %v", uri, err)
	}
	if p.workspace != "prod" || p.app != "web" || p.isDeploy {
		t.Errorf("round-trip mismatch: %+v", p)
	}
}

// TestHTTPTransport drives the HTTP handler directly (no network) and checks the
// request/response profile and the Origin guard.
func TestHTTPTransport(t *testing.T) {
	s := New(Options{Version: "test"})

	// A request gets a JSON-RPC response.
	req := httptest.NewRequest(http.MethodPost, "/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	rec := httptest.NewRecorder()
	s.httpHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST status = %d, want 200", rec.Code)
	}
	var resp rpcResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	// A notification gets 202 and no body.
	note := httptest.NewRequest(http.MethodPost, "/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))
	rec = httptest.NewRecorder()
	s.httpHandler(rec, note)
	if rec.Code != http.StatusAccepted || rec.Body.Len() != 0 {
		t.Errorf("notification: status=%d bodyLen=%d, want 202/0", rec.Code, rec.Body.Len())
	}

	// A cross-origin request is rejected.
	ebr := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader([]byte(`{}`)))
	ebr.Header.Set("Origin", "https://evil.example.com")
	rec = httptest.NewRecorder()
	s.httpHandler(rec, ebr)
	if rec.Code != http.StatusForbidden {
		t.Errorf("cross-origin status = %d, want 403", rec.Code)
	}

	// GET is not allowed (no server-initiated stream).
	get := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	rec = httptest.NewRecorder()
	s.httpHandler(rec, get)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET status = %d, want 405", rec.Code)
	}
}

func TestOriginAllowed(t *testing.T) {
	cases := map[string]bool{
		"":                         true,
		"http://localhost:3000":    true,
		"http://127.0.0.1:8765":    true,
		"https://evil.example.com": false,
		"::not a url":              false,
	}
	for origin, want := range cases {
		if got := originAllowed(origin); got != want {
			t.Errorf("originAllowed(%q) = %v, want %v", origin, got, want)
		}
	}
}
