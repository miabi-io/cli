// Package host manages the Miabi stack on the machine the CLI runs on, as opposed to talking to a
// panel over HTTP. It is the CLI's half of the platform-stack engine: everything Docker-specific
// lives behind Open, which is stubbed out on platforms that cannot host a stack.
package host

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/miabi-io/miabi/pkg/stack"
)

// ErrUnsupported is returned by Open on a platform with no Docker host to manage.
var ErrUnsupported = errors.New("stack management requires a Linux or macOS host with Docker")

// Session is an open connection to the local Docker engine plus the stack service driving it.
// Close releases the connection.
type Session struct {
	Svc      *stack.Service
	Manifest string
	closer   func() error
}

// Close releases the Docker connection.
func (s *Session) Close() error {
	if s == nil || s.closer == nil {
		return nil
	}
	return s.closer()
}

// ManifestPath resolves the manifest to operate on: the --file flag if given, else the platform
// default (see [stack.ManifestPath]).
func ManifestPath(flag string) string {
	if p := strings.TrimSpace(flag); p != "" {
		return p
	}
	return stack.ManifestPath()
}

// RequireWritable fails early when this process cannot write the manifest, rather than surfacing a
// permission error from three layers down.
func RequireWritable(path string) error {
	dir := filepath.Dir(path)
	if writable(dir) {
		return nil
	}

	if _, err := os.Stat(dir); os.IsNotExist(err) && writable(filepath.Dir(dir)) {
		return nil
	}
	return fmt.Errorf("cannot write %s\n\n"+
		"  Either re-run with sudo, or install under your own account:\n"+
		"    miabi setup --file %s --domain …", path, userPathHint())
}

// RequireReadable fails early when the manifest exists but this process cannot read it. The file is
// mode 0600 because it holds the database password, so "you are not the owner" is the likely reason
// and is worth saying outright.
func RequireReadable(path string) error {
	f, err := os.Open(path)
	if err == nil {
		_ = f.Close()
		return nil
	}
	if os.IsNotExist(err) {
		return nil
	}
	return fmt.Errorf("cannot read %s — it is owned by the user that installed it (mode 0600)\n\n"+
		"  Re-run with sudo, or point at your own install:  --file %s", path, userPathHint())
}

// OtherInstall returns a manifest that exists somewhere other than path, or "" when there is none.
//
// Two installs on one host are not two installs: they converge the same container, volume and
// network names against the same Docker daemon, and the second one adopts and then fights over the
// first one's containers. Better to refuse than to discover it as a restart loop.
func OtherInstall(path string) string {
	for _, p := range []string{stack.DefaultConfigPath, UserManifestPath()} {
		if p == "" || p == path {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// writable reports whether a directory can be written by this process. It tries rather than
// reasoning about modes and group membership, which is the only answer that is never wrong.
func writable(dir string) bool {
	f, err := os.CreateTemp(dir, ".miabi-write-check-*")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return true
}

// UserManifestPath is where a non-root install keeps its manifest: ~/.miabi/miabi.yaml, beside the
// CLI's own config. The gateway config lands next to it, because everything an install needs is
// resolved relative to the manifest. Empty when there is no home directory.
//
// It mirrors stack.UserConfigPath in the platform module, which this CLI tracks by release: fold
// this into a call to that one on the next dependency bump.
func UserManifestPath() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	return filepath.Join(home, ".miabi", "miabi.yaml")
}

// userPathHint names the per-user manifest in an error, falling back to the literal path when there
// is no home to resolve.
func userPathHint() string {
	if p := UserManifestPath(); p != "" {
		return p
	}
	return "~/.miabi/miabi.yaml"
}
