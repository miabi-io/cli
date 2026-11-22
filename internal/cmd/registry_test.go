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
	"slices"
	"testing"

	"github.com/miabi-io/cli/internal/api"
)

func TestParseDays(t *testing.T) {
	for in, want := range map[string]int{"30": 30, "30d": 30, "2w": 14, " 7D ": 7} {
		if got, err := parseDays(in); err != nil || got != want {
			t.Errorf("parseDays(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "0", "-3d", "12h", "soon"} {
		if _, err := parseDays(bad); err == nil {
			t.Errorf("parseDays(%q) accepted", bad)
		}
	}
}

func TestDeletableTags(t *testing.T) {
	res := &api.TagDeleteResult{Tags: []api.TagOutcome{
		{Tag: "v1", Result: "would_delete"}, {Tag: "live", Result: "in_use"}, {Tag: "v2", Result: "would_delete"},
	}}
	if got := deletableTags(res); !slices.Equal(got, []string{"v1", "v2"}) {
		t.Fatalf("got %v; an in-use tag is never sent to the real delete", got)
	}
}
