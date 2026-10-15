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
	"fmt"

	"github.com/miabi-io/cli/internal/ui"
)

// cliUI renders stackcmd's output the way the rest of the CLI looks — colored, with the same
// symbols as every other command. The server image implements the same interface with plain fmt,
// which is the only thing that should differ between the two front-ends.
type cliUI struct{}

func (cliUI) Printf(format string, a ...any)  { fmt.Printf(format, a...) }
func (cliUI) Info(format string, a ...any)    { ui.Info(format, a...) }
func (cliUI) Success(format string, a ...any) { ui.Success(format, a...) }
func (cliUI) Warn(format string, a ...any)    { ui.Warn(format, a...) }
func (cliUI) Confirm(prompt string) bool      { return ui.Confirm(prompt) }
