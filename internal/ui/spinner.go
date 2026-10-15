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

package ui

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// Spinner is a lightweight terminal spinner for long-running waits. On a
// non-terminal (pipe/CI) it degrades to nothing — the caller's plain status
// lines carry the information instead.
type Spinner struct {
	mu     sync.Mutex
	msg    string
	stop   chan struct{}
	done   chan struct{}
	active bool
}

var frames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// NewSpinner creates a spinner with an initial message (not yet started).
func NewSpinner(msg string) *Spinner { return &Spinner{msg: msg} }

// Start begins animating on stderr. No-op off a TTY.
func (s *Spinner) Start() {
	if !IsTerminal() || s.active {
		return
	}
	s.active = true
	s.stop = make(chan struct{})
	s.done = make(chan struct{})
	go func() {
		defer close(s.done)
		t := time.NewTicker(80 * time.Millisecond)
		defer t.Stop()
		i := 0
		draw := func() {
			s.mu.Lock()
			m := s.msg
			s.mu.Unlock()
			fmt.Fprintf(os.Stderr, "\r%s %s", Cyan(frames[i%len(frames)]), m)
			i++
		}
		draw() // paint immediately so there's no blank gap before the first tick
		for {
			select {
			case <-s.stop:
				fmt.Fprint(os.Stderr, "\r\033[K") // clear the line
				return
			case <-t.C:
				draw()
			}
		}
	}()
}

// Update changes the spinner's message in place.
func (s *Spinner) Update(msg string) {
	s.mu.Lock()
	s.msg = msg
	s.mu.Unlock()
}

// Stop halts the animation and clears the line. Safe to call more than once.
func (s *Spinner) Stop() {
	if !s.active {
		return
	}
	s.active = false
	close(s.stop)
	<-s.done
}
