package host

import (
	"os"
	"path/filepath"
	"testing"
)

// Two installs on one host converge the same containers against the same Docker daemon, so setup
// has to see the other one before it makes a second.
func TestOtherInstall(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	user := filepath.Join(home, ".miabi", "miabi.yaml")

	if got := OtherInstall(user); got != "" {
		t.Fatalf("with nothing installed, OtherInstall = %q, want none", got)
	}

	if err := os.MkdirAll(filepath.Dir(user), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(user, []byte("version: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// The install we are asking about is never "another" one: setup converges it.
	if got := OtherInstall(user); got != "" {
		t.Errorf("OtherInstall(%q) = %q, want none — that is the install being asked about", user, got)
	}
	// Seen from anywhere else, it is.
	if got := OtherInstall("/srv/elsewhere/miabi.yaml"); got != user {
		t.Errorf("OtherInstall = %q, want the per-user install at %q", got, user)
	}
}

// UserManifestPath puts the manifest beside the CLI's own config, and the gateway config lands
// next to it because everything is resolved from the manifest's directory.
func TestUserManifestPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	want := filepath.Join(home, ".miabi", "miabi.yaml")
	if got := UserManifestPath(); got != want {
		t.Fatalf("UserManifestPath() = %q, want %q", got, want)
	}
}

// RequireWritable asks whether the file can be written, not whether the caller is root: a
// directory that does not exist yet is writable when its parent is, which is the ordinary case for
// a fresh ~/.miabi.
func TestRequireWritable(t *testing.T) {
	home := t.TempDir()

	if err := RequireWritable(filepath.Join(home, "miabi.yaml")); err != nil {
		t.Errorf("an existing writable directory: %v", err)
	}
	if err := RequireWritable(filepath.Join(home, ".miabi", "miabi.yaml")); err != nil {
		t.Errorf("a directory that does not exist yet, under a writable parent: %v", err)
	}

	locked := filepath.Join(home, "locked")
	if err := os.Mkdir(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() == 0 {
		t.Skip("root can write a 0500 directory, so there is nothing to refuse")
	}
	if err := RequireWritable(filepath.Join(locked, "miabi.yaml")); err == nil {
		t.Error("a directory this process cannot write must be refused")
	}
}

// A manifest that exists but belongs to someone else is a permission problem, not a missing one —
// and the message has to say which, because the remedies differ.
func TestRequireReadable(t *testing.T) {
	home := t.TempDir()

	missing := filepath.Join(home, "nothing.yaml")
	if err := RequireReadable(missing); err != nil {
		t.Errorf("a missing manifest is not unreadable — Load reports it: %v", err)
	}

	unreadable := filepath.Join(home, "secret.yaml")
	if err := os.WriteFile(unreadable, []byte("version: 1\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() == 0 {
		t.Skip("root can read a 0000 file, so there is nothing to refuse")
	}
	if err := RequireReadable(unreadable); err == nil {
		t.Error("a manifest this process cannot read must be refused")
	}
}
