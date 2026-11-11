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

package api

import "time"

// Read-only mirrors of the documented JSON, not generated from server DTOs: the OpenAPI spec is the contract.

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

type LoginToken struct {
	Token     string `json:"token"`
	ServerURL string `json:"server_url"`
	ExpiresAt string `json:"expires_at"`
}

type Workspace struct {
	ID          uint   `json:"id"`
	UID         string `json:"uid"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
}

type App struct {
	ID          uint   `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Image       string `json:"image"`
	Tag         string `json:"tag"`
	// SourceType is "image" or "git".
	SourceType       string    `json:"source_type"`
	GitRepo          string    `json:"git_repo,omitempty"`
	GitRef           string    `json:"git_ref,omitempty"`
	Status           string    `json:"status"`
	CurrentReleaseID *uint     `json:"current_release_id"`
	CreatedAt        time.Time `json:"created_at"`
}

// CreateAppRequest: the server derives Name from DisplayName when Name is empty.
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

// SetAppSourceRequest is a whole-source replacement; the server clears the other source's fields.
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

// SourceChange is worth surfacing: none of it shows in the app record afterwards.
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

// ResyncPipelineResult.Changed is false when the stored document already matched: a no-op, not a failure.
type ResyncPipelineResult struct {
	Pipeline *struct {
		Name       string `json:"name"`
		SourcePath string `json:"source_path"`
		SourceRef  string `json:"source_ref"`
	} `json:"pipeline"`
	Changed bool `json:"changed"`
	Adopted bool `json:"adopted"`
}

type Deployment struct {
	ID uint `json:"id"`
	// Number is the per-app number users see; ID is the durable key API paths use.
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
	Current       bool       `json:"current"`
}

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

// "running" is a legacy terminal success; any status that is not terminal is in progress.
const (
	StatusSucceeded = "succeeded"
	StatusRunning   = "running" // legacy terminal success
	StatusFailed    = "failed"
	// StatusCanary is not terminal: it waits for a human to promote or abort it.
	StatusCanary = "canary"
)

func IsTerminal(status string) bool {
	switch status {
	case StatusSucceeded, StatusRunning, StatusFailed:
		return true
	default:
		return false
	}
}

func IsFailure(status string) bool { return status == StatusFailed }

// IsSettled is also true for a canary, which will not move until someone promotes it.
func IsSettled(status string) bool { return IsTerminal(status) || status == StatusCanary }

// DeployRequest carries no image (the app owns it), only a tag/registry/strategy override.
type DeployRequest struct {
	Tag        string `json:"tag,omitempty"`
	Strategy   string `json:"strategy,omitempty"`
	RegistryID *uint  `json:"registry_id,omitempty"`
	// NoCache rebuilds every layer for this deploy only (git-source apps).
	NoCache bool `json:"no_cache,omitempty"`
}

type Pipeline struct {
	ID          uint      `json:"id"`
	Name        string    `json:"name"`
	DisplayName string    `json:"display_name"`
	Enabled     bool      `json:"enabled"`
	Source      string    `json:"source,omitempty"` // manual | repo
	CreatedAt   time.Time `json:"created_at"`
}

