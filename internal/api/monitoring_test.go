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
	"net/http"
	"strings"
	"testing"
)

func TestWorkspaceEventsQuery(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.RequestURI(); got != "/api/v1/workspaces/prod/events?page=0&size=100&order=desc" {
			t.Errorf("unexpected request %s", got)
		}
		writeData(w, `[{"id":1,"type":"deploy_failed","severity":"error","message":"boom","app_name":"web"}]`)
	})
	es, err := c.WorkspaceEvents(context.Background(), "prod", 500)
	if err != nil {
		t.Fatal(err)
	}
	if len(es) != 1 || es[0].AppName != "web" {
		t.Errorf("unexpected events %+v", es)
	}
}

func TestResolveLogicalDatabaseID(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeData(w, `[{"id":9,"name":"shop"},{"id":10,"name":"auth"}]`)
	})
	ctx := context.Background()
	if id, err := c.ResolveLogicalDatabaseID(ctx, "prod", 4, "auth"); err != nil || id != 10 {
		t.Errorf("by name: %d, %v", id, err)
	}
	if id, err := c.ResolveLogicalDatabaseID(ctx, "prod", 4, "9"); err != nil || id != 9 {
		t.Errorf("by id: %d, %v", id, err)
	}
	if _, err := c.ResolveLogicalDatabaseID(ctx, "prod", 4, ""); err == nil || !strings.Contains(err.Error(), "shop") {
		t.Errorf("an ambiguous instance should list its databases, got %v", err)
	}
}
