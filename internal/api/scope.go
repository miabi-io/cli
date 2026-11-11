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

package api

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
)

// scopeRequiredHeader is set in warn mode, where the request still succeeds.
const scopeRequiredHeader = "X-Miabi-Scope-Required"

// An enforce-mode scope 403 carries no header or code, so this message prefix is the only signal.
const scopeDeniedPrefix = "API key missing required scope:"

// ScopeError is a 403 for a missing API key scope, not the caller's workspace role.
type ScopeError struct {
	*APIError
	Scope string
}

func (e *ScopeError) Error() string {
	return fmt.Sprintf("this token lacks the %q scope required for this action. %s", e.Scope, scopeRemedy(e.Scope))
}

func (e *ScopeError) Unwrap() error { return e.APIError }

func scopeRemedy(scope string) string {
	switch scope {
	case "read", "write", "deploy":
		return fmt.Sprintf("Log in again with it, e.g. `miabi login --scopes read,write,deploy` (plain `miabi login` grants all three), or use an API key that carries %q.", scope)
	default:
		return fmt.Sprintf("Login tokens never carry %q: admin-only actions (revealing secrets and configs, database credentials) need an API key with the admin scope, created in the web console, set via MIABI_TOKEN or `miabi login --token`.", scope)
	}
}

func apiError(e *APIError, status int) error {
	if e.StatusCode == 0 {
		e.StatusCode = status
	}
	if e.StatusCode == http.StatusForbidden {
		for _, msg := range []string{e.Message, e.Detail} {
			if rest, ok := strings.CutPrefix(msg, scopeDeniedPrefix); ok {
				if scope := strings.TrimSpace(rest); scope != "" {
					return &ScopeError{APIError: e, Scope: scope}
				}
			}
		}
	}
	return e
}

var warnedScopes sync.Map

// warnScope warns once per scope that a request passed only because enforcement is in warn mode.
func warnScope(scope string) {
	if scope == "" {
		return
	}
	if _, seen := warnedScopes.LoadOrStore(scope, true); seen {
		return
	}
	fmt.Fprintf(os.Stderr, "warning: this token lacks the %q scope; the server allows it for now but will refuse it once scope enforcement is on. %s\n", scope, scopeRemedy(scope))
}
