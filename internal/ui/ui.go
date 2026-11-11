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

// Package ui degrades to plain text when output is not a terminal or color is disabled.
package ui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

var colorEnabled = envColor() && IsTerminal()

// See https://no-color.org: NO_COLOR with any value disables color.
func envColor() bool {
	return os.Getenv("NO_COLOR") == "" && os.Getenv("MIABI_NO_COLOR") == ""
}

func IsTerminal() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// SetColor overrides auto-detection, but color is still only honored on a TTY.
func SetColor(on bool) { colorEnabled = on && IsTerminal() }

const (
	cReset   = "\033[0m"
	cBold    = "\033[1m"
	cDim     = "\033[2m"
	cRed     = "\033[31m"
	cGreen   = "\033[32m"
	cYellow  = "\033[33m"
	cBlue    = "\033[34m"
	cMagenta = "\033[35m"
	cCyan    = "\033[36m"
)

func paint(code, s string) string {
	if !colorEnabled {
		return s
	}
	return code + s + cReset
}

func Bold(s string) string    { return paint(cBold, s) }
func Dim(s string) string     { return paint(cDim, s) }
func Red(s string) string     { return paint(cRed, s) }
func Green(s string) string   { return paint(cGreen, s) }
func Yellow(s string) string  { return paint(cYellow, s) }
func Cyan(s string) string    { return paint(cCyan, s) }
func Magenta(s string) string { return paint(cMagenta, s) }

func Success(format string, a ...any) {
	fmt.Fprintln(os.Stdout, Green("✓")+" "+fmt.Sprintf(format, a...))
}

func Info(format string, a ...any) {
	fmt.Fprintln(os.Stdout, Cyan("→")+" "+fmt.Sprintf(format, a...))
}

func Detail(format string, a ...any) {
	fmt.Fprintln(os.Stdout, fmt.Sprintf(format, a...))
}

func Warn(format string, a ...any) {
	fmt.Fprintln(os.Stderr, Yellow("!")+" "+fmt.Sprintf(format, a...))
}

func Fail(format string, a ...any) {
	fmt.Fprintln(os.Stderr, Red("✗")+" "+fmt.Sprintf(format, a...))
}

func Status(s string) string {
	switch strings.ToLower(s) {
	case "succeeded", "completed", "running", "active", "healthy":
		return Green(s)
	case "failed", "error", "crashed", "unhealthy":
		return Red(s)
	default:
		return Yellow(s)
	}
}

func Age(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Since(t)
	switch {
	case d < 0:
		return "just now"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

type Table struct {
	headers []string
	rows    [][]string
}

func NewTable(headers ...string) *Table { return &Table{headers: headers} }

func (t *Table) Row(cells ...any) {
	r := make([]string, len(cells))
	for i, c := range cells {
		r[i] = fmt.Sprintf("%v", c)
	}
	t.rows = append(t.rows, r)
}

func (t *Table) Render(w io.Writer) {
	widths := make([]int, len(t.headers))
	for i, h := range t.headers {
		widths[i] = visibleWidth(h)
	}
	for _, r := range t.rows {
		for i, c := range r {
			if i < len(widths) {
				if vw := visibleWidth(c); vw > widths[i] {
					widths[i] = vw
				}
			}
		}
	}
	var b strings.Builder
	var line strings.Builder
	for i, h := range t.headers {
		line.WriteString(pad(h, widths[i], i == len(t.headers)-1))
	}
	b.WriteString(Dim(strings.TrimRight(line.String(), " ")))
	b.WriteByte('\n')
	for _, r := range t.rows {
		line.Reset()
		for i, c := range r {
			line.WriteString(pad(c, widths[i], i == len(t.headers)-1))
		}
		b.WriteString(strings.TrimRight(line.String(), " "))
		b.WriteByte('\n')
	}
	fmt.Fprint(w, b.String())
}

func (t *Table) Print() { t.Render(os.Stdout) }

func pad(s string, w int, last bool) string {
	if last {
		return s
	}
	if vw := visibleWidth(s); vw < w {
		s += strings.Repeat(" ", w-vw)
	}
	return s + "  "
}

// visibleWidth counts runes, which is exact only because cells hold no wide (CJK/emoji) runes.
func visibleWidth(s string) int {
	width, inEscape := 0, false
	for _, r := range s {
		switch {
		case inEscape:
			// A CSI sequence ends at its final byte, a letter.
			if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
				inEscape = false
			}
		case r == '\033':
			inEscape = true
		default:
			width++
		}
	}
	return width
}

// Confirm returns false off a TTY, so non-interactive callers must pass --yes.
func Confirm(prompt string) bool {
	if !IsTerminal() {
		return false
	}
	fmt.Fprintf(os.Stderr, "%s [y/N] ", prompt)
	sc := bufio.NewScanner(os.Stdin)
	if !sc.Scan() {
		return false
	}
	ans := strings.ToLower(strings.TrimSpace(sc.Text()))
	return ans == "y" || ans == "yes"
}
