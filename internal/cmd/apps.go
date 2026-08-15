package cmd

import (
	"context"
	"fmt"

	"github.com/miabi-io/cli/internal/api"
	"github.com/miabi-io/cli/internal/config"
	"github.com/miabi-io/cli/internal/ui"
	"github.com/spf13/cobra"
)

var (
	appCreateImage       string
	appCreateTag         string
	appCreateGitRepo     string
	appCreateGitRef      string
	appCreateBuildMethod string
	appCreatePort        int
	appCreateNode        uint
	appCreateUse         bool
	appRmYes             bool
)

func init() {
	f := appCreateCmd.Flags()
	f.StringVar(&appCreateImage, "image", "", "container image (image source, e.g. miabi/guestbook)")
	f.StringVar(&appCreateTag, "tag", "", "image tag (image source; default: latest)")
	f.StringVar(&appCreateGitRepo, "git-repo", "", "git clone URL (git source)")
	f.StringVar(&appCreateGitRef, "git-ref", "", "git branch/ref (git source)")
	f.StringVar(&appCreateBuildMethod, "build-method", "", "git build method: auto | dockerfile | buildpack")
	f.IntVar(&appCreatePort, "port", 0, "primary container port")
	f.UintVar(&appCreateNode, "node", 0, "node/server id to place on (0 = local)")
	f.BoolVar(&appCreateUse, "use", false, "bind the new app as the current app (like `miabi use`)")

	appRmCmd.Flags().BoolVarP(&appRmYes, "yes", "y", false, "skip the confirmation prompt")

	appCmd.AddCommand(appLsCmd, appCreateCmd, appRmCmd, appStartCmd, appStopCmd, appRestartCmd)
	rootCmd.AddCommand(appCmd)
}

var appCmd = &cobra.Command{
	Use:     "app",
	Aliases: []string{"apps", "application", "applications"},
	Short:   "Manage applications",
	Long: "Everything application-scoped: list, create, deploy, logs, status, releases,\n" +
		"rollback, env, and lifecycle (start/stop/restart). Each verb takes the app as\n" +
		"its first argument, or uses the one bound by `miabi use`.",
	Example: "  miabi apps ls\n" +
		"  miabi apps create web --image miabi/guestbook\n" +
		"  miabi apps deploy web --tag $GIT_SHA --wait\n" +
		"  miabi apps logs web\n" +
		"  miabi apps restart web\n" +
		"  miabi apps rm web",
}

var appCreateCmd = &cobra.Command{
	Use:   "create <name> (--image <img> | --git-repo <url>) [flags]",
	Short: "Create an application (from an image or a git repo)",
	Long: "Creates an app in the workspace. Provide --image for an image-source app or\n" +
		"--git-repo for a git-source app. The app is created but not deployed — run\n" +
		"`miabi deploy <app>` (or pass --use to bind it first).",
	Example: "  miabi apps create web --image miabi/guestbook --tag 1.0 --port 3000\n" +
		"  miabi apps create api --git-repo https://github.com/acme/api --git-ref main --use",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// Infer the source type from the flags; require exactly one source.
		source := ""
		switch {
		case appCreateGitRepo != "":
			source = "git"
		case appCreateImage != "":
			source = "image"
		default:
			return fmt.Errorf("specify a source: --image <img> or --git-repo <url>")
		}
		if appCreateImage != "" && appCreateGitRepo != "" {
			return fmt.Errorf("--image and --git-repo are mutually exclusive")
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
		app, err := c.CreateApp(ctx, ws, api.CreateAppRequest{
			DisplayName: args[0],
			Name:        args[0],
			SourceType:  source,
			Image:       appCreateImage,
			Tag:         appCreateTag,
			GitRepo:     appCreateGitRepo,
			GitRef:      appCreateGitRef,
			BuildMethod: appCreateBuildMethod,
			Port:        appCreatePort,
			ServerID:    appCreateNode,
		})
		if err != nil {
			return err
		}

		if appCreateUse {
			if f, ferr := config.Load(); ferr == nil {
				f.EnsureCurrent().App = &config.AppRef{ID: app.ID, Name: app.Name, DisplayName: app.DisplayName}
				_ = config.Save(f)
			}
		}

		if structured() {
			return emit(app)
		}
		ui.Success("Created %s %s", ui.Bold(app.Name), ui.Dim(fmt.Sprintf("(%s)", source)))
		if appCreateUse {
			ui.Info("Now using %s", app.Name)
		}
		ui.Info("Deploy it: miabi apps deploy %s", app.Name)
		return nil
	},
}

var appRmCmd = &cobra.Command{
	Use:     "rm [app]",
	Aliases: []string{"delete", "destroy"},
	Short:   "Delete an application (removes its container and all releases)",
	Long: "Deletes the app from the workspace, stopping and removing its container and\n" +
		"deployment history. This cannot be undone. Uses the app bound by `miabi use`\n" +
		"when no argument is given; clears that binding if it was the deleted app.",
	Args:              cobra.MaximumNArgs(1),
	ValidArgsFunction: completeApps,
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
		if !appRmYes && !structured() {
			prompt := fmt.Sprintf("Delete application %s and all its releases? This cannot be undone.", ui.Bold(appRef))

			if app, aerr := c.App(ctx, ws, appID); aerr == nil && app.Status == "running" {
				prompt = fmt.Sprintf("Application %s is currently running. Deleting it stops and removes its container and all releases. This cannot be undone.\nDelete it?", ui.Bold(appRef))
			}
			if !ui.Confirm(prompt) {
				ui.Info("Aborted")
				return nil
			}
		}
		if err := c.DeleteApp(ctx, ws, appID); err != nil {
			return err
		}
		// Clear the bound app if we just deleted it, so later commands don't
		// resolve a dangling reference.
		if f, ferr := config.Load(); ferr == nil {
			if cur := f.CurrentContext(); cur != nil && cur.App != nil && cur.App.ID == appID {
				cur.App = nil
				_ = config.Save(f)
			}
		}
		if structured() {
			return emit(map[string]any{"deleted": true, "app": appRef})
		}
		ui.Success("Deleted %s", ui.Bold(appRef))
		return nil
	},
}

var appLsCmd = &cobra.Command{
	Use:     "ls",
	Aliases: []string{"list"},
	Short:   "List applications in the workspace",
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
		apps, err := c.Apps(ctx, ws)
		if err != nil {
			return err
		}
		if structured() {
			return emit(apps)
		}
		bound := ""
		if eff.App != nil {
			bound = eff.App.Name
		}
		t := ui.NewTable("", "NAME", "IMAGE", "TAG", "STATUS", "AGE")
		for _, a := range apps {
			marker := " "
			if a.Name == bound {
				marker = ui.Cyan("→")
			}
			t.Row(marker, a.Name, a.Image, a.Tag, ui.Status(a.Status), ui.Age(a.CreatedAt))
		}
		t.Print()
		return nil
	},
}
