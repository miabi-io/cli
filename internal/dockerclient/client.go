// SPDX-FileCopyrightText: 2026 Jonas Kaninda
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package dockerclient implements the stack engine's Docker seam against a real engine, using the
// maintained moby/moby client. It is a separate module so the Docker SDK never enters the control
// plane's dependency graph — the server satisfies the same interface with the client it already has.
package dockerclient

import (
	"bytes"
	"context"
	"fmt"
	"net/netip"
	"strconv"
	"strings"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/miabi-io/miabi/pkg/stack/docker"
	"github.com/moby/moby/api/pkg/authconfig"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/api/types/registry"
	"github.com/moby/moby/client"
)

// Engine is a [docker.Client] backed by a real Docker engine. Beyond the seam it also exposes
// Ping and Close, which a caller owning the connection needs but the engine itself never calls.
type Engine struct {
	cli *client.Client
}

var _ docker.Client = (*Engine)(nil)

// New connects to the Docker engine using the standard environment (DOCKER_HOST etc.) with API
// version negotiation.
func New() (*Engine, error) {
	cli, err := client.New(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}
	return &Engine{cli: cli}, nil
}

// Ping checks the daemon is reachable and responding.
func (e *Engine) Ping(ctx context.Context) error {
	_, err := e.cli.Ping(ctx, client.PingOptions{})
	return err
}

// Close releases the underlying HTTP transport.
func (e *Engine) Close() error { return e.cli.Close() }

func (e *Engine) ListContainers(ctx context.Context, all bool) ([]docker.Container, error) {
	list, err := e.cli.ContainerList(ctx, client.ContainerListOptions{All: all})
	if err != nil {
		return nil, err
	}
	out := make([]docker.Container, 0, len(list.Items))
	for _, c := range list.Items {
		ports := make([]docker.Port, 0, len(c.Ports))
		for _, p := range c.Ports {
			ports = append(ports, docker.Port{PrivatePort: p.PrivatePort, PublicPort: p.PublicPort, Protocol: p.Type})
		}
		out = append(out, docker.Container{
			ID: c.ID, Names: c.Names, Image: c.Image, State: string(c.State),
			Status: c.Status, Created: c.Created, Ports: ports, Labels: c.Labels,
		})
	}
	return out, nil
}

func (e *Engine) InspectContainer(ctx context.Context, id string) (docker.Container, error) {
	res, err := e.cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		return docker.Container{}, wrapNotFound(err)
	}
	c := res.Container

	out := docker.Container{
		ID:           c.ID,
		Names:        []string{strings.TrimPrefix(c.Name, "/")},
		RestartCount: c.RestartCount,
	}
	if c.State != nil {
		out.State = string(c.State.Status)
		out.Status = string(c.State.Status)
		out.Restarting = c.State.Restarting
		out.ExitCode = c.State.ExitCode
		out.StartedAt = c.State.StartedAt
		if c.State.Health != nil {
			out.Health = string(c.State.Health.Status)
		}
	}
	if c.Config != nil {
		out.Image = c.Config.Image
		out.Labels = c.Config.Labels
	}
	if c.NetworkSettings != nil {
		for name, ep := range c.NetworkSettings.Networks {
			if ep == nil || !ep.IPAddress.IsValid() {
				continue
			}
			out.Networks = append(out.Networks, docker.ContainerNetwork{
				Name: name, IPAddress: ep.IPAddress.String(), Gateway: gatewayString(ep.Gateway), Aliases: ep.Aliases,
			})
		}
	}
	return out, nil
}

