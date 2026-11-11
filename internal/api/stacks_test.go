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
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestImportStackSendsComposeAndEnv(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/workspaces/prod/stacks/import" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		want := map[string]string{"name": "shop", "compose": "services: {}\n", "env": "A=1\n", "location": "eu"}
		for k, v := range want {
			if body[k] != v {
				t.Errorf("body[%s] = %q, want %q", k, body[k], v)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"success":true,"data":{"stack":{"id":4,"name":"shop"},"created":["web"],"volumes":[],
			"port_requests":1,"port_conflicts":[],"skipped":[{"service":"builder","reason":"build-only"}],"warnings":["DB is not set"]}}`)
	}))
	defer srv.Close()
	c, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.ImportStack(context.Background(), "prod", ImportStackRequest{Name: "shop", Compose: "services: {}\n", Env: "A=1\n", Location: "eu"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Stack.ID != 4 || len(res.Created) != 1 || res.PortRequests != 1 || res.Skipped[0].Service != "builder" || len(res.Warnings) != 1 {
		t.Errorf("result = %+v", res)
	}
}

func TestResolveStackIDByName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":[{"id":3,"name":"blog"},{"id":9,"name":"shop","uid":"0190-abc"}]}`)
	}))
	defer srv.Close()
	c, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	for ref, want := range map[string]uint{"shop": 9, "0190-abc": 9, "12": 12} {
		if got, err := c.ResolveStackID(context.Background(), "prod", ref); err != nil || got != want {
			t.Errorf("ResolveStackID(%q) = %d, %v; want %d", ref, got, err, want)
		}
	}
	if _, err := c.ResolveStackID(context.Background(), "prod", "missing"); err == nil {
		t.Error("an unknown stack must be an error")
	}
}
