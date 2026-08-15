// Package cmd is the cobra command tree for the miabi CLI. Each file is one
// command group; all HTTP work goes through internal/api.
package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/miabi-io/cli/internal/api"
	"github.com/miabi-io/cli/internal/config"
	"github.com/miabi-io/cli/internal/ui"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// version is overridden at build time via -ldflags.
var version = "dev"

// Persistent flags shared by every command.
var (
	flagContext   string
	flagServer    string
	flagURL       string // deprecated alias of --server
	flagToken     string
	flagCA        string
	flagInsecure  bool
	flagWorkspace string
	flagJSON      bool
	flagOutput    string
	flagNoColor   bool
	flagVerbose   bool
)

// serverFlag returns the effective server URL from the flags: --server, else the
// deprecated --url.
func serverFlag() string { return firstNonEmpty(flagServer, flagURL) }

var rootCmd = &cobra.Command{
	Use:   "miabi",
	Short: "Imperative client for a Miabi control panel",
	Long: "miabi drives the deploy flow against a Miabi panel's /api/v1 HTTP API.\n\n" +
		"Authenticate with MIABI_SERVER + MIABI_TOKEN (or `miabi login`), pick a workspace\n" +
		"with `miabi workspace switch`, and bind a default app with `miabi use <app>` so\n" +
		"app commands need no argument.",
	Example: "  miabi use web                      # bind a default app\n" +
		"  miabi apps deploy --tag $GIT_SHA --wait\n" +
		"  miabi apps logs                    # live logs of the bound app\n" +
		"  miabi apps deployments             # deploy history (by number)",
	SilenceUsage:  true,
	SilenceErrors: true,
	// Normalize output/color once for every command.
	PersistentPreRun: func(_ *cobra.Command, _ []string) {
		if flagJSON && flagOutput == "table" {
			flagOutput = "json"
		}
		// Structured output and --no-color both force plain (uncolored) text.
		if flagNoColor || structured() {
			ui.SetColor(false)
		}
	},
}

// Execute runs the CLI, mapping any error to a non-zero exit code.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		ui.Fail("%s", err)
		os.Exit(1)
	}
}

func init() {
	api.Version = version
	rootCmd.Version = version
	pf := rootCmd.PersistentFlags()
	pf.StringVar(&flagContext, "context", "", "config context to use (env MIABI_CONTEXT; default: current)")
	pf.StringVar(&flagServer, "server", "", "Miabi server URL (env MIABI_SERVER)")
	pf.StringVar(&flagURL, "url", "", "deprecated: use --server")
	_ = pf.MarkDeprecated("url", "use --server instead")
	pf.StringVar(&flagToken, "token", "", "API token (env MIABI_TOKEN)")
	pf.StringVar(&flagCA, "certificate-authority", "", "path to a CA bundle to trust (env MIABI_CA)")
	pf.BoolVar(&flagInsecure, "insecure-skip-tls-verify", false, "skip TLS verification (env MIABI_INSECURE_SKIP_TLS_VERIFY)")
	pf.StringVarP(&flagWorkspace, "workspace", "w", "", "workspace name or id (default: active/bound)")
	pf.StringVarP(&flagOutput, "output", "o", "table", "output format: table | json | yaml")
	pf.BoolVar(&flagJSON, "json", false, "shorthand for --output json")
	pf.BoolVar(&flagNoColor, "no-color", false, "disable colored output")
	pf.BoolVar(&flagVerbose, "verbose", false, "log HTTP requests to stderr")
}

// newClient resolves the connection context and returns an API client.
func newClient() (*api.Client, *config.Effective, error) {
	eff, err := config.Resolve(config.Flags{
		Context: flagContext, Server: serverFlag(), Token: flagToken, CA: flagCA, InsecureSkip: flagInsecure,
	})
	if err != nil {
		return nil, nil, err
	}
	// Skipping TLS verification is a foot-gun that persists in a context, so warn
	// loudly on every use (to stderr; suppressed for structured output so it never
	// corrupts a machine-readable stream).
	if eff.InsecureSkip && !structured() {
		ui.Warn("TLS verification disabled for %s — this connection can be intercepted.", eff.URL)
	}
	c, err := api.New(api.Options{
		BaseURL: eff.URL, Token: eff.Token, CAFile: eff.CA, InsecureSkip: eff.InsecureSkip, Verbose: flagVerbose,
	})
	if err != nil {
		return nil, nil, err
	}
	return c, eff, nil
}

