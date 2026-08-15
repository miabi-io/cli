package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/miabi-io/cli/internal/api"
	"github.com/miabi-io/cli/internal/ui"
	"github.com/spf13/cobra"
)

var (
	secretValue    string
	secretFromFile string
	secretDesc     string
	secretRmYes    bool
)

func init() {
	secretSetCmd.Flags().StringVar(&secretValue, "value", "", "the secret value (leaks into shell history; prefer --from-file or stdin)")
	secretSetCmd.Flags().StringVar(&secretFromFile, "from-file", "", "read the value from a file (use \"-\" for stdin)")
	secretSetCmd.Flags().StringVar(&secretDesc, "description", "", "human-readable description")

	secretRmCmd.Flags().BoolVarP(&secretRmYes, "yes", "y", false, "skip the confirmation prompt")

	for _, c := range []*cobra.Command{secretGetCmd, secretSetCmd, secretRevealCmd, secretUsageCmd, secretRmCmd} {
		c.ValidArgsFunction = completeSecrets
	}

	secretCmd.AddCommand(secretLsCmd, secretGetCmd, secretSetCmd, secretRevealCmd, secretUsageCmd, secretRmCmd)
	rootCmd.AddCommand(secretCmd)
}

var secretCmd = &cobra.Command{
	Use:     "secrets",
	Aliases: []string{"secret", "vault"},
	Short:   "Manage the workspace secret vault",
	Long: "Create, rotate, reveal, and delete workspace secrets. Values are stored\n" +
		"encrypted at rest and are write-only over the API — reference them from an\n" +
		"app's env as ${{ secrets.NAME }}, and reveal a value explicitly when needed.",
	Example: "  miabi secrets ls\n" +
		"  miabi secrets get STRIPE_KEY\n" +
		"  miabi secrets set STRIPE_KEY --from-file key.txt\n" +
		"  cat key.txt | miabi secrets set STRIPE_KEY --from-file -\n" +
		"  miabi secrets reveal STRIPE_KEY\n" +
		"  miabi secrets rm STRIPE_KEY",
}

var secretLsCmd = &cobra.Command{
	Use:     "ls",
	Aliases: []string{"list"},
	Short:   "List secrets in the workspace (no values)",
	RunE: func(cmd *cobra.Command, _ []string) error {
		ctx := context.Background()
		c, eff, err := newClient()
		if err != nil {
			return err
		}
		ws, err := workspaceRef(ctx, c, eff)
		if err != nil {
			return err
		}
		secrets, err := c.Secrets(ctx, ws)
		if err != nil {
			return err
		}
		if structured() {
			return emit(secrets)
		}
		if len(secrets) == 0 {
			ui.Info("No secrets in this workspace. Add one: miabi secrets set NAME --from-file value.txt")
			return nil
		}
		t := ui.NewTable("NAME", "VERSION", "MANAGED", "UPDATED")
		for _, s := range secrets {
			managed := ""
			if s.Managed {
				managed = "yes"
			}
			t.Row(s.Name, fmt.Sprintf("v%d", s.Version), managed, ui.Age(s.UpdatedAt))
		}
		t.Print()
		return nil
	},
}

var secretGetCmd = &cobra.Command{
	Use:     "get <name>",
	Aliases: []string{"show"},
	Short:   "Show a secret's details (no value)",
	Long: "Shows a secret's metadata — description, version, and created/updated dates.\n" +
		"The value is not shown; use `miabi secrets reveal` for that.",
	Example: "  miabi secrets get db_wordpress_wordpress_url",
	Args:    cobra.ExactArgs(1),
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
		s, err := findSecret(ctx, c, ws, args[0])
		if err != nil {
			return err
		}
		if structured() {
			return emit(s)
		}
		ui.Detail("Name:        %s", ui.Bold(s.Name))
		if s.DisplayName != "" && s.DisplayName != s.Name {
			ui.Detail("Label:       %s", s.DisplayName)
		}
		desc := s.Description
		if desc == "" {
			desc = ui.Dim("(none)")
		}
		ui.Detail("Description: %s", desc)
		ui.Detail("Version:     v%d", s.Version)
		if s.Managed {
			ui.Detail("Managed:     yes %s", ui.Dim("(owned by a platform resource — rotate via its owner)"))
		}
		ui.Detail("Created:     %s", fmtTimestamp(s.CreatedAt))
		ui.Detail("Updated:     %s", fmtTimestamp(s.UpdatedAt))
		ui.Detail("%s", ui.Dim("Reference:   ${{ secrets."+s.Name+" }}   ·   reveal: miabi secrets reveal "+s.Name))
		return nil
	},
}

