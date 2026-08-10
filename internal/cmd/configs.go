package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/miabi-io/miabi-cli/internal/api"
	"github.com/miabi-io/miabi-cli/internal/ui"
	"github.com/spf13/cobra"
)

var (
	configFromFile []string
	configFromDir  string
	configRecurse  bool
	configMerge    bool
	configMode     string
	configSensitiv bool
	configDelims   string
	configDesc     string
	configReveal   bool
	configRmYes    bool
	configSetYes   bool
)

func init() {
	configSetCmd.Flags().StringArrayVar(&configFromFile, "from-file", nil, "add a file as [key=]path (repeatable; \"-\" reads stdin and requires key=)")
	configSetCmd.Flags().StringVar(&configFromDir, "from-dir", "", "add every file in a directory, keyed by its relative path")
	configSetCmd.Flags().BoolVar(&configRecurse, "recursive", false, "with --from-dir, walk subdirectories")
	configSetCmd.Flags().BoolVar(&configMerge, "merge", false, "patch the named files instead of replacing the whole set")
	configSetCmd.Flags().StringVar(&configMode, "mode", "", "default octal file mode (e.g. 0644)")
	configSetCmd.Flags().BoolVar(&configSensitiv, "sensitive", false, "mark content as credentials: redacted in responses, diffed by digest")
	configSetCmd.Flags().StringVar(&configDelims, "delimiters", "", "interpolation delimiters as \"<<,>>\" for files whose own syntax uses {{ }}")
	configSetCmd.Flags().StringVar(&configDesc, "description", "", "human-readable description")
	configSetCmd.Flags().BoolVarP(&configSetYes, "yes", "y", false, "skip the redeploy confirmation")

	configCatCmd.Flags().BoolVar(&configReveal, "reveal", false, "print a sensitive config's content (admin only, audited)")
	configEditCmd.Flags().BoolVar(&configReveal, "reveal", false, "edit a sensitive config (admin only, audited)")
	configEditCmd.Flags().BoolVarP(&configSetYes, "yes", "y", false, "skip the redeploy confirmation")
	configRmCmd.Flags().BoolVarP(&configRmYes, "yes", "y", false, "skip the confirmation prompt")

	for _, c := range []*cobra.Command{configGetCmd, configCatCmd, configSetCmd, configEditCmd, configUsageCmd, configRmCmd} {
		c.ValidArgsFunction = completeConfigs
	}

	configCmd.AddCommand(configLsCmd, configGetCmd, configCatCmd, configSetCmd, configEditCmd, configUsageCmd, configRmCmd)
	rootCmd.AddCommand(configCmd)
}

var configCmd = &cobra.Command{
	Use:     "configs",
	Aliases: []string{"config"},
	Short:   "Manage workspace configuration files",
	Long: "Create, edit and delete configuration files mounted into applications as\n" +
		"read-only files. Content is encrypted at rest; a sensitive config's files are\n" +
		"shown only with --reveal, which is admin-only and audited.",
	Example: "  miabi configs ls\n" +
		"  miabi configs set prom-conf --from-file prometheus.yml\n" +
		"  miabi configs set rules --from-file alerts.yml --delimiters \"<<,>>\"\n" +
		"  miabi configs cat prom-conf prometheus.yml\n" +
		"  miabi configs edit prom-conf prometheus.yml\n" +
		"  miabi configs rm prom-conf",
}

var configLsCmd = &cobra.Command{
	Use:     "ls",
	Aliases: []string{"list"},
	Short:   "List configs in the workspace (no content)",
	RunE: func(_ *cobra.Command, _ []string) error {
		ctx, c, ws, err := configContext()
		if err != nil {
			return err
		}
		cfgs, err := c.Configs(ctx, ws)
		if err != nil {
			return err
		}
		if structured() {
			return emit(cfgs)
		}
		if len(cfgs) == 0 {
			ui.Info("No configs in this workspace. Add one: miabi configs set NAME --from-file app.conf")
			return nil
		}
		t := ui.NewTable("NAME", "FILES", "SIZE", "VERSION", "SENSITIVE", "UPDATED")
		for _, cfg := range cfgs {
			total := 0
			for _, n := range cfg.Sizes {
				total += n
			}
			sensitive := ""
			if cfg.Sensitive {
				sensitive = "yes"
			}
			t.Row(cfg.Name, fmt.Sprintf("%d", len(cfg.Keys)), humanBytes(int64(total)), fmt.Sprintf("v%d", cfg.Version), sensitive, ui.Age(cfg.UpdatedAt))
		}
		t.Print()
		return nil
	},
}

