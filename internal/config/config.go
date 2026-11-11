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

// Package config resolves the CLI connection context from flags, env and ~/.miabi/config.yaml, in that order.
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

// WorkspaceRef is the active workspace persisted by `miabi workspace switch`; ID is the durable key.
type WorkspaceRef struct {
	ID          uint   `yaml:"id" json:"id"`
	Name        string `yaml:"name" json:"name"`
	DisplayName string `yaml:"display_name,omitempty" json:"display_name,omitempty"`
}

// Identity is captured at login for offline display only; the token remains the source of truth.
type Identity struct {
	Name     string `yaml:"name,omitempty" json:"name,omitempty"`
	Username string `yaml:"username,omitempty" json:"username,omitempty"`
	Email    string `yaml:"email,omitempty" json:"email,omitempty"`
}

// AppRef is the app bound by `miabi use`; it is cleared whenever the workspace changes.
type AppRef struct {
	ID          uint   `yaml:"id" json:"id"`
	Name        string `yaml:"name" json:"name"`
	DisplayName string `yaml:"display_name,omitempty" json:"display_name,omitempty"`
}

// Server describes how to reach and trust one Miabi control plane.
type Server struct {
	URL string `yaml:"url,omitempty"`
	// CA is a PEM bundle trusted in addition to the system roots.
	CA string `yaml:"ca,omitempty"`
	// InsecureSkip disables TLS verification entirely; never use it against a public server.
	InsecureSkip bool `yaml:"insecure_skip,omitempty"`
}

// Context is one named connection profile (server, token, bound workspace/app).
type Context struct {
	Server    Server        `yaml:"server,omitempty"`
	Token     string        `yaml:"token,omitempty"`
	User      *Identity     `yaml:"user,omitempty"`
	Workspace *WorkspaceRef `yaml:"workspace,omitempty"`
	App       *AppRef       `yaml:"app,omitempty"`
}

// contextYAML mirrors Context without its (Un)marshalers so encoding does not recurse.
type contextYAML struct {
	Server    Server        `yaml:"server,omitempty"`
	Token     string        `yaml:"token,omitempty"`
	User      *Identity     `yaml:"user,omitempty"`
	Workspace *WorkspaceRef `yaml:"workspace,omitempty"`
	App       *AppRef       `yaml:"app,omitempty"`
}

// MarshalYAML base64-encodes the token: obfuscation only, not encryption.
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

// decodeToken passes legacy plaintext tokens through: their `mb_` prefix contains "_", which base64 never does.
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

	// Legacy single-context fields, migrated on Load and cleared so Save never re-emits them.
	LegacyServer    *Server       `yaml:"server,omitempty"`
	LegacyToken     string        `yaml:"token,omitempty"`
	LegacyUser      *Identity     `yaml:"user,omitempty"`
	LegacyWorkspace *WorkspaceRef `yaml:"workspace,omitempty"`
	LegacyApp       *AppRef       `yaml:"app,omitempty"`
	LegacyURL       string        `yaml:"url,omitempty"`
}

// CurrentContext returns the active context, or nil when none is set.
func (f *File) CurrentContext() *Context {
	if f.Current == "" || f.Contexts == nil {
		return nil
	}
	return f.Contexts[f.Current]
}

// EnsureContext returns the named context, creating it if absent, and makes it current.
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

// EnsureCurrent creates a "default" context when none is set, so binding works before login.
func (f *File) EnsureCurrent() *Context {
	if c := f.CurrentContext(); c != nil {
		return c
	}
	return f.EnsureContext("default")
}

// SelectedName picks --context, else MIABI_CONTEXT, else the file's current context.
func (f *File) SelectedName(flag string) string {
	return firstNonEmpty(flag, os.Getenv("MIABI_CONTEXT"), f.Current)
}

// Selected returns the selected context, or nil when none is set or it is unknown.
func (f *File) Selected(flag string) *Context {
	if f.Contexts == nil {
		return nil
	}
	return f.Contexts[f.SelectedName(flag)]
}

// EnsureSelected is EnsureCurrent for the selected context; it never changes `current`.
func (f *File) EnsureSelected(flag string) *Context {
	name := f.SelectedName(flag)
	if name == "" || name == f.Current {
		return f.EnsureCurrent()
	}
	if c := f.Selected(flag); c != nil {
		return c
	}
	if f.Contexts == nil {
		f.Contexts = map[string]*Context{}
	}
	c := &Context{}
	f.Contexts[name] = c
	return c
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

// Path returns MIABI_CONFIG, else ~/.miabi/config.yaml.
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

// Load treats a missing file as empty, so env-only CI never needs one.
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

// Save writes the file with mode 0600 because it holds a token.
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
	Context      string // "" when no context applies
	URL          string
	Token        string
	CA           string
	InsecureSkip bool
	Workspace    *WorkspaceRef
	App          *AppRef
}

// Flags carries the parsed connection flags, which take precedence over env and context.
type Flags struct {
	Context      string
	Server       string // --server or the deprecated --url
	Token        string
	CA           string
	InsecureSkip bool
}

// Resolve drops the context's token and settings when a server override points elsewhere, so they never leak.
func Resolve(fl Flags) (*Effective, error) {
	f, err := Load()
	if err != nil {
		return nil, err
	}
	ctxName := f.SelectedName(fl.Context)
	c := f.Selected(fl.Context)
	if c == nil && firstNonEmpty(fl.Context, os.Getenv("MIABI_CONTEXT")) != "" {
		return nil, fmt.Errorf("context %q not found — see `miabi context list`", ctxName)
	}
	override := firstNonEmpty(fl.Server, os.Getenv("MIABI_SERVER"), os.Getenv("MIABI_URL"))
	if c != nil && override != "" && !sameURL(override, c.Server.URL) {
		c = nil
	}
	var srv Server
	var ctxToken string
	var ws *WorkspaceRef
	var app *AppRef
	if c != nil {
		srv, ctxToken, ws, app = c.Server, c.Token, c.Workspace, c.App
	} else {
		ctxName = ""
	}
	url := firstNonEmpty(override, srv.URL)
	token := firstNonEmpty(fl.Token, os.Getenv("MIABI_TOKEN"), ctxToken)
	ca := firstNonEmpty(fl.CA, os.Getenv("MIABI_CA"), srv.CA)
	insecure := fl.InsecureSkip || envBool("MIABI_INSECURE_SKIP_TLS_VERIFY") || srv.InsecureSkip
	if url == "" {
		return nil, fmt.Errorf("no server URL configured — pass --server, set MIABI_SERVER, or run `miabi login`")
	}
	if token == "" {
		return nil, fmt.Errorf("no API token configured — pass --token, set MIABI_TOKEN, or run `miabi login`")
	}
	return &Effective{Context: ctxName, URL: url, Token: token, CA: ca, InsecureSkip: insecure, Workspace: ws, App: app}, nil
}

func sameURL(a, b string) bool {
	return strings.EqualFold(strings.TrimRight(a, "/"), strings.TrimRight(b, "/"))
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

func envBool(key string) bool {
	b, _ := strconv.ParseBool(os.Getenv(key))
	return b
}
