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
	"context"
	"testing"
	"time"
)

func TestPollStatusStopsAtATerminalStatus(t *testing.T) {
	prev := volumeBackupPollInterval
	volumeBackupPollInterval = time.Millisecond
	defer func() { volumeBackupPollInterval = prev }()

	seq := []string{"running", "running", "completed"}
	var seen []string
	n := 0
	got, err := pollStatus(context.Background(), func(s string) { seen = append(seen, s) }, func() (*Restore, string, error) {
		s := seq[n]
		n++
		return &Restore{ID: 3, Status: s}, s, nil
	})
	if err != nil || got.Status != "completed" || n != 3 {
		t.Fatalf("pollStatus = %+v, %v after %d fetches", got, err, n)
	}
	if len(seen) != 2 {
		t.Errorf("updates %v, want one per change", seen)
	}
}

func TestPollStatusHonoursTheDeadline(t *testing.T) {
	prev := volumeBackupPollInterval
	volumeBackupPollInterval = time.Millisecond
	defer func() { volumeBackupPollInterval = prev }()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := pollStatus(ctx, nil, func() (*Restore, string, error) { return &Restore{}, "running", nil }); err == nil {
		t.Fatal("a run that never settles must time out")
	}
}
