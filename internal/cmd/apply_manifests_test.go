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
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReadManifestsReadsADirectoryLikeAGitSource(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "jobs.yaml"), "kind: Job\n")
	writeFile(t, filepath.Join(dir, "app.yml"), "kind: Application\n")
	writeFile(t, filepath.Join(dir, "databases", "db.YAML"), "kind: Database\n")
	writeFile(t, filepath.Join(dir, "README.md"), "kind: Ignored\n")
	writeFile(t, filepath.Join(dir, "empty.yaml"), "\n")

	got, err := readManifests([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	want := "kind: Application\n---\nkind: Database\n---\nkind: Job"
	if got != want {
		t.Fatalf("bundle =\n%s\nwant\n%s", got, want)
	}
}

func TestReadManifestsMixesFilesAndDirectories(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "base", "app.yaml"), "kind: Application\n")
	extra := filepath.Join(dir, "route.yaml")
	writeFile(t, extra, "kind: Route\n")

	got, err := readManifests([]string{extra, filepath.Join(dir, "base")})
	if err != nil {
		t.Fatal(err)
	}
	if got != "kind: Route\n---\nkind: Application" {
		t.Fatalf("bundle = %q", got)
	}
}

func TestReadManifestsRefusesADirectoryWithoutManifests(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "notes.txt"), "nothing\n")

	_, err := readManifests([]string{dir})
	if err == nil || !strings.Contains(err.Error(), "no .yaml or .yml files") {
		t.Fatalf("err = %v, want a no-manifests error", err)
	}
}

func TestReadManifestsReportsAMissingPath(t *testing.T) {
	_, err := readManifests([]string{filepath.Join(t.TempDir(), "missing.yaml")})
	if err == nil || !strings.Contains(err.Error(), "missing.yaml") {
		t.Fatalf("err = %v, want it to name the path", err)
	}
}
