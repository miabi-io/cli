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
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/miabi-io/cli/internal/api"
)

func TestFindComposeFileFollowsComposeOrder(t *testing.T) {
	for _, name := range composeFileNames {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, name), "services: {}\n")
		got, warn, err := findComposeFile(dir)
		if err != nil || filepath.Base(got) != name || warn != "" {
			t.Errorf("%s: got %q, warn %q, err %v", name, got, warn, err)
		}
	}

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "docker-compose.yml"), "services: {}\n")
	writeFile(t, filepath.Join(dir, "compose.yaml"), "services: {}\n")
	got, warn, err := findComposeFile(dir)
	if err != nil || filepath.Base(got) != "compose.yaml" || !strings.Contains(warn, "using compose.yaml") {
		t.Errorf("both present: got %q, warn %q, err %v", got, warn, err)
	}

	if _, _, err := findComposeFile(t.TempDir()); err == nil || !strings.Contains(err.Error(), "compose.yaml") {
		t.Errorf("none present: err = %v, want it to name what was looked for", err)
	}
}

func TestLoadComposeSourceEnvRules(t *testing.T) {
	dir := t.TempDir()
	compose := filepath.Join(dir, "app", "compose.yaml")
	writeFile(t, compose, "services: {}\n")
	writeFile(t, filepath.Join(dir, "app", ".env"), "A=1\n")
	other := filepath.Join(dir, "prod.env")
	writeFile(t, other, "A=2\nB=3")

	src, err := loadComposeSource(compose, nil, false)
	if err != nil || src.env != "A=1\n" {
		t.Fatalf("default .env beside the Compose file: env %q, err %v", src.env, err)
	}
	if src, err = loadComposeSource(compose, []string{other}, false); err != nil || src.env != "A=2\nB=3\n" {
		t.Fatalf("--env-file replaces the default: env %q, err %v", src.env, err)
	}
	if src, err = loadComposeSource(compose, nil, true); err != nil || src.env != "" {
		t.Fatalf("--no-env-file: env %q, err %v", src.env, err)
	}

	bare := filepath.Join(dir, "bare", "compose.yaml")
	writeFile(t, bare, "services: {}\n")
	if src, err = loadComposeSource(bare, nil, false); err != nil || src.env != "" {
		t.Fatalf("a missing .env is not an error: env %q, err %v", src.env, err)
	}
}

func TestLoadComposeSourceRefusesOversizedFiles(t *testing.T) {
	dir := t.TempDir()
	compose := filepath.Join(dir, "compose.yaml")
	if err := os.WriteFile(compose, make([]byte, maxImportBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadComposeSource(compose, nil, true); err == nil || !strings.Contains(err.Error(), "1 MiB") {
		t.Fatalf("err = %v, want a size error", err)
	}
}

func TestMergeEnvFilesKeepsOrder(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.env"), filepath.Join(dir, "b.env")
	writeFile(t, a, "KEY=first\n")
	writeFile(t, b, "KEY=second\n")
	got, err := mergeEnvFiles([]string{a, b})
	if err != nil || got != "KEY=first\nKEY=second\n" {
		t.Fatalf("got %q, err %v", got, err)
	}
}

func TestDefaultStackName(t *testing.T) {
	cases := []struct{ compose, dir, want string }{
		{"name: My_App\nservices: {}\n", "/work/ignored", "my-app"},
		{"services: {}\n", "/work/Shop Front", "shop-front"},
		{"name: '!!!'\nservices: {}\n", "/work/fallback", "fallback"},
		{"not: [valid", "/work/broken", "broken"},
	}
	for _, c := range cases {
		if got := defaultStackName([]byte(c.compose), c.dir); got != c.want {
			t.Errorf("defaultStackName(%q, %q) = %q, want %q", c.compose, c.dir, got, c.want)
		}
	}
}

func TestStrictImportErr(t *testing.T) {
	clean := &api.StackImportResult{}
	partial := &api.StackImportResult{Skipped: []api.StackImportSkip{{Service: "web", Reason: "build-only"}}}
	warned := &api.StackImportResult{Warnings: []string{"DB_PASSWORD is not set"}}
	if strictImportErr(partial, false) != nil || strictImportErr(clean, true) != nil {
		t.Error("only --strict with a skip or warning may fail")
	}
	if strictImportErr(partial, true) == nil || strictImportErr(warned, true) == nil {
		t.Error("--strict must fail a partial import")
	}
}

func TestFinishStackDeployWaitsAndReportsFailures(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/workspaces/prod/apps/1/deployments/10":
			_, _ = io.WriteString(w, `{"success":true,"data":{"id":10,"number":1,"status":"succeeded"}}`)
		case "/api/v1/workspaces/prod/apps/2/deployments/20":
			_, _ = io.WriteString(w, `{"success":true,"data":{"id":20,"number":4,"status":"failed","error":"image not found"}}`)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	c, err := api.New(api.Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}

	stackWait, stackTimeout = true, time.Minute
	defer func() { stackWait = false }()
	deploys := []api.StackDeployResult{
		{AppID: 1, AppName: "web", Status: "queued", DeploymentID: 10},
		{AppID: 2, AppName: "worker", Status: "queued", DeploymentID: 20},
		{AppID: 3, AppName: "cron", Status: api.StatusFailed, Error: "no image"},
	}
	err = finishStackDeploy(context.Background(), c, "prod", deploys)
	if err == nil || !strings.Contains(err.Error(), "2 of 3") {
		t.Fatalf("err = %v, want 2 of 3 failed", err)
	}
}

func TestStackCommandRouting(t *testing.T) {
	imp, _, err := rootCmd.Find([]string{"stack", "import"})
	if err != nil || imp != stackImportCmd {
		t.Fatalf("stack import resolved to %v (%v), want the stacks import command", imp, err)
	}
	for _, path := range [][]string{{"stack", "status"}, {"stack", "env", "set"}, {"stack", "migrate-config"}} {
		c, _, err := rootCmd.Find(path)
		if err != nil || c.Name() != path[len(path)-1] || c.Deprecated == "" {
			t.Errorf("%v: got %v (%v), want a deprecated host command", path, c, err)
		}
	}
	if c, _, _ := rootCmd.Find([]string{"stack", "env", "set"}); !strings.Contains(c.Deprecated, "miabi system env set") {
		t.Errorf("stack env set deprecation = %q, want it to name miabi system env set", c.Deprecated)
	}
	for _, path := range [][]string{{"system", "status"}, {"system", "env", "set"}, {"system", "config", "migrate"}, {"sys", "upgrade"}} {
		c, _, err := rootCmd.Find(path)
		if err != nil || c.Name() != path[len(path)-1] || c.Deprecated != "" {
			t.Errorf("%v: got %v (%v), want a current command", path, c, err)
		}
	}
}