var secretSetCmd = &cobra.Command{
	Use:   "set <name> [--value V | --from-file F] [--description D]",
	Short: "Create a secret, or rotate an existing one's value",
	Long: "Sets a workspace secret. If the named secret exists, its value is rotated;\n" +
		"otherwise it is created. Provide the value with --value, --from-file, or by\n" +
		"piping it on stdin (--from-file -). Reading from a file or stdin keeps the\n" +
		"value out of your shell history.",
	Example: "  miabi secrets set API_KEY --from-file api.key\n" +
		"  printf '%s' \"$TOKEN\" | miabi secrets set API_KEY --from-file -\n" +
		"  miabi secrets set API_KEY --description \"rotated 2026-07\"   # keep value, edit description",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		value, hasValue, err := readSecretValue()
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
		existing, err := c.FindSecretByName(ctx, ws, name)
		if err != nil {
			return err
		}

		descChanged := cmd.Flags().Changed("description")
		if existing != nil {
			if existing.Managed {
				return fmt.Errorf("secret %q is managed by a platform resource — rotate it via its owner, not by hand", name)
			}
			if !hasValue && !descChanged {
				return fmt.Errorf("nothing to change: pass a new value (--value/--from-file) or --description")
			}
			// Blank value keeps the stored value; preserve the description unless the
			// flag was explicitly set.
			desc := existing.Description
			if descChanged {
				desc = secretDesc
			}
			if _, err := c.UpdateSecret(ctx, ws, existing.ID, api.UpdateSecretRequest{Value: value, Description: desc}); err != nil {
				return err
			}
			switch {
			case hasValue && descChanged:
				ui.Success("Rotated %s and updated its description %s", ui.Bold(name), ui.Dim("(redeploy referencing apps to apply)"))
			case hasValue:
				ui.Success("Rotated %s %s", ui.Bold(name), ui.Dim("(redeploy referencing apps to apply)"))
			default:
				ui.Success("Updated %s description", ui.Bold(name))
			}
			return nil
		}

		if !hasValue {
			return fmt.Errorf("a value is required to create %q — pass --value, --from-file, or pipe it on stdin", name)
		}
		s, err := c.CreateSecret(ctx, ws, api.CreateSecretRequest{Name: name, Value: value, Description: secretDesc})
		if err != nil {
			return err
		}
		ui.Success("Created secret %s %s", ui.Bold(s.Name), ui.Dim("— reference it as ${{ secrets."+s.Name+" }}"))
		return nil
	},
}

var secretRevealCmd = &cobra.Command{
	Use:   "reveal <name>",
	Short: "Print a secret's decrypted value (admin only, audited)",
	Long: "Reveals a secret's value. The value is written to stdout with no decoration\n" +
		"so it can be captured (e.g. TOKEN=$(miabi secrets reveal TOKEN)). This action\n" +
		"is admin-only and recorded in the audit log.",
	Args: cobra.ExactArgs(1),
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
		id, err := c.ResolveSecretID(ctx, ws, args[0])
		if err != nil {
			return err
		}
		val, err := c.RevealSecret(ctx, ws, id)
		if err != nil {
			return err
		}
		if structured() {
			return emit(api.SecretReveal{Value: val})
		}
		// Raw value, no trailing formatting, so it pipes cleanly.
		fmt.Fprintln(os.Stdout, val)
		return nil
	},
}