var configGetCmd = &cobra.Command{
	Use:     "get <name>",
	Aliases: []string{"show"},
	Short:   "Show a config's files and digest (no content)",
	Args:    cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		ctx, c, ws, err := configContext()
		if err != nil {
			return err
		}
		cfg, err := resolveConfig(ctx, c, ws, args[0])
		if err != nil {
			return err
		}
		if structured() {
			return emit(cfg)
		}
		ui.Detail("Name:        %s", ui.Bold(cfg.Name))
		if cfg.Description != "" {
			ui.Detail("Description: %s", cfg.Description)
		}
		ui.Detail("Version:     v%d", cfg.Version)
		ui.Detail("Mode:        %s", cfg.Mode)
		ui.Detail("Sensitive:   %t", cfg.Sensitive)
		if len(cfg.Delimiters) == 2 {
			ui.Detail("Delimiters:  %s %s", cfg.Delimiters[0], cfg.Delimiters[1])
		}
		ui.Detail("Digest:      %s", shortDigest(cfg.Digest))
		fmt.Println()
		t := ui.NewTable("FILE", "SIZE")
		for _, k := range cfg.Keys {
			t.Row(k, humanBytes(int64(cfg.Sizes[k])))
		}
		t.Print()
		return nil
	},
}

var configCatCmd = &cobra.Command{
	Use:   "cat <name> [key]",
	Short: "Print a config file's content",
	Long: "Prints one file's content. The key may be omitted when the config holds a\n" +
		"single file. A sensitive config requires --reveal, which is admin-only and\n" +
		"audited.",
	Args: cobra.RangeArgs(1, 2),
	RunE: func(_ *cobra.Command, args []string) error {
		ctx, c, ws, err := configContext()
		if err != nil {
			return err
		}
		cfg, err := resolveConfig(ctx, c, ws, args[0])
		if err != nil {
			return err
		}
		if cfg.Sensitive && !configReveal {
			return fmt.Errorf("config %q is sensitive — pass --reveal to print its content (admin only, audited)", cfg.Name)
		}
		data, err := c.ConfigData(ctx, ws, cfg.ID)
		if err != nil {
			return err
		}
		key, err := pickKey(cfg, data, args)
		if err != nil {
			return err
		}
		if structured() {
			return emit(map[string]string{key: data[key]})
		}
		fmt.Print(data[key])
		if !strings.HasSuffix(data[key], "\n") {
			fmt.Println()
		}
		return nil
	},
}

var configSetCmd = &cobra.Command{
	Use:   "set <name> --from-file [key=]path…",
	Short: "Create a config, or replace its files",
	Long: "Creates the config when it does not exist, otherwise replaces its whole file\n" +
		"set. Replacement is the default because a config is a declarative object and\n" +
		"`set` has to be idempotent in CI — use --merge to patch individual files.\n" +
		"Apps mounting the config are redeployed unless they set reloadPolicy: none.",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		data, err := collectFiles()
		if err != nil {
			return err
		}
		ctx, c, ws, err := configContext()
		if err != nil {
			return err
		}
		existing, err := c.FindConfigByName(ctx, ws, name)
		if err != nil {
			return err
		}
		delims, err := parseDelimiters(configDelims)
		if err != nil {
			return err
		}

		if existing == nil {
			if len(data) == 0 {
				return fmt.Errorf("at least one file is required to create %q — pass --from-file or --from-dir", name)
			}
			cfg, cerr := c.CreateConfig(ctx, ws, api.CreateConfigRequest{
				Name: name, Description: configDesc, Data: data,
				Mode: configMode, Sensitive: configSensitiv, Delimiters: delims,
			})
			if cerr != nil {
				return cerr
			}
			ui.Success("Created config %s %s", ui.Bold(cfg.Name), ui.Dim(fmt.Sprintf("(%d file(s))", len(cfg.Keys))))
			return nil
		}

		if len(data) == 0 && !cmd.Flags().Changed("description") && !cmd.Flags().Changed("mode") {
			return fmt.Errorf("nothing to change: pass --from-file/--from-dir, --description or --mode")
		}
		if len(data) > 0 && configMerge {
			current, derr := c.ConfigData(ctx, ws, existing.ID)
			if derr != nil {
				return fmt.Errorf("read current files for --merge: %w", derr)
			}
			for k, v := range data {
				current[k] = v
			}
			data = current
		}
		if len(data) > 0 {
			if err := confirmRedeploy(ctx, c, ws, existing); err != nil {
				return err
			}
		}
		cfg, err := c.UpdateConfig(ctx, ws, existing.ID, api.UpdateConfigRequest{
			Data: data, Description: configDesc, Mode: configMode, Delimiters: delims,
		})
		if err != nil {
			return err
		}
		ui.Success("Updated %s %s", ui.Bold(cfg.Name), ui.Dim(fmt.Sprintf("(v%d, %d file(s))", cfg.Version, len(cfg.Keys))))
		return nil
	},
}