type PipelineRun struct {
	ID         uint       `json:"id"`
	PipelineID uint       `json:"pipeline_id"`
	Number     int        `json:"number"`
	Status     string     `json:"status"`
	Trigger    string     `json:"trigger,omitempty"`
	Branch     string     `json:"branch,omitempty"`
	Commit     string     `json:"commit,omitempty"`
	Version    string     `json:"version,omitempty"`
	ImageTag   string     `json:"image_tag,omitempty"`
	NoCache    bool       `json:"no_cache,omitempty"`
	Error      string     `json:"error,omitempty"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

func IsRunTerminal(status string) bool {
	switch status {
	case "succeeded", "failed", "canceled":
		return true
	default:
		return false
	}
}

// DeployResult sets exactly one of Deployment and Run: a repo-owned pipeline deploys by running it.
type DeployResult struct {
	Kind       string       `json:"kind"` // deployment | pipeline_run
	Deployment *Deployment  `json:"deployment,omitempty"`
	Run        *PipelineRun `json:"run,omitempty"`
	// WaitTimedOut is set when the server was asked to wait and gave up first.
	WaitTimedOut bool `json:"wait_timed_out,omitempty"`
}

type TriggerPipelineRequest struct {
	Branch  string `json:"branch,omitempty"`
	Commit  string `json:"commit,omitempty"`
	NoCache bool   `json:"no_cache,omitempty"`
	// Bump (patch|minor|major) or Version (exact, admins only) override this run's version.
	Bump    string `json:"bump,omitempty"`
	Version string `json:"version,omitempty"`
}

type VersionSettings struct {
	Scheme          string          `json:"scheme"`
	Template        string          `json:"template,omitempty"`
	Series          string          `json:"series,omitempty"`
	Bump            string          `json:"bump,omitempty"`
	Start           string          `json:"start,omitempty"`
	CalverFormat    string          `json:"calver_format,omitempty"`
	Branches        VersionBranches `json:"branches"`
	FloatingTags    []string        `json:"floating_tags,omitempty"`
	BumpFromCommits bool            `json:"bump_from_commits,omitempty"`
}

type VersionBranches struct {
	Release []string          `json:"release,omitempty"`
	Others  string            `json:"others,omitempty"`
	Series  map[string]string `json:"series,omitempty"`
}

type VersionPolicy struct {
	ID              uint              `json:"id"`
	UID             string            `json:"uid"`
	Scheme          string            `json:"scheme"`
	Template        string            `json:"template"`
	Series          string            `json:"series,omitempty"`
	Bump            string            `json:"bump"`
	Start           string            `json:"start,omitempty"`
	CalverFormat    string            `json:"calver_format,omitempty"`
	ReleaseBranches []string          `json:"release_branches,omitempty"`
	OtherBranches   string            `json:"other_branches"`
	BranchSeries    map[string]string `json:"branch_series,omitempty"`
	FloatingTags    []string          `json:"floating_tags,omitempty"`
	BumpFromCommits bool              `json:"bump_from_commits"`
	ManagedBy       string            `json:"managed_by,omitempty"`
	Current         string            `json:"current,omitempty"`
	OwnerKind       string            `json:"owner_kind,omitempty"`
	Repository      string            `json:"repository,omitempty"`
	Next            string            `json:"next,omitempty"`
}

func (p VersionPolicy) Settings() VersionSettings {
	return VersionSettings{
		Scheme: p.Scheme, Template: p.Template, Series: p.Series, Bump: p.Bump, Start: p.Start,
		CalverFormat: p.CalverFormat,
		Branches:     VersionBranches{Release: p.ReleaseBranches, Others: p.OtherBranches, Series: p.BranchSeries},
		FloatingTags: p.FloatingTags, BumpFromCommits: p.BumpFromCommits,
	}
}

type VersionAllocation struct {
	Version    string    `json:"version"`
	Tag        string    `json:"tag"`
	Series     string    `json:"series,omitempty"`
	Branch     string    `json:"branch,omitempty"`
	Prerelease bool      `json:"prerelease"`
	Status     string    `json:"status"`
	RunNumber  int       `json:"run_number,omitempty"`
	Digest     string    `json:"digest,omitempty"`
	Note       string    `json:"note,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

type RerunPipelineRequest struct {
	NoCache bool `json:"no_cache,omitempty"`
}

type RollbackRequest struct {
	ReleaseID uint `json:"release_id"`
}

// The server masks secret values, so Value is never their plaintext.
type EnvVar struct {
	Key      string `json:"key"`
	Value    string `json:"value"`
	IsSecret bool   `json:"is_secret"`
}

type SetEnvRequest struct {
	Key      string `json:"key"`
	Value    string `json:"value"`
	IsSecret bool   `json:"is_secret"`
}

type ImportEnvRequest struct {
	Content  string `json:"content"`
	IsSecret bool   `json:"is_secret"`
}

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

type LogicalDatabase struct {
	ID            uint              `json:"id"`
	InstanceID    uint              `json:"instance_id"`
	Name          string            `json:"name"`
	Username      string            `json:"username"`
	Status        string            `json:"status"`
	ApplicationID *uint             `json:"application_id"`
	EnvPrefix     string            `json:"env_prefix,omitempty"`
	EnvMap        map[string]string `json:"env_map,omitempty"`
	EnvVars       []string          `json:"env_vars,omitempty"`
	SizeBytes     int64             `json:"size_bytes,omitempty"`
	CreatedAt     time.Time         `json:"created_at"`
}

// AppDatabase is a logical database (kind "database") or a whole instance (kind "instance", ids are its).
type AppDatabase struct {
	LogicalDatabase
	Kind         string `json:"kind"`
	InstanceName string `json:"instance_name"`
	Engine       string `json:"engine"`
	Host         string `json:"host"`
	Port         int    `json:"port"`
}

// LinkDatabaseRequest.EnvMap renames a connection field's env var; an empty name skips that field.
type LinkDatabaseRequest struct {
	EnvPrefix string            `json:"env_prefix,omitempty"`
	EnvMap    map[string]string `json:"env_map,omitempty"`
}

type LinkDatabaseResult struct {
	EnvInjected bool     `json:"env_injected"`
	EnvVars     []string `json:"env_vars"`
}

type ConnectionInfo struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	Database string `json:"database"`
	URI      string `json:"uri"`
}

