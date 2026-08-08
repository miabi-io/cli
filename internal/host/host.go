// Package host manages the Miabi stack on the machine the CLI runs on, as opposed to talking to a
// panel over HTTP. It is the CLI's half of the platform-stack engine: everything Docker-specific
// lives behind Open, which is stubbed out on platforms that cannot host a stack.
package host

import (
	"errors"
	"fmt"
	"os"
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

// RequireRoot fails early when the process cannot write /etc/miabi or reach the Docker socket,
// rather than surfacing a permission error from three layers down.
func RequireRoot() error {
	if os.Geteuid() == 0 {
		return nil
	}
	return fmt.Errorf("this command manages the host's Docker stack and needs root — re-run with sudo")
}
