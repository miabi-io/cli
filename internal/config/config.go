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

// Package config resolves the CLI's connection context — the server URL and how
// to trust it, the API token, and the active workspace — from flags, environment,
// and ~/.miabi/config.yaml, in that precedence. The token is never logged.
package config

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// WorkspaceRef is the active workspace persisted by `miabi workspace switch`.
// ID is the durable key (paths address by it); Name is the unique handle and
// DisplayName the free-text label, kept for display.
type WorkspaceRef struct {
	ID          uint   `yaml:"id" json:"id"`
	Name        string `yaml:"name" json:"name"`
	DisplayName string `yaml:"display_name,omitempty" json:"display_name,omitempty"`
}

// Identity is the authenticated user, captured at `miabi login` for offline
// display (`whoami`/`workspace show`). The token remains the source of truth.
type Identity struct {
	Name     string `yaml:"name,omitempty" json:"name,omitempty"` // display name
	Username string `yaml:"username,omitempty" json:"username,omitempty"`
	Email    string `yaml:"email,omitempty" json:"email,omitempty"`
}

// AppRef is the current application bound by `miabi use <app>`, so app-scoped
// commands need no explicit argument. It belongs to the active Workspace and is
// cleared whenever the workspace changes. Name is the unique handle commands
// address by; DisplayName is the free-text label, kept for display.
type AppRef struct {
	ID          uint   `yaml:"id" json:"id"`
	Name        string `yaml:"name" json:"name"`
	DisplayName string `yaml:"display_name,omitempty" json:"display_name,omitempty"`
}

// Server describes how to reach and trust one Miabi control plane: its base URL,
// an optional custom CA bundle to trust, and an escape hatch to skip TLS
// verification (self-signed / homelab).
type Server struct {
	URL string `yaml:"url,omitempty"`
	// CA is a path to a PEM CA bundle to trust in addition to the system roots
	// (for a private/self-signed control-plane certificate). Empty uses system roots.
	CA string `yaml:"ca,omitempty"`
	// InsecureSkip disables TLS verification entirely. A blunt instrument for dev;
	// prefer CA. Never use against a public server.
	InsecureSkip bool `yaml:"insecure_skip,omitempty"`
}

// Context is one named connection profile: how to reach and trust a Miabi server,
// the token to authenticate with, and the workspace/app bound for that server.
// Multiple contexts let you switch between Miabi instances (dev / staging / prod)
// with `miabi context use`.
type Context struct {
	Server    Server        `yaml:"server,omitempty"`
	Token     string        `yaml:"token,omitempty"`
	User      *Identity     `yaml:"user,omitempty"`
	Workspace *WorkspaceRef `yaml:"workspace,omitempty"`
	App       *AppRef       `yaml:"app,omitempty"`
}

// The token is stored base64-encoded in the file so it isn't sitting in plain
// text (light obfuscation — NOT encryption; anyone who can read the file, which
// is mode 0600, can decode it). Code always sees the plaintext token: Context's
// YAML methods encode on write and decode on read, transparently, and still read
// legacy plaintext tokens written before this change.

// contextYAML mirrors Context but has no custom (Un)marshaler, so it drives the
// default field-based encoding without recursing back into Context's methods.
type contextYAML struct {
	Server    Server        `yaml:"server,omitempty"`
	Token     string        `yaml:"token,omitempty"`
	User      *Identity     `yaml:"user,omitempty"`
	Workspace *WorkspaceRef `yaml:"workspace,omitempty"`
	App       *AppRef       `yaml:"app,omitempty"`
}

func (c Context) MarshalYAML() (any, error) {
	y := contextYAML(c)
	if y.Token != "" {
		y.Token = base64.StdEncoding.EncodeToString([]byte(y.Token))
	}
	return y, nil
}

func (c *Context) UnmarshalYAML(node *yaml.Node) error {
	var y contextYAML
	if err := node.Decode(&y); err != nil {
		return err
	}
	y.Token = decodeToken(y.Token)
	*c = Context(y)
	return nil
}

// decodeToken returns the plaintext token from its stored form. A legacy plaintext
// Miabi token contains "_" (the `mb_` prefix), which standard base64 never does,
// so it is returned as-is; anything else is base64-decoded (falling back to the
// raw value if it isn't valid base64).
func decodeToken(raw string) string {
	if raw == "" || strings.Contains(raw, "_") {
		return raw
	}
	if dec, err := base64.StdEncoding.DecodeString(raw); err == nil {
		return string(dec)
	}
	return raw
}

// File mirrors ~/.miabi/config.yaml: a set of named contexts and the active one.
type File struct {
	Current  string              `yaml:"current,omitempty"`
	Contexts map[string]*Context `yaml:"contexts,omitempty"`

	// --- Legacy single-context fields (pre-multi-context). Read for backward
	// compat and migrated into a context on Load, then cleared so Save writes only
	// the multi-context form. Pointers/omitempty so they never re-emit. ---
	LegacyServer    *Server       `yaml:"server,omitempty"`
	LegacyToken     string        `yaml:"token,omitempty"`
	LegacyUser      *Identity     `yaml:"user,omitempty"`
	LegacyWorkspace *WorkspaceRef `yaml:"workspace,omitempty"`
	LegacyApp       *AppRef       `yaml:"app,omitempty"`
	LegacyURL       string        `yaml:"url,omitempty"` // oldest flat form
}

// CurrentContext returns the active context, or nil when none is set (e.g. a
// fresh install, or env-only CI use with no config file).
func (f *File) CurrentContext() *Context {
	if f.Current == "" || f.Contexts == nil {
		return nil
	}
	return f.Contexts[f.Current]
}

