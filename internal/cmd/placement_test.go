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

import "testing"

func TestNodeTarget(t *testing.T) {
	for in, want := range map[string]struct {
		id   uint
		name string
	}{
		"":         {0, ""},
		"3":        {3, ""},
		" node-2 ": {0, "node-2"},
		"node-2":   {0, "node-2"},
	} {
		if id, name := nodeTarget(in); id != want.id || name != want.name {
			t.Errorf("nodeTarget(%q) = %d, %q; want %d, %q", in, id, name, want.id, want.name)
		}
	}
}
