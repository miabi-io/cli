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
	"strconv"
	"strings"
)

const (
	nodeFlagHelp     = "node to place on, by its fixed name (the node's name, not its display name); a bare number is the admin-only server id"
	locationFlagHelp = "location to create in (default: the workspace's default location)"
)

// A bare number keeps the older admin-only server-id form, so scripts that passed an id still work.
func nodeTarget(v string) (serverID uint, name string) {
	v = strings.TrimSpace(v)
	if n, err := strconv.ParseUint(v, 10, 32); err == nil {
		return uint(n), ""
	}
	return 0, v
}