// EnsureContext returns the named context, creating it (and the map) if absent,
// and makes it the current one. Used by login to write a connection profile.
func (f *File) EnsureContext(name string) *Context {
	if f.Contexts == nil {
		f.Contexts = map[string]*Context{}
	}
	c := f.Contexts[name]
	if c == nil {
		c = &Context{}
		f.Contexts[name] = c
	}
	f.Current = name
	return c
}

// EnsureCurrent returns the current context, creating a "default" one when none
// is set — so binding a workspace/app works even before an explicit login.
func (f *File) EnsureCurrent() *Context {
	if c := f.CurrentContext(); c != nil {
		return c
	}
	return f.EnsureContext("default")
}

// ContextNames returns the context names sorted, for listing.
func (f *File) ContextNames() []string {
	names := make([]string, 0, len(f.Contexts))
	for n := range f.Contexts {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// migrate folds the oldest flat `url:` and the single-context legacy fields into
// a "default" context, then clears them so Save writes only the new form.
func (f *File) migrate() {
	if f.LegacyURL != "" {
		if f.LegacyServer == nil {
			f.LegacyServer = &Server{URL: f.LegacyURL}
		} else if f.LegacyServer.URL == "" {
			f.LegacyServer.URL = f.LegacyURL
		}
	}
	hasLegacy := f.LegacyServer != nil || f.LegacyToken != "" || f.LegacyUser != nil || f.LegacyWorkspace != nil || f.LegacyApp != nil
	if len(f.Contexts) == 0 && hasLegacy {
		var srv Server
		if f.LegacyServer != nil {
			srv = *f.LegacyServer
		}
		f.Contexts = map[string]*Context{"default": {
			Server: srv, Token: f.LegacyToken, User: f.LegacyUser,
			Workspace: f.LegacyWorkspace, App: f.LegacyApp,
		}}
		if f.Current == "" {
			f.Current = "default"
		}
	}
	f.LegacyServer, f.LegacyToken, f.LegacyUser, f.LegacyWorkspace, f.LegacyApp, f.LegacyURL = nil, "", nil, nil, nil, ""
}

// Path returns the config file location (honoring MIABI_CONFIG, else
// ~/.miabi/config.yaml).
func Path() string {
	if p := os.Getenv("MIABI_CONFIG"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".miabi-config.yaml"
	}
	return filepath.Join(home, ".miabi", "config.yaml")
}

// Load reads the config file. A missing file is not an error (returns an empty
// File), so CI that uses only env vars never needs one.
func Load() (*File, error) {
	data, err := os.ReadFile(Path())
	if err != nil {
		if os.IsNotExist(err) {
			return &File{}, nil
		}
		return nil, err
	}
	var f File
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", Path(), err)
	}
	f.migrate()
	return &f, nil
}

// Save writes the config file, creating ~/.miabi with 0700 and the file with
// 0600 (it holds a token).
func Save(f *File) error {
	p := Path()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	data, err := yaml.Marshal(f)
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o600)
}

// Effective is the resolved context a command runs against.
type Effective struct {
	URL          string
	Token        string
	CA           string        // path to a CA bundle; may be empty (system roots)
	InsecureSkip bool          // skip TLS verification
	Workspace    *WorkspaceRef // from the file; may be nil
	App          *AppRef       // current app bound by `miabi use`; may be nil
}

// Flags carries the connection flags a command parsed, for Resolve to apply at
// the top of the precedence chain (flags → env → active context).
type Flags struct {
	Context      string // --context (select which context to use for this call)
	Server       string // --server (or the deprecated --url)
	Token        string // --token
	CA           string // --certificate-authority
	InsecureSkip bool   // --insecure-skip-tls-verify
}

// Resolve resolves the connection context: it picks the active context
// (--context → MIABI_CONTEXT → the file's `current`) and applies precedence
// flags → env → that context's values on top. Flags/env alone are enough (no
// context needed), so env-only CI keeps working. MIABI_SERVER is preferred over
// the legacy MIABI_URL. A missing/unknown context is not an error here — the
// generic "no server configured" surfaces if nothing else supplies one.
func Resolve(fl Flags) (*Effective, error) {
	f, err := Load()
	if err != nil {
		return nil, err
	}
	ctxName := firstNonEmpty(fl.Context, os.Getenv("MIABI_CONTEXT"), f.Current)
	var srv Server
	var ctxToken string
	var ws *WorkspaceRef
	var app *AppRef
	if ctxName != "" && f.Contexts != nil {
		if c := f.Contexts[ctxName]; c != nil {
			srv, ctxToken, ws, app = c.Server, c.Token, c.Workspace, c.App
		}
	}
	url := firstNonEmpty(fl.Server, os.Getenv("MIABI_SERVER"), os.Getenv("MIABI_URL"), srv.URL)
	token := firstNonEmpty(fl.Token, os.Getenv("MIABI_TOKEN"), ctxToken)
	ca := firstNonEmpty(fl.CA, os.Getenv("MIABI_CA"), srv.CA)
	insecure := fl.InsecureSkip || envBool("MIABI_INSECURE_SKIP_TLS_VERIFY") || srv.InsecureSkip
	if url == "" {
		return nil, fmt.Errorf("no server URL configured — pass --server, set MIABI_SERVER, or run `miabi login`")
	}
	if token == "" {
		return nil, fmt.Errorf("no API token configured — pass --token, set MIABI_TOKEN, or run `miabi login`")
	}
	return &Effective{URL: url, Token: token, CA: ca, InsecureSkip: insecure, Workspace: ws, App: app}, nil
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

// envBool parses a boolean env var, defaulting to false on unset/garbage.
func envBool(key string) bool {
	b, _ := strconv.ParseBool(os.Getenv(key))
	return b
}
