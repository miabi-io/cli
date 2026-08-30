package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/miabi-io/cli/internal/api"
)

func TestCollectDriverOpts(t *testing.T) {
	t.Cleanup(func() { volDriverOpt, volDriverOptFile = nil, nil })

	volDriverOpt = []string{"device=:/export", "o=addr=10.0.0.5,rw"}
	opts, err := collectDriverOpts()
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	// The value keeps its own "=" — mount options are themselves key=value lists.
	if opts["o"] != "addr=10.0.0.5,rw" {
		t.Errorf("o = %q, want the whole value after the first =", opts["o"])
	}
	if opts["device"] != ":/export" {
		t.Errorf("device = %q", opts["device"])
	}

	// No options at all must send nothing rather than an empty object, so the
	// server keeps its own driver defaults.
	volDriverOpt = nil
	if opts, err = collectDriverOpts(); err != nil || opts != nil {
		t.Fatalf("opts = %v, %v; want nil, nil", opts, err)
	}

	volDriverOpt = []string{"device"}
	if _, err = collectDriverOpts(); err == nil {
		t.Error("expected an error for an option without =")
	}
}

// The file form exists so a share password never reaches the shell history; the
// trailing newline a text editor adds must not become part of the value.
func TestCollectDriverOptsFromFile(t *testing.T) {
	t.Cleanup(func() { volDriverOpt, volDriverOptFile = nil, nil })
	p := filepath.Join(t.TempDir(), "mount-opts.txt")
	if err := os.WriteFile(p, []byte("username=svc,password=s3cr3t,vers=3.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	volDriverOpt = []string{"device=//nas/share"}
	volDriverOptFile = []string{"o=" + p}
	opts, err := collectDriverOpts()
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if opts["o"] != "username=svc,password=s3cr3t,vers=3.0" {
		t.Errorf("o = %q, want the file content without its trailing newline", opts["o"])
	}
	if opts["device"] != "//nas/share" {
		t.Errorf("device = %q, want the inline option kept", opts["device"])
	}

	volDriverOptFile = []string{"o"}
	if _, err = collectDriverOpts(); err == nil {
		t.Error("expected an error for a file option without a path")
	}
}

// A declared capacity of 0 means "no ceiling", which humanBytes renders as "-".
func TestVolumeSize(t *testing.T) {
	if got := volumeSize(0); got != "unlimited" {
		t.Errorf("volumeSize(0) = %q, want unlimited", got)
	}
	if got := volumeSize(1024 * 1024); got != "1.0MB" {
		t.Errorf("volumeSize(1MiB) = %q", got)
	}
}

// Never measured and measured-as-empty are different facts and must not render
// the same: a volume full of data the sweep has not reached would read as 0B.
func TestVolumeUsedDistinguishesUnmeasured(t *testing.T) {
	if got := volumeUsed(api.Volume{UsedBytes: 0}); got != "-" {
		t.Errorf("unmeasured = %q, want -", got)
	}
	now := time.Now()
	if got := volumeUsed(api.Volume{UsedBytes: 0, UsedMeasuredAt: &now}); got != "0B" {
		t.Errorf("measured empty = %q, want 0B", got)
	}
	if got := volumeUsed(api.Volume{UsedBytes: 2048, UsedMeasuredAt: &now}); got != "2.0KB" {
		t.Errorf("measured = %q", got)
	}
}

func TestVolumeNodeAndDriverFallBack(t *testing.T) {
	if got := volumeNode(api.Volume{}); got != "local" {
		t.Errorf("node = %q, want local for server 0", got)
	}
	if got := volumeNode(api.Volume{ServerID: 4}); got != "4" {
		t.Errorf("node = %q, want the id when the panel sent no name", got)
	}
	if got := volumeNode(api.Volume{ServerID: 4, ServerName: "edge-1"}); got != "edge-1" {
		t.Errorf("node = %q, want the display name", got)
	}
	// An older panel does not send the driver column.
	if got := volumeDriver(api.Volume{}); got != "local" {
		t.Errorf("driver = %q, want local", got)
	}
}

// A workspace plan uses -1 for unlimited and 0 for none, and 0 is the column's
// default — so the plan that grants no storage is the easy one to create. Sending
// it through volumeSize said "unlimited", which is the opposite of the truth.
func TestStorageLimitDoesNotReadNoneAsUnlimited(t *testing.T) {
	if got := storageLimit(-1); got != "unlimited" {
		t.Errorf("storageLimit(-1) = %q, want unlimited", got)
	}
	got := storageLimit(0)
	if strings.Contains(got, "unlimited") {
		t.Errorf("storageLimit(0) = %q — 0 means no storage at all, not unlimited", got)
	}
	if !strings.HasPrefix(got, "none") {
		t.Errorf("storageLimit(0) = %q, want it to lead with none", got)
	}
	if got := storageLimit(1024); got != "1.0GB" {
		t.Errorf("storageLimit(1024) = %q", got)
	}
}
