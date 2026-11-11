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
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/miabi-io/cli/internal/api"
	"github.com/miabi-io/cli/internal/config"
)

func TestTTLHours(t *testing.T) {
	for _, tc := range []struct {
		in   time.Duration
		want int
		err  bool
	}{
		{time.Hour, 1, false},
		{90 * time.Minute, 2, false},
		{168 * time.Hour, 168, false},
		{30 * time.Minute, 0, true},
		{169 * time.Hour, 0, true},
	} {
		got, err := ttlHours(tc.in)
		if (err != nil) != tc.err || got != tc.want {
			t.Errorf("ttlHours(%s) = %d, %v; want %d, err %v", tc.in, got, err, tc.want, tc.err)
		}
	}
}

func TestImportFlagsBody(t *testing.T) {
	b, err := (&importFlags{}).body("miabi-migrate:x", "acme")
	if err != nil || !b.RestoreData || b.SourceWorkspace != "acme" || b.Code != "miabi-migrate:x" {
		t.Errorf("default body = %+v, %v; want data restored", b, err)
	}
	if b, _ = (&importFlags{noData: true}).body("c", ""); b.RestoreData {
		t.Error("--no-data must not restore data")
	}
	if b, _ = (&importFlags{rehearse: true}).body("c", ""); b.RestoreData || !b.Rehearse {
		t.Errorf("--rehearse body = %+v", b)
	}
	// The server would drop these silently; saying so here stops a user believing the source froze.
	for _, o := range []importFlags{{rehearse: true, freeze: true}, {rehearse: true, deploy: true}, {rehearse: true, retire: true}} {
		if _, err := o.body("c", ""); err == nil {
			t.Errorf("%+v should be refused", o)
		}
	}

	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, []byte("-----BEGIN CERTIFICATE-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	b, err = (&importFlags{srcCA: ca, srcInsecure: true}).body("c", "")
	if err != nil || !strings.HasPrefix(b.CABundle, "-----BEGIN") || !b.InsecureTLS {
		t.Errorf("--source-ca must send the PEM text: %+v, %v", b, err)
	}
	if _, err := (&importFlags{srcCA: filepath.Join(t.TempDir(), "missing.pem")}).body("c", ""); err == nil {
		t.Error("a missing --source-ca must fail")
	}
}

func TestPickSourceWorkspace(t *testing.T) {
	two := &api.MigrationProbe{Workspaces: []api.MigrationWorkspace{{Name: "acme"}, {Name: "beta"}}}
	for _, tc := range []struct {
		probe *api.MigrationProbe
		want  string
		got   string
		err   string
	}{
		{&api.MigrationProbe{CodeWorkspace: "acme", Workspaces: []api.MigrationWorkspace{{Name: "acme"}}}, "", "acme", ""},
		{&api.MigrationProbe{Workspaces: []api.MigrationWorkspace{{Name: "solo"}}}, "", "solo", ""},
		{two, "beta", "beta", ""},
		{two, "", "", "pick one with --workspace"},
		{two, "gamma", "", "does not open workspace"},
		{&api.MigrationProbe{}, "", "", "opens no workspace"},
	} {
		w, err := pickSourceWorkspace(tc.probe, tc.want)
		switch {
		case tc.err != "" && (err == nil || !strings.Contains(err.Error(), tc.err)):
			t.Errorf("pick(%q) err = %v, want %q", tc.want, err, tc.err)
		case tc.err == "" && (err != nil || w.Name != tc.got):
			t.Errorf("pick(%q) = %v, %v; want %s", tc.want, w, err, tc.got)
		}
	}
}

type migrationFixture struct {
	mu        sync.Mutex
	srcOff    bool
	probeFail bool
	finalRun  string
	started   bool
	cancelled bool
	probed    api.MigrationSource
	imported  api.MigrationImport
	polls     int
	srcTokens []string
	dstTokens []string
}

func writeJSON(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": data})
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "error": map[string]any{"status_code": status, "message": msg}})
}

const testCode = "miabi-migrate:secret"