var secretUsageCmd = &cobra.Command{
	Use:     "usage <name>",
	Aliases: []string{"uses", "refs"},
	Short:   "List the apps that reference a secret",
	Args:    cobra.ExactArgs(1),
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
		id, err := c.ResolveSecretID(ctx, ws, args[0])
		if err != nil {
			return err
		}
		apps, err := c.SecretUsage(ctx, ws, id)
		if err != nil {
			return err
		}
		if structured() {
			return emit(apps)
		}
		if len(apps) == 0 {
			ui.Info("%s is not referenced by any app", ui.Bold(args[0]))
			return nil
		}
		t := ui.NewTable("APP", "ID")
		for _, a := range apps {
			t.Row(a.Name, itoa(int(a.ID)))
		}
		t.Print()
		return nil
	},
}

var secretRmCmd = &cobra.Command{
	Use:     "rm <name>",
	Aliases: []string{"delete"},
	Short:   "Delete a secret (blocked while apps still reference it)",
	Args:    cobra.ExactArgs(1),
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
		s, err := findSecret(ctx, c, ws, args[0])
		if err != nil {
			return err
		}
		// Managed secrets are owned by a platform resource and are removed with
		// their owner — refuse the hand delete (the server enforces this too).
		if s.Managed {
			return fmt.Errorf("secret %q is managed by a platform resource; delete its owner instead", s.Name)
		}
		if !secretRmYes && !structured() {
			if !ui.Confirm(fmt.Sprintf("Delete secret %s? Apps that reference it will lose it on next deploy.", ui.Bold(s.Name))) {
				ui.Info("Aborted")
				return nil
			}
		}
		if err := c.DeleteSecret(ctx, ws, s.ID); err != nil {
			return err
		}
		ui.Success("Deleted secret %s", ui.Bold(s.Name))
		return nil
	},
}

// readSecretValue resolves the secret value from --value, --from-file (with "-"
// meaning stdin), or piped stdin. It returns (value, provided, error); provided
// is false when no value source was given (a description-only edit).
func readSecretValue() (string, bool, error) {
	if secretValue != "" {
		return secretValue, true, nil
	}
	if secretFromFile != "" {
		v, err := readFileOrStdin(secretFromFile)
		if err != nil {
			return "", false, err
		}
		return v, true, nil
	}
	// Piped stdin (not an interactive terminal) is treated as the value.
	if fi, err := os.Stdin.Stat(); err == nil && (fi.Mode()&os.ModeCharDevice) == 0 {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", false, err
		}
		if v := strings.TrimRight(string(b), "\n"); v != "" {
			return v, true, nil
		}
	}
	return "", false, nil
}

// fmtTimestamp renders an absolute local time with a relative age suffix, e.g.
// "2026-07-03 15:04 (2 days ago)".
func fmtTimestamp(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	age := ui.Age(t)
	if age != "just now" {
		age += " ago"
	}
	return fmt.Sprintf("%s %s", t.Local().Format("2006-01-02 15:04"), ui.Dim("("+age+")"))
}

// findSecret resolves a secret by name (or numeric id) to its full record,
// erroring when none matches in the workspace.
func findSecret(ctx context.Context, c *api.Client, ws, ref string) (*api.Secret, error) {
	secrets, err := c.Secrets(ctx, ws)
	if err != nil {
		return nil, err
	}
	for i := range secrets {
		if secrets[i].Name == ref || itoa(int(secrets[i].ID)) == ref {
			return &secrets[i], nil
		}
	}
	return nil, fmt.Errorf("secret %q not found in this workspace", ref)
}

// completeSecrets tab-completes secret handles in the active workspace.
func completeSecrets(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
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
	secrets, err := c.Secrets(ctx, ws)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var out []string
	for _, s := range secrets {
		if toComplete == "" || strings.HasPrefix(s.Name, toComplete) {
			out = append(out, s.Name)
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}
