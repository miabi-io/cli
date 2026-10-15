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

package release

import (
	"context"
	"testing"
)

// TestLatestLive hits versions.miabi.io (or GitHub as the fallback). Skipped in -short so CI and offline work stay green;
// run with `go test ./internal/release -run Live -v` to check the contract against production.
func TestLatestLive(t *testing.T) {
	if testing.Short() {
		t.Skip("network test")
	}
	v, err := Latest(context.Background(), "miabi-cli/test")
	if err != nil {
		t.Skipf("release lookup unreachable: %v", err)
	}
	if v == "" || v[0] == 'v' {
		t.Fatalf("Latest() = %q; want a bare version with no leading v", v)
	}
	t.Logf("latest published Miabi release: %s", v)
}

func TestNormalize(t *testing.T) {
	for in, want := range map[string]string{
		"v1.8.0": "1.8.0", "1.8.0": "1.8.0", "V1.8.0": "1.8.0",
		"v1.8.0-rc.1": "1.8.0-rc.1", " v1.7.3 ": "1.7.3",
		"vnext": "vnext", "": "", // only a "v" before a digit is a version prefix
	} {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}
