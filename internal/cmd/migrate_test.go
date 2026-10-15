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
	"testing"

	"github.com/miabi-io/cli/internal/api"
)

func TestParseDBChoices(t *testing.T) {
	plan := &api.MigrationPlan{Databases: []api.MigrationDBItem{
		{InstanceID: 3, InstanceName: "shop-db"},
		{InstanceID: 5, InstanceName: "cache"},
	}}
	got, err := parseDBChoices([]string{"shop-db=existing:42", "cache=move"}, plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != (api.MigrationDBChoice{InstanceID: 3, Strategy: "existing_instance", TargetInstanceID: 42}) ||
		got[1] != (api.MigrationDBChoice{InstanceID: 5, Strategy: "move"}) {
		t.Errorf("got %+v", got)
	}
	for _, bad := range []string{"shop-db", "nope=move", "shop-db=teleport", "shop-db=existing", "shop-db=existing:x"} {
		if _, err := parseDBChoices([]string{bad}, plan); err == nil {
			t.Errorf("%q should be refused", bad)
		}
	}
}