type EngineDefault struct {
	Engine  string `json:"engine"`
	Image   string `json:"image"`
	Version string `json:"version"`
}

type CreateDatabaseRequest struct {
	Name     string `json:"name"`
	Engine   string `json:"engine"`
	Version  string `json:"version,omitempty"`
	ServerID uint   `json:"server_id,omitempty"`
	SizeMB   int    `json:"size_mb,omitempty"`
}

type UpgradeDatabaseRequest struct {
	Version  string `json:"version"`
	StopApps bool   `json:"stop_apps,omitempty"`
}

type CreateLogicalDatabaseRequest struct {
	Name          string `json:"name"`
	ApplicationID *uint  `json:"application_id"`
}

type CreateLogicalDatabaseResult struct {
	Database    LogicalDatabase `json:"database"`
	EnvInjected bool            `json:"env_injected"`
}

// Volume.UsedBytes is measured, SizeBytes declared; a nil UsedMeasuredAt means never measured, not empty.
type Volume struct {
	ID          uint   `json:"id"`
	UID         string `json:"uid"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	// DockerName is the underlying Docker volume, which is not the handle.
	DockerName string `json:"docker_name"`
	// ServerID 0 is the local control-plane node.
	ServerID       uint       `json:"server_id"`
	ServerName     string     `json:"server_name,omitempty"`
	Mountpoint     string     `json:"mountpoint,omitempty"`
	SizeBytes      int64      `json:"size_bytes"`
	UsedBytes      int64      `json:"used_bytes"`
	UsedMeasuredAt *time.Time `json:"used_measured_at,omitempty"`
	// Driver is local (rwo) or nfs | cifs | host (rwx).
	Driver     string `json:"driver"`
	AccessMode string `json:"access_mode"`
	HostPath   string `json:"host_path,omitempty"`
	// Imported marks a volume that adopted a pre-existing external Docker volume.
	Imported  bool      `json:"imported"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type VolumeUsage struct {
	AppID          uint   `json:"app_id"`
	AppName        string `json:"app_name"`
	AppDisplayName string `json:"app_display_name"`
	Path           string `json:"path"`
}

type VolumeDetail struct {
	// Inlined for yaml.v3, which otherwise nests the anonymous struct under "volume"; encoding/json flattens it.
	Volume `yaml:",inline"`
	Exists bool          `json:"exists"`
	InUse  bool          `json:"in_use"`
	UsedBy []VolumeUsage `json:"used_by"`
}

// CreateVolumeRequest is final: a volume has no update endpoint.
type CreateVolumeRequest struct {
	Name     string `json:"name"`
	ServerID uint   `json:"server_id,omitempty"`
	SizeMB   int    `json:"size_mb,omitempty"`
	// DriverOpts are the backend's mount options (NFS/CIFS: device, o; host: path), encrypted and never returned.
	Driver     string            `json:"driver,omitempty"`
	DriverOpts map[string]string `json:"driver_opts,omitempty"`
}

// WorkspaceStorage.LimitMB is -1 when unlimited.
type WorkspaceStorage struct {
	DeclaredBytes int64      `json:"declared_bytes"`
	UsedBytes     int64      `json:"used_bytes"`
	LimitMB       int        `json:"limit_mb"`
	MeasuredAt    *time.Time `json:"measured_at,omitempty"`
	VolumeCount   int64      `json:"volume_count"`
}

// VolumeFile paths are relative to the volume root; ModTime is unix seconds; directories report size 0.
type VolumeFile struct {
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	ModTime int64  `json:"mod_time"`
	IsDir   bool   `json:"is_dir"`
}

type VolumeUploadResult struct {
	Path string `json:"path"`
}

type VolumeBackup struct {
	ID          uint `json:"id"`
	WorkspaceID uint `json:"workspace_id"`
	VolumeID    uint `json:"volume_id"`
	ServerID    uint `json:"server_id"`
	// VolumeName is the Docker volume that was archived, not the handle.
	VolumeName string `json:"volume_name"`
	Status     string `json:"status"`  // pending | running | completed | failed
	Trigger    string `json:"trigger"` // manual | scheduled
	S3Bucket   string `json:"s3_bucket,omitempty"`
	S3Path     string `json:"s3_path,omitempty"`
	Filename   string `json:"filename,omitempty"`
	SizeBytes  int64  `json:"size_bytes"`
	// Logs is the tail the record carries; the full log is a separate download.
	Logs       string     `json:"logs,omitempty"`
	Error      string     `json:"error,omitempty"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`

	// Ref names a recovery point (Enterprise); empty on a plain archive.
	Ref          string     `json:"ref,omitempty"`
	Consistency  string     `json:"consistency,omitempty"` // hot
	Encrypted    bool       `json:"encrypted"`
	Pinned       bool       `json:"pinned"`
	VerifiedAt   *time.Time `json:"verified_at,omitempty"`
	VerifyStatus string     `json:"verify_status,omitempty"` // ok | failed; empty = never
	VerifyError  string     `json:"verify_error,omitempty"`
}

// Backup statuses: "running" is in flight here but terminal for a deployment.
const (
	BackupPending   = "pending"
	BackupRunning   = "running"
	BackupCompleted = "completed"
	BackupFailed    = "failed"
)

func IsBackupTerminal(status string) bool {
	return status == BackupCompleted || status == BackupFailed
}

func IsBackupFailure(status string) bool { return status == BackupFailed }

type VolumeBackupStatus struct {
	S3Configured bool `json:"s3_configured"`
	Entitled     bool `json:"entitled"`
	Mutable      bool `json:"mutable"`
	Sealing      bool `json:"sealing"`
}

type AttachVolumeRequest struct {
	VolumeID uint   `json:"volume_id"`
	Path     string `json:"path"`
}

// Secret values are write-only over the API; Name is the handle env refs use (${{ secrets.NAME }}).
type Secret struct {
	ID          uint   `json:"id"`
	UID         string `json:"uid"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
	// Version bumps on each value rotation.
	Version int `json:"version"`
	// Managed secrets belong to a platform resource; rotate them via the owner, not by hand.
	Managed bool `json:"managed"`
	// SealedKeyVersion is set when the value came from a sealed value in a manifest.
	SealedKeyVersion int       `json:"sealed_key_version,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type SealingKey struct {
	Version   int    `json:"version"`
	PublicKey string `json:"public_key"`
}

type CreateSecretRequest struct {
	Name        string `json:"name"`
	Value       string `json:"value"`
	Description string `json:"description,omitempty"`
}

// UpdateSecretRequest: a blank Value keeps the stored value; a new one rotates the secret.
type UpdateSecretRequest struct {
	Value       string `json:"value"`
	Description string `json:"description"`
}

type SecretReveal struct {
	Value string `json:"value"`
}

// Config content never comes back on list or get, only keys, sizes and the digest.
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

type ConfigReveal struct {
	Data map[string]string `json:"data"`
}

type SecretUsage struct {
	ID   uint   `json:"id"`
	Name string `json:"name"`
}

type ApplyRequest struct {
	Manifests string `json:"manifests"`
	Prune     bool   `json:"prune"`
	DryRun    bool   `json:"dry_run"`
	// Delete removes exactly the resources the bundle names instead of converging.
	Delete bool `json:"delete,omitempty"`
	// Force changes resources a GitOps project owns; its next sync reverts them.
	Force bool `json:"force,omitempty"`
}

type Change struct {
	Action string `json:"action"` // create | update | delete | noop
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Reason string `json:"reason,omitempty"`
}

type Plan struct {
	Changes []Change `json:"changes"`
}

type ApplyFailure struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Action string `json:"action"`
	Error  string `json:"error"`
}

