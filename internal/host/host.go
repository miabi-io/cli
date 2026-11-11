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

// Package host keeps everything Docker-specific behind Open, stubbed on platforms that cannot host a stack.
package host

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/miabi-io/miabi/pkg/stack"
)

var ErrUnsupported = errors.New("stack management requires a Linux or macOS host with Docker")

type Session struct {
	Svc      *stack.Service
	Manifest string
	closer   func() error
}

func (s *Session) Close() error {
	if s == nil || s.closer == nil {
		return nil
	}
	return s.closer()
}

func ManifestPath(flag string) string {
	if p := strings.TrimSpace(flag); p != "" {
		return p
	}
	return stack.ManifestPath()
}

// RequireWritable fails early rather than surfacing a permission error from three layers down.
func RequireWritable(path string) error {
	dir := filepath.Dir(path)
	if writable(dir) {
		return nil
	}

	if _, err := os.Stat(dir); os.IsNotExist(err) && writable(filepath.Dir(dir)) {
		return nil
	}
	return fmt.Errorf("cannot write %s\n\n"+
		"  Either re-run with sudo, or install under your own account:\n"+
		"    miabi setup --file %s --domain …", path, userPathHint())
}

// RequireReadable names ownership outright: the manifest is mode 0600 because it holds the DB password.
func RequireReadable(path string) error {
	f, err := os.Open(path)
	if err == nil {
		_ = f.Close()
		return nil
	}
	if os.IsNotExist(err) {
		return nil
	}
	return fmt.Errorf("cannot read %s — it is owned by the user that installed it (mode 0600)\n\n"+
		"  Re-run with sudo, or point at your own install:  --file %s", path, userPathHint())
}

// OtherInstall exists because two installs on one daemon fight over the same container, volume and network names.
func OtherInstall(path string) string {
	for _, p := range []string{stack.DefaultConfigPath, UserManifestPath()} {
		if p == "" || p == path {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// writable tries a write rather than reasoning about modes and groups, the only answer that is never wrong.
func writable(dir string) bool {
	f, err := os.CreateTemp(dir, ".miabi-write-check-*")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return true
}

// UserManifestPath mirrors stack.UserConfigPath; fold it into that call on the next dependency bump.
func UserManifestPath() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	return filepath.Join(home, ".miabi", "miabi.yaml")
}

func userPathHint() string {
	if p := UserManifestPath(); p != "" {
		return p
	}
	return "~/.miabi/miabi.yaml"
}
