package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/miabi-io/miabi-cli/internal/host"
	"github.com/miabi-io/miabi-cli/internal/release"
	"github.com/miabi-io/miabi-cli/internal/ui"
	"github.com/miabi-io/miabi/pkg/stack"
	"github.com/miabi-io/miabi/pkg/stack/stackcmd"
	"github.com/spf13/cobra"
)

func init() {
	stackCmd.AddCommand(
		newUpgradeCmd("upgrade"),
		newRestartCmd(),
		newStatusCmd(),
		newUninstallCmd(),
		newMigrateConfigCmd(),
	)
	rootCmd.AddCommand(stackCmd, newSetupCmd("setup"), newUpgradeCmd("upgrade"))
}

var stackCmd = &cobra.Command{
	Use:   "stack",
	Short: "Manage the Miabi stack installed on this host",
	Long: "These commands drive the Docker stack on the machine they run on — not a panel over\n" +
		"HTTP. They read and write the manifest at " + stack.DefaultConfigPath + ", need root,\n" +
		"and require a reachable Docker daemon.",
	Example: "  sudo miabi setup --domain miabi.example.com\n" +
		"  sudo miabi upgrade\n" +
		"  sudo miabi stack status\n" +
		"  sudo miabi stack restart miabi-gateway",
	// A bare `miabi stack` shows help; an unknown subcommand must FAIL. Cobra's default for a
	// group is to print help and exit 0 either way — which would let a script still calling the
	// retired `stack install` report success while doing nothing at all.
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
}

// stackCtx cancels on Ctrl-C, so a half-finished converge can still run its cleanup rather than
// leaving a container mid-pull.
func stackCtx() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// openHost resolves the manifest path, checks privileges, and connects to Docker. Every stack
// command starts here.
func openHost(file string) (*host.Session, error) {
	if err := host.RequireRoot(); err != nil {
		return nil, err
	}
	return host.Open(host.ManifestPath(file), func(f string, a ...any) { fmt.Printf("  "+f+"\n", a...) })
}

// platformRepo is where the control plane is pulled from when the operator names no registry.
const platformRepo = "miabi/miabi"

// DefaultPlatformImage is the last resort when the version cannot be looked up. It is a floating
// tag on purpose — there is no fixed version to name at that point — which is why falling back to
// it is reported rather than silent: :latest costs the automatic rollback (a rollout skips the
// rollback when the previous reference equals the new one) and makes drift undetectable.
const DefaultPlatformImage = platformRepo + ":latest"

// defaultControlPlaneImage is the image `setup` and `upgrade` install when neither --version nor
// --image was given.
//
// The platform version is NOT baked into this binary. The CLI releases on its own cadence — one cut
// months ago still has to install today's Miabi — so a build-time stamp would pin every install to
// whatever was current when the CLI was built, and would go stale the moment the platform shipped
// without the CLI. It is looked up instead, falling back to :latest so a host with no route to
// GitHub still installs rather than refusing outright.
func defaultControlPlaneImage() string {
	ctx, cancel := context.WithTimeout(context.Background(), releaseLookupTimeout)
	defer cancel()
	v, err := release.Latest(ctx, "miabi-cli/"+version)
	if err != nil {
		ui.Warn("could not determine the latest Miabi version (%v) — falling back to %s.\n"+
			"    Pin one with --version 1.8.0 to keep rollback and drift detection working.", err, DefaultPlatformImage)
		return DefaultPlatformImage
	}
	return platformRepo + ":" + v
}

// releaseLookupTimeout sits in front of an install, so it fails fast enough that the operator
// reaches the --version advice while still paying attention.
const releaseLookupTimeout = 20 * time.Second

// imageForVersion turns a bare --version into a reference. setup has no version field on
// stackcmd.SetupOptions, so the CLI resolves it here and passes an image.
func imageForVersion(v string) string { return platformRepo + ":" + release.Normalize(v) }