type ApplyResult struct {
	Plan     *Plan          `json:"plan"`
	Applied  int            `json:"applied"`
	DryRun   bool           `json:"dry_run"`
	Failures []ApplyFailure `json:"failures,omitempty"`
}

type OverviewApp struct {
	ID     uint   `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Health string `json:"health"` // healthy | unhealthy | unknown
}

type WorkspaceOverview struct {
	Apps         []OverviewApp `json:"apps"`
	TotalApps    int           `json:"total_apps"`
	Running      int           `json:"running"`
	Failed       int           `json:"failed"`
	Databases    int           `json:"databases"`
	Stacks       int           `json:"stacks"`
	RecentEvents []Event       `json:"recent_events"`
}

type Alert struct {
	ID          uint      `json:"id"`
	Category    string    `json:"category"`
	Severity    string    `json:"severity"`
	State       string    `json:"state"` // firing | acknowledged | resolved
	Title       string    `json:"title"`
	Body        string    `json:"body"`
	Count       int64     `json:"count"`
	SubjectType string    `json:"subject_type"`
	SubjectRef  string    `json:"subject_ref"`
	FirstSeen   time.Time `json:"first_seen"`
	LastSeen    time.Time `json:"last_seen"`
}

// Event.AppName is only filled on the workspace-wide feed.
type Event struct {
	ID            uint              `json:"id"`
	SubjectType   string            `json:"subject_type,omitempty"`
	ApplicationID uint              `json:"application_id,omitempty"`
	DatabaseID    uint              `json:"database_id,omitempty"`
	Type          string            `json:"type"`
	Severity      string            `json:"severity"`
	Message       string            `json:"message"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	AppName       string            `json:"app_name,omitempty"`
	DatabaseName  string            `json:"database_name,omitempty"`
	CreatedAt     time.Time         `json:"created_at"`
}

