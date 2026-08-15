//go:build linux || darwin

package host

import (
	"context"
	"fmt"
	"time"

	"github.com/miabi-io/cli/internal/dockerclient"
	"github.com/miabi-io/miabi/pkg/stack"
)

// Open connects to the local Docker engine and returns a stack service bound to manifestPath. The
// daemon is pinged before anything else so an unreachable or permission-denied socket is reported
// once, in the operator's language, instead of failing inside the first converge step.
func Open(manifestPath string, log func(string, ...any)) (*Session, error) {
	dc, err := dockerclient.New()
	if err != nil {
		return nil, fmt.Errorf("cannot reach Docker (it must be installed, and this user must be able to use it — the `docker` group, or root): %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := dc.Ping(ctx); err != nil {
		_ = dc.Close()
		return nil, fmt.Errorf("the Docker daemon is not responding: %w", err)
	}
	return &Session{
		Svc:      stack.New(dc, log, manifestPath),
		Manifest: manifestPath,
		closer:   dc.Close,
	}, nil
}

// InspectSelfImage reads back the image of the container with the given id, so an installer running
// as a container can pin the exact ref it was started from. Empty when it cannot be determined.
func InspectSelfImage(ctx context.Context, id string) string {
	dc, err := dockerclient.New()
	if err != nil {
		return ""
	}
	defer func() { _ = dc.Close() }()
	cfg, err := dc.InspectContainerConfig(ctx, id)
	if err != nil {
		return ""
	}
	return cfg.Image
}
