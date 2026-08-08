// Package api is a thin wrapper over the Miabi /api/v1 HTTP surface, built on
// github.com/jkaninda/okapi/client. It unwraps the server's {success,data,error}
// envelope and maps the structured error to a Go error with a stable code.
package api

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/jkaninda/okapi/client"
)

// Version is stamped at build time (see .goreleaser.yaml); used in User-Agent.
var Version = "dev"

// Client talks to one panel with one token.
type Client struct {
	c       *client.Client
	verbose bool
}

// Options configures a client: the server base URL, bearer token, TLS trust
// (a custom CA bundle path and/or skip-verify), and verbose HTTP logging.
type Options struct {
	BaseURL      string
	Token        string
	CAFile       string
	InsecureSkip bool
	Verbose      bool
}

// New builds a client from o. It returns an error only when a configured CA
// bundle cannot be read or parsed — a misconfigured trust root should fail loudly
// rather than silently fall back to the system roots.
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
	httpc := &http.Client{
		Timeout:   30 * time.Second,
		Transport: &http.Transport{TLSClientConfig: tlsCfg},
	}
	opts := []client.Option{
		client.WithHTTPClient(httpc),
		client.WithBearerToken(o.Token),
		client.WithUserAgent("miabi-cli/" + Version),
		// Safe to retry: every call below is a GET or an idempotent action.
		client.WithRetry(client.RetryPolicy{MaxAttempts: 3, BaseDelay: 200 * time.Millisecond, MaxDelay: 2 * time.Second}),
	}
	if o.Verbose {
		opts = append(opts, client.WithMiddleware(client.LoggingMiddleware(os.Stderr)))
	}
	return &Client{c: client.New(o.BaseURL, opts...), verbose: o.Verbose}, nil
}

// APIError is the server's structured error envelope.
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

// envelope is the standard response wrapper.
type envelope struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   *APIError       `json:"error"`
}