var configEditCmd = &cobra.Command{
	Use:   "edit <name> [key]",
	Short: "Edit a config file in $EDITOR",
	Long: "Opens one file in $EDITOR (or $VISUAL) and saves it back when the editor\n" +
		"exits. The key may be omitted when the config holds a single file.",
	Args: cobra.RangeArgs(1, 2),
	RunE: func(_ *cobra.Command, args []string) error {
		ctx, c, ws, err := configContext()
		if err != nil {
			return err
		}
		cfg, err := resolveConfig(ctx, c, ws, args[0])
		if err != nil {
			return err
		}
		if cfg.Sensitive && !configReveal {
			return fmt.Errorf("config %q is sensitive — pass --reveal to edit it (admin only, audited)", cfg.Name)
		}
		data, err := c.ConfigData(ctx, ws, cfg.ID)
		if err != nil {
			return err
		}
		key, err := pickKey(cfg, data, args)
		if err != nil {
			return err
		}
		edited, err := editInEditor(key, data[key])
		if err != nil {
			return err
		}
		if edited == data[key] {
			ui.Info("No changes.")
			return nil
		}
		if err := confirmRedeploy(ctx, c, ws, cfg); err != nil {
			return err
		}
		data[key] = edited
		updated, err := c.UpdateConfig(ctx, ws, cfg.ID, api.UpdateConfigRequest{Data: data})
		if err != nil {
			return err
		}
		ui.Success("Updated %s/%s %s", ui.Bold(cfg.Name), key, ui.Dim(fmt.Sprintf("(v%d)", updated.Version)))
		return nil
	},
}

var configUsageCmd = &cobra.Command{
	Use:     "usage <name>",
	Aliases: []string{"uses", "refs"},
	Short:   "List the apps mounting a config",
	Args:    cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		ctx, c, ws, err := configContext()
		if err != nil {
			return err
		}
		cfg, err := resolveConfig(ctx, c, ws, args[0])
		if err != nil {
			return err
		}
		apps, err := c.ConfigUsage(ctx, ws, cfg.ID)
		if err != nil {
			return err
		}
		if structured() {
			return emit(apps)
		}
		if len(apps) == 0 {
			ui.Info("No applications mount %s.", cfg.Name)
			return nil
		}
		t := ui.NewTable("APPLICATION")
		for _, a := range apps {
			t.Row(a.Name)
		}
		t.Print()
		return nil
	},
}

var configRmCmd = &cobra.Command{
	Use:     "rm <name>",
	Aliases: []string{"delete"},
	Short:   "Delete a config (blocked while apps still mount it)",
	Args:    cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		ctx, c, ws, err := configContext()
		if err != nil {
			return err
		}
		cfg, err := resolveConfig(ctx, c, ws, args[0])
		if err != nil {
			return err
		}
		if !configRmYes && !structured() {
			if !ui.Confirm(fmt.Sprintf("Delete config %s and its %d file(s)?", cfg.Name, len(cfg.Keys))) {
				ui.Info("Aborted.")
				return nil
			}
		}
		if err := c.DeleteConfig(ctx, ws, cfg.ID); err != nil {
			return err
		}
		ui.Success("Deleted config %s", ui.Bold(cfg.Name))
		return nil
	},
}

func configContext() (context.Context, *api.Client, string, error) {
	ctx := context.Background()
	c, eff, err := newClient()
	if err != nil {
		return nil, nil, "", err
	}
	ws, err := workspaceRef(ctx, c, eff)
	if err != nil {
		return nil, nil, "", err
	}
	return ctx, c, ws, nil
}

func resolveConfig(ctx context.Context, c *api.Client, ws, ref string) (*api.Config, error) {
	id, err := c.ResolveConfigID(ctx, ws, ref)
	if err != nil {
		return nil, err
	}
	return c.Config(ctx, ws, id)
}

