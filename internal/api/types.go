package api

import "time"

// These are thin, read-only mirrors of the documented JSON — only the fields the
// CLI uses. They are intentionally not generated from the server's internal DTOs
// (the OpenAPI spec is the contract); extend them as commands grow.

// Me is GET /api/v1/me — the authenticated identity and, for an API key, its
// scopes and bound workspace.
type Me struct {
	ID       uint   `json:"id"`
	Name     string `json:"name"`
	Username string `json:"username"`
	Email    string `json:"email"`
	Role     string `json:"role"`
	Auth     struct {
		Method      string   `json:"method"`       // api_key | jwt
		APIKeyID    *uint    `json:"api_key_id"`   // when method=api_key
		WorkspaceID *uint    `json:"workspace_id"` // set only for workspace-bound keys
		Scopes      []string `json:"scopes"`
	} `json:"auth"`
}

// LoginToken is the minted CLI token returned by the login-token claim endpoint
// (the browser hands off a single-use code; this is what it exchanges to).
type LoginToken struct {
	Token     string `json:"token"`
	ServerURL string `json:"server_url"`
	ExpiresAt string `json:"expires_at"`
}

// Workspace is one entry of GET /api/v1/workspaces. Name is the unique handle;
// DisplayName is the free-text label.
type Workspace struct {
	ID          uint   `json:"id"`
	UID         string `json:"uid"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
}

// App is an application listing/detail (only the fields the CLI surfaces). Name
// is the unique handle; DisplayName is the free-text label.
type App struct {
	ID          uint   `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Image       string `json:"image"`
	Tag         string `json:"tag"`
	// SourceType is "image" or "git" — what the app builds from.
	SourceType       string    `json:"source_type"`
	GitRepo          string    `json:"git_repo,omitempty"`
	GitRef           string    `json:"git_ref,omitempty"`
	Status           string    `json:"status"`
	CurrentReleaseID *uint     `json:"current_release_id"`
	CreatedAt        time.Time `json:"created_at"`
}

// CreateAppRequest is the body of POST .../apps (only the common fields the CLI
// exposes). SourceType is "image" or "git"; the CLI infers it from the flags.
// DisplayName is the required free-text label; Name pins the handle (the server
// otherwise derives it from DisplayName).
type CreateAppRequest struct {
	DisplayName string   `json:"display_name"`
	Name        string   `json:"name,omitempty"`
	ServerID    uint     `json:"server_id,omitempty"`
	SourceType  string   `json:"source_type,omitempty"`
	Image       string   `json:"image,omitempty"`
	Tag         string   `json:"tag,omitempty"`
	GitRepo     string   `json:"git_repo,omitempty"`
	GitRef      string   `json:"git_ref,omitempty"`
	BuildMethod string   `json:"build_method,omitempty"`
	Port        int      `json:"port,omitempty"`
	Command     []string `json:"command,omitempty"`
}

// SetAppSourceRequest is a whole-source replacement. Only the fields of the chosen source_type are
// read; the server clears the other source's.
type SetAppSourceRequest struct {
	SourceType string `json:"source_type"`

	Image      string `json:"image,omitempty"`
	Tag        string `json:"tag,omitempty"`
	RegistryID *uint  `json:"registry_id,omitempty"`

	GitRepo         string `json:"git_repo,omitempty"`
	GitRef          string `json:"git_ref,omitempty"`
	GitRepositoryID *uint  `json:"git_repository_id,omitempty"`
	BuildMethod     string `json:"build_method,omitempty"`
	Builder         string `json:"builder,omitempty"`
}

// SourceChange reports what else moved when the source changed. None of it is visible in the app
// record afterwards, so it is worth surfacing.
type SourceChange struct {
	From string `json:"from"`
	To   string `json:"to"`
	// Switched is false when only the details changed (a new tag, a different branch).
	Switched bool `json:"switched"`
	// PipelineRemoved is true when leaving git dropped a repo-owned pipeline.
	PipelineRemoved bool `json:"pipeline_removed"`
	// RedeployRequired is true when the running container no longer matches the source.
	RedeployRequired bool `json:"redeploy_required"`
}