func (e *Engine) InspectContainerConfig(ctx context.Context, id string) (docker.ContainerConfig, error) {
	res, err := e.cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		return docker.ContainerConfig{}, wrapNotFound(err)
	}
	c := res.Container

	cfg := docker.ContainerConfig{ID: c.ID, Name: strings.TrimPrefix(c.Name, "/")}
	if c.State != nil {
		cfg.State = string(c.State.Status)
	}
	if c.Config != nil {
		cfg.Image = c.Config.Image
		cfg.Command = c.Config.Cmd
		cfg.Entrypoint = c.Config.Entrypoint
		cfg.Env = c.Config.Env
		cfg.Labels = c.Config.Labels
	}
	if c.HostConfig != nil {
		cfg.MemoryBytes = c.HostConfig.Memory
		cfg.NanoCPUs = c.HostConfig.NanoCPUs
		cfg.RestartPolicy = restartPolicyString(c.HostConfig.RestartPolicy)
		for p, binds := range c.HostConfig.PortBindings {
			pm := docker.PortMapping{ContainerPort: int(p.Num()), Protocol: string(p.Proto())}
			for _, b := range binds {
				if hp, perr := strconv.Atoi(b.HostPort); perr == nil && hp > 0 {
					pm.HostPort = hp
					break
				}
			}
			cfg.Ports = append(cfg.Ports, pm)
		}
	}
	for _, m := range c.Mounts {
		cfg.Mounts = append(cfg.Mounts, docker.ContainerMount{
			Type: string(m.Type), Name: m.Name, Source: m.Source,
			Destination: m.Destination, ReadOnly: !m.RW,
		})
	}
	if c.NetworkSettings != nil {
		for name := range c.NetworkSettings.Networks {
			cfg.Networks = append(cfg.Networks, name)
		}
	}
	return cfg, nil
}

func (e *Engine) RunContainer(ctx context.Context, spec docker.RunSpec) (string, error) {
	exposed := network.PortSet{}
	bindings := network.PortMap{}
	for cp, hp := range spec.Ports {
		p, err := network.ParsePort(cp)
		if err != nil {
			return "", fmt.Errorf("port %q: %w", cp, err)
		}
		exposed[p] = struct{}{}
		hostIP := netip.IPv4Unspecified() // 0.0.0.0 — all interfaces
		if ip := spec.PortBindIPs[cp]; ip != "" {
			// publish on a specific interface (e.g. the node's private IP)
			if hostIP, err = netip.ParseAddr(ip); err != nil {
				return "", fmt.Errorf("bind IP %q for port %s: %w", ip, cp, err)
			}
		}
		bindings[p] = []network.PortBinding{{HostIP: hostIP, HostPort: hp}}
	}

	binds, volMounts := containerVolumeMounts(spec)

	cfg := &container.Config{
		Image:        spec.Image,
		Hostname:     spec.Hostname,
		User:         spec.User, // "" = image default; "uid:0" under the restricted profile
		Env:          spec.Env,
		Entrypoint:   spec.Entrypoint,
		Cmd:          spec.Cmd,
		WorkingDir:   spec.WorkingDir,
		Labels:       managedLabels(spec.Labels),
		ExposedPorts: exposed,
	}
	if hc := spec.Healthcheck; hc != nil && len(hc.Test) > 0 {
		cfg.Healthcheck = &container.HealthConfig{
			Test:        hc.Test,
			Interval:    hc.Interval,
			Timeout:     hc.Timeout,
			Retries:     hc.Retries,
			StartPeriod: hc.StartPeriod,
		}
	}
	hostCfg := &container.HostConfig{
		PortBindings: bindings,
		Binds:        binds,
		Mounts:       volMounts,
		Resources: container.Resources{
			Memory:         spec.MemoryBytes,
			NanoCPUs:       spec.NanoCPUs,
			DeviceRequests: toDeviceRequests(spec.GPUs),
		},
		RestartPolicy: restartPolicy(spec.RestartPolicy),
		CapDrop:       spec.CapDrop,
		GroupAdd:      spec.GroupAdd,
	}
	if spec.NoNewPrivileges {
		hostCfg.SecurityOpt = append(hostCfg.SecurityOpt, "no-new-privileges")
	}

	created, err := e.cli.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name:             spec.Name,
		Config:           cfg,
		HostConfig:       hostCfg,
		NetworkingConfig: networkingConfig(spec),
	})
	if err != nil {
		return "", err
	}
	if _, err := e.cli.ContainerStart(ctx, created.ID, client.ContainerStartOptions{}); err != nil {
		return created.ID, err
	}
	return created.ID, nil
}

func (e *Engine) RestartContainer(ctx context.Context, id string, timeoutSeconds int) error {
	t := timeoutSeconds
	_, err := e.cli.ContainerRestart(ctx, id, client.ContainerRestartOptions{Timeout: &t})
	return wrapNotFound(err)
}