// pickKey resolves the file to act on: the explicit argument, or the only file
// when the config holds one. Guessing among several would edit the wrong file.
func pickKey(cfg *api.Config, data map[string]string, args []string) (string, error) {
	if len(args) == 2 {
		if _, ok := data[args[1]]; !ok {
			return "", fmt.Errorf("config %q has no file %q (have: %s)", cfg.Name, args[1], strings.Join(cfg.Keys, ", "))
		}
		return args[1], nil
	}
	if len(data) == 1 {
		for k := range data {
			return k, nil
		}
	}
	return "", fmt.Errorf("config %q holds %d files — name one: %s", cfg.Name, len(data), strings.Join(cfg.Keys, ", "))
}

// collectFiles reads --from-file and --from-dir into the file set. A key is
// inferred from the basename unless given as key=path; stdin has no basename to
// infer from, so it requires the explicit form.
func collectFiles() (map[string]string, error) {
	data := map[string]string{}
	for _, spec := range configFromFile {
		key, pathArg := "", spec
		if k, p, ok := strings.Cut(spec, "="); ok {
			key, pathArg = k, p
		}
		var (
			content []byte
			err     error
		)
		if pathArg == "-" {
			if key == "" {
				return nil, fmt.Errorf("reading from stdin needs an explicit key: --from-file key=-")
			}
			content, err = io.ReadAll(os.Stdin)
		} else {
			content, err = os.ReadFile(pathArg)
			if key == "" {
				key = filepath.Base(pathArg)
			}
		}
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", pathArg, err)
		}
		data[key] = string(content)
	}
	if configFromDir != "" {
		if err := collectDir(configFromDir, data); err != nil {
			return nil, err
		}
	}
	return data, nil
}

func collectDir(dir string, data map[string]string) error {
	return filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(dir, p)
		if rerr != nil {
			return rerr
		}
		if info.IsDir() {
			if rel != "." && !configRecurse {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(filepath.Base(p), ".") {
			return nil
		}
		content, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		data[filepath.ToSlash(rel)] = string(content)
		return nil
	})
}

func parseDelimiters(s string) ([]string, error) {
	if s == "" {
		return nil, nil
	}
	left, right, ok := strings.Cut(s, ",")
	if !ok || left == "" || right == "" || left == right {
		return nil, fmt.Errorf("--delimiters must be two distinct non-empty markers, e.g. \"<<,>>\"")
	}
	return []string{left, right}, nil
}

// confirmRedeploy names the apps a content change will restart. A config edit
// restarting production apps should never be a silent side effect.
func confirmRedeploy(ctx context.Context, c *api.Client, ws string, cfg *api.Config) error {
	if configSetYes || structured() {
		return nil
	}
	apps, err := c.ConfigUsage(ctx, ws, cfg.ID)
	if err != nil || len(apps) == 0 {
		return nil
	}
	names := make([]string, 0, len(apps))
	for _, a := range apps {
		names = append(names, a.Name)
	}
	sort.Strings(names)
	if !ui.Confirm(fmt.Sprintf("This will redeploy %d app(s): %s. Continue?", len(names), strings.Join(names, ", "))) {
		return fmt.Errorf("aborted")
	}
	return nil
}

func editInEditor(key, content string) (string, error) {
	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		return "", fmt.Errorf("no editor configured — set $EDITOR or $VISUAL")
	}
	f, err := os.CreateTemp("", "miabi-config-*"+filepath.Ext(key))
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		return "", err
	}
	f.Close()

	parts := strings.Fields(editor)
	cmd := exec.Command(parts[0], append(parts[1:], f.Name())...) //nolint:gosec // the editor is the user's own $EDITOR
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("editor exited: %w", err)
	}
	edited, err := os.ReadFile(f.Name())
	if err != nil {
		return "", err
	}
	return string(edited), nil
}

func shortDigest(d string) string {
	if len(d) > 19 {
		return d[:19] + "…"
	}
	return d
}

func completeConfigs(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	ctx, c, ws, err := configContext()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	// The second argument is a file key, which needs the named config's listing.
	if len(args) == 1 {
		cfg, cerr := resolveConfig(ctx, c, ws, args[0])
		if cerr != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var out []string
		for _, k := range cfg.Keys {
			if strings.HasPrefix(k, toComplete) {
				out = append(out, k)
			}
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	}
	if len(args) > 1 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	cfgs, err := c.Configs(ctx, ws)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var out []string
	for _, cfg := range cfgs {
		if strings.HasPrefix(cfg.Name, toComplete) {
			out = append(out, cfg.Name)
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}
