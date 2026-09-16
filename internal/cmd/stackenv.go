package cmd

import (
	"fmt"
	"sort"

	"github.com/miabi-io/cli/internal/host"
	"github.com/miabi-io/miabi/pkg/stack"
	"github.com/miabi-io/miabi/pkg/stack/stackcmd"
	"github.com/spf13/cobra"
)

// stackEnvOpts is the flag surface shared by the env verbs. Named apart from the app-level `env`
// command in this package, which edits an application's variables over the API rather than the
// installed stack's manifest on this host.
type stackEnvOpts struct {
	gateway bool
	noApply bool
	yes     bool
	file    string
}

func (o *stackEnvOpts) options() stackcmd.EnvOptions {
	return stackcmd.EnvOptions{Gateway: o.gateway, NoApply: o.noApply, Yes: o.yes}
}

// newStackEnvCmd builds the env command group. Constructed rather than declared so it can be
// registered under both `stack` and `setup` — a cobra command may have only one parent.
func newStackEnvCmd() *cobra.Command {
	o := &stackEnvOpts{}
	c := &cobra.Command{
		Use:   "env",
		Short: "Read and change the installed stack's environment variables",
		Long: "Edits the manifest at " + stack.DefaultConfigPath + " and converges the stack, so a\n" +
			"variable takes effect without hand-editing the file. Only the component whose\n" +
			"environment changed is recreated.\n\n" +
			"Settings the manifest models with their own field — the registry, the networks, the\n" +
			"backup destination — are refused here and the error names where they live.",
		Example: "  sudo miabi stack env ls\n" +
			"  sudo miabi stack env set MIABI_SMTP_HOST=smtp.example.com\n" +
			"  sudo miabi stack env set GOMA_LOG_LEVEL=debug --gateway\n" +
			"  sudo miabi stack env unset MIABI_SMTP_HOST",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	f := c.PersistentFlags()
	f.BoolVar(&o.gateway, "gateway", false, "operate on the gateway's environment instead of the control plane's")
	f.StringVarP(&o.file, "file", "f", "", "manifest path (default "+stack.DefaultConfigPath+")")

	c.AddCommand(
		newStackEnvLsCmd(o),
		newStackEnvGetCmd(o),
		newStackEnvSetCmd(o),
		newStackEnvUnsetCmd(o),
	)
	return c
}

// readPath resolves the manifest for the read-only verbs. They need no Docker connection — only the
// file — but it is mode 0600, so say "run as root" rather than letting it fail as a bare
// permission error the operator has to interpret.
func readPath(o *stackEnvOpts) (string, error) {
	if err := host.RequireRoot(); err != nil {
		return "", err
	}
	return host.ManifestPath(o.file), nil
}

func newStackEnvLsCmd(o *stackEnvOpts) *cobra.Command {
	return &cobra.Command{
		Use:           "ls",
		Aliases:       []string{"list"},
		Short:         "List the environment the manifest carries",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(*cobra.Command, []string) error {
			path, err := readPath(o)
			if err != nil {
				return err
			}
			env, block, err := stackcmd.EnvList(path, o.options())
			if err != nil {
				return err
			}
			if len(env) == 0 {
				fmt.Printf("%s is empty.\n", block)
				return nil
			}
			keys := make([]string, 0, len(env))
			for k := range env {
				keys = append(keys, k)
			}
			sort.Strings(keys)

			fmt.Printf("%s:\n\n", block)
			for _, k := range keys {
				fmt.Printf("  %s=%s\n", k, env[k])
			}
			return nil
		},
	}
}

func newStackEnvGetCmd(o *stackEnvOpts) *cobra.Command {
	return &cobra.Command{
		Use:           "get KEY",
		Short:         "Print one variable's value",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(_ *cobra.Command, args []string) error {
			path, err := readPath(o)
			if err != nil {
				return err
			}
			value, found, err := stackcmd.EnvGet(path, args[0], o.options())
			if err != nil {
				return err
			}
			// Distinguish "set to empty" from "not set": printing a blank line for both would hide
			// which one an operator is looking at.
			if !found {
				return fmt.Errorf("%s is not set in the manifest", args[0])
			}
			fmt.Println(value)
			return nil
		},
	}
}

func newStackEnvSetCmd(o *stackEnvOpts) *cobra.Command {
	c := &cobra.Command{
		Use:   "set KEY=VALUE [KEY=VALUE…]",
		Short: "Set variables and converge the stack",
		Long: "Writes the values into the manifest, shows what changes, and recreates the component\n" +
			"whose environment they belong to. --no-apply saves without converging, for batching\n" +
			"several edits behind one `miabi setup`.",
		Args:          cobra.MinimumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(_ *cobra.Command, args []string) error {
			return withStackEnvSession(o, func(sess *host.Session) error {
				ctx, cancel := stackCtx()
				defer cancel()
				return stackcmd.EnvSet(ctx, sess.Svc, sess.Manifest, args, o.options(), cliUI{})
			})
		},
	}
	stackEnvWriteFlags(c, o)
	return c
}

func newStackEnvUnsetCmd(o *stackEnvOpts) *cobra.Command {
	c := &cobra.Command{
		Use:   "unset KEY [KEY…]",
		Short: "Remove variables and converge the stack",
		Long: "Removes the values from the manifest and recreates the component they belonged to.\n" +
			"A variable Miabi seeds (TZ, MIABI_LOG_LEVEL) comes back at its default rather than\n" +
			"disappearing, and the output says so.",
		Args:          cobra.MinimumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(_ *cobra.Command, args []string) error {
			return withStackEnvSession(o, func(sess *host.Session) error {
				ctx, cancel := stackCtx()
				defer cancel()
				return stackcmd.EnvUnset(ctx, sess.Svc, sess.Manifest, args, o.options(), cliUI{})
			})
		},
	}
	stackEnvWriteFlags(c, o)
	return c
}

func stackEnvWriteFlags(c *cobra.Command, o *stackEnvOpts) {
	c.Flags().BoolVar(&o.noApply, "no-apply", false, "save the manifest without converging the stack")
	c.Flags().BoolVarP(&o.yes, "yes", "y", false, "skip the confirmation prompt")
}

func withStackEnvSession(o *stackEnvOpts, run func(*host.Session) error) error {
	sess, err := openHost(o.file)
	if err != nil {
		return err
	}
	defer func() { _ = sess.Close() }()
	return run(sess)
}