type SetAppSourceResult struct {
	Application App          `json:"application"`
	Change      SourceChange `json:"change"`
}

// ResyncPipelineResult reports the pipeline the app ended up bound to. Changed is false when the
// repository's document already matched what was stored — a successful no-op, not a failure.
type ResyncPipelineResult struct {
	Pipeline *struct {
		Name       string `json:"name"`
		SourcePath string `json:"source_path"`
		SourceRef  string `json:"source_ref"`
	} `json:"pipeline"`
	Changed bool `json:"changed"`
	Adopted bool `json:"adopted"`
}

// Deployment is the deploy/rollback response and status object.
type Deployment struct {
	ID uint `json:"id"`
	// Number is the per-application deployment number (1, 2, 3…) users address
	// deployments by; ID is the durable platform-wide key used in API paths.
	Number        int        `json:"number"`
	ApplicationID uint       `json:"application_id"`
	Status        string     `json:"status"`
	Image         string     `json:"image"`
	Trigger       string     `json:"trigger"`
	Strategy      string     `json:"strategy"`
	Error         string     `json:"error"`
	StartedAt     *time.Time `json:"started_at"`
	FinishedAt    *time.Time `json:"finished_at"`
	CreatedAt     time.Time  `json:"created_at"`
	// Current marks the deployment whose release is live right now.
	Current bool `json:"current"`
}

// Release is one immutable release of an app.
type Release struct {
	ID            uint      `json:"id"`
	ApplicationID uint      `json:"application_id"`
	DeploymentID  uint      `json:"deployment_id"`
	Version       int       `json:"version"`
	Image         string    `json:"image"`
	Active        bool      `json:"active"`
	Digest        *string   `json:"digest"`
	CreatedAt     time.Time `json:"created_at"`
}

// Deploy-status classification. The server treats succeeded (and the legacy
// "running") as terminal success and "failed" as terminal failure; everything
// else is in-progress.
const (
	StatusSucceeded = "succeeded"
	StatusRunning   = "running" // legacy terminal success
	StatusFailed    = "failed"
)

// IsTerminal reports whether a deployment status will not change further.
func IsTerminal(status string) bool {
	switch status {
	case StatusSucceeded, StatusRunning, StatusFailed:
		return true
	default:
		return false
	}
}

// IsFailure reports a terminal failure.
func IsFailure(status string) bool { return status == StatusFailed }

// DeployRequest is the body of POST .../deploy. image is not part of the deploy
// contract (the app owns its image); only a tag/registry/strategy override.
type DeployRequest struct {
	Tag        string `json:"tag,omitempty"`
	Strategy   string `json:"strategy,omitempty"`
	RegistryID *uint  `json:"registry_id,omitempty"`
	// NoCache rebuilds every layer for this deploy only (git-source apps).
	NoCache bool `json:"no_cache,omitempty"`
}

// Pipeline is a CI/CD pipeline definition in the workspace.
type Pipeline struct {
	ID          uint      `json:"id"`
	Name        string    `json:"name"`
	DisplayName string    `json:"display_name"`
	Enabled     bool      `json:"enabled"`
	Source      string    `json:"source,omitempty"` // manual | repo
	CreatedAt   time.Time `json:"created_at"`
}

