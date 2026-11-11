// Copyright (c) 2026 Jonas Kaninda.
//
// This file is part of Miabi.
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package api wraps the Miabi /api/v1 HTTP surface, unwrapping its {success,data,error} envelope.
package api

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jkaninda/okapi/client"
)

// Version is stamped at build time (see .goreleaser.yaml); used in User-Agent.
var Version = "dev"

type Client struct {
	c       *client.Client
	stream  *http.Client
	baseURL string
	token   string
	verbose bool
}

type Options struct {
	BaseURL      string
	Token        string
	CAFile       string
	InsecureSkip bool
	Verbose      bool
}

// New fails on an unreadable CA bundle rather than silently falling back to the system roots.
func New(o Options) (*Client, error) {
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: o.InsecureSkip} //nolint:gosec // opt-in via config
	if o.CAFile != "" {
		pem, err := os.ReadFile(o.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read CA bundle %s: %w", o.CAFile, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("CA bundle %s contains no valid certificates", o.CAFile)
		}
		tlsCfg.RootCAs = pool
	}
	tr := &http.Transport{TLSClientConfig: tlsCfg}
	httpc := &http.Client{
		Timeout:       30 * time.Second,
		Transport:     tr,
		CheckRedirect: checkAPIRedirect,
	}
	streamc := &http.Client{Transport: tr, CheckRedirect: checkAPIRedirect}
	opts := []client.Option{
		client.WithHTTPClient(httpc),
		client.WithBearerToken(o.Token),
		client.WithUserAgent("miabi-cli/" + Version),
		// Asking for JSON makes proxies and SSO gateways answer with an error, not their HTML sign-in page.
		client.WithHeader("Accept", "application/json"),
		// Safe to retry: every call below is a GET or an idempotent action.
		client.WithRetry(client.RetryPolicy{MaxAttempts: 3, BaseDelay: 200 * time.Millisecond, MaxDelay: 2 * time.Second}),
	}
	if o.Verbose {
		opts = append(opts, client.WithMiddleware(client.LoggingMiddleware(os.Stderr)))
	}
	return &Client{
		c:       client.New(o.BaseURL, opts...),
		stream:  streamc,
		baseURL: strings.TrimRight(o.BaseURL, "/"),
		token:   o.Token,
		verbose: o.Verbose,
	}, nil
}

func checkAPIRedirect(req *http.Request, via []*http.Request) error {
	orig := via[0]
	switch {
	case req.URL.Host != orig.URL.Host:
		return fmt.Errorf("redirected to %s://%s — the server URL is not serving the Miabi API directly (a proxy or SSO gateway is intercepting it)", req.URL.Scheme, req.URL.Host)
	case req.Method != orig.Method:
		return fmt.Errorf("%s was redirected to %s and rewritten as %s — use the panel's canonical URL (https://, no redirect) as the server", orig.Method, req.URL.Scheme+"://"+req.URL.Host, req.Method)
	case len(via) >= 5:
		return fmt.Errorf("too many redirects for %s", orig.URL)
	}
	return nil
}

type APIError struct {
	StatusCode int    `json:"status_code"`
	Code       string `json:"code"`
	Message    string `json:"message"`
	Detail     string `json:"error"`
}

func (e *APIError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = e.Detail
	}
	if e.Code != "" {
		return fmt.Sprintf("%s: %s", e.Code, msg)
	}
	return msg
}

type envelope struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   *APIError       `json:"error"`
}

// do returns the server's *APIError on a non-2xx when the body carries one; out may be nil.
func (c *Client) do(rb *client.RequestBuilder, out any) error {
	_, err := c.doResp(rb, out)
	return err
}

func (c *Client) doResp(rb *client.RequestBuilder, out any) (*client.Response, error) {
	resp, err := rb.Do()
	if err != nil {
		return nil, err
	}

	empty := len(bytes.TrimSpace(resp.Body)) == 0
	var env envelope
	switch {
	case !empty && json.Unmarshal(resp.Body, &env) != nil:
		return resp, notAPIResponse(resp)
	case env.Error != nil:
		return resp, apiError(env.Error, resp.StatusCode)
	case !resp.IsSuccess():
		return resp, fmt.Errorf("%s %s: HTTP %d", resp.Method, resp.URL, resp.StatusCode)
	}
	warnScope(resp.Header.Get(scopeRequiredHeader))
	if out == nil || len(env.Data) == 0 || string(env.Data) == "null" {
		return resp, nil
	}
	return resp, json.Unmarshal(env.Data, out)
}

// Every /api route answers JSON, so an HTML body came from a proxy, SSO gateway, WAF or the wrong host.
func notAPIResponse(resp *client.Response) error {
	return fmt.Errorf("%s %s returned HTTP %d, %s — that is not the Miabi API. Check the server URL and that /api/ requests reach the panel untouched (no SSO gateway, WAF, or captive portal in front)",
		resp.Method, resp.URL, resp.StatusCode, describeBody(resp.Header.Get("Content-Type"), resp.Body))
}

var titleRe = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)

func describeBody(contentType string, body []byte) string {
	kind, _, _ := strings.Cut(contentType, ";")
	if kind = strings.TrimSpace(kind); kind == "" {
		kind = "an untyped body"
	}
	hint := ""
	if m := titleRe.FindSubmatch(body); m != nil {
		hint = string(m[1])
	} else if !strings.Contains(kind, "html") {
		hint = string(body)
	}
	if hint = strings.Join(strings.Fields(hint), " "); hint == "" {
		return kind
	}
	if len(hint) > 120 {
		hint = hint[:120] + "…"
	}
	return fmt.Sprintf("%s (%q)", kind, hint)
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	return c.do(c.c.Get(path).WithContext(ctx), out)
}

func (c *Client) post(ctx context.Context, path string, body, out any) error {
	rb := c.c.Post(path).WithContext(ctx)
	if body != nil {
		rb = rb.JSONBody(body)
	}
	return c.do(rb, out)
}

