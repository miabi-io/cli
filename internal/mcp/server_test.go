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
	"strings"
	"testing"
)

func drive(t *testing.T, allowWrite bool, requests ...string) []rpcResponse {
	t.Helper()
	s := New(Options{AllowWrite: allowWrite, Version: "test"})
	var out strings.Builder
	if err := s.Serve(context.Background(), strings.NewReader(strings.Join(requests, "\n")), &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	var resps []rpcResponse
	dec := json.NewDecoder(strings.NewReader(out.String()))
	for dec.More() {
		var r rpcResponse
		if err := dec.Decode(&r); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		resps = append(resps, r)
	}
	return resps
}

func TestInitialize(t *testing.T) {
	resps := drive(t, false, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	if len(resps) != 1 {
		t.Fatalf("want 1 response, got %d", len(resps))
	}
	var res initializeResult
	remarshal(t, resps[0].Result, &res)
	if res.ProtocolVersion != protocolVersion {
		t.Errorf("protocolVersion = %q, want %q", res.ProtocolVersion, protocolVersion)
	}
	if res.ServerInfo.Name != "miabi" {
		t.Errorf("serverInfo.name = %q, want miabi", res.ServerInfo.Name)
	}
	if _, ok := res.Capabilities["tools"]; !ok {
		t.Errorf("expected tools capability, got %v", res.Capabilities)
	}
}

func TestNotificationGetsNoReply(t *testing.T) {
	resps := drive(t, false, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if len(resps) != 0 {
		t.Fatalf("notification should get no reply, got %d", len(resps))
	}
}

func TestToolsListGating(t *testing.T) {
	names := func(allowWrite bool) map[string]bool {
		resps := drive(t, allowWrite, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
		var res toolsListResult
		remarshal(t, resps[0].Result, &res)
		got := map[string]bool{}
		for _, d := range res.Tools {
			got[d.Name] = true
		}
		return got
	}

	ro := names(false)
	if !ro["list_apps"] {
		t.Error("read-only catalog should include list_apps")
	}
	for _, w := range []string{"deploy_app", "restart_app", "rollback_app", "stop_app", "start_app"} {
		if ro[w] {
			t.Errorf("read-only catalog must not include mutating tool %q", w)
		}
	}

	rw := names(true)
	for _, w := range []string{"deploy_app", "restart_app", "rollback_app"} {
		if !rw[w] {
			t.Errorf("read-write catalog should include %q", w)
		}
	}
}

func TestReadOnlyToolsHaveHint(t *testing.T) {
	resps := drive(t, true, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
	var res toolsListResult
	remarshal(t, resps[0].Result, &res)
	for _, d := range res.Tools {
		if d.Name == "list_apps" {
			if d.Annotations == nil || !d.Annotations.ReadOnlyHint {
				t.Errorf("list_apps should carry readOnlyHint")
			}
		}
		if d.Name == "rollback_app" {
			if d.Annotations == nil || !d.Annotations.DestructiveHint {
				t.Errorf("rollback_app should carry destructiveHint")
			}
		}
	}
}

func TestUnknownMethod(t *testing.T) {
	resps := drive(t, false, `{"jsonrpc":"2.0","id":9,"method":"does/not/exist"}`)
	if len(resps) != 1 || resps[0].Error == nil {
		t.Fatalf("want one error response, got %+v", resps)
	}
	if resps[0].Error.Code != codeMethodNotFound {
		t.Errorf("code = %d, want %d", resps[0].Error.Code, codeMethodNotFound)
	}
}

// remarshal round-trips v (an any decoded from JSON) into the typed target.
func remarshal(t *testing.T, v any, target any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := json.Unmarshal(b, target); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
}
