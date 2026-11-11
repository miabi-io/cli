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

package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/miabi-io/cli/internal/host"
	"github.com/miabi-io/cli/internal/release"
	"github.com/miabi-io/cli/internal/ui"
	"github.com/miabi-io/miabi/pkg/stack"
	"github.com/miabi-io/miabi/pkg/stack/stackcmd"
	"github.com/spf13/cobra"
)

// stackCtx cancels on Ctrl-C so a half-finished converge still runs its cleanup.
func stackCtx() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

func openHost(file string) (*host.Session, error) {
	path := host.ManifestPath(file)
	if err := host.RequireWritable(path); err != nil {
		return nil, err
	}
	return host.Open(path, func(f string, a ...any) { fmt.Printf("  "+f+"\n", a...) })
}

// An unwritable path offers the per-user install instead of refusing: neither manifest nor socket needs root.
func setupManifestPath(o *setupOpts) (string, error) {
	path := host.ManifestPath(o.file)
	err := host.RequireWritable(path)
	if err == nil {
		return path, nil
	}

	if _, serr := os.Stat(path); serr == nil {
		return "", fmt.Errorf("%s belongs to the user that installed it\n\n"+
			"  Converge it:  sudo miabi setup …", path)
	}
	user := host.UserManifestPath()
	explicit := strings.TrimSpace(o.file) != "" || strings.TrimSpace(os.Getenv(stack.ConfigPathEnv)) != ""
	if explicit || o.yes || user == "" || user == path {
		return "", err
	}
	if other := host.OtherInstall(user); other != "" {
		return "", fmt.Errorf("this host already has an install at %s\n\n"+
			"  A second one would converge the same containers against the same Docker daemon.\n"+
			"  Converge that one:  sudo miabi setup …", other)
	}
	ui.Warn("%s is not writable — you are not root.", filepath.Dir(path))
	if !ui.Confirm(fmt.Sprintf("Install under your own account instead, in %s?", filepath.Dir(user))) {
		return "", err
	}
	if werr := host.RequireWritable(user); werr != nil {
		return "", werr
	}

	ui.Info("Installing into %s. Stack commands find it there on their own; no sudo, and nothing to export.", filepath.Dir(user))
	return user, nil
}

const platformRepo = "miabi/miabi"

// DefaultPlatformImage is the floating fallback; :latest loses automatic rollback, so using it is reported.
const DefaultPlatformImage = platformRepo + ":latest"

// The platform version is looked up, not baked in, because the CLI releases on its own cadence.
func defaultControlPlaneImage() string {
	ctx, cancel := context.WithTimeout(context.Background(), releaseLookupTimeout)
	defer cancel()
	r, err := release.LatestRelease(ctx, "miabi-cli/"+version)
	if err != nil {
		ui.Warn("could not determine the latest Miabi version (%v) — falling back to %s.\n"+
			"    Pin one with --version 1.8.0 to keep rollback and drift detection working.", err, DefaultPlatformImage)
		return DefaultPlatformImage
	}
	if r.Reason == release.ReasonSecurity {
		ui.Info("Miabi %s is a security release: %s", r.Version, r.ReleaseURL)
	}
	return platformRepo + ":" + r.Version
}

// releaseLookupTimeout is short because it blocks an install the operator is waiting on.
const releaseLookupTimeout = 20 * time.Second

// imageForVersion exists because stackcmd.SetupOptions has no version field, only an image.
func imageForVersion(v string) string { return platformRepo + ":" + release.Normalize(v) }

type setupOpts struct {
	domain, adminEmail, acmeEmail, controlURL    string
	version                                      string
	image, gatewayImage, runnerImage, gomaConfig string
	registry, noHostProc, ipv6, yes              bool
	registryHost, subnet, internalSubnet, file   string
}