// ---------------------------------------------------------------------------
// setup

type setupOpts struct {
	domain, adminEmail, acmeEmail, controlURL    string
	version                                      string
	image, gatewayImage, runnerImage, gomaConfig string
	registry, noHostProc, yes                    bool
	registryHost, subnet, file                   string
}

// newSetupCmd builds the install/converge command. It is constructed rather than declared so the
// verb can be registered under more than one parent; a cobra command may have only one.
func newSetupCmd(use string) *cobra.Command {
	o := &setupOpts{}
	c := &cobra.Command{
		Use:   use,
		Short: "Install the Miabi stack on this host, or converge an existing install",
		Long: "setup is idempotent. Run against an existing manifest it converges the stack to match,\n" +
			"keeping the stored secrets — a regenerated database password would lock the control plane\n" +
			"out of its own data. Re-running after hand-editing the manifest is how that edit is applied.",
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
	f.StringVar(&o.subnet, "subnet", "", "CIDR for the shared `miabi` network (default "+stack.DefaultSubnet+")")
	f.StringVarP(&o.file, "file", "f", "", "manifest path (default "+stack.DefaultConfigPath+")")
	f.BoolVarP(&o.yes, "yes", "y", false, "skip the confirmation prompt")
	return c
}

func runSetup(_ *cobra.Command, o *setupOpts) error {
	sess, err := openHost(o.file)
	if err != nil {
		return err
	}
	defer func() { _ = sess.Close() }()

	ctx, cancel := stackCtx()
	defer cancel()

	// --version names a release; --image names a reference. setup takes only the latter, so a
	// version is turned into one here. An explicit --image always wins.
	image := o.image
	if image == "" && strings.TrimSpace(o.version) != "" {
		image = imageForVersion(o.version)
	}

	res, err := stackcmd.Setup(ctx, sess.Svc, sess.Manifest, stackcmd.SetupOptions{
		Domain: o.domain, AdminEmail: o.adminEmail, ACMEEmail: o.acmeEmail, ControlURL: o.controlURL,
		Image: image, GatewayImage: o.gatewayImage, RunnerImage: o.runnerImage, GomaConfig: o.gomaConfig,
		RegistryHost: o.registryHost, Subnet: o.subnet,
		Registry: o.registry, NoHostProc: o.noHostProc, Yes: o.yes,
		DefaultImage: defaultControlPlaneImage,
	}, cliUI{})
	if err != nil {
		return err
	}
	_ = res
	fmt.Printf("\n  Manage it:\n\n    sudo miabi stack status\n\n" +
		"    …and likewise `miabi upgrade` (rolls the stack forward, rolling back if it fails)\n" +
		"    or `miabi stack uninstall` (keeps your data; add --volumes to destroy it).\n")
	return nil
}

// ---------------------------------------------------------------------------
// upgrade

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
			"the reference outright. A leading \"v\" is optional: v1.8.0 and 1.8.0 both work.",
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

// ---------------------------------------------------------------------------
// restart

// newRestartCmd restarts containers WITHOUT recreating them, so they re-read what is on disk.
// Editing the gateway's goma.yml is the obvious case (Goma watches its providers directory, not its
// base config); a spec change is the obvious non-case.
func newRestartCmd() *cobra.Command {
	var file string
	var yes bool
	c := &cobra.Command{
		Use:           "restart [component]",
		Short:         "Restart the stack, or one component (miabi, miabi-gateway, …)",
		Long:          "Restarts in place without recreating containers, so they re-read their on-disk config.\nA changed image or spec needs `miabi upgrade` instead.",
		Example:       "  sudo miabi stack restart\n  sudo miabi stack restart miabi-gateway",
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

// ---------------------------------------------------------------------------
// status

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

// ---------------------------------------------------------------------------
// uninstall

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

// ---------------------------------------------------------------------------
// migrate-config

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
			if err := host.RequireRoot(); err != nil {
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
