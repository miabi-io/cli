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

//go:build !linux && !darwin

package host

import "context"

// Open always fails here: the commands stay visible in --help on every platform so the error
// explains the constraint, rather than the verb simply not existing.
func Open(string, func(string, ...any)) (*Session, error) { return nil, ErrUnsupported }

// InspectSelfImage has nothing to inspect without a Docker host.
func InspectSelfImage(context.Context, string) string { return "" }