type TrafficSummary struct {
	Totals struct {
		Requests     int64   `json:"requests"`
		ErrorRate    float64 `json:"error_rate"` // 0..1
		AvgLatencyMS float64 `json:"avg_latency_ms"`
		P95LatencyMS float64 `json:"p95_latency_ms"`
		P99LatencyMS float64 `json:"p99_latency_ms"`
	} `json:"totals"`
	Status struct {
		S2xx int64 `json:"s2xx"`
		S3xx int64 `json:"s3xx"`
		S4xx int64 `json:"s4xx"`
		S5xx int64 `json:"s5xx"`
	} `json:"status"`
}

type Backup struct {
	ID          uint       `json:"id"`
	Number      int        `json:"number"`
	Status      string     `json:"status"`
	Trigger     string     `json:"trigger"`
	Destination string     `json:"destination"`
	SizeBytes   int64      `json:"size_bytes"`
	Comment     string     `json:"comment,omitempty"`
	Error       string     `json:"error,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
}

type VerifyResult struct {
	Ref        string   `json:"ref"`
	OK         bool     `json:"ok"`
	Checked    int      `json:"checked"`
	Missing    []string `json:"missing,omitempty"`
	Resized    []string `json:"resized,omitempty"`
	EnvelopeOK bool     `json:"envelope_ok"`
	Error      string   `json:"error,omitempty"`
}