// PipelineRun is one execution of a pipeline.
type PipelineRun struct {
	ID         uint       `json:"id"`
	PipelineID uint       `json:"pipeline_id"`
	Number     int        `json:"number"`
	Status     string     `json:"status"`
	Trigger    string     `json:"trigger,omitempty"`
	Branch     string     `json:"branch,omitempty"`
	Commit     string     `json:"commit,omitempty"`
	NoCache    bool       `json:"no_cache,omitempty"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

// TriggerPipelineRequest is the body of POST .../pipelines/{id}/trigger.
type TriggerPipelineRequest struct {
	Branch  string `json:"branch,omitempty"`
	Commit  string `json:"commit,omitempty"`
	NoCache bool   `json:"no_cache,omitempty"`
}

// RerunPipelineRequest is the body of POST .../pipeline-runs/{id}/rerun.
type RerunPipelineRequest struct {
	NoCache bool `json:"no_cache,omitempty"`
}

// RollbackRequest is the body of POST .../rollback.
type RollbackRequest struct {
	ReleaseID uint `json:"release_id"`
}

// EnvVar is one of an application's environment variables. The server masks the
// value of a secret var, so Value is never the plaintext for those.
type EnvVar struct {
	Key      string `json:"key"`
	Value    string `json:"value"`
	IsSecret bool   `json:"is_secret"`
}

// SetEnvRequest is the body of PUT .../env.
type SetEnvRequest struct {
	Key      string `json:"key"`
	Value    string `json:"value"`
	IsSecret bool   `json:"is_secret"`
}

// ImportEnvRequest is the body of POST .../env/import.
type ImportEnvRequest struct {
	Content  string `json:"content"`
	IsSecret bool   `json:"is_secret"`
}

// === databases ============================================================

// DatabaseInstance is a managed database server (only the fields the CLI shows).
// Name is the unique handle; DisplayName is the free-text label.
type DatabaseInstance struct {
	ID          uint      `json:"id"`
	Name        string    `json:"name"`
	DisplayName string    `json:"display_name"`
	Engine      string    `json:"engine"`
	Version     string    `json:"version"`
	Status      string    `json:"status"`
	ServerID    uint      `json:"server_id"`
	ServerName  string    `json:"server_name,omitempty"`
	Host        string    `json:"host"`
	Port        int       `json:"port"`
	AdminUser   string    `json:"admin_user"`
	SizeBytes   int64     `json:"size_bytes,omitempty"`
	NetworkName string    `json:"network_name,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// LogicalDatabase is a database hosted on an instance (SQL engines).
type LogicalDatabase struct {
	ID            uint      `json:"id"`
	InstanceID    uint      `json:"instance_id"`
	Name          string    `json:"name"`
	Username      string    `json:"username"`
	Status        string    `json:"status"`
	ApplicationID *uint     `json:"application_id"`
	EnvPrefix     string    `json:"env_prefix,omitempty"`
	SizeBytes     int64     `json:"size_bytes,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

// ConnectionInfo is a revealed database connection (admin credentials / DSN).
type ConnectionInfo struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	Database string `json:"database"`
	URI      string `json:"uri"`
}

// EngineDefault is one entry of GET /database-engines.
type EngineDefault struct {
	Engine  string `json:"engine"`
	Image   string `json:"image"`
	Version string `json:"version"`
}

// CreateDatabaseRequest is the body of POST .../databases.
type CreateDatabaseRequest struct {
	Name     string `json:"name"`
	Engine   string `json:"engine"`
	Version  string `json:"version,omitempty"`
	ServerID uint   `json:"server_id,omitempty"`
	SizeMB   int    `json:"size_mb,omitempty"`
}

// UpgradeDatabaseRequest is the body of POST .../databases/{id}/upgrade.
type UpgradeDatabaseRequest struct {
	Version  string `json:"version"`
	StopApps bool   `json:"stop_apps,omitempty"`
}

// CreateLogicalDatabaseRequest is the body of POST .../databases/{id}/databases.
type CreateLogicalDatabaseRequest struct {
	Name          string `json:"name"`
	ApplicationID *uint  `json:"application_id"`
}

// CreateLogicalDatabaseResult is the create-logical-database response.
type CreateLogicalDatabaseResult struct {
	Database    LogicalDatabase `json:"database"`
	EnvInjected bool            `json:"env_injected"`
}

// === secrets (workspace Vault) ============================================

// Secret is one entry of the workspace Vault. Values are write-only over the API
// and never appear here — reveal them explicitly (admin-only, audited). Name is
// the unique handle env refs use (${{ secrets.NAME }}); DisplayName is a label.
type Secret struct {
	ID          uint   `json:"id"`
	UID         string `json:"uid"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
	// Version bumps on each value rotation.
	Version int `json:"version"`
	// Managed marks a secret owned by a platform resource (e.g. a managed
	// database); rotate it via its owner, not by hand.
	Managed   bool      `json:"managed"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// CreateSecretRequest is the body of POST .../secrets.
type CreateSecretRequest struct {
	Name        string `json:"name"`
	Value       string `json:"value"`
	Description string `json:"description,omitempty"`
}

// UpdateSecretRequest is the body of PUT .../secrets/{id}. A blank Value keeps
// the stored value (a description-only edit); a new value rotates the secret.
type UpdateSecretRequest struct {
	Value       string `json:"value"`
	Description string `json:"description"`
}

// SecretReveal is the payload of GET .../secrets/{id}/reveal.
type SecretReveal struct {
	Value string `json:"value"`
}

// Config is a workspace configuration file set. Content never comes back on a
// list or get — only keys, their sizes and the digest.
type Config struct {
	ID          uint           `json:"id"`
	UID         string         `json:"uid"`
	Name        string         `json:"name"`
	DisplayName string         `json:"display_name"`
	Description string         `json:"description"`
	Digest      string         `json:"digest"`
	Mode        string         `json:"mode"`
	Sensitive   bool           `json:"sensitive"`
	Delimiters  []string       `json:"delimiters,omitempty"`
	Version     int            `json:"version"`
	Managed     bool           `json:"managed"`
	Keys        []string       `json:"keys"`
	Sizes       map[string]int `json:"sizes"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

type CreateConfigRequest struct {
	Name        string            `json:"name"`
	DisplayName string            `json:"display_name,omitempty"`
	Description string            `json:"description,omitempty"`
	Data        map[string]string `json:"data"`
	Mode        string            `json:"mode,omitempty"`
	Sensitive   bool              `json:"sensitive,omitempty"`
	Delimiters  []string          `json:"delimiters,omitempty"`
}

type UpdateConfigRequest struct {
	Data        map[string]string `json:"data,omitempty"`
	DisplayName string            `json:"display_name,omitempty"`
	Description string            `json:"description,omitempty"`
	Mode        string            `json:"mode,omitempty"`
	Delimiters  []string          `json:"delimiters,omitempty"`
}

// ConfigReveal is the decrypted file set (admin only, audited).
type ConfigReveal struct {
	Data map[string]string `json:"data"`
}

// SecretUsage is one app referencing a secret (GET .../secrets/{id}/usage).
type SecretUsage struct {
	ID   uint   `json:"id"`
	Name string `json:"name"`
}

// ApplyRequest is the body of POST .../apply — a miabi.io/v1 manifest bundle and
// the converge options.
type ApplyRequest struct {
	Manifests string `json:"manifests"`
	Prune     bool   `json:"prune"`
	DryRun    bool   `json:"dry_run"`
	// Delete removes exactly the resources the bundle names instead of converging.
	Delete bool `json:"delete,omitempty"`
}

// Change is one planned operation on a resource.
type Change struct {
	Action string `json:"action"` // create | update | delete | noop
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Reason string `json:"reason,omitempty"`
}

// Plan is what a dry-run apply returns.
type Plan struct {
	Changes []Change `json:"changes"`
}

// ApplyFailure is one resource that failed to converge.
type ApplyFailure struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Action string `json:"action"`
	Error  string `json:"error"`
}

// ApplyResult is what a non-dry-run apply returns.
type ApplyResult struct {
	Plan     *Plan          `json:"plan"`
	Applied  int            `json:"applied"`
	DryRun   bool           `json:"dry_run"`
	Failures []ApplyFailure `json:"failures,omitempty"`
}