// workspaceRef resolves the workspace name (handle) the API addresses by for the
// current invocation: --workspace, else the persisted active workspace, else a
// workspace-bound token's own workspace, else the sole accessible workspace.
func workspaceRef(ctx context.Context, c *api.Client, eff *config.Effective) (string, error) {
	var fallback string
	if eff.Workspace != nil {
		if eff.Workspace.Name != "" {
			fallback = eff.Workspace.Name
		} else if eff.Workspace.ID != 0 {
			// Older config without a saved name: resolve by id.
			fallback = strconv.FormatUint(uint64(eff.Workspace.ID), 10)
		}
	}
	if flagWorkspace == "" && fallback == "" {
		if me, err := c.Me(ctx); err == nil && me.Auth.WorkspaceID != nil {
			fallback = strconv.FormatUint(uint64(*me.Auth.WorkspaceID), 10)
		}
	}
	return c.ResolveWorkspaceName(ctx, flagWorkspace, fallback)
}

// itoa is a terse strconv.Itoa for building table cells.
func itoa(n int) string { return strconv.Itoa(n) }

// readFileOrStdin reads a value from path, or from stdin when path is "-". The
// trailing newline is trimmed, so `printf '%s\n' secret > f` and `echo secret |
// … -` both yield the value without a stray \n. Reading from a file or a pipe is
// how a secret stays out of your shell history.
func readFileOrStdin(path string) (string, error) {
	var (
		b   []byte
		err error
	)
	if path == "-" {
		b, err = io.ReadAll(os.Stdin)
	} else {
		b, err = os.ReadFile(path)
	}
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(b), "\n"), nil
}

// structured reports whether the user asked for machine-readable output
// (--json or -o json|yaml), in which case commands skip their human rendering.
func structured() bool { return flagJSON || flagOutput == "json" || flagOutput == "yaml" }

// emit renders v in the requested structured format. Commands call it only when
// structured() is true.
func emit(v any) error {
	if flagOutput == "yaml" {
		return yaml.NewEncoder(os.Stdout).Encode(v)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// resolveAppRef resolves the target application for an app-scoped command:
// an explicit reference (the command's positional arg) wins, else the app bound
// by `miabi use`, else a helpful error. It returns the numeric id API paths use
// and the handle to display. ref is the handle or numeric id ("" to use the bound app).
func resolveAppRef(ctx context.Context, c *api.Client, eff *config.Effective, ws, ref string) (uint, string, error) {
	if ref == "" && eff.App != nil {
		if eff.App.Name != "" {
			ref = eff.App.Name
		} else if eff.App.ID != 0 {
			ref = strconv.FormatUint(uint64(eff.App.ID), 10)
		}
	}
	if ref == "" {
		return 0, "", fmt.Errorf("no application specified — pass it as the first argument, or bind a default with `miabi use <app>`")
	}
	id, err := c.ResolveAppID(ctx, ws, ref)
	if err != nil {
		return 0, "", err
	}
	return id, ref, nil
}

// appArg pulls the optional leading app reference from a command's args: it
// returns args[0] when present (else ""), for feeding to resolveAppRef.
func appArg(args []string) string {
	if len(args) > 0 {
		return args[0]
	}
	return ""
}

// completeApps is a cobra ValidArgsFunction that tab-completes app handles in the
// active workspace. Best-effort: any error yields no completions.
func completeApps(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	ctx := context.Background()
	c, eff, err := newClient()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	ws, err := workspaceRef(ctx, c, eff)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	apps, err := c.Apps(ctx, ws)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var out []string
	for _, a := range apps {
		if toComplete == "" || strings.HasPrefix(a.Name, toComplete) {
			out = append(out, a.Name)
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}
