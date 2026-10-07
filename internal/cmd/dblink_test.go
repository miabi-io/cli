package cmd

import "testing"

func TestParseEnvMap(t *testing.T) {
	m, err := parseEnvMap([]string{"url=SPRING_DATASOURCE_URL", "database_url=", " host = H "})
	if err != nil {
		t.Fatal(err)
	}
	if m["url"] != "SPRING_DATASOURCE_URL" || m["host"] != "H" {
		t.Errorf("map = %v", m)
	}
	if v, ok := m["database_url"]; !ok || v != "" {
		t.Errorf("database_url should be present and empty, got %q (%v)", v, ok)
	}
	for _, bad := range []string{"url", "=NAME"} {
		if _, err := parseEnvMap([]string{bad}); err == nil {
			t.Errorf("%q: expected an error", bad)
		}
	}
}