// postLong is for endpoints that do their work inline instead of handing it to the worker.
func (c *Client) postLong(ctx context.Context, path string, body, out any, timeout time.Duration) error {
	rb := c.c.Post(path).WithContext(ctx).Timeout(timeout)
	if body != nil {
		rb = rb.JSONBody(body)
	}
	return c.do(rb, out)
}

func (c *Client) put(ctx context.Context, path string, body, out any) error {
	rb := c.c.Put(path).WithContext(ctx)
	if body != nil {
		rb = rb.JSONBody(body)
	}
	return c.do(rb, out)
}

func (c *Client) patch(ctx context.Context, path string, body, out any) error {
	rb := c.c.Patch(path).WithContext(ctx)
	if body != nil {
		rb = rb.JSONBody(body)
	}
	return c.do(rb, out)
}

func (c *Client) del(ctx context.Context, path string, out any) error {
	return c.do(c.c.Delete(path).WithContext(ctx), out)
}

func (c *Client) Me(ctx context.Context) (*Me, error) {
	var m Me
	return &m, c.get(ctx, "/api/v1/me", &m)
}

// ClaimLoginToken is public (the one-time code is the credential), so it works on a client without a token.
func (c *Client) ClaimLoginToken(ctx context.Context, code string) (*LoginToken, error) {
	var t LoginToken
	return &t, c.post(ctx, "/api/v1/auth/login-token/claim", map[string]string{"handoff": code}, &t)
}

func (c *Client) Workspaces(ctx context.Context) ([]Workspace, error) {
	var ws []Workspace
	return ws, c.get(ctx, "/api/v1/workspaces", &ws)
}

// ResolveWorkspaceName falls back to fallback, then the sole visible workspace; it always returns a name.
func (c *Client) ResolveWorkspaceName(ctx context.Context, ref, fallback string) (string, error) {
	if ref == "" {
		ref = fallback
	}
	if ref != "" {
		// The server resolves a name, UID or id itself, which also works for a key that cannot list workspaces.
		w, err := c.Workspace(ctx, ref)
		if err != nil {
			var ae *APIError
			if errors.As(err, &ae) && (ae.StatusCode == http.StatusNotFound || ae.StatusCode == http.StatusForbidden) {
				return "", fmt.Errorf("workspace %q not found among your workspaces", ref)
			}
			return "", err
		}
		return w.Name, nil
	}
	ws, err := c.Workspaces(ctx)
	if err != nil {
		// A workspace-bound key cannot list workspaces; it names its own.
		if me, merr := c.Me(ctx); merr == nil && me.Auth.WorkspaceID != nil {
			w, werr := c.Workspace(ctx, strconv.FormatUint(uint64(*me.Auth.WorkspaceID), 10))
			if werr == nil {
				return w.Name, nil
			}
		}
		return "", err
	}
	if len(ws) == 1 {
		return ws[0].Name, nil
	}
	return "", fmt.Errorf("no workspace selected — pass --workspace, or run `miabi workspace switch <name>`")
}

func (c *Client) Workspace(ctx context.Context, ref string) (*Workspace, error) {
	var w Workspace
	return &w, c.get(ctx, "/api/v1/workspaces/"+url.PathEscape(ref), &w)
}

func (c *Client) Apps(ctx context.Context, ws string) ([]App, error) {
	var apps []App
	return apps, c.get(ctx, fmt.Sprintf("/api/v1/workspaces/%s/apps", ws), &apps)
}

func (c *Client) App(ctx context.Context, ws string, app string) (*App, error) {
	var a App
	return &a, c.get(ctx, fmt.Sprintf("/api/v1/workspaces/%s/apps/%s", ws, url.PathEscape(app)), &a)
}

func (c *Client) CreateApp(ctx context.Context, ws string, req CreateAppRequest) (*App, error) {
	var a App
	return &a, c.post(ctx, fmt.Sprintf("/api/v1/workspaces/%s/apps", ws), req, &a)
}

// MaxServerWait is the longest ?wait= the server accepts on deploy and rollback.
const MaxServerWait = 900 * time.Second

// Deploy: a server may answer before the deployment settles despite wait, so callers must still poll.
func (c *Client) Deploy(ctx context.Context, ws string, app string, req DeployRequest, wait time.Duration) (*DeployResult, error) {
	return c.deployCall(ctx, fmt.Sprintf("/api/v1/workspaces/%s/apps/%s/deploy", ws, url.PathEscape(app)), req, wait)
}

func (c *Client) Rollback(ctx context.Context, ws string, app string, req RollbackRequest, wait time.Duration) (*DeployResult, error) {
	return c.deployCall(ctx, fmt.Sprintf("/api/v1/workspaces/%s/apps/%s/rollback", ws, url.PathEscape(app)), req, wait)
}

func (c *Client) deployCall(ctx context.Context, path string, body any, wait time.Duration) (*DeployResult, error) {
	rb := c.c.Post(path).WithContext(ctx).JSONBody(body)
	if wait > 0 {
		wait = min(wait, MaxServerWait)
		secs := max(int((wait+time.Second-1)/time.Second), 1)

		rb = rb.QueryParam("wait", strconv.Itoa(secs)).Timeout(wait + 30*time.Second)
	}
	var raw json.RawMessage
	resp, err := c.doResp(rb, &raw)
	if err != nil {
		return nil, err
	}
	res, err := decodeDeployResult(raw)
	if err != nil {
		return nil, err
	}
	res.WaitTimedOut = resp.Header.Get("X-Miabi-Wait") == "timeout"
	return res, nil
}

