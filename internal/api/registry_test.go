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
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestRegistryTagsPagesThroughEveryTag(t *testing.T) {
	const total = 230
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("name") != "team/api" {
			t.Errorf("name = %q; a repository with a slash travels as a query parameter", r.URL.Query().Get("name"))
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		var tags []RegistryTag
		for i := page * 100; i < total && i < (page+1)*100; i++ {
			tags = append(tags, RegistryTag{Name: fmt.Sprintf("v%d", i)})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": tags})
	}))
	defer srv.Close()
	c, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	tags, err := c.RegistryTags(context.Background(), "4", "team/api")
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != total {
		t.Fatalf("got %d tags, want %d", len(tags), total)
	}
}

func TestDeleteRegistryTagsSendsTheDryRun(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if r.URL.Path != "/api/v1/workspaces/4/registry/repository/tags/delete" || body["dry_run"] != true || body["repository"] != "web" {
			t.Errorf("path %s body %v", r.URL.Path, body)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": TagDeleteResult{
			DryRun: true, Removed: []string{"latest", "v1"},
			Tags: []TagOutcome{{Tag: "v1", Result: "would_delete", Siblings: []string{"latest"}}},
		}})
	}))
	defer srv.Close()
	c, _ := New(Options{BaseURL: srv.URL})
	res, err := c.DeleteRegistryTags(context.Background(), "4", "web", []string{"v1"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Removed) != 2 || res.Tags[0].Siblings[0] != "latest" {
		t.Fatalf("result = %+v", res)
	}
}
