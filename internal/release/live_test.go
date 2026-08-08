package release

import (
	"context"
	"testing"
)

// TestLatestLive hits the real GitHub API. Skipped in -short so CI and offline work stay green;
// run with `go test ./internal/release -run Live -v` to check the contract against production.
func TestLatestLive(t *testing.T) {
	if testing.Short() {
		t.Skip("network test")
	}
	v, err := Latest(context.Background(), "miabi-cli/test")
	if err != nil {
		t.Skipf("GitHub unreachable or rate-limited: %v", err)
	}
	if v == "" || v[0] == 'v' {
		t.Fatalf("Latest() = %q; want a bare version with no leading v", v)
	}
	t.Logf("latest published Miabi release: %s", v)
}

func TestNormalize(t *testing.T) {
	for in, want := range map[string]string{
		"v1.8.0": "1.8.0", "1.8.0": "1.8.0", "V1.8.0": "1.8.0",
		"v1.8.0-rc.1": "1.8.0-rc.1", " v1.7.3 ": "1.7.3",
		"vnext": "vnext", "": "", // only a "v" before a digit is a version prefix
	} {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}
