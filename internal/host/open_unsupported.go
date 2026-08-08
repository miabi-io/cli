//go:build !linux && !darwin

package host

import "context"

// Open always fails here: the commands stay visible in --help on every platform so the error
// explains the constraint, rather than the verb simply not existing.
func Open(string, func(string, ...any)) (*Session, error) { return nil, ErrUnsupported }

// InspectSelfImage has nothing to inspect without a Docker host.
func InspectSelfImage(context.Context, string) string { return "" }