func (e *Engine) RemoveContainer(ctx context.Context, id string, force bool) error {
	_, err := e.cli.ContainerRemove(ctx, id, client.ContainerRemoveOptions{Force: force})
	return wrapNotFound(err)
}

// createOneShot creates (but does not start) a one-shot helper container. Resource limits are
// honored so callers can cap a build/probe container; one-shots never restart.
func (e *Engine) createOneShot(ctx context.Context, spec docker.RunSpec) (string, error) {
	binds := make([]string, 0, len(spec.Mounts)+len(spec.Binds))
	for vol, path := range spec.Mounts {
		binds = append(binds, vol+":"+path)
	}
	binds = append(binds, hostBinds(spec.Binds)...)

	created, err := e.cli.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name: spec.Name,
		Config: &container.Config{
			Image: spec.Image, Env: spec.Env, Entrypoint: spec.Entrypoint,
			Cmd: spec.Cmd, WorkingDir: spec.WorkingDir, Labels: managedLabels(spec.Labels),
		},
		HostConfig: &container.HostConfig{
			Binds: binds,
			Resources: container.Resources{
				Memory:         spec.MemoryBytes,
				NanoCPUs:       spec.NanoCPUs,
				DeviceRequests: toDeviceRequests(spec.GPUs), // used by the GPU inventory probe
			},
		},
		NetworkingConfig: networkingConfig(spec),
	})
	if err != nil {
		return "", err
	}
	return created.ID, nil
}

func (e *Engine) RunOneShot(ctx context.Context, spec docker.RunSpec) (int, string, error) {
	id, err := e.createOneShot(ctx, spec)
	if err != nil {
		return -1, "", err
	}
	defer func() {
		_, _ = e.cli.ContainerRemove(context.WithoutCancel(ctx), id, client.ContainerRemoveOptions{Force: true})
	}()

	if _, err := e.cli.ContainerStart(ctx, id, client.ContainerStartOptions{}); err != nil {
		return -1, "", err
	}

	wait := e.cli.ContainerWait(ctx, id, client.ContainerWaitOptions{Condition: container.WaitConditionNotRunning})
	var exitCode int
	select {
	case err := <-wait.Error:
		if err != nil {
			return -1, e.collectLogs(ctx, id), err
		}
	case st := <-wait.Result:
		exitCode = int(st.StatusCode)
	case <-ctx.Done():
		return -1, e.collectLogs(ctx, id), ctx.Err()
	}
	return exitCode, e.collectLogs(ctx, id), nil
}

func (e *Engine) PullImage(ctx context.Context, ref string, auth *docker.RegistryAuth) error {
	opts := client.ImagePullOptions{}
	if auth != nil && (auth.Username != "" || auth.Password != "") {
		encoded, err := authconfig.Encode(registry.AuthConfig{
			Username:      auth.Username,
			Password:      auth.Password,
			ServerAddress: auth.Server,
		})
		if err != nil {
			return fmt.Errorf("encode registry auth: %w", err)
		}
		opts.RegistryAuth = encoded
	}
	rc, err := e.cli.ImagePull(ctx, ref, opts)
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }()
	// The pull only completes once the response body is drained; Wait surfaces any mid-stream error.
	return rc.Wait(ctx)
}