// do performs a request and decodes the envelope's data into out (out may be
// nil). On a non-2xx it returns the server's *APIError when present.
func (c *Client) do(rb *client.RequestBuilder, out any) error {
	resp, err := rb.Do()
	if err != nil {
		// Transport-level or non-2xx (okapi returns *client.HTTPError). Try to
		// surface the structured envelope from the body.
		if he, ok := err.(*client.HTTPError); ok {
			var env envelope
			if json.Unmarshal(he.Body, &env) == nil && env.Error != nil {
				return env.Error
			}
			return fmt.Errorf("HTTP %d: %s", he.StatusCode, string(he.Body))
		}
		return err
	}
	if out == nil {
		return nil
	}
	var env envelope
	if err := json.Unmarshal(resp.Body, &env); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if env.Error != nil {
		return env.Error
	}
	if len(env.Data) == 0 || string(env.Data) == "null" {
		return nil
	}
	return json.Unmarshal(env.Data, out)
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

func (c *Client) put(ctx context.Context, path string, body, out any) error {
	rb := c.c.Put(path).WithContext(ctx)
	if body != nil {
		rb = rb.JSONBody(body)
	}
	return c.do(rb, out)
}

func (c *Client) del(ctx context.Context, path string, out any) error {
	return c.do(c.c.Delete(path).WithContext(ctx), out)
}

// === identity & workspaces ===================================

func (c *Client) Me(ctx context.Context) (*Me, error) {
	var m Me
	return &m, c.get(ctx, "/api/v1/me", &m)
}

// ClaimLoginToken exchanges a single-use hand-off code (delivered to the CLI's
// loopback callback by the browser login flow) for the minted token. The
// endpoint is public — the code is itself the short-lived, one-time credential —
// so this works on a client built without a token.
func (c *Client) ClaimLoginToken(ctx context.Context, code string) (*LoginToken, error) {
	var t LoginToken
	return &t, c.post(ctx, "/api/v1/auth/login-token/claim", map[string]string{"handoff": code}, &t)
}

func (c *Client) Workspaces(ctx context.Context) ([]Workspace, error) {
	var ws []Workspace
	return ws, c.get(ctx, "/api/v1/workspaces", &ws)
}

// ResolveWorkspaceName turns a user-supplied workspace reference into the
// canonical name (handle) the API addresses by. ref may be the name, a UID, or a
// numeric id (resolved to its name for back-compat); an empty ref falls back to
// fallback (the persisted/bound workspace) and finally to the sole workspace the
// caller can see. The returned value is always a name, since scoped routes are
// addressed by name.
func (c *Client) ResolveWorkspaceName(ctx context.Context, ref, fallback string) (string, error) {
	if ref == "" {
		ref = fallback
	}
	if ref == "" {
		ws, err := c.Workspaces(ctx)
		if err != nil {
			return "", err
		}
		if len(ws) == 1 {
			return ws[0].Name, nil
		}
		return "", fmt.Errorf("no workspace selected — pass --workspace, or run `miabi workspace switch <name>`")
	}
	ws, err := c.Workspaces(ctx)
	if err != nil {
		return "", err
	}
	idMatch, _ := strconv.ParseUint(ref, 10, 64)
	for _, w := range ws {
		if w.Name == ref || w.UID == ref || (idMatch != 0 && w.ID == uint(idMatch)) {
			return w.Name, nil
		}
	}
	return "", fmt.Errorf("workspace %q not found among your workspaces", ref)
}

// ======== applications ===================================

func (c *Client) Apps(ctx context.Context, ws string) ([]App, error) {
	var apps []App
	return apps, c.get(ctx, fmt.Sprintf("/api/v1/workspaces/%s/apps", ws), &apps)
}

func (c *Client) App(ctx context.Context, ws string, appID uint) (*App, error) {
	var a App
	return &a, c.get(ctx, fmt.Sprintf("/api/v1/workspaces/%s/apps/%d", ws, appID), &a)
}

func (c *Client) CreateApp(ctx context.Context, ws string, req CreateAppRequest) (*App, error) {
	var a App
	return &a, c.post(ctx, fmt.Sprintf("/api/v1/workspaces/%s/apps", ws), req, &a)
}

// ResolveAppID turns an app handle (or numeric id) into the numeric id paths use.
func (c *Client) ResolveAppID(ctx context.Context, ws string, ref string) (uint, error) {
	if id, err := strconv.ParseUint(ref, 10, 64); err == nil {
		return uint(id), nil
	}
	apps, err := c.Apps(ctx, ws)
	if err != nil {
		return 0, err
	}
	for _, a := range apps {
		if a.Name == ref {
			return a.ID, nil
		}
	}
	return 0, fmt.Errorf("application %q not found in this workspace", ref)
}

// --- deploy / rollback / releases -----------------------------------------

func (c *Client) Deploy(ctx context.Context, ws string, appID uint, req DeployRequest) (*Deployment, error) {
	var d Deployment
	return &d, c.post(ctx, fmt.Sprintf("/api/v1/workspaces/%s/apps/%d/deploy", ws, appID), req, &d)
}

// appAction posts to an app lifecycle sub-path (start|stop|restart).
func (c *Client) appAction(ctx context.Context, ws string, appID uint, action string) error {
	return c.post(ctx, fmt.Sprintf("/api/v1/workspaces/%s/apps/%d/%s", ws, appID, action), nil, nil)
}

func (c *Client) StartApp(ctx context.Context, ws string, appID uint) error {
	return c.appAction(ctx, ws, appID, "start")
}
func (c *Client) StopApp(ctx context.Context, ws string, appID uint) error {
	return c.appAction(ctx, ws, appID, "stop")
}
func (c *Client) RestartApp(ctx context.Context, ws string, appID uint) error {
	return c.appAction(ctx, ws, appID, "restart")
}

// SetAppSource replaces where an app's image comes from, including switching between a prebuilt
// image and a Git build. It is a whole-source replacement: the server clears the fields belonging
// to the source being left, so a partial request would leave the app describing both.
func (c *Client) SetAppSource(ctx context.Context, ws string, appID uint, req SetAppSourceRequest) (*SetAppSourceResult, error) {
	var r SetAppSourceResult
	return &r, c.put(ctx, fmt.Sprintf("/api/v1/workspaces/%s/apps/%d/source", ws, appID), req, &r)
}

// ResyncAppPipeline reloads the repository's pipelines.yaml: adopting one when the app has none,
// and re-syncing the stored spec when it already has one.
func (c *Client) ResyncAppPipeline(ctx context.Context, ws string, appID uint) (*ResyncPipelineResult, error) {
	var r ResyncPipelineResult
	return &r, c.post(ctx, fmt.Sprintf("/api/v1/workspaces/%s/apps/%d/pipeline/resync", ws, appID), nil, &r)
}

func (c *Client) DeleteApp(ctx context.Context, ws string, appID uint) error {
	return c.del(ctx, fmt.Sprintf("/api/v1/workspaces/%s/apps/%d", ws, appID), nil)
}

func (c *Client) Rollback(ctx context.Context, ws string, appID uint, req RollbackRequest) (*Deployment, error) {
	var d Deployment
	return &d, c.post(ctx, fmt.Sprintf("/api/v1/workspaces/%s/apps/%d/rollback", ws, appID), req, &d)
}

func (c *Client) Deployments(ctx context.Context, ws string, appID uint) ([]Deployment, error) {
	var deps []Deployment
	return deps, c.get(ctx, fmt.Sprintf("/api/v1/workspaces/%s/apps/%d/deployments", ws, appID), &deps)
}

// Deployment resolves a single deployment. The API exposes no per-deployment GET
// route, so it is found within the (recent) deployments list.
func (c *Client) Deployment(ctx context.Context, ws string, appID, depID uint) (*Deployment, error) {
	deps, err := c.Deployments(ctx, ws, appID)
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

// DeploymentByNumber resolves a per-application deployment number (the value
// users pass) to the full deployment, whose durable ID API paths address by.
func (c *Client) DeploymentByNumber(ctx context.Context, ws string, appID uint, number int) (*Deployment, error) {
	deps, err := c.Deployments(ctx, ws, appID)
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

// ReleaseByVersion resolves a per-application release version to the full
// release (the ID is what the rollback API expects).
func (c *Client) ReleaseByVersion(ctx context.Context, ws string, appID uint, version int) (*Release, error) {
	rels, err := c.Releases(ctx, ws, appID)
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

func (c *Client) Releases(ctx context.Context, ws string, appID uint) ([]Release, error) {
	var rs []Release
	return rs, c.get(ctx, fmt.Sprintf("/api/v1/workspaces/%s/apps/%d/releases", ws, appID), &rs)
}

// --- databases -------------------------------------------------------------

func (c *Client) Databases(ctx context.Context, ws string) ([]DatabaseInstance, error) {
	var dbs []DatabaseInstance
	return dbs, c.get(ctx, fmt.Sprintf("/api/v1/workspaces/%s/databases", ws), &dbs)
}

func (c *Client) Database(ctx context.Context, ws string, id uint) (*DatabaseInstance, error) {
	var d DatabaseInstance
	return &d, c.get(ctx, fmt.Sprintf("/api/v1/workspaces/%s/databases/%d", ws, id), &d)
}

// ResolveDatabaseID turns a database handle (or numeric id) into the numeric id.
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

// dbAction posts to a lifecycle sub-path (start|stop|restart) that returns a message.
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

// Logical databases hosted on an instance.

func (c *Client) LogicalDatabases(ctx context.Context, ws string, id uint) ([]LogicalDatabase, error) {
	var dbs []LogicalDatabase
	return dbs, c.get(ctx, fmt.Sprintf("/api/v1/workspaces/%s/databases/%d/databases", ws, id), &dbs)
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

// --- env -------------------------------------------------------------------

// EnvVars lists an application's environment variables. Secret values come back
// masked — the API never returns them in plaintext.
func (c *Client) EnvVars(ctx context.Context, ws string, appID uint) ([]EnvVar, error) {
	var vars []EnvVar
	return vars, c.get(ctx, fmt.Sprintf("/api/v1/workspaces/%s/apps/%d/env", ws, appID), &vars)
}

func (c *Client) SetEnv(ctx context.Context, ws string, appID uint, req SetEnvRequest) error {
	return c.put(ctx, fmt.Sprintf("/api/v1/workspaces/%s/apps/%d/env", ws, appID), req, nil)
}

func (c *Client) ImportEnv(ctx context.Context, ws string, appID uint, req ImportEnvRequest) error {
	return c.post(ctx, fmt.Sprintf("/api/v1/workspaces/%s/apps/%d/env/import", ws, appID), req, nil)
}

// --- secrets (workspace Vault) ---------------------------------------------

// Secrets lists a workspace's secrets (names + metadata, never values). The list
// endpoint is paginated; a large page size fetches them in one call.
func (c *Client) Secrets(ctx context.Context, ws string) ([]Secret, error) {
	var s []Secret
	return s, c.get(ctx, fmt.Sprintf("/api/v1/workspaces/%s/secrets?page=0&size=500", ws), &s)
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

// FindSecretByName returns the workspace secret with the given name, or nil when
// none exists (used by `set` to choose between create and rotate).
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

// ResolveSecretID turns a secret name (or numeric id) into its numeric id.
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

// ========= declarative apply ===================================

// PlanApply previews converging the workspace to the manifest bundle (dry-run).
func (c *Client) PlanApply(ctx context.Context, ws string, manifests string, prune bool) (*Plan, error) {
	var p Plan
	req := ApplyRequest{Manifests: manifests, Prune: prune, DryRun: true}
	return &p, c.post(ctx, fmt.Sprintf("/api/v1/workspaces/%s/apply", ws), req, &p)
}

// Apply converges the workspace to the manifest bundle.
func (c *Client) Apply(ctx context.Context, ws string, manifests string, prune bool) (*ApplyResult, error) {
	var r ApplyResult
	req := ApplyRequest{Manifests: manifests, Prune: prune, DryRun: false}
	return &r, c.post(ctx, fmt.Sprintf("/api/v1/workspaces/%s/apply", ws), req, &r)
}

// PlanDelete previews deleting exactly the resources the bundle names (dry-run).
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

// WaitForDeploy polls a deployment until it reaches a terminal state or the
// context is cancelled/times out, calling onUpdate (if non-nil) on each status
// change. It returns the final deployment.
func (c *Client) WaitForDeploy(ctx context.Context, ws string, appID, depID uint, onUpdate func(status string)) (*Deployment, error) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	last := ""
	for {
		d, err := c.Deployment(ctx, ws, appID, depID)
		if err != nil {
			return nil, err
		}
		if d.Status != last {
			last = d.Status
			if onUpdate != nil {
				onUpdate(d.Status)
			}
		}
		if IsTerminal(d.Status) {
			return d, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}