// newSetupCmd is a constructor so the verb can register under several parents; a cobra command has one.
func newSetupCmd(use string) *cobra.Command {
	o := &setupOpts{}
	c := &cobra.Command{
		Use:   use,
		Short: "Install the Miabi stack on this host, or converge an existing install",
		Long: "setup is idempotent. Run against an existing manifest it converges the stack to match,\n" +
			"keeping the stored secrets — a regenerated database password would lock the control plane\n" +
			"out of its own data. Re-running after hand-editing the manifest is how that edit is applied.\n\n" +
			"The first admin's password is generated unless " + stack.AdminPasswordEnv + " is set (at least\n" +
			"12 characters). It is read from the environment, not a flag, so it stays out of the process list.",
		Example: "  sudo miabi setup --domain miabi.example.com\n" +
			"  sudo miabi setup --domain miabi.example.com --registry --yes",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE:          func(cmd *cobra.Command, _ []string) error { return runSetup(cmd, o) },
	}
	f := c.Flags()
	f.StringVarP(&o.domain, "domain", "d", "", "public hostname for the panel, e.g. miabi.example.com")
	f.StringVar(&o.adminEmail, "admin-email", "", "first admin's email (default admin@<domain>)")
	f.StringVar(&o.acmeEmail, "acme-email", "", "contact for Let's Encrypt (default admin@<domain>)")
	f.StringVar(&o.controlURL, "control-url", "", "URL remote nodes and agents dial back on (default: the panel's own URL)")
	f.StringVar(&o.version, "version", "", "platform version to install (1.8.0 or v1.8.0); default: the latest release")
	f.StringVar(&o.image, "image", "", "exact control-plane image, overriding --version (e.g. registry.example.com/miabi:1.8.0)")
	f.StringVar(&o.gatewayImage, "gateway-image", "", "Goma Gateway image")
	f.StringVar(&o.runnerImage, "runner-image", "", "CI runner image (shown in the runner enrollment command)")
	f.StringVar(&o.gomaConfig, "goma-config", "", "gateway config file, relative to the manifest's directory (default goma.yml)")
	f.BoolVar(&o.registry, "registry", false, "enable the built-in container registry")
	f.StringVar(&o.registryHost, "registry-host", "", "registry hostname (default registry.<domain>); implies --registry")
	f.BoolVar(&o.noHostProc, "no-host-proc", false, "do not bind the host's /proc into the control plane (host metrics fall back to the container's /proc)")
	f.BoolVar(&o.ipv6, "ipv6", false, "give the app network and workspace networks IPv6 (dual-stack)")
	f.StringVar(&o.subnet, "subnet", "", "CIDR for the shared `"+stack.DefaultNetwork+"` network — apps and the gateway (default "+stack.DefaultSubnet+")")
	f.StringVar(&o.internalSubnet, "internal-subnet", "", "CIDR for the private `"+stack.DefaultInternalNetwork+"` network — control plane, database, cache (default "+stack.DefaultInternalSubnet+")")
	f.StringVarP(&o.file, "file", "f", "", "manifest path (default "+stack.DefaultConfigPath+")")
	f.BoolVarP(&o.yes, "yes", "y", false, "skip the confirmation prompt")
	return c
}

func runSetup(_ *cobra.Command, o *setupOpts) error {
	file, err := setupManifestPath(o)
	if err != nil {
		return err
	}
	sess, err := openHost(file)
	if err != nil {
		return err
	}
	defer func() { _ = sess.Close() }()

	ctx, cancel := stackCtx()
	defer cancel()

	// setup takes only an image, so --version is turned into one here; an explicit --image wins.
	image := o.image
	if image == "" && strings.TrimSpace(o.version) != "" {
		image = imageForVersion(o.version)
	}

	res, err := stackcmd.Setup(ctx, sess.Svc, sess.Manifest, stackcmd.SetupOptions{
		Domain: o.domain, AdminEmail: o.adminEmail, ACMEEmail: o.acmeEmail, ControlURL: o.controlURL,
		Image: image, GatewayImage: o.gatewayImage, RunnerImage: o.runnerImage, GomaConfig: o.gomaConfig,
		RegistryHost: o.registryHost, Subnet: o.subnet, InternalSubnet: o.internalSubnet,
		Registry: o.registry, NoHostProc: o.noHostProc, IPv6: o.ipv6, Yes: o.yes,
		AdminPassword: os.Getenv(stack.AdminPasswordEnv),
		DefaultImage:  defaultControlPlaneImage,
	}, cliUI{})
	if err != nil {
		return err
	}
	_ = res
	fmt.Printf("\n  Manage it:\n\n    sudo miabi system status\n\n" +
		"    …and likewise `miabi upgrade` (rolls the stack forward, rolling back if it fails)\n" +
		"    or `miabi system uninstall` (keeps your data; add --volumes to destroy it).\n")
	return nil
}

