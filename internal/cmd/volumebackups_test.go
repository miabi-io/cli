package cmd

import (
	"testing"
	"time"

	"github.com/miabi-io/cli/internal/api"
)

func TestParseBackupID(t *testing.T) {
	for _, ref := range []string{"42", "#42"} {
		id, err := parseBackupID(ref)
		if err != nil || id != 42 {
			t.Errorf("parseBackupID(%q) = %d, %v; want 42, nil", ref, id, err)
		}
	}
	// A volume name in the id position is the likely slip, and "0" would address
	// nothing — both must be refused rather than sent to the panel.
	for _, ref := range []string{"web-data", "0", "", "-1"} {
		if _, err := parseBackupID(ref); err == nil {
			t.Errorf("parseBackupID(%q) accepted a bad id", ref)
		}
	}
}

func TestArchiveCell(t *testing.T) {
	tests := []struct {
		name string
		b    api.VolumeBackup
		want string
	}{
		{"full", api.VolumeBackup{S3Bucket: "miabi", S3Path: "ws1/web-data", Filename: "2026-08-26.tar.gz"},
			"miabi/ws1/web-data/2026-08-26.tar.gz"},
		{"no prefix", api.VolumeBackup{S3Bucket: "miabi", Filename: "dump.tar.gz"}, "miabi/dump.tar.gz"},
		{"no bucket", api.VolumeBackup{Filename: "dump.tar.gz"}, "dump.tar.gz"},
		// A pending or failed run has no archive to point at.
		{"none", api.VolumeBackup{S3Bucket: "miabi"}, "-"},
	}
	for _, tt := range tests {
		if got := archiveCell(tt.b); got != tt.want {
			t.Errorf("%s: archiveCell = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestBackupDuration(t *testing.T) {
	start := time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *time.Time { t := start.Add(d); return &t }

	tests := []struct {
		name string
		b    api.VolumeBackup
		want string
	}{
		{"seconds", api.VolumeBackup{StartedAt: &start, FinishedAt: at(42 * time.Second)}, "42s"},
		{"minutes", api.VolumeBackup{StartedAt: &start, FinishedAt: at(3*time.Minute + 7*time.Second)}, "3m7s"},
		{"hours", api.VolumeBackup{StartedAt: &start, FinishedAt: at(2*time.Hour + 5*time.Minute)}, "2h5m"},
		// Still running: started, not finished.
		{"in flight", api.VolumeBackup{StartedAt: &start}, "-"},
		{"never started", api.VolumeBackup{}, "-"},
	}
	for _, tt := range tests {
		if got := backupDuration(tt.b); got != tt.want {
			t.Errorf("%s: backupDuration = %q, want %q", tt.name, got, tt.want)
		}
	}
}

// A backup's "running" is in flight; a deployment's "running" is terminal
// success. Reusing api.IsTerminal here would end the wait on the first poll.
func TestBackupStatusClassification(t *testing.T) {
	for _, s := range []string{api.BackupCompleted, api.BackupFailed} {
		if !api.IsBackupTerminal(s) {
			t.Errorf("%q should be terminal", s)
		}
	}
	for _, s := range []string{api.BackupPending, api.BackupRunning} {
		if api.IsBackupTerminal(s) {
			t.Errorf("%q should not be terminal", s)
		}
	}
	if api.IsTerminal(api.BackupRunning) == api.IsBackupTerminal(api.BackupRunning) {
		t.Error("the deploy and backup classifications must differ on \"running\"")
	}
	if !api.IsBackupFailure(api.BackupFailed) || api.IsBackupFailure(api.BackupCompleted) {
		t.Error("IsBackupFailure misclassifies a terminal status")
	}
}
