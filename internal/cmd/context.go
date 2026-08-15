package cmd

import (
	"fmt"

	"github.com/miabi-io/cli/internal/config"
	"github.com/miabi-io/cli/internal/ui"
	"github.com/spf13/cobra"
)

func init() {
	contextCmd.AddCommand(contextListCmd, contextCurrentCmd, contextUseCmd, contextDeleteCmd)
	rootCmd.AddCommand(contextCmd)
}

var contextCmd = &cobra.Command{
	Use:     "context",
	Aliases: []string{"ctx"},
	Short:   "Manage connection contexts (switch between Miabi servers)",
	Long: "A context is a named connection profile — server URL + TLS trust, token, and the\n" +
		"bound workspace/app. `miabi login` writes one; switch between them with\n" +
		"`miabi context use <name>`, or target one for a single command with --context.",
}

var contextListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List configured contexts",
	RunE: func(cmd *cobra.Command, _ []string) error {
		f, err := config.Load()
		if err != nil {
			return err
		}
		if structured() {
			return emit(contextsView(f))
		}
		if len(f.Contexts) == 0 {
			ui.Info("No contexts. Create one with `miabi login`.")
			return nil
		}
		t := ui.NewTable("", "NAME", "SERVER", "USER", "WORKSPACE")
		for _, name := range f.ContextNames() {
			c := f.Contexts[name]
			marker := " "
			if name == f.Current {
				marker = ui.Cyan("→")
			}
			t.Row(marker, name, c.Server.URL, contextUser(c), contextWorkspace(c))
		}
		t.Print()
		return nil
	},
}

var contextCurrentCmd = &cobra.Command{
	Use:   "current",
	Short: "Print the current context name",
	RunE: func(cmd *cobra.Command, _ []string) error {
		f, err := config.Load()
		if err != nil {
			return err
		}
		if f.Current == "" {
			ui.Info("No current context. Run `miabi login`.")
			return nil
		}
		fmt.Println(f.Current)
		return nil
	},
}

var contextUseCmd = &cobra.Command{
	Use:               "use <name>",
	Short:             "Switch the current context",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeContexts,
	RunE: func(cmd *cobra.Command, args []string) error {
		f, err := config.Load()
		if err != nil {
			return err
		}
		name := args[0]
		if f.Contexts[name] == nil {
			return fmt.Errorf("no context named %q — see `miabi context list`", name)
		}
		f.Current = name
		if err := config.Save(f); err != nil {
			return err
		}
		ui.Success("Switched to context %s", ui.Bold(name))
		return nil
	},
}

var contextDeleteCmd = &cobra.Command{
	Use:               "delete <name>",
	Aliases:           []string{"rm"},
	Short:             "Delete a context",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeContexts,
	RunE: func(cmd *cobra.Command, args []string) error {
		f, err := config.Load()
		if err != nil {
			return err
		}
		name := args[0]
		if f.Contexts[name] == nil {
			return fmt.Errorf("no context named %q", name)
		}
		delete(f.Contexts, name)
		// If we removed the current context, clear (or repoint to the sole remaining
		// one) so later commands don't reference a missing context.
		if f.Current == name {
			f.Current = ""
			if names := f.ContextNames(); len(names) == 1 {
				f.Current = names[0]
			}
		}
		if err := config.Save(f); err != nil {
			return err
		}
		ui.Success("Deleted context %s", ui.Bold(name))
		if f.Current != "" {
			ui.Info("Current context is now %s", ui.Bold(f.Current))
		}
		return nil
	},
}

// contextView is the structured (`-o json`) shape of one context — never the token.
type contextView struct {
	Name      string `json:"name"`
	Current   bool   `json:"current"`
	Server    string `json:"server"`
	User      string `json:"user,omitempty"`
	Workspace string `json:"workspace,omitempty"`
	App       string `json:"app,omitempty"`
}

func contextsView(f *config.File) []contextView {
	out := make([]contextView, 0, len(f.Contexts))
	for _, name := range f.ContextNames() {
		c := f.Contexts[name]
		out = append(out, contextView{
			Name: name, Current: name == f.Current, Server: c.Server.URL,
			User: contextUser(c), Workspace: contextWorkspace(c), App: contextApp(c),
		})
	}
	return out
}

func contextUser(c *config.Context) string {
	if c.User == nil {
		return ""
	}
	if c.User.Username != "" {
		return "@" + c.User.Username
	}
	return c.User.Email
}

func contextWorkspace(c *config.Context) string {
	if c.Workspace == nil {
		return ""
	}
	return c.Workspace.Name
}

func contextApp(c *config.Context) string {
	if c.App == nil {
		return ""
	}
	return c.App.Name
}

// completeContexts provides shell completion of context names.
func completeContexts(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	f, err := config.Load()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return f.ContextNames(), cobra.ShellCompDirectiveNoFileComp
}
