package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/miabi-io/cli/internal/api"
)

func TestCollectFilesInfersKeyFromBasename(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "prometheus.yml")
	if err := os.WriteFile(p, []byte("global: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { configFromFile, configFromDir, configRecurse = nil, "", false })
	configFromFile = []string{p}
	data, err := collectFiles()
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if _, ok := data["prometheus.yml"]; !ok {
		t.Fatalf("key not inferred from basename: %v", keysOf(data))
	}

	// The key=path form wins, which is how a nested key is expressed.
	configFromFile = []string{"rules/alerts.yml=" + p}
	data, err = collectFiles()
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if _, ok := data["rules/alerts.yml"]; !ok {
		t.Fatalf("explicit key ignored: %v", keysOf(data))
	}
}

// Stdin has no basename, so guessing a key would produce a config whose only
// file is named "-".
func TestCollectFilesRequiresKeyForStdin(t *testing.T) {
	t.Cleanup(func() { configFromFile = nil })
	configFromFile = []string{"-"}
	if _, err := collectFiles(); err == nil {
		t.Fatal("expected an error for stdin without an explicit key")
	}
}

func TestCollectFilesFromDir(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.conf"), []byte("a"), 0o600)
	os.WriteFile(filepath.Join(dir, ".hidden"), []byte("x"), 0o600)
	os.Mkdir(filepath.Join(dir, "sub"), 0o755)
	os.WriteFile(filepath.Join(dir, "sub", "b.conf"), []byte("b"), 0o600)

	t.Cleanup(func() { configFromFile, configFromDir, configRecurse = nil, "", false })
	configFromDir = dir
	data, err := collectFiles()
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(data) != 1 || data["a.conf"] != "a" {
		t.Fatalf("non-recursive walk should take only a.conf, got %v", keysOf(data))
	}

	configRecurse = true
	data, err = collectFiles()
	if err != nil {
		t.Fatalf("collect recursive: %v", err)
	}
	if data["sub/b.conf"] != "b" {
		t.Fatalf("recursive walk missed sub/b.conf: %v", keysOf(data))
	}
	if _, ok := data[".hidden"]; ok {
		t.Error("dotfiles should be skipped")
	}
}

func TestParseDelimiters(t *testing.T) {
	got, err := parseDelimiters("<<,>>")
	if err != nil || len(got) != 2 || got[0] != "<<" || got[1] != ">>" {
		t.Fatalf("got %v, err %v", got, err)
	}
	if got, err := parseDelimiters(""); err != nil || got != nil {
		t.Fatalf("empty should be nil: %v %v", got, err)
	}
	for _, bad := range []string{"<<", ",>>", "<<,", "<<,<<"} {
		if _, err := parseDelimiters(bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

func TestPickKey(t *testing.T) {
	cfg := &api.Config{Name: "c", Keys: []string{"a.yml", "b.yml"}}
	data := map[string]string{"a.yml": "1", "b.yml": "2"}

	if _, err := pickKey(cfg, data, []string{"c"}); err == nil {
		t.Error("an ambiguous config should require an explicit key")
	}
	if k, err := pickKey(cfg, data, []string{"c", "b.yml"}); err != nil || k != "b.yml" {
		t.Errorf("got %q, %v", k, err)
	}
	if _, err := pickKey(cfg, data, []string{"c", "nope"}); err == nil {
		t.Error("an unknown key should fail")
	}
	single := map[string]string{"only.yml": "x"}
	if k, err := pickKey(&api.Config{Name: "c"}, single, []string{"c"}); err != nil || k != "only.yml" {
		t.Errorf("a single-file config should not need a key: %q %v", k, err)
	}
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
