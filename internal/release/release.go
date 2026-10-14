// Package release resolves the newest published Miabi platform version.
//
// The CLI releases on its own cadence — a CLI cut months ago still has to install today's Miabi —
// so the platform version cannot be baked into the binary. It is looked up when needed, and an
// operator can always state it outright with --version or --image instead.
package release

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jkaninda/okapi/client"
)

const (
	// VersionsURL is the platform's entry on versions.miabi.io, written by Miabi's release workflow.
	VersionsURL  = "https://versions.miabi.io/v1/miabi/latest.json"
	githubAPI    = "https://api.github.com"
	releasesPath = "/repos/miabi-io/miabi/releases"
	APIEnv       = "MIABI_RELEASE_API"
	timeout      = 15 * time.Second
	maxPage      = 30
)

// Overridden only by tests.
var (
	versionsURL = VersionsURL
	githubBase  = githubAPI
)

// ReasonSecurity marks a release published to fix a vulnerability.
const ReasonSecurity = "security"

// Release is the newest stable platform release.
type Release struct {
	Version    string
	Reason     string
	ReleaseURL string
}

// Latest returns the newest stable platform version without a leading "v".
func Latest(ctx context.Context, userAgent string) (string, error) {
	r, err := LatestRelease(ctx, userAgent)
	return r.Version, err
}

// LatestRelease returns the newest stable platform release. Prereleases are never returned: an
// unattended `miabi upgrade` must not walk onto a release candidate.
//
// userAgent identifies the caller honestly. The request says nothing else about the install: no
// id, no host, no license.
func LatestRelease(ctx context.Context, userAgent string) (Release, error) {
	if mirror := strings.TrimRight(strings.TrimSpace(os.Getenv(APIEnv)), "/"); mirror != "" {
		return fromGitHub(ctx, mirror, userAgent)
	}
	r, err := fromVersions(ctx, versionsURL, userAgent)
	if err == nil {
		return r, nil
	}
	r, ghErr := fromGitHub(ctx, githubBase, userAgent)
	if ghErr != nil {
		return Release{}, errors.Join(err, ghErr)
	}
	return r, nil
}

func newClient(base, userAgent, accept string) *client.Client {
	return client.New(base,
		client.WithTimeout(timeout),
		client.WithUserAgent(userAgent),
		client.WithHeader("Accept", accept),
	)
}

type versionsManifest struct {
	Stable *struct {
		Version      string `json:"version"`
		ReleaseURL   string `json:"release_url"`
		ChangelogURL string `json:"changelog_url"`
		Reason       string `json:"reason"`
	} `json:"stable"`
}

func fromVersions(ctx context.Context, url, userAgent string) (Release, error) {
	resp, err := newClient(url, userAgent, "application/json").Get("").WithContext(ctx).Do()
	if err != nil {
		return Release{}, fmt.Errorf("reach %s: %w", url, err)
	}
	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("%s returned %s", url, resp.Status)
	}
	var m versionsManifest
	if err := resp.JSON(&m); err != nil {
		return Release{}, fmt.Errorf("decode %s: %w", url, err)
	}
	if m.Stable == nil {
		return Release{}, fmt.Errorf("%s lists no stable release", url)
	}
	v := Normalize(m.Stable.Version)
	if v == "" || strings.Contains(v, "-") {
		return Release{}, fmt.Errorf("%s lists %q as stable, which is not a stable version", url, m.Stable.Version)
	}
	link := m.Stable.ReleaseURL
	if m.Stable.ChangelogURL != "" {
		link = m.Stable.ChangelogURL
	}
	return Release{Version: v, Reason: m.Stable.Reason, ReleaseURL: link}, nil
}

type ghRelease struct {
	TagName    string `json:"tag_name"`
	HTMLURL    string `json:"html_url"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
}

func fromGitHub(ctx context.Context, base, userAgent string) (Release, error) {
	resp, err := newClient(base, userAgent, "application/vnd.github+json").Get(releasesPath).
		WithContext(ctx).
		QueryParam("per_page", strconv.Itoa(maxPage)).
		Do()
	if err != nil {
		return Release{}, fmt.Errorf("reach %s: %w", base, err)
	}
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusForbidden, http.StatusTooManyRequests:
		return Release{}, fmt.Errorf("%s returned %s — this is usually GitHub's unauthenticated rate limit", base, resp.Status)
	default:
		return Release{}, fmt.Errorf("%s returned %s", base, resp.Status)
	}

	var list []ghRelease
	if err := resp.JSON(&list); err != nil {
		return Release{}, fmt.Errorf("decode releases from %s: %w", base, err)
	}
	for _, r := range list {
		if r.Draft || r.Prerelease {
			continue
		}
		if v := Normalize(r.TagName); v != "" {
			return Release{Version: v, ReleaseURL: r.HTMLURL}, nil
		}
	}
	return Release{}, fmt.Errorf("no published release found at %s%s", base, releasesPath)
}

// Normalize turns a Git tag into a Docker tag: releases are cut as v1.8.0 and published as 1.8.0.
// The "v" is stripped only when a digit follows, so a tag like "vnext" is left alone.
func Normalize(tag string) string {
	t := strings.TrimSpace(tag)
	if len(t) > 1 && (t[0] == 'v' || t[0] == 'V') && t[1] >= '0' && t[1] <= '9' {
		return t[1:]
	}
	return t
}