func newUpgradeCmd(use string) *cobra.Command {
	var image, versionFlag, file string
	var yes bool
	c := &cobra.Command{
		Use:   use + " [component]",
		Short: "Move the installed stack to a newer version (all components, or one by name)",
		Long: "With no argument the control plane moves to the latest published Miabi release and the\n" +
			"rest of the stack re-converges. The version is looked up rather than baked in: the CLI\n" +
			"releases on its own cadence, so an older CLI still upgrades to today's Miabi.\n" +
			"Naming a component rolls out just that one, to whatever the manifest pins\n" +
			"unless --version/--image says otherwise — bumping Postgres is a database restart you have\n" +
			"to ask for.\n\n" +
			"--version keeps the current registry and repository and swaps only the tag, so it stays\n" +
			"correct on a private registry and on components that are not miabi/miabi. --image replaces\n" +
			"the reference outright. A leading \"v\" is optional: v1.8.0 and 1.8.0 both work.\n\n" +
			"A failed rollout rolls back the image, not a schema the new version already migrated.\n" +
			"Take a dump first with `miabi system backup`.",
		Example: "  sudo miabi upgrade\n" +
			"  sudo miabi upgrade --version v1.8.0\n" +
			"  sudo miabi upgrade miabi-gateway --version 0.14.0\n" +
			"  sudo miabi upgrade --image registry.example.com/miabi:1.8.0 --yes",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(_ *cobra.Command, args []string) error {
			return runUpgrade(args, image, versionFlag, file, yes)
		},
	}
	f := c.Flags()
	f.StringVar(&versionFlag, "version", "", "roll out this version, keeping the current registry and repository (1.8.0 or v1.8.0)")
	f.StringVar(&image, "image", "", "roll out this exact image, overriding --version (e.g. registry.example.com/miabi:1.8.0)")
	f.StringVarP(&file, "file", "f", "", "manifest path")
	f.BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return c
}

func runUpgrade(args []string, image, versionFlag, file string, yes bool) error {
	sess, err := openHost(file)
	if err != nil {
		return err
	}
	defer func() { _ = sess.Close() }()

	ctx, cancel := stackCtx()
	defer cancel()

	component := ""
	if len(args) > 0 {
		component = args[0]
	}
	return stackcmd.Upgrade(ctx, sess.Svc, sess.Manifest, stackcmd.UpgradeOptions{
		Component: component, Image: image, Version: versionFlag, Yes: yes,
		DefaultImage: defaultControlPlaneImage,
	}, cliUI{})
}

func newApplyCmd() *cobra.Command {
	var file string
	var yes bool
	c := &cobra.Command{
		Use:   "apply",
		Short: "Bring the running stack in line with the manifest",
		Long: "Recreates only the components whose spec changed since they were created — the step\n" +
			"after one or more `miabi system env set` edits. Nothing changed means nothing restarts.",
		Example:       "  sudo miabi system env set MIABI_SMTP_HOST=smtp.example.com MIABI_SMTP_PORT=587\n  sudo miabi system apply",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			sess, err := openHost(file)
			if err != nil {
				return err
			}
			defer func() { _ = sess.Close() }()
			ctx, cancel := stackCtx()
			defer cancel()
			return stackcmd.Apply(ctx, sess.Svc, sess.Manifest, yes, cliUI{})
		},
	}
	c.Flags().StringVarP(&file, "file", "f", "", "manifest path")
	c.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return c
}

// newRestartCmd does not recreate containers, so they re-read what is on disk (e.g. an edited goma.yml).
func newRestartCmd() *cobra.Command {
	var file string
	var yes bool
	c := &cobra.Command{
		Use:           "restart [component]",
		Short:         "Restart the stack, or one component (miabi, miabi-gateway, …)",
		Long:          "Restarts in place without recreating containers, so they re-read their on-disk config.\nA changed image or spec needs `miabi upgrade` instead.",
		Example:       "  sudo miabi system restart\n  sudo miabi system restart miabi-gateway",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(_ *cobra.Command, args []string) error {
			sess, err := openHost(file)
			if err != nil {
				return err
			}
			defer func() { _ = sess.Close() }()
			component := ""
			if len(args) > 0 {
				component = args[0]
			}
			ctx, cancel := stackCtx()
			defer cancel()
			return stackcmd.Restart(ctx, sess.Svc, sess.Manifest, component, yes, cliUI{})
		},
	}
	c.Flags().StringVarP(&file, "file", "f", "", "manifest path")
	c.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return c
}