func decodeDeployResult(raw json.RawMessage) (*DeployResult, error) {
	var probe struct {
		Kind string       `json:"kind"`
		Run  *PipelineRun `json:"run"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, err
	}
	if probe.Kind == "pipeline_run" {
		if probe.Run == nil {
			return nil, fmt.Errorf("deploy answered a pipeline run without the run")
		}
		return &DeployResult{Kind: "pipeline_run", Run: probe.Run}, nil
	}
	var d Deployment
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, err
	}
	return &DeployResult{Kind: "deployment", Deployment: &d}, nil
}

func (c *Client) InvalidateBuildCache(ctx context.Context, ws string, app string) error {
	return c.post(ctx, fmt.Sprintf("/api/v1/workspaces/%s/apps/%s/build-cache/invalidate", ws, url.PathEscape(app)), nil, nil)
}

func (c *Client) Pipelines(ctx context.Context, ws string) ([]Pipeline, error) {
	var ps []Pipeline
	return ps, c.get(ctx, fmt.Sprintf("/api/v1/workspaces/%s/pipelines", ws), &ps)
}

// TriggerPipeline takes the pipeline's numeric id or uid.
func (c *Client) TriggerPipeline(ctx context.Context, ws, pipeline string, req TriggerPipelineRequest) (*PipelineRun, error) {
	var r PipelineRun
	return &r, c.post(ctx, fmt.Sprintf("/api/v1/workspaces/%s/pipelines/%s/trigger", ws, pipeline), req, &r)
}

func (c *Client) PipelineVersioning(ctx context.Context, ws, pipeline string) (*VersionPolicy, error) {
	var p VersionPolicy
	return &p, c.get(ctx, fmt.Sprintf("/api/v1/workspaces/%s/pipelines/%s/versioning", ws, url.PathEscape(pipeline)), &p)
}

func (c *Client) UpdateVersionPolicy(ctx context.Context, ws, policy string, s VersionSettings) (*VersionPolicy, error) {
	var p VersionPolicy
	return &p, c.put(ctx, fmt.Sprintf("/api/v1/workspaces/%s/versioning/policies/%s", ws, url.PathEscape(policy)), s, &p)
}

func (c *Client) SwitchVersionSeries(ctx context.Context, ws, policy, series string) (*VersionPolicy, error) {
	var p VersionPolicy
	return &p, c.post(ctx, fmt.Sprintf("/api/v1/workspaces/%s/versioning/policies/%s/switch-series", ws, url.PathEscape(policy)), map[string]string{"series": series}, &p)
}

func (c *Client) VersionAllocations(ctx context.Context, ws, policy string, size int) ([]VersionAllocation, error) {
	var out []VersionAllocation
	return out, c.get(ctx, fmt.Sprintf("/api/v1/workspaces/%s/versioning/policies/%s/allocations?size=%d", ws, url.PathEscape(policy), size), &out)
}

func (c *Client) RerunPipelineRun(ctx context.Context, ws string, runID uint, req RerunPipelineRequest) (*PipelineRun, error) {
	var r PipelineRun
	return &r, c.post(ctx, fmt.Sprintf("/api/v1/workspaces/%s/pipeline-runs/%d/rerun", ws, runID), req, &r)
}

func (c *Client) appAction(ctx context.Context, ws string, app string, action string) error {
	return c.post(ctx, fmt.Sprintf("/api/v1/workspaces/%s/apps/%s/%s", ws, url.PathEscape(app), action), nil, nil)
}

func (c *Client) StartApp(ctx context.Context, ws string, app string) error {
	return c.appAction(ctx, ws, app, "start")
}
func (c *Client) StopApp(ctx context.Context, ws string, app string) error {
	return c.appAction(ctx, ws, app, "stop")
}
func (c *Client) RestartApp(ctx context.Context, ws string, app string) error {
	return c.appAction(ctx, ws, app, "restart")
}

// SetAppSource replaces the whole source: the server clears the fields of the source being left.
func (c *Client) SetAppSource(ctx context.Context, ws string, app string, req SetAppSourceRequest) (*SetAppSourceResult, error) {
	var r SetAppSourceResult
	return &r, c.put(ctx, fmt.Sprintf("/api/v1/workspaces/%s/apps/%s/source", ws, url.PathEscape(app)), req, &r)
}

func (c *Client) ResyncAppPipeline(ctx context.Context, ws string, app string) (*ResyncPipelineResult, error) {
	var r ResyncPipelineResult
	return &r, c.post(ctx, fmt.Sprintf("/api/v1/workspaces/%s/apps/%s/pipeline/resync", ws, url.PathEscape(app)), nil, &r)
}

func (c *Client) DeleteApp(ctx context.Context, ws string, app string) error {
	return c.del(ctx, fmt.Sprintf("/api/v1/workspaces/%s/apps/%s", ws, url.PathEscape(app)), nil)
}

func (c *Client) Deployments(ctx context.Context, ws string, app string) ([]Deployment, error) {
	var deps []Deployment
	return deps, c.get(ctx, fmt.Sprintf("/api/v1/workspaces/%s/apps/%s/deployments", ws, url.PathEscape(app)), &deps)
}

// Deployment falls back to the recent list when the server lacks the per-deployment GET (404/405).
func (c *Client) Deployment(ctx context.Context, ws string, app string, depID uint) (*Deployment, error) {
	var d Deployment
	err := c.get(ctx, fmt.Sprintf("/api/v1/workspaces/%s/apps/%s/deployments/%d", ws, url.PathEscape(app), depID), &d)
	if err == nil {
		return &d, nil
	}
	var ae *APIError
	if !errors.As(err, &ae) || (ae.StatusCode != http.StatusNotFound && ae.StatusCode != http.StatusMethodNotAllowed) {
		return nil, err
	}
	deps, err := c.Deployments(ctx, ws, app)
	if err != nil {
		return nil, err
	}
	for i := range deps {
		if deps[i].ID == depID {
			return &deps[i], nil
		}
	}
	return nil, fmt.Errorf("deployment #%d not found", depID)
}

// DeploymentByNumber maps the per-app number users pass to the deployment, whose ID API paths use.
func (c *Client) DeploymentByNumber(ctx context.Context, ws string, app string, number int) (*Deployment, error) {
	deps, err := c.Deployments(ctx, ws, app)
	if err != nil {
		return nil, err
	}
	for i := range deps {
		if deps[i].Number == number {
			return &deps[i], nil
		}
	}
	return nil, fmt.Errorf("deployment #%d not found for this app", number)
}

// ReleaseByVersion resolves a version to the release, since rollback takes the release ID.
func (c *Client) ReleaseByVersion(ctx context.Context, ws string, app string, version int) (*Release, error) {
	rels, err := c.Releases(ctx, ws, app)
	if err != nil {
		return nil, err
	}
	for i := range rels {
		if rels[i].Version == version {
			return &rels[i], nil
		}
	}
	return nil, fmt.Errorf("release v%d not found for this app", version)
}

func (c *Client) Releases(ctx context.Context, ws string, app string) ([]Release, error) {
	var rs []Release
	return rs, c.get(ctx, fmt.Sprintf("/api/v1/workspaces/%s/apps/%s/releases", ws, url.PathEscape(app)), &rs)
}

func (c *Client) Databases(ctx context.Context, ws string) ([]DatabaseInstance, error) {
	var dbs []DatabaseInstance
	return dbs, c.get(ctx, fmt.Sprintf("/api/v1/workspaces/%s/databases", ws), &dbs)
}

func (c *Client) Database(ctx context.Context, ws string, id uint) (*DatabaseInstance, error) {
	var d DatabaseInstance
	return &d, c.get(ctx, fmt.Sprintf("/api/v1/workspaces/%s/databases/%d", ws, id), &d)
}

func (c *Client) ResolveDatabaseID(ctx context.Context, ws, ref string) (uint, error) {
	if id, err := strconv.ParseUint(ref, 10, 64); err == nil {
		return uint(id), nil
	}
	dbs, err := c.Databases(ctx, ws)
	if err != nil {
		return 0, err
	}
	for _, d := range dbs {
		if d.Name == ref {
			return d.ID, nil
		}
	}
	return 0, fmt.Errorf("database %q not found in this workspace", ref)
}

func (c *Client) DatabaseEngines(ctx context.Context, ws string) ([]EngineDefault, error) {
	var e []EngineDefault
	return e, c.get(ctx, fmt.Sprintf("/api/v1/workspaces/%s/database-engines", ws), &e)
}

func (c *Client) CreateDatabase(ctx context.Context, ws string, req CreateDatabaseRequest) (*DatabaseInstance, error) {
	var d DatabaseInstance
	return &d, c.post(ctx, fmt.Sprintf("/api/v1/workspaces/%s/databases", ws), req, &d)
}

func (c *Client) dbAction(ctx context.Context, ws string, id uint, action string) error {
	return c.post(ctx, fmt.Sprintf("/api/v1/workspaces/%s/databases/%d/%s", ws, id, action), nil, nil)
}

func (c *Client) StartDatabase(ctx context.Context, ws string, id uint) error {
	return c.dbAction(ctx, ws, id, "start")
}
func (c *Client) StopDatabase(ctx context.Context, ws string, id uint) error {
	return c.dbAction(ctx, ws, id, "stop")
}
func (c *Client) RestartDatabase(ctx context.Context, ws string, id uint) error {
	return c.dbAction(ctx, ws, id, "restart")
}

func (c *Client) DeleteDatabaseInstance(ctx context.Context, ws string, id uint) error {
	return c.del(ctx, fmt.Sprintf("/api/v1/workspaces/%s/databases/%d", ws, id), nil)
}

func (c *Client) DatabaseCredentials(ctx context.Context, ws string, id uint) (*ConnectionInfo, error) {
	var info ConnectionInfo
	return &info, c.get(ctx, fmt.Sprintf("/api/v1/workspaces/%s/databases/%d/credentials", ws, id), &info)
}

func (c *Client) UpgradeDatabase(ctx context.Context, ws string, id uint, req UpgradeDatabaseRequest) (*DatabaseInstance, error) {
	var d DatabaseInstance
	return &d, c.post(ctx, fmt.Sprintf("/api/v1/workspaces/%s/databases/%d/upgrade", ws, id), req, &d)
}

func (c *Client) LogicalDatabases(ctx context.Context, ws string, id uint) ([]LogicalDatabase, error) {
	var dbs []LogicalDatabase
	return dbs, c.get(ctx, fmt.Sprintf("/api/v1/workspaces/%s/databases/%d/databases", ws, id), &dbs)
}

func (c *Client) AppDatabases(ctx context.Context, ws string, app string) ([]AppDatabase, error) {
	var dbs []AppDatabase
	return dbs, c.get(ctx, fmt.Sprintf("/api/v1/workspaces/%s/apps/%s/databases", ws, url.PathEscape(app)), &dbs)
}

func (c *Client) AttachDatabase(ctx context.Context, ws string, app string, dbID uint, req LinkDatabaseRequest) (*LinkDatabaseResult, error) {
	var r LinkDatabaseResult
	return &r, c.put(ctx, fmt.Sprintf("/api/v1/workspaces/%s/apps/%s/databases/%d", ws, url.PathEscape(app), dbID), req, &r)
}

func (c *Client) DetachDatabase(ctx context.Context, ws string, app string, dbID uint) error {
	return c.del(ctx, fmt.Sprintf("/api/v1/workspaces/%s/apps/%s/databases/%d", ws, url.PathEscape(app), dbID), nil)
}

func (c *Client) LinkDatabaseInstance(ctx context.Context, ws string, app string, instID uint, req LinkDatabaseRequest) (*LinkDatabaseResult, error) {
	var r LinkDatabaseResult
	return &r, c.put(ctx, fmt.Sprintf("/api/v1/workspaces/%s/apps/%s/database-instances/%d", ws, url.PathEscape(app), instID), req, &r)
}

func (c *Client) UnlinkDatabaseInstance(ctx context.Context, ws string, app string, instID uint) error {
	return c.del(ctx, fmt.Sprintf("/api/v1/workspaces/%s/apps/%s/database-instances/%d", ws, url.PathEscape(app), instID), nil)
}

func (c *Client) CreateLogicalDatabase(ctx context.Context, ws string, id uint, req CreateLogicalDatabaseRequest) (*CreateLogicalDatabaseResult, error) {
	var r CreateLogicalDatabaseResult
	return &r, c.post(ctx, fmt.Sprintf("/api/v1/workspaces/%s/databases/%d/databases", ws, id), req, &r)
}

func (c *Client) LogicalDatabaseConnection(ctx context.Context, ws string, id, dbID uint) (*ConnectionInfo, error) {
	var info ConnectionInfo
	return &info, c.get(ctx, fmt.Sprintf("/api/v1/workspaces/%s/databases/%d/databases/%d/connection", ws, id, dbID), &info)
}

func (c *Client) DeleteLogicalDatabase(ctx context.Context, ws string, id, dbID uint) error {
	return c.del(ctx, fmt.Sprintf("/api/v1/workspaces/%s/databases/%d/databases/%d", ws, id, dbID), nil)
}

// EnvVars returns secret values masked; the API never returns them in plaintext.
func (c *Client) EnvVars(ctx context.Context, ws string, app string) ([]EnvVar, error) {
	var vars []EnvVar
	return vars, c.get(ctx, fmt.Sprintf("/api/v1/workspaces/%s/apps/%s/env", ws, url.PathEscape(app)), &vars)
}

func (c *Client) SetEnv(ctx context.Context, ws string, app string, req SetEnvRequest) error {
	return c.put(ctx, fmt.Sprintf("/api/v1/workspaces/%s/apps/%s/env", ws, url.PathEscape(app)), req, nil)
}

func (c *Client) ImportEnv(ctx context.Context, ws string, app string, req ImportEnvRequest) error {
	return c.post(ctx, fmt.Sprintf("/api/v1/workspaces/%s/apps/%s/env/import", ws, url.PathEscape(app)), req, nil)
}

// Configs returns keys and digests only, never content.
func (c *Client) Configs(ctx context.Context, ws string) ([]Config, error) {
	var cfgs []Config
	return cfgs, c.get(ctx, fmt.Sprintf("/api/v1/workspaces/%s/configs?page=0&size=500", ws), &cfgs)
}

func (c *Client) Config(ctx context.Context, ws string, id uint) (*Config, error) {
	var cfg Config
	return &cfg, c.get(ctx, fmt.Sprintf("/api/v1/workspaces/%s/configs/%d", ws, id), &cfg)
}

func (c *Client) CreateConfig(ctx context.Context, ws string, req CreateConfigRequest) (*Config, error) {
	var cfg Config
	return &cfg, c.post(ctx, fmt.Sprintf("/api/v1/workspaces/%s/configs", ws), req, &cfg)
}

func (c *Client) UpdateConfig(ctx context.Context, ws string, id uint, req UpdateConfigRequest) (*Config, error) {
	var cfg Config
	return &cfg, c.put(ctx, fmt.Sprintf("/api/v1/workspaces/%s/configs/%d", ws, id), req, &cfg)
}

// ConfigData returns a config's decrypted files (admin only, audited).
func (c *Client) ConfigData(ctx context.Context, ws string, id uint) (map[string]string, error) {
	var r ConfigReveal
	if err := c.get(ctx, fmt.Sprintf("/api/v1/workspaces/%s/configs/%d/reveal", ws, id), &r); err != nil {
		return nil, err
	}
	return r.Data, nil
}

func (c *Client) ConfigUsage(ctx context.Context, ws string, id uint) ([]SecretUsage, error) {
	var u []SecretUsage
	return u, c.get(ctx, fmt.Sprintf("/api/v1/workspaces/%s/configs/%d/usage", ws, id), &u)
}

func (c *Client) DeleteConfig(ctx context.Context, ws string, id uint) error {
	return c.del(ctx, fmt.Sprintf("/api/v1/workspaces/%s/configs/%d", ws, id), nil)
}

// FindConfigByName returns nil, not an error, when no config has that name.
func (c *Client) FindConfigByName(ctx context.Context, ws, name string) (*Config, error) {
	cfgs, err := c.Configs(ctx, ws)
	if err != nil {
		return nil, err
	}
	for i := range cfgs {
		if cfgs[i].Name == name {
			return &cfgs[i], nil
		}
	}
	return nil, nil
}

func (c *Client) ResolveConfigID(ctx context.Context, ws, ref string) (uint, error) {
	if id, err := strconv.ParseUint(ref, 10, 64); err == nil {
		return uint(id), nil
	}
	cfg, err := c.FindConfigByName(ctx, ws, ref)
	if err != nil {
		return 0, err
	}
	if cfg == nil {
		return 0, fmt.Errorf("config %q not found", ref)
	}
	return cfg.ID, nil
}

// Volumes fetches every volume at once; the endpoint is not paginated.
func (c *Client) Volumes(ctx context.Context, ws string) ([]Volume, error) {
	var vols []Volume
	return vols, c.get(ctx, fmt.Sprintf("/api/v1/workspaces/%s/volumes", ws), &vols)
}

// Volume takes a numeric id only, unlike the file routes; resolve names with ResolveVolumeID.
func (c *Client) Volume(ctx context.Context, ws string, id uint) (*VolumeDetail, error) {
	var v VolumeDetail
	return &v, c.get(ctx, fmt.Sprintf("/api/v1/workspaces/%s/volumes/%d", ws, id), &v)
}

func (c *Client) CreateVolume(ctx context.Context, ws string, req CreateVolumeRequest) (*Volume, error) {
	var v Volume
	return &v, c.post(ctx, fmt.Sprintf("/api/v1/workspaces/%s/volumes", ws), req, &v)
}

// DeleteVolume fails with 409 while an application still mounts the volume.
func (c *Client) DeleteVolume(ctx context.Context, ws string, id uint) error {
	return c.del(ctx, fmt.Sprintf("/api/v1/workspaces/%s/volumes/%d", ws, id), nil)
}

func (c *Client) WorkspaceStorage(ctx context.Context, ws string) (*WorkspaceStorage, error) {
	var s WorkspaceStorage
	return &s, c.get(ctx, fmt.Sprintf("/api/v1/workspaces/%s/storage", ws), &s)
}

// FindVolumeByName returns the named volume, or nil when none exists.
func (c *Client) FindVolumeByName(ctx context.Context, ws, name string) (*Volume, error) {
	vols, err := c.Volumes(ctx, ws)
	if err != nil {
		return nil, err
	}
	for i := range vols {
		if vols[i].Name == name {
			return &vols[i], nil
		}
	}
	return nil, nil
}

func (c *Client) ResolveVolumeID(ctx context.Context, ws, ref string) (uint, error) {
	if id, err := strconv.ParseUint(ref, 10, 64); err == nil {
		return uint(id), nil
	}
	v, err := c.FindVolumeByName(ctx, ws, ref)
	if err != nil {
		return 0, err
	}
	if v == nil {
		return 0, fmt.Errorf("volume %q not found in this workspace", ref)
	}
	return v.ID, nil
}

// AttachVolume only flags the app for a redeploy; it does not restart it.
func (c *Client) AttachVolume(ctx context.Context, ws string, app string, req AttachVolumeRequest) error {
	return c.post(ctx, fmt.Sprintf("/api/v1/workspaces/%s/apps/%s/volumes", ws, url.PathEscape(app)), req, nil)
}

// DetachVolume keeps the volume's data.
func (c *Client) DetachVolume(ctx context.Context, ws string, app string, volumeID uint) error {
	return c.del(ctx, fmt.Sprintf("/api/v1/workspaces/%s/apps/%s/volumes/%d", ws, url.PathEscape(app), volumeID), nil)
}

// FileTransferTimeout replaces the 30s control-plane timeout, too short for large files on slow links.
const FileTransferTimeout = 30 * time.Minute

// MaxVolumeUploadBytes mirrors the panel's cap so an oversized file fails before it is sent.
const MaxVolumeUploadBytes = 512 << 20

// MaxListedVolumeFiles mirrors the panel's unreported listing cap; a listing this long may be truncated.
const MaxListedVolumeFiles = 5000

func (c *Client) VolumeFiles(ctx context.Context, ws string, id uint) ([]VolumeFile, error) {
	var files []VolumeFile
	return files, c.get(ctx, fmt.Sprintf("/api/v1/workspaces/%s/volumes/%d/files", ws, id), &files)
}

// UploadVolumeFile overwrites an existing file; subdir may be empty.
func (c *Client) UploadVolumeFile(ctx context.Context, ws string, id uint, subdir, name string, content io.Reader) (string, error) {
	var out VolumeUploadResult
	rb := c.c.Post(fmt.Sprintf("/api/v1/workspaces/%s/volumes/%d/files", ws, id)).
		WithContext(ctx).
		Timeout(FileTransferTimeout).
		Multipart(func(w *multipart.Writer) error {
			if subdir != "" {
				if err := w.WriteField("path", subdir); err != nil {
					return err
				}
			}
			fw, err := w.CreateFormFile("file", name)
			if err != nil {
				return err
			}
			_, err = io.Copy(fw, content)
			return err
		})
	if err := c.do(rb, &out); err != nil {
		return "", err
	}
	return out.Path, nil
}

// DownloadVolumeFile streams so a file up to the panel's 512 MiB cap is never held in memory.
func (c *Client) DownloadVolumeFile(ctx context.Context, ws string, id uint, path string, dst io.Writer) (int64, error) {
	body, err := c.streamRaw(ctx, fmt.Sprintf("/api/v1/workspaces/%s/volumes/%d/files/download?path=%s", ws, id, url.QueryEscape(path)))
	if err != nil {
		return 0, err
	}
	defer func() { _ = body.Close() }()
	return io.Copy(dst, body)
}

// DeleteVolumeFile is recursive for directories.
func (c *Client) DeleteVolumeFile(ctx context.Context, ws string, id uint, path string) error {
	return c.del(ctx, fmt.Sprintf("/api/v1/workspaces/%s/volumes/%d/files?path=%s", ws, id, url.QueryEscape(path)), nil)
}

// errorBodyLimit bounds the read of a failed response: it is a title or an API error, not a file.
const errorBodyLimit = 64 << 10

// streamRaw vets the envelope and HTML before returning the open body; closing it releases the deadline.
func (c *Client) streamRaw(ctx context.Context, path string) (io.ReadCloser, error) {
	ctx, cancel := context.WithTimeout(ctx, FileTransferTimeout)
	fail := func(err error) (io.ReadCloser, error) {
		cancel()
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return fail(err)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("User-Agent", "miabi-cli/"+Version)
	// Same Accept as other calls, so an SSO gateway or WAF answers with an error, not a sign-in page.
	req.Header.Set("Accept", "application/json")

	resp, err := c.stream.Do(req)
	if err != nil {
		return fail(err)
	}
	if c.verbose {
		fmt.Fprintf(os.Stderr, "GET %s -> %s\n", req.URL, resp.Status)
	}

	reject := func() ([]byte, *client.Response) {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, errorBodyLimit))
		_ = resp.Body.Close()
		return b, &client.Response{Response: resp, Body: b, Method: http.MethodGet, URL: req.URL.String()}
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, cr := reject()
		var env envelope
		if json.Unmarshal(b, &env) == nil && env.Error != nil {
			return fail(env.Error)
		}
		if len(bytes.TrimSpace(b)) == 0 {
			return fail(fmt.Errorf("GET %s: HTTP %d", req.URL, resp.StatusCode))
		}
		return fail(notAPIResponse(cr))
	}
	// The download handler always answers octet-stream; HTML here is a sign-in page, not the file.
	if kind, _, _ := strings.Cut(resp.Header.Get("Content-Type"), ";"); strings.TrimSpace(kind) == "text/html" {
		_, cr := reject()
		return fail(notAPIResponse(cr))
	}
	return &cancelOnClose{ReadCloser: resp.Body, cancel: cancel}, nil
}

// cancelOnClose keeps the deadline alive past streamRaw; cancelling it is what frees a stalled transfer.
type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelOnClose) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}

// getRaw buffers the body; use streamRaw for anything that can be large.
func (c *Client) getRaw(ctx context.Context, path string) ([]byte, error) {
	resp, err := c.c.Get(path).WithContext(ctx).Timeout(FileTransferTimeout).Do()
	if err != nil {
		return nil, err
	}
	if !resp.IsSuccess() {
		var env envelope
		if json.Unmarshal(resp.Body, &env) == nil && env.Error != nil {
			return nil, apiError(env.Error, resp.StatusCode)
		}
		if len(bytes.TrimSpace(resp.Body)) == 0 {
			return nil, fmt.Errorf("%s %s: HTTP %d", resp.Method, resp.URL, resp.StatusCode)
		}
		return nil, notAPIResponse(resp)
	}
	// The download handler always answers octet-stream; HTML here is a sign-in page, not the file.
	if kind, _, _ := strings.Cut(resp.Header.Get("Content-Type"), ";"); strings.TrimSpace(kind) == "text/html" {
		return nil, notAPIResponse(resp)
	}
	return resp.Body, nil
}

// RestoreTimeout exceeds the 30s default because the panel restores inline before answering.
const RestoreTimeout = 60 * time.Minute

func (c *Client) VolumeBackups(ctx context.Context, ws string, id uint) ([]VolumeBackup, error) {
	var bs []VolumeBackup
	return bs, c.get(ctx, fmt.Sprintf("/api/v1/workspaces/%s/volumes/%d/backups", ws, id), &bs)
}

func (c *Client) VolumeBackupConfigured(ctx context.Context, ws string, id uint) (bool, error) {
	var st VolumeBackupStatus
	err := c.get(ctx, fmt.Sprintf("/api/v1/workspaces/%s/volumes/%d/backups/status", ws, id), &st)
	return st.S3Configured, err
}

// RunVolumeBackup only enqueues: the returned record is pending, not finished.
func (c *Client) RunVolumeBackup(ctx context.Context, ws string, id uint) (*VolumeBackup, error) {
	var b VolumeBackup
	return &b, c.post(ctx, fmt.Sprintf("/api/v1/workspaces/%s/volumes/%d/backups", ws, id), nil, &b)
}

// RestoreVolumeBackup overwrites the volume and blocks for the whole restore.
func (c *Client) RestoreVolumeBackup(ctx context.Context, ws string, id, backupID uint) error {
	return c.postLong(ctx, fmt.Sprintf("/api/v1/workspaces/%s/volumes/%d/backups/%d/restore", ws, id, backupID), nil, nil, RestoreTimeout)
}

func (c *Client) DeleteVolumeBackup(ctx context.Context, ws string, id, backupID uint) error {
	return c.del(ctx, fmt.Sprintf("/api/v1/workspaces/%s/volumes/%d/backups/%d", ws, id, backupID), nil)
}

// VerifyVolumeBackup checks presence, size and, for a sealed recovery point, that the passphrase opens it.
func (c *Client) VerifyVolumeBackup(ctx context.Context, ws string, id, backupID uint) (*VerifyResult, error) {
	var r VerifyResult
	return &r, c.post(ctx, fmt.Sprintf("/api/v1/workspaces/%s/volumes/%d/backups/%d/verify", ws, id, backupID), nil, &r)
}

func (c *Client) PinVolumeBackup(ctx context.Context, ws string, id, backupID uint, pinned bool) (*VolumeBackup, error) {
	var b VolumeBackup
	return &b, c.patch(ctx, fmt.Sprintf("/api/v1/workspaces/%s/volumes/%d/backups/%d", ws, id, backupID),
		map[string]bool{"pinned": pinned}, &b)
}

func (c *Client) VolumeBackupLogs(ctx context.Context, ws string, id, backupID uint) ([]byte, error) {
	return c.getRaw(ctx, fmt.Sprintf("/api/v1/workspaces/%s/volumes/%d/backups/%d/logs/download", ws, id, backupID))
}

// FindVolumeBackup filters the history because there is no single-backup endpoint.
func (c *Client) FindVolumeBackup(ctx context.Context, ws string, id, backupID uint) (*VolumeBackup, error) {
	bs, err := c.VolumeBackups(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	for i := range bs {
		if bs[i].ID == backupID {
			return &bs[i], nil
		}
	}
	return nil, fmt.Errorf("backup %d not found for this volume", backupID)
}

// volumeBackupPollInterval is a variable so tests do not sleep through real backoff.
var volumeBackupPollInterval = 3 * time.Second

func (c *Client) WaitForVolumeBackup(ctx context.Context, ws string, id, backupID uint, onUpdate func(status string)) (*VolumeBackup, error) {
	ticker := time.NewTicker(volumeBackupPollInterval)
	defer ticker.Stop()
	last := ""
	for {
		b, err := c.FindVolumeBackup(ctx, ws, id, backupID)
		if err != nil {
			return nil, err
		}
		if b.Status != last {
			last = b.Status
			if onUpdate != nil {
				onUpdate(b.Status)
			}
		}
		if IsBackupTerminal(b.Status) {
			return b, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

// Secrets asks for a large page so the paginated list arrives in one call; values never come back.
func (c *Client) Secrets(ctx context.Context, ws string) ([]Secret, error) {
	var s []Secret
	return s, c.get(ctx, fmt.Sprintf("/api/v1/workspaces/%s/secrets?page=0&size=500", ws), &s)
}

func (c *Client) SealingKey(ctx context.Context, ws string) (*SealingKey, error) {
	var k SealingKey
	return &k, c.get(ctx, fmt.Sprintf("/api/v1/workspaces/%s/sealing-key", ws), &k)
}

func (c *Client) CreateSecret(ctx context.Context, ws string, req CreateSecretRequest) (*Secret, error) {
	var s Secret
	return &s, c.post(ctx, fmt.Sprintf("/api/v1/workspaces/%s/secrets", ws), req, &s)
}

func (c *Client) UpdateSecret(ctx context.Context, ws string, id uint, req UpdateSecretRequest) (*Secret, error) {
	var s Secret
	return &s, c.put(ctx, fmt.Sprintf("/api/v1/workspaces/%s/secrets/%d", ws, id), req, &s)
}

// RevealSecret returns a secret's decrypted value (admin only, audited).
func (c *Client) RevealSecret(ctx context.Context, ws string, id uint) (string, error) {
	var r SecretReveal
	if err := c.get(ctx, fmt.Sprintf("/api/v1/workspaces/%s/secrets/%d/reveal", ws, id), &r); err != nil {
		return "", err
	}
	return r.Value, nil
}

func (c *Client) SecretUsage(ctx context.Context, ws string, id uint) ([]SecretUsage, error) {
	var u []SecretUsage
	return u, c.get(ctx, fmt.Sprintf("/api/v1/workspaces/%s/secrets/%d/usage", ws, id), &u)
}

func (c *Client) DeleteSecret(ctx context.Context, ws string, id uint) error {
	return c.del(ctx, fmt.Sprintf("/api/v1/workspaces/%s/secrets/%d", ws, id), nil)
}

// FindSecretByName returns nil, not an error, when no secret has that name.
func (c *Client) FindSecretByName(ctx context.Context, ws, name string) (*Secret, error) {
	secrets, err := c.Secrets(ctx, ws)
	if err != nil {
		return nil, err
	}
	for i := range secrets {
		if secrets[i].Name == name {
			return &secrets[i], nil
		}
	}
	return nil, nil
}

func (c *Client) ResolveSecretID(ctx context.Context, ws, ref string) (uint, error) {
	if id, err := strconv.ParseUint(ref, 10, 64); err == nil {
		return uint(id), nil
	}
	s, err := c.FindSecretByName(ctx, ws, ref)
	if err != nil {
		return 0, err
	}
	if s == nil {
		return 0, fmt.Errorf("secret %q not found in this workspace", ref)
	}
	return s.ID, nil
}

func (c *Client) PlanApply(ctx context.Context, ws string, manifests string, prune, force bool) (*Plan, error) {
	var p Plan
	req := ApplyRequest{Manifests: manifests, Prune: prune, Force: force, DryRun: true}
	return &p, c.post(ctx, fmt.Sprintf("/api/v1/workspaces/%s/apply", ws), req, &p)
}

func (c *Client) Apply(ctx context.Context, ws string, manifests string, prune, force bool) (*ApplyResult, error) {
	var r ApplyResult
	req := ApplyRequest{Manifests: manifests, Prune: prune, Force: force, DryRun: false}
	return &r, c.post(ctx, fmt.Sprintf("/api/v1/workspaces/%s/apply", ws), req, &r)
}

func (c *Client) PlanDelete(ctx context.Context, ws string, manifests string) (*Plan, error) {
	var p Plan
	req := ApplyRequest{Manifests: manifests, Delete: true, DryRun: true}
	return &p, c.post(ctx, fmt.Sprintf("/api/v1/workspaces/%s/apply", ws), req, &p)
}

// Delete removes exactly the resources the bundle names.
func (c *Client) Delete(ctx context.Context, ws string, manifests string) (*ApplyResult, error) {
	var r ApplyResult
	req := ApplyRequest{Manifests: manifests, Delete: true, DryRun: false}
	return &r, c.post(ctx, fmt.Sprintf("/api/v1/workspaces/%s/apply", ws), req, &r)
}

// deployPollInterval is a variable so tests do not sleep through it.
var deployPollInterval = 2 * time.Second

// WaitForDeploy also stops on a canary awaiting promotion, which will not move on its own.
func (c *Client) WaitForDeploy(ctx context.Context, ws string, app string, depID uint, onUpdate func(status string)) (*Deployment, error) {
	ticker := time.NewTicker(deployPollInterval)
	defer ticker.Stop()
	last := ""
	for {
		d, err := c.Deployment(ctx, ws, app, depID)
		if err != nil {
			return nil, err
		}
		if d.Status != last {
			last = d.Status
			if onUpdate != nil {
				onUpdate(d.Status)
			}
		}
		if IsSettled(d.Status) {
			return d, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (c *Client) PipelineRun(ctx context.Context, ws string, runID uint) (*PipelineRun, error) {
	var r PipelineRun
	return &r, c.get(ctx, fmt.Sprintf("/api/v1/workspaces/%s/pipeline-runs/%d", ws, runID), &r)
}

func (c *Client) WaitForPipelineRun(ctx context.Context, ws string, runID uint, onUpdate func(status string)) (*PipelineRun, error) {
	ticker := time.NewTicker(deployPollInterval)
	defer ticker.Stop()
	last := ""
	for {
		r, err := c.PipelineRun(ctx, ws, runID)
		if err != nil {
			return nil, err
		}
		if r.Status != last {
			last = r.Status
			if onUpdate != nil {
				onUpdate(r.Status)
			}
		}
		if IsRunTerminal(r.Status) {
			return r, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}