// setupMigration starts a source and a target server and saves them as the contexts "old" and "new",
// "new" being current.
func setupMigration(t *testing.T, fx *migrationFixture) {
	t.Helper()
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fx.mu.Lock()
		defer fx.mu.Unlock()
		fx.srcTokens = append(fx.srcTokens, r.Header.Get("Authorization"))
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/migrations/status":
			writeJSON(w, api.MigrationStatus{Enabled: !fx.srcOff})
		case "GET /api/v1/workspaces/acme":
			writeJSON(w, map[string]any{"id": 3, "name": "acme"})
		case "POST /api/v1/workspaces/acme/migration/start":
			fx.started = true
			var req api.MigrationStartRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req.ExpiresInHours != 2 {
				t.Errorf("expires_in_hours = %d, want 2", req.ExpiresInHours)
			}
			writeJSON(w, api.MigrationStarted{Code: testCode, ExpiresAt: time.Now().Add(2 * time.Hour), SourceURL: "https://old.example", Workspace: "acme"})
		case "DELETE /api/v1/workspaces/acme/migration":
			fx.cancelled = true
			writeJSON(w, map[string]any{"cancelled": true})
		default:
			t.Errorf("source: unexpected %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(src.Close)
	dst := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fx.mu.Lock()
		defer fx.mu.Unlock()
		fx.dstTokens = append(fx.dstTokens, r.Header.Get("Authorization"))
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/migrations/status":
			writeJSON(w, api.MigrationStatus{Enabled: true})
		case "POST /api/v1/migrations/probe":
			_ = json.NewDecoder(r.Body).Decode(&fx.probed)
			if fx.probeFail {
				writeError(w, http.StatusBadGateway, "source unreachable")
				return
			}
			writeJSON(w, api.MigrationProbe{Version: "1.11.0", CodeWorkspace: "acme",
				Workspaces: []api.MigrationWorkspace{{Name: "acme", Apps: 3, Databases: 1, Volumes: 2, SizeBytes: 5 << 20}}})
		case "POST /api/v1/migrations":
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &fx.imported)
			writeJSON(w, map[string]any{"id": 4, "status": "pending", "source_workspace": "acme"})
		case "GET /api/v1/migrations/4":
			fx.polls++
			if fx.polls < 2 {
				writeJSON(w, map[string]any{"id": 4, "status": "running", "phase": "pulling"})
				return
			}
			writeJSON(w, map[string]any{"id": 4, "status": fx.finalRun, "source_workspace": "acme", "error": "boom",
				"report": map[string]any{"items": []map[string]string{{"kind": "app", "name": "web", "action": "created"}}, "notes": []string{"Point DNS at the new server"}}})
		default:
			t.Errorf("target: unexpected %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(dst.Close)

	t.Setenv("MIABI_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	if err := config.Save(&config.File{Current: "new", Contexts: map[string]*config.Context{
		"old": {Server: config.Server{URL: src.URL}, Token: "mb_old"},
		"new": {Server: config.Server{URL: dst.URL}, Token: "mb_new"},
	}}); err != nil {
		t.Fatal(err)
	}

	oldPoll := migrationPoll
	migrationPoll = time.Millisecond
	wsmFrom, wsmTo, flagWorkspace, wsmExpires = "old", "new", "acme", 2*time.Hour
	wsmOpts = importFlags{yes: true, freeze: true, timeout: time.Minute}
	t.Cleanup(func() {
		migrationPoll = oldPoll
		wsmFrom, wsmTo, flagWorkspace = "", "", ""
		wsmOpts, wsiOpts = importFlags{}, importFlags{}
		wsiCode, wsiCodeFile = "", ""
	})
}

func TestWorkspaceMigrateCompletedLeavesCodeToCloseItself(t *testing.T) {
	fx := &migrationFixture{finalRun: "completed"}
	setupMigration(t, fx)

	if err := runWorkspaceMigrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := api.MigrationImport{MigrationSource: api.MigrationSource{Code: testCode}, SourceWorkspace: "acme", RestoreData: true, Freeze: true}
	if fx.imported != want {
		t.Errorf("import body = %+v, want %+v", fx.imported, want)
	}
	if fx.probed.Code != testCode {
		t.Errorf("probe code = %q", fx.probed.Code)
	}
	if fx.cancelled {
		t.Error("a completed import closes the code on the source; the CLI must not cancel")
	}
	// Each side must be called with its own context's token, never the other's.
	for _, tok := range fx.srcTokens {
		if tok != "Bearer mb_old" {
			t.Errorf("source got %q", tok)
		}
	}
	for _, tok := range fx.dstTokens {
		if tok != "Bearer mb_new" {
			t.Errorf("target got %q", tok)
		}
	}
}

func TestWorkspaceMigratePartialCancels(t *testing.T) {
	fx := &migrationFixture{finalRun: "partial"}
	setupMigration(t, fx)

	if err := runWorkspaceMigrate(context.Background()); err != nil {
		t.Fatalf("a partial run warns but succeeds: %v", err)
	}
	if !fx.cancelled {
		t.Error("a partial import leaves the code open; the CLI must cancel it")
	}
}

func TestWorkspaceMigrateRehearsalCancels(t *testing.T) {
	fx := &migrationFixture{finalRun: "completed"}
	setupMigration(t, fx)
	wsmOpts = importFlags{yes: true, rehearse: true, timeout: time.Minute}

	if err := runWorkspaceMigrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !fx.cancelled {
		t.Error("a rehearsal does not close the code; the CLI must cancel it")
	}
}

