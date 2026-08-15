package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/miabi-io/cli/internal/api"
	"github.com/miabi-io/cli/internal/ui"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	envSecret   bool
	envFile     string
	envFromFile string
)

func init() {
	envSetCmd.Flags().BoolVar(&envSecret, "secret", false, "store the value encrypted at rest")
	envSetCmd.Flags().StringVar(&envFromFile, "from-file", "", `read the value from a file ("-" for stdin); pass a bare KEY, not KEY=VALUE`)

	envImportCmd.Flags().StringVar(&envFile, "file", "", `path to a .env file ("-" for stdin)`)
	envImportCmd.Flags().BoolVar(&envSecret, "secret", false, "mark all imported vars as secrets")
	// Accept --from-file here too, so the flag means the same thing across the CLI
	// (`secrets set --from-file`, `apps env set --from-file`).
	envImportCmd.Flags().SetNormalizeFunc(func(_ *pflag.FlagSet, name string) pflag.NormalizedName {
		if name == "from-file" {
			name = "file"
		}
		return pflag.NormalizedName(name)
	})
	_ = envImportCmd.MarkFlagRequired("file")

	envLsCmd.ValidArgsFunction = completeApps
	envSetCmd.ValidArgsFunction = completeApps
	envImportCmd.ValidArgsFunction = completeApps

	envCmd.AddCommand(envLsCmd, envSetCmd, envImportCmd)
	appCmd.AddCommand(envCmd)
}

var envCmd = &cobra.Command{
	Use:   "env",
	Short: "Manage an application's environment variables",
}

var envLsCmd = &cobra.Command{
	Use:     "ls [app]",
	Aliases: []string{"list"},
	Short:   "List an application's environment variables",
	Long: "Lists the app's env vars (positional, or the app bound by `miabi use`).\n" +
		"Values of vars marked secret are masked by the server — the API never returns\n" +
		"them in plaintext.",
	Example: "  miabi apps env ls web\n  miabi apps env ls          # bound app",
	Args:    cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		c, eff, err := newClient()
		if err != nil {
			return err
		}
		ws, err := workspaceRef(ctx, c, eff)
		if err != nil {
			return err
		}
		appID, appRef, err := resolveAppRef(ctx, c, eff, ws, appArg(args))
		if err != nil {
			return err
		}
		vars, err := c.EnvVars(ctx, ws, appID)
		if err != nil {
			return err
		}
		if structured() {
			return emit(vars)
		}
		if len(vars) == 0 {
			ui.Info("No environment variables on %s. Add one: miabi apps env set %s KEY=VALUE", appRef, appRef)
			return nil
		}
		t := ui.NewTable("KEY", "VALUE", "SECRET")
		for _, v := range vars {
			secret := ""
			if v.IsSecret {
				secret = "yes"
			}
			t.Row(v.Key, v.Value, secret)
		}
		t.Print()
		return nil
	},
}

var envSetCmd = &cobra.Command{
	Use:   "set [app] KEY=VALUE",
	Short: "Set a single environment variable",
	Long: "Sets one env var on the app (positional, or the app bound by `miabi use`).\n" +
		"Pass KEY=VALUE inline, or a bare KEY with --from-file to read the value from a\n" +
		"file or stdin — which keeps it out of your shell history. Pair --from-file with\n" +
		"--secret to store a credential encrypted at rest.",
	Example: "  miabi apps env set web LOG_LEVEL=debug\n" +
		"  miabi apps env set API_KEY --from-file key.txt --secret   # bound app\n" +
		"  printf '%s' \"$TOKEN\" | miabi apps env set API_KEY --from-file - --secret",
	Args: cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		// The last arg is the KEY (or KEY=VALUE); an optional leading arg is the app.
		appRefArg := ""
		last := args[len(args)-1]
		if len(args) == 2 {
			appRefArg = args[0]
		}

		var key, value string
		if envFromFile != "" {
			key = last
			if strings.Contains(key, "=") {
				return fmt.Errorf("with --from-file pass a bare KEY (the value comes from the file), got %q", key)
			}
			v, err := readFileOrStdin(envFromFile)
			if err != nil {
				return err
			}
			value = v
		} else {
			var ok bool
			key, value, ok = strings.Cut(last, "=")
			if !ok || key == "" {
				return fmt.Errorf("expected KEY=VALUE (or a bare KEY with --from-file), got %q", last)
			}
		}

		ctx := context.Background()
		c, eff, err := newClient()
		if err != nil {
			return err
		}
		ws, err := workspaceRef(ctx, c, eff)
		if err != nil {
			return err
		}
		appID, _, err := resolveAppRef(ctx, c, eff, ws, appRefArg)
		if err != nil {
			return err
		}
		if err := c.SetEnv(ctx, ws, appID, api.SetEnvRequest{Key: key, Value: value, IsSecret: envSecret}); err != nil {
			return err
		}
		ui.Success("Set %s %s", ui.Bold(key), ui.Dim("(redeploy to apply)"))
		return nil
	},
}

var envImportCmd = &cobra.Command{
	Use:   "import [app] --from-file .env",
	Short: "Bulk-import environment variables from a .env file",
	Example: "  miabi apps env import web --from-file .env\n" +
		"  miabi apps env import --from-file .env   # bound app\n" +
		"  cat .env | miabi apps env import --from-file -",
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		content, err := readFileOrStdin(envFile)
		if err != nil {
			return err
		}
		ctx := context.Background()
		c, eff, err := newClient()
		if err != nil {
			return err
		}
		ws, err := workspaceRef(ctx, c, eff)
		if err != nil {
			return err
		}
		appID, _, err := resolveAppRef(ctx, c, eff, ws, appArg(args))
		if err != nil {
			return err
		}
		if err := c.ImportEnv(ctx, ws, appID, api.ImportEnvRequest{Content: content, IsSecret: envSecret}); err != nil {
			return err
		}
		ui.Success("Imported env from %s %s", envFile, ui.Dim("(redeploy to apply)"))
		return nil
	},
}
