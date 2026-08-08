// Package release resolves the newest published Miabi platform version.
//
// The CLI releases on its own cadence — a CLI cut months ago still has to install today's Miabi —
// so the platform version cannot be baked into the binary. It is looked up when needed, and an
// operator can always state it outright with --version or --image instead.
package release

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jkaninda/okapi/client"
)

const (
	// defaultAPI is GitHub's REST root. releasesPath lists releases newest-first.
	//
	// Deliberately NOT /releases/latest: that endpoint excludes prereleases, and a project whose
	// releases are all prereleases gets a 404 from it — the lookup would fail for no visible
	// reason. Listing and filtering ourselves also lets a prerelease be skipped explicitly rather
	// than by GitHub's definition of "latest".
	defaultAPI   = "https://api.github.com"
	releasesPath = "/repos/miabi-io/miabi/releases"

	// APIEnv points the lookup at a mirror — an internal proxy on a host with no route to GitHub.
	// It must serve GitHub's releases JSON shape.
	APIEnv = "MIABI_RELEASE_API"

	// timeout bounds the whole exchange. This sits in front of an install: a hung GitHub must fail
	// fast enough that the operator reaches the --version advice while still paying attention.
	timeout = 15 * time.Second

	// maxPage is plenty to find a stable release behind a run of prereleases without paging.
	maxPage = 30
)

type ghRelease struct {
	TagName    string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
}

// Latest returns the newest published platform version WITHOUT a leading "v" — "1.8.0", matching
// the Docker tag rather than the Git tag. Drafts and prereleases are skipped: an unattended
// `miabi upgrade` must not walk onto a release candidate.
//
// userAgent identifies the caller honestly. The request says nothing else about the install: no
// id, no host, no license.
func Latest(ctx context.Context, userAgent string) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv(APIEnv)), "/")
	if base == "" {
		base = defaultAPI
	}
	// The User-Agent identifies the caller honestly and is the ONLY thing this request says about
	// the install: no id, no host, no license.
	c := client.New(base,
		client.WithTimeout(timeout),
		client.WithUserAgent(userAgent),
		client.WithHeader("Accept", "application/vnd.github+json"),
	)

	resp, err := c.Get(releasesPath).
		WithContext(ctx).
		QueryParam("per_page", strconv.Itoa(maxPage)).
		Do()
	if err != nil {
		return "", fmt.Errorf("reach %s: %w", base, err)
	}
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusForbidden, http.StatusTooManyRequests:
		return "", fmt.Errorf("%s returned %s — this is usually GitHub's unauthenticated rate limit", base, resp.Status)
	default:
		return "", fmt.Errorf("%s returned %s", base, resp.Status)
	}

	var list []ghRelease
	if err := resp.JSON(&list); err != nil {
		return "", fmt.Errorf("decode releases from %s: %w", base, err)
	}
	for _, r := range list {
		if r.Draft || r.Prerelease {
			continue
		}
		if v := Normalize(r.TagName); v != "" {
			return v, nil
		}
	}
	return "", fmt.Errorf("no published release found at %s%s", base, releasesPath)
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