func (e *Engine) ImageExists(ctx context.Context, ref string) (bool, error) {
	if _, err := e.cli.ImageInspect(ctx, ref); err != nil {
		if cerrdefs.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (e *Engine) EnsureNetworkSpec(ctx context.Context, spec docker.NetworkSpec) (string, error) {
	if existing, err := e.cli.NetworkInspect(ctx, spec.Name, client.NetworkInspectOptions{}); err == nil {
		return existing.Network.ID, nil
	}
	opts, err := createNetworkOptions(spec)
	if err != nil {
		return "", err
	}
	created, err := e.cli.NetworkCreate(ctx, spec.Name, opts)
	if err != nil {
		return "", err
	}
	return created.ID, nil
}

func (e *Engine) CreateVolume(ctx context.Context, name string, labels map[string]string, sizeBytes int64) (docker.Volume, error) {
	labels = managedLabels(labels)
	// Record the declared capacity as a label. A *hard* size cap needs a sized backing volume, which
	// depends on the node's storage backend and is layered on separately; the label makes the size
	// visible to `docker volume inspect` and to quota tracking for soft enforcement.
	if sizeBytes > 0 {
		labels[docker.LabelSizeBytes] = strconv.FormatInt(sizeBytes, 10)
	}
	res, err := e.cli.VolumeCreate(ctx, client.VolumeCreateOptions{Name: name, Labels: labels})
	if err != nil {
		return docker.Volume{}, err
	}
	v := res.Volume
	return docker.Volume{Name: v.Name, Driver: v.Driver, Mountpoint: v.Mountpoint, CreatedAt: v.CreatedAt}, nil
}

func (e *Engine) InspectVolume(ctx context.Context, name string) (docker.Volume, error) {
	res, err := e.cli.VolumeInspect(ctx, name, client.VolumeInspectOptions{})
	if err != nil {
		return docker.Volume{}, wrapNotFound(err)
	}
	v := res.Volume
	return docker.Volume{Name: v.Name, Driver: v.Driver, Mountpoint: v.Mountpoint, CreatedAt: v.CreatedAt, Labels: v.Labels}, nil
}

func (e *Engine) RemoveVolume(ctx context.Context, name string, force bool) error {
	_, err := e.cli.VolumeRemove(ctx, name, client.VolumeRemoveOptions{Force: force})
	return wrapNotFound(err)
}

func (e *Engine) collectLogs(ctx context.Context, id string) string {
	rc, err := e.cli.ContainerLogs(context.WithoutCancel(ctx), id, client.ContainerLogsOptions{
		ShowStdout: true, ShowStderr: true, Tail: "all",
	})
	if err != nil {
		return ""
	}
	defer func() { _ = rc.Close() }()
	var buf bytes.Buffer
	_, _ = stdcopy.StdCopy(&buf, &buf, rc)
	return buf.String()
}

// managedLabels copies the caller's labels and stamps the Miabi managed marker, so a container or
// volume this client creates is always recognizable to housekeeping.
func managedLabels(labels map[string]string) map[string]string {
	out := make(map[string]string, len(labels)+2)
	for k, v := range labels {
		out[k] = v
	}
	out[docker.ManagedLabel] = "true"
	return out
}

// networkingConfig renders the spec's networks with their DNS aliases. AliasesByNetwork adds
// per-network aliases on top of the shared NetworkAliases.
func networkingConfig(spec docker.RunSpec) *network.NetworkingConfig {
	if len(spec.Networks) == 0 {
		return nil
	}
	endpoints := make(map[string]*network.EndpointSettings, len(spec.Networks))
	for _, n := range spec.Networks {
		aliases := spec.NetworkAliases
		if extra := spec.AliasesByNetwork[n]; len(extra) > 0 {
			aliases = append(append([]string{}, spec.NetworkAliases...), extra...)
		}
		endpoints[n] = &network.EndpointSettings{Aliases: aliases}
	}
	return &network.NetworkingConfig{EndpointsConfig: endpoints}
}

// containerVolumeMounts renders a RunSpec's named volumes and host binds for the HostConfig. Named
// volumes are bind strings unless NoCopyVolumes is set, in which case they use the Mount API with
// NoCopy so copy-up can't undo the restricted-profile chown. Host binds stay plain.
func containerVolumeMounts(spec docker.RunSpec) ([]string, []mount.Mount) {
	binds := make([]string, 0, len(spec.Mounts)+len(spec.Binds))
	var volMounts []mount.Mount
	for vol, path := range spec.Mounts {
		if spec.NoCopyVolumes {
			volMounts = append(volMounts, mount.Mount{
				Type: mount.TypeVolume, Source: vol, Target: path,
				VolumeOptions: &mount.VolumeOptions{NoCopy: true},
			})
			continue
		}
		binds = append(binds, vol+":"+path)
	}
	binds = append(binds, hostBinds(spec.Binds)...)
	return binds, volMounts
}

func hostBinds(mounts []docker.BindMount) []string {
	out := make([]string, 0, len(mounts))
	for _, m := range mounts {
		b := m.Source + ":" + m.Target
		if m.ReadOnly {
			b += ":ro"
		}
		out = append(out, b)
	}
	return out
}

func createNetworkOptions(spec docker.NetworkSpec) (client.NetworkCreateOptions, error) {
	driver := spec.Driver
	if driver == "" {
		driver = "bridge"
	}
	opts := client.NetworkCreateOptions{
		Driver:     driver,
		Internal:   spec.Internal,
		Attachable: spec.Attachable,
		Labels:     managedLabels(spec.Labels),
	}
	if spec.Encrypted {
		opts.Options = map[string]string{"encrypted": ""}
	}
	if spec.Subnet != "" {
		subnet, err := netip.ParsePrefix(spec.Subnet)
		if err != nil {
			return opts, fmt.Errorf("subnet %q: %w", spec.Subnet, err)
		}
		cfg := network.IPAMConfig{Subnet: subnet}
		if spec.Gateway != "" {
			if cfg.Gateway, err = netip.ParseAddr(spec.Gateway); err != nil {
				return opts, fmt.Errorf("gateway %q: %w", spec.Gateway, err)
			}
		}
		opts.IPAM = &network.IPAM{Config: []network.IPAMConfig{cfg}}
	}
	return opts, nil
}

// restartPolicy maps a RunSpec restart-policy string onto the engine policy. Empty or unrecognized
// falls back to "unless-stopped", the platform's historical default. "on-failure" may carry a ":N"
// max-retry suffix.
func restartPolicy(p string) container.RestartPolicy {
	switch {
	case p == string(container.RestartPolicyDisabled): // "no"
		return container.RestartPolicy{Name: container.RestartPolicyDisabled}
	case p == string(container.RestartPolicyAlways):
		return container.RestartPolicy{Name: container.RestartPolicyAlways}
	case p == string(container.RestartPolicyOnFailure) || strings.HasPrefix(p, string(container.RestartPolicyOnFailure)+":"):
		rp := container.RestartPolicy{Name: container.RestartPolicyOnFailure}
		if _, rest, ok := strings.Cut(p, ":"); ok {
			if n, err := strconv.Atoi(rest); err == nil {
				rp.MaximumRetryCount = n
			}
		}
		return rp
	default:
		return container.RestartPolicy{Name: container.RestartPolicyUnlessStopped}
	}
}

// restartPolicyString renders an engine restart policy back to the string form Miabi stores
// (e.g. "on-failure:3"). Empty and "no" both map to "no".
func restartPolicyString(p container.RestartPolicy) string {
	switch p.Name {
	case container.RestartPolicyAlways:
		return string(container.RestartPolicyAlways)
	case container.RestartPolicyUnlessStopped:
		return string(container.RestartPolicyUnlessStopped)
	case container.RestartPolicyOnFailure:
		if p.MaximumRetryCount > 0 {
			return string(container.RestartPolicyOnFailure) + ":" + strconv.Itoa(p.MaximumRetryCount)
		}
		return string(container.RestartPolicyOnFailure)
	default:
		return string(container.RestartPolicyDisabled) // "no"
	}
}

func toDeviceRequests(gpus []docker.GPURequest) []container.DeviceRequest {
	if len(gpus) == 0 {
		return nil
	}
	out := make([]container.DeviceRequest, 0, len(gpus))
	for _, g := range gpus {
		dr := container.DeviceRequest{Driver: "nvidia", Capabilities: g.Capabilities}
		if len(dr.Capabilities) == 0 {
			dr.Capabilities = [][]string{{"gpu"}}
		}
		if len(g.DeviceIDs) > 0 {
			dr.DeviceIDs = g.DeviceIDs
		} else {
			dr.Count = g.Count
		}
		out = append(out, dr)
	}
	return out
}

// wrapNotFound normalizes the engine's not-found error to the seam's sentinel, so the stack engine
// need not know anything about the Docker SDK's error types.
func wrapNotFound(err error) error {
	if err != nil && cerrdefs.IsNotFound(err) {
		return docker.ErrNotFound
	}
	return err
}

// gatewayString renders an optional gateway address; an unset one stays empty rather than
// rendering as "invalid IP".
func gatewayString(a netip.Addr) string {
	if !a.IsValid() {
		return ""
	}
	return a.String()
}