func TestWorkspaceMigrateFailedRunFails(t *testing.T) {
	fx := &migrationFixture{finalRun: "failed"}
	setupMigration(t, fx)

	err := runWorkspaceMigrate(context.Background())
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want the run's error", err)
	}
	if !fx.cancelled {
		t.Error("the migration must be cancelled after a failed run")
	}
}

func TestWorkspaceMigrateProbeFailureCancels(t *testing.T) {
	fx := &migrationFixture{probeFail: true}
	setupMigration(t, fx)

	err := runWorkspaceMigrate(context.Background())
	if err == nil || !strings.Contains(err.Error(), "source unreachable") {
		t.Fatalf("err = %v, want the probe's error", err)
	}
	if !fx.cancelled || fx.imported.Code != "" {
		t.Errorf("cancelled=%v imported=%+v; want cancelled and no import", fx.cancelled, fx.imported)
	}
}

func TestWorkspaceMigrateRefusesWhenSourceDisabled(t *testing.T) {
	fx := &migrationFixture{srcOff: true}
	setupMigration(t, fx)

	err := runWorkspaceMigrate(context.Background())
	if err == nil || !strings.Contains(err.Error(), "MIABI_MIGRATION_ENABLED") {
		t.Fatalf("err = %v, want a disabled refusal", err)
	}
	if fx.started {
		t.Error("no migration may start when the source has migration off")
	}
}

// With --no-wait the target is still pulling with the code, so cancelling would break the move.
func TestWorkspaceMigrateNoWaitKeepsMigrationOpen(t *testing.T) {
	fx := &migrationFixture{finalRun: "completed"}
	setupMigration(t, fx)
	wsmOpts.noWait = true

	if err := runWorkspaceMigrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fx.cancelled || fx.polls != 0 {
		t.Errorf("cancelled=%v polls=%d; want the migration kept open and no polling", fx.cancelled, fx.polls)
	}
}

func TestWorkspaceImportWithCodeFile(t *testing.T) {
	fx := &migrationFixture{finalRun: "completed"}
	setupMigration(t, fx)
	flagWorkspace = ""
	codeFile := filepath.Join(t.TempDir(), "code.txt")
	if err := os.WriteFile(codeFile, []byte("  "+testCode+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	wsiCodeFile = codeFile
	wsiOpts = importFlags{yes: true, newName: "acme-eu", timeout: time.Minute}

	if err := runWorkspaceImport(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := api.MigrationImport{MigrationSource: api.MigrationSource{Code: testCode}, SourceWorkspace: "acme", NewWorkspace: "acme-eu", RestoreData: true}
	if fx.imported != want {
		t.Errorf("import body = %+v, want %+v", fx.imported, want)
	}
	if len(fx.srcTokens) != 0 {
		t.Errorf("import must only talk to the current context, the source got %d calls", len(fx.srcTokens))
	}
}

func TestWorkspaceImportUnknownWorkspaceDoesNotImport(t *testing.T) {
	fx := &migrationFixture{finalRun: "completed"}
	setupMigration(t, fx)
	flagWorkspace = "beta"
	wsiCode = testCode
	wsiOpts = importFlags{yes: true, timeout: time.Minute}

	if err := runWorkspaceImport(context.Background()); err == nil || !strings.Contains(err.Error(), `"beta"`) {
		t.Fatalf("err = %v, want an unknown-workspace refusal", err)
	}
	if fx.imported.Code != "" {
		t.Error("nothing may be imported after the preview refused")
	}
}

func TestMigrationPaths(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Method+" "+r.URL.Path)
		switch r.Method {
		case http.MethodPost:
			writeJSON(w, api.MigrationStarted{Code: testCode, ExpiresAt: time.Now().Add(time.Hour)})
		case http.MethodGet:
			writeJSON(w, api.MigrationOverview{Enabled: true})
		default:
			writeJSON(w, map[string]any{"cancelled": true})
		}
	}))
	defer srv.Close()
	c, err := api.New(api.Options{BaseURL: srv.URL, Token: "mb_x"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, ws := range []string{"acme", ""} {
		if _, err := c.StartWorkspaceMigration(ctx, ws, api.MigrationStartRequest{ExpiresInHours: 24}); err != nil {
			t.Fatal(err)
		}
		if _, err := c.WorkspaceMigration(ctx, ws); err != nil {
			t.Fatal(err)
		}
		if err := c.CancelWorkspaceMigration(ctx, ws); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{
		"POST /api/v1/workspaces/acme/migration/start", "GET /api/v1/workspaces/acme/migration", "DELETE /api/v1/workspaces/acme/migration",
		"POST /api/v1/admin/migration/start", "GET /api/v1/admin/migration", "DELETE /api/v1/admin/migration",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("paths:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
