package release

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func jsonServer(t *testing.T, status int, body any) (*httptest.Server, *int) {
	t.Helper()
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.Header.Get("Authorization") != "" {
			t.Error("request carries credentials")
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func point(t *testing.T, versions, github string) {
	t.Helper()
	oldV, oldG := versionsURL, githubBase
	versionsURL, githubBase = versions, github
	t.Cleanup(func() { versionsURL, githubBase = oldV, oldG })
	t.Setenv(APIEnv, "")
}

func TestLatestReadsVersionsService(t *testing.T) {
	vs, _ := jsonServer(t, http.StatusOK, map[string]any{
		"stable": map[string]any{
			"version": "1.10.19", "reason": "security",
			"release_url": "https://github.example/v1.10.19", "changelog_url": "https://docs.example/1.10.19",
		},
		"prerelease": map[string]any{"version": "1.11.0-beta.1"},
	})
	gh, ghHits := jsonServer(t, http.StatusOK, []ghRelease{{TagName: "v0.0.1"}})
	point(t, vs.URL, gh.URL)

	r, err := LatestRelease(context.Background(), "miabi-cli/test")
	if err != nil {
		t.Fatal(err)
	}
	if r.Version != "1.10.19" || r.Reason != ReasonSecurity || r.ReleaseURL != "https://docs.example/1.10.19" {
		t.Fatalf("release = %+v", r)
	}
	if *ghHits != 0 {
		t.Error("GitHub was asked although versions.miabi.io answered")
	}
}

func TestLatestFallsBackToGitHub(t *testing.T) {
	vs, _ := jsonServer(t, http.StatusBadGateway, nil)
	gh, _ := jsonServer(t, http.StatusOK, []ghRelease{
		{TagName: "v1.11.0", Draft: true},
		{TagName: "v1.11.0-rc.1", Prerelease: true},
		{TagName: "v1.10.18", HTMLURL: "https://github.example/v1.10.18"},
	})
	point(t, vs.URL, gh.URL)

	v, err := Latest(context.Background(), "miabi-cli/test")
	if err != nil || v != "1.10.18" {
		t.Fatalf("Latest() = %q, %v; want the newest stable GitHub release", v, err)
	}
}

func TestLatestReportsBothFailures(t *testing.T) {
	vs, _ := jsonServer(t, http.StatusNotFound, nil)
	gh, _ := jsonServer(t, http.StatusForbidden, nil)
	point(t, vs.URL, gh.URL)

	_, err := Latest(context.Background(), "miabi-cli/test")
	if err == nil || !strings.Contains(err.Error(), "404") || !strings.Contains(err.Error(), "rate limit") {
		t.Fatalf("err = %v; want both lookups explained", err)
	}
}

func TestStableChannelMustHoldAStableVersion(t *testing.T) {
	vs, _ := jsonServer(t, http.StatusOK, map[string]any{"stable": map[string]any{"version": "1.11.0-rc.1"}})
	if _, err := fromVersions(context.Background(), vs.URL, "miabi-cli/test"); err == nil {
		t.Fatal("a prerelease in the stable channel was accepted")
	}
}

// A mirror stands in for both lookups: a host with no route out must not wait on versions.miabi.io first.
func TestMirrorReplacesBothLookups(t *testing.T) {
	vs, vsHits := jsonServer(t, http.StatusOK, map[string]any{"stable": map[string]any{"version": "9.9.9"}})
	mirror, _ := jsonServer(t, http.StatusOK, []ghRelease{{TagName: "v1.10.18"}})
	point(t, vs.URL, "http://127.0.0.1:1")
	t.Setenv(APIEnv, mirror.URL+"/")

	v, err := Latest(context.Background(), "miabi-cli/test")
	if err != nil || v != "1.10.18" || *vsHits != 0 {
		t.Fatalf("Latest() = %q, %v, versions hits %d; want the mirror only", v, err, *vsHits)
	}
}