func newStatusCmd() *cobra.Command {
	var file string
	c := &cobra.Command{
		Use:           "status",
		Short:         "Show the installed stack against its manifest",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			sess, err := openHost(file)
			if err != nil {
				return err
			}
			defer func() { _ = sess.Close() }()

			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			r, err := stackcmd.Status(ctx, sess.Svc, sess.Manifest)
			if err != nil {
				return err
			}

			switch {
			case r.LegacyConfig != nil:
				ui.Warn("%v", r.LegacyConfig)
				fmt.Println()
			case r.Unmanaged:
				fmt.Printf("No stack manifest at %s — this stack was not installed by `miabi setup`.\n", r.Path)
				fmt.Printf("Manage it with the tool that created it (for Compose: docker compose up -d).\n\n")
			default:
				fmt.Printf("%s  →  %s\n", r.Manifest.Domain, r.Path)
				fmt.Printf("%s\n\n", ui.Dim(fmt.Sprintf("cli %s · manifest schema v%d", version, r.Manifest.Version)))
			}

			t := ui.NewTable("COMPONENT", "STATE", "HEALTH", "OWNER", "IMAGE")
			for _, c := range r.Found {
				t.Row(c.Name, ui.Status(c.State), orDash(c.Health), orElse(c.ManagedBy, "unlabeled"), c.Image)
			}
			t.Print()

			if len(r.Drift) > 0 {
				fmt.Printf("\nDrift (run `miabi setup` to reconcile):\n%s\n", strings.Join(r.Drift, "\n"))
			}
			return nil
		},
	}
	c.Flags().StringVarP(&file, "file", "f", "", "manifest path")
	return c
}

func orDash(s string) string { return orElse(s, "—") }

func orElse(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

func newUninstallCmd() *cobra.Command {
	var file string
	var volumes, yes bool
	c := &cobra.Command{
		Use:           "uninstall",
		Short:         "Remove the Miabi stack's containers (volumes are KEPT unless --volumes)",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			sess, err := openHost(file)
			if err != nil {
				return err
			}
			defer func() { _ = sess.Close() }()
			ctx, cancel := stackCtx()
			defer cancel()
			return stackcmd.Uninstall(ctx, sess.Svc, sess.Manifest, volumes, yes, cliUI{}, func(p string) error {
				if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
					return err
				}
				return nil
			})
		},
	}
	c.Flags().StringVarP(&file, "file", "f", "", "manifest path")
	c.Flags().BoolVar(&volumes, "volumes", false, "ALSO delete the data volumes — this destroys the database")
	c.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return c
}

func newMigrateConfigCmd() *cobra.Command {
	var yes bool
	c := &cobra.Command{
		Use:   "migrate-config",
		Short: "Rename the legacy " + stack.LegacyConfigPath + " to " + stack.DefaultConfigPath,
		Long: "The legacy manifest is still read, so this is optional. It is a rename, not a copy: the\n" +
			"file holds the database password and the encryption key, and leaving two copies of those\n" +
			"around is worse than either one alone.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := host.RequireWritable(stack.DefaultConfigPath); err != nil {
				return err
			}
			from, to := stack.LegacyConfigPath, stack.DefaultConfigPath
			if _, err := os.Stat(from); err != nil {
				if os.IsNotExist(err) {
					ui.Info("Nothing to migrate — %s does not exist.", from)
					return nil
				}
				return err
			}
			if _, err := os.Stat(to); err == nil {
				return fmt.Errorf("%s already exists — remove or rename it first, then re-run", to)
			}
			if !yes && !ui.Confirm(fmt.Sprintf("Rename %s to %s?", from, to)) {
				return errors.New("cancelled")
			}
			if err := os.Rename(from, to); err != nil {
				return err
			}
			ui.Success("Renamed %s → %s", from, to)
			return nil
		},
	}
	c.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return c
}
