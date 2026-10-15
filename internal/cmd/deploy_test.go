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

package cmd

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/miabi-io/cli/internal/api"
)

// A server that predates ?wait answers with a pending deployment; --wait must
// then fall back to polling.
func TestSettleDeploymentPollsWhenServerDidNotWait(t *testing.T) {
	polled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/workspaces/prod/apps/7/deployments/5" {
			t.Errorf("unexpected request %s", r.URL.Path)
		}
		polled = true
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":{"id":5,"number":3,"status":"succeeded"}}`)
	}))
	defer srv.Close()
	c, err := api.New(api.Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}

	pending := &api.Deployment{ID: 5, Number: 3, Status: "pending"}
	final, err := settleDeployment(context.Background(), c, "prod", "7", pending, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !polled || final.Status != api.StatusSucceeded {
		t.Errorf("polled=%v final=%+v", polled, final)
	}

	settled := &api.Deployment{ID: 5, Number: 3, Status: api.StatusCanary}
	polled = false
	if got, err := settleDeployment(context.Background(), c, "prod", "7", settled, time.Now().Add(time.Minute)); err != nil || got != settled || polled {
		t.Errorf("a settled deployment must be used as-is: %v, %v, polled=%v", got, err, polled)
	}
}
