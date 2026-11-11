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

package config

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withConfig points MIABI_CONFIG at a temp file seeded with content, and returns
// its path.
func withConfig(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if content != "" {
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("MIABI_CONFIG", p)
	// Isolate from the developer's real environment.
	for _, k := range []string{"MIABI_SERVER", "MIABI_URL", "MIABI_TOKEN", "MIABI_CA", "MIABI_CONTEXT", "MIABI_INSECURE_SKIP_TLS_VERIFY"} {
		t.Setenv(k, "")
	}
	return p
}

func TestMigrateFlatURLToDefaultContext(t *testing.T) {
	withConfig(t, "url: https://old.example.com\ntoken: mb_legacy\nworkspace:\n  id: 1\n  name: web\n")

	f, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if f.Current != "default" {
		t.Fatalf("current = %q, want default", f.Current)
	}
	c := f.Contexts["default"]
	if c == nil || c.Server.URL != "https://old.example.com" || c.Token != "mb_legacy" {
		t.Fatalf("legacy not migrated: %+v", c)
	}
	if c.Workspace == nil || c.Workspace.Name != "web" {
		t.Fatalf("workspace not migrated: %+v", c)
	}
}

func TestMigrateServerBlockToDefaultContext(t *testing.T) {
	// The intermediate single-`server:` form also migrates.
	withConfig(t, "server:\n  url: https://m.example.com\n  insecure_skip: true\ntoken: t\n")
	f, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	c := f.Contexts["default"]
	if c == nil || c.Server.URL != "https://m.example.com" || !c.Server.InsecureSkip {
		t.Fatalf("server block not migrated: %+v", c)
	}
}

func TestSaveWritesContextsNotLegacy(t *testing.T) {
	p := withConfig(t, "")
	f := &File{}
	c := f.EnsureContext("prod")
	c.Server = Server{URL: "https://prod.example.com", CA: "/etc/ca.pem", InsecureSkip: true}
	c.Token = "mb_prod"
	if err := Save(f); err != nil {
		t.Fatal(err)
	}
	s := string(mustRead(t, p))
	for _, want := range []string{"current: prod", "contexts:", "prod:", "url: https://prod.example.com", "ca: /etc/ca.pem", "insecure_skip: true"} {
		if !strings.Contains(s, want) {
			t.Errorf("saved config missing %q:\n%s", want, s)
		}
	}
	// No legacy flat keys at the top level.
	if strings.HasPrefix(s, "url:") || strings.HasPrefix(s, "server:") || strings.HasPrefix(s, "token:") {
		t.Errorf("saved config still writes legacy top-level keys:\n%s", s)
	}
}

func TestResolveUsesCurrentContext(t *testing.T) {
	withConfig(t, `current: prod
contexts:
  prod:
    server: {url: https://prod.example.com, ca: /prod/ca.pem, insecure_skip: true}
    token: prod_token
    workspace: {id: 2, name: api}
  dev:
    server: {url: https://dev.example.com}
    token: dev_token
`)
	eff, err := Resolve(Flags{})
	if err != nil {
		t.Fatal(err)
	}
	if eff.URL != "https://prod.example.com" || eff.Token != "prod_token" || eff.CA != "/prod/ca.pem" || !eff.InsecureSkip {
		t.Fatalf("current-context resolution wrong: %+v", eff)
	}
	if eff.Workspace == nil || eff.Workspace.Name != "api" {
		t.Fatalf("workspace not carried from context: %+v", eff.Workspace)
	}
}

func TestResolveContextFlagSelectsAnother(t *testing.T) {
	withConfig(t, `current: prod
contexts:
  prod: {server: {url: https://prod.example.com}, token: p}
  dev:  {server: {url: https://dev.example.com},  token: d}
`)
	eff, err := Resolve(Flags{Context: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	if eff.URL != "https://dev.example.com" || eff.Token != "d" {
		t.Fatalf("--context did not select dev: %+v", eff)
	}
}

func TestResolveFlagsOverrideContext(t *testing.T) {
	withConfig(t, "current: prod\ncontexts:\n  prod: {server: {url: https://prod.example.com}, token: p}\n")
	eff, err := Resolve(Flags{Server: "https://flag.example.com", Token: "flagtok"})
	if err != nil {
		t.Fatal(err)
	}
	if eff.URL != "https://flag.example.com" || eff.Token != "flagtok" {
		t.Fatalf("flags did not override context: %+v", eff)
	}
}

func TestResolveEnvOnlyNoConfig(t *testing.T) {
	// No config file at all: env alone must resolve (CI path).
	withConfig(t, "")
	t.Setenv("MIABI_SERVER", "https://ci.example.com")
	t.Setenv("MIABI_TOKEN", "ci_token")
	eff, err := Resolve(Flags{})
	if err != nil {
		t.Fatal(err)
	}
	if eff.URL != "https://ci.example.com" || eff.Token != "ci_token" {
		t.Fatalf("env-only resolution wrong: %+v", eff)
	}
}

func TestEnsureContextSetsCurrent(t *testing.T) {
	f := &File{}
	c := f.EnsureContext("staging")
	if f.Current != "staging" || f.Contexts["staging"] != c {
		t.Fatalf("EnsureContext did not create+select: current=%q", f.Current)
	}
	// EnsureCurrent returns it without creating another.
	if got := f.EnsureCurrent(); got != c {
		t.Fatal("EnsureCurrent created a new context instead of returning current")
	}
}

func TestTokenStoredBase64RoundTrips(t *testing.T) {
	p := withConfig(t, "")
	f := &File{}
	f.EnsureContext("prod").Token = "mb_secrettoken"
	if err := Save(f); err != nil {
		t.Fatal(err)
	}
	// On disk: base64, never the plaintext token.
	s := string(mustRead(t, p))
	if strings.Contains(s, "mb_secrettoken") {
		t.Fatalf("plaintext token leaked into the file:\n%s", s)
	}
	if !strings.Contains(s, base64.StdEncoding.EncodeToString([]byte("mb_secrettoken"))) {
		t.Fatalf("token not base64-encoded in the file:\n%s", s)
	}
	// In memory after reload: plaintext.
	f2, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := f2.Contexts["prod"].Token; got != "mb_secrettoken" {
		t.Fatalf("token did not round-trip: got %q", got)
	}
}

func TestLegacyPlaintextTokenUnderContextStillReads(t *testing.T) {
	// A context whose token was written plaintext before base64 (mb_ prefix) must
	// still load correctly.
	withConfig(t, "current: prod\ncontexts:\n  prod:\n    server: {url: https://x}\n    token: mb_plainlegacy\n")
	f, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := f.Contexts["prod"].Token; got != "mb_plainlegacy" {
		t.Fatalf("legacy plaintext token not read: got %q", got)
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestResolveEnvContextSelectsAnother(t *testing.T) {
	withConfig(t, `current: prod
contexts:
  prod: {server: {url: https://prod.example.com}, token: p, workspace: {id: 1, name: live}}
  dev:  {server: {url: https://dev.example.com},  token: d, workspace: {id: 2, name: test}}
`)
	t.Setenv("MIABI_CONTEXT", "dev")
	eff, err := Resolve(Flags{})
	if err != nil {
		t.Fatal(err)
	}
	if eff.Context != "dev" || eff.URL != "https://dev.example.com" || eff.Token != "d" || eff.Workspace.Name != "test" {
		t.Fatalf("MIABI_CONTEXT did not select dev: %+v", eff)
	}
}

func TestResolveUnknownExplicitContextFails(t *testing.T) {
	withConfig(t, "current: prod\ncontexts:\n  prod: {server: {url: https://prod.example.com}, token: p}\n")
	if _, err := Resolve(Flags{Context: "typo"}); err == nil || !strings.Contains(err.Error(), `"typo"`) {
		t.Fatalf("unknown --context: err = %v", err)
	}
	t.Setenv("MIABI_CONTEXT", "typo")
	if _, err := Resolve(Flags{}); err == nil {
		t.Fatal("unknown MIABI_CONTEXT fell back to the current context")
	}
}

func TestResolveOtherServerDropsContextValues(t *testing.T) {
	withConfig(t, `current: prod
contexts:
  prod:
    server: {url: https://prod.example.com, ca: /prod/ca.pem, insecure_skip: true}
    token: prod_token
    workspace: {id: 1, name: live}
    app: {id: 3, name: web}
`)
	t.Setenv("MIABI_TOKEN", "")
	if _, err := Resolve(Flags{Server: "https://other.example.com"}); err == nil {
		t.Fatal("the prod token was sent to another server")
	}
	eff, err := Resolve(Flags{Server: "https://other.example.com", Token: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if eff.Context != "" || eff.CA != "" || eff.InsecureSkip || eff.Workspace != nil || eff.App != nil {
		t.Fatalf("context values leaked to another server: %+v", eff)
	}
	eff, err = Resolve(Flags{Server: "https://PROD.example.com/"})
	if err != nil {
		t.Fatal(err)
	}
	if eff.Context != "prod" || eff.Token != "prod_token" || eff.Workspace == nil {
		t.Fatalf("same server should keep the context: %+v", eff)
	}
}

func TestEnsureSelectedKeepsCurrent(t *testing.T) {
	withConfig(t, "current: prod\ncontexts:\n  prod: {server: {url: https://prod.example.com}, token: p}\n  dev: {server: {url: https://dev.example.com}, token: d}\n")
	f, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	f.EnsureSelected("dev").Workspace = &WorkspaceRef{ID: 2, Name: "test"}
	if f.Current != "prod" || f.Contexts["dev"].Workspace == nil || f.Contexts["prod"].Workspace != nil {
		t.Fatalf("binding landed in the wrong context: current=%s prod=%+v dev=%+v", f.Current, f.Contexts["prod"], f.Contexts["dev"])
	}
}
