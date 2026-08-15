package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/miabi-io/cli/internal/api"
	"github.com/miabi-io/cli/internal/ui"
	"github.com/spf13/cobra"
)

var (
	srcImage       string
	srcTag         string
	srcRegistryID  uint
	srcGitRepo     string
	srcGitRef      string
	srcGitRepoID   uint
	srcBuildMethod string
	srcBuilder     string
	srcYes         bool
)

func init() {
	f := appSetSourceCmd.Flags()
	f.StringVar(&srcImage, "image", "", "container image — selects the image source (e.g. nginx)")
	f.StringVar(&srcTag, "tag", "", "image tag (image source; default: latest)")
	f.UintVar(&srcRegistryID, "registry-id", 0, "registry credential for a private image (0 = public)")
	f.StringVar(&srcGitRepo, "git-repo", "", "git clone URL — selects the git source")
	f.StringVar(&srcGitRef, "git-ref", "", "git branch/ref (git source)")
	f.UintVar(&srcGitRepoID, "git-repository-id", 0, "saved repository supplying the URL and credentials")
	f.StringVar(&srcBuildMethod, "build-method", "", "git build method: auto | dockerfile | buildpack")
	f.StringVar(&srcBuilder, "builder", "", "buildpack builder image (git source, advanced)")
	f.BoolVarP(&srcYes, "yes", "y", false, "skip the confirmation prompt when switching source type")

	appSetSourceCmd.ValidArgsFunction = completeApps
	appResyncPipelineCmd.ValidArgsFunction = completeApps
	appCmd.AddCommand(appSetSourceCmd, appResyncPipelineCmd)
}

var appSetSourceCmd = &cobra.Command{
	Use:   "set-source [app] (--image <img> | --git-repo <url> | --git-repository-id <id>)",
	Short: "Change where an application's image comes from",
	Long: "Switches an app between a prebuilt Docker image and a Git build, or edits the details of\n" +
		"its current source. Everything else is kept: domains, environment, secrets, volumes,\n" +
		"databases, routes and deployment history all survive.\n\n" +
		"This is a whole-source replacement, so the fields of the source being left are cleared. A\n" +
		"pipeline adopted from the old repository is removed with it, and the app is marked for\n" +
		"redeploy — the running container was built from the old source.",
	Example: "  miabi apps set-source web --image nginx --tag 1.27\n" +
		"  miabi apps set-source web --git-repo https://github.com/acme/web --git-ref main\n" +
		"  miabi apps set-source web --git-repository-id 3 --build-method buildpack",
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		req, err := buildSourceRequest()
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
		appID, appRef, err := resolveAppRef(ctx, c, eff, ws, appArg(args))
		if err != nil {
			return err
		}

		// Confirm only a genuine switch: editing a tag or a branch is routine, changing what the
		// app is built from is not, and it discards the other source's configuration.
		if !srcYes && !structured() {
			if app, aerr := c.App(ctx, ws, appID); aerr == nil && app.SourceType != "" && app.SourceType != req.SourceType {
				to := "a Docker image"
				lost := "its repository, branch and build settings"
				if req.SourceType == "git" {
					to, lost = "a Git repository", "its image, tag and registry credential"
				}
				if !ui.Confirm(fmt.Sprintf(
					"Switch %s to %s? This clears %s, removes any pipeline adopted from its repository, and requires a redeploy.",
					ui.Bold(appRef), to, lost)) {
					ui.Info("Aborted")
					return nil
				}
			}
		}

		res, err := c.SetAppSource(ctx, ws, appID, req)
		if err != nil {
			return err
		}
		if structured() {
			return emit(res)
		}
		if res.Change.Switched {
			ui.Success("%s now builds from %s", ui.Bold(appRef), sourceLabel(res.Change.To))
		} else {
			ui.Success("Updated the %s source for %s", sourceLabel(res.Change.To), ui.Bold(appRef))
		}
		if res.Change.PipelineRemoved {
			ui.Warn("Its repository pipeline was removed — it was bound to the previous repository.")
		}
		if res.Change.RedeployRequired {
			ui.Info("Redeploy to run from the new source: miabi apps deploy %s", appRef)
		}
		return nil
	},
}

func buildSourceRequest() (api.SetAppSourceRequest, error) {
	image := strings.TrimSpace(srcImage)
	repo := strings.TrimSpace(srcGitRepo)
	wantImage := image != ""
	wantGit := repo != "" || srcGitRepoID > 0

	switch {
	case wantImage && wantGit:
		return api.SetAppSourceRequest{}, errors.New(
			"pass either --image or --git-repo/--git-repository-id, not both — they select different sources")
	case wantImage:
		req := api.SetAppSourceRequest{SourceType: "image", Image: image, Tag: strings.TrimSpace(srcTag)}
		if srcRegistryID > 0 {
			req.RegistryID = &srcRegistryID
		}
		return req, nil
	case wantGit:
		req := api.SetAppSourceRequest{
			SourceType:  "git",
			GitRepo:     repo,
			GitRef:      strings.TrimSpace(srcGitRef),
			BuildMethod: strings.TrimSpace(srcBuildMethod),
			Builder:     strings.TrimSpace(srcBuilder),
		}
		if srcGitRepoID > 0 {
			req.GitRepositoryID = &srcGitRepoID
		}
		return req, nil
	default:
		return api.SetAppSourceRequest{}, errors.New(
			"nothing to set: pass --image for an image source, or --git-repo/--git-repository-id for a git source")
	}
}

func sourceLabel(t string) string {
	if t == "git" {
		return "Git repository"
	}
	return "Docker image"
}

var appResyncPipelineCmd = &cobra.Command{
	Use:     "resync-pipeline [app]",
	Aliases: []string{"resync"},
	Short:   "Reload the repository's pipelines.yaml for a git application",
	Long: "Adopts the repository's pipeline when the app has none — the file was added after the app\n" +
		"was created, or the app only just became a git app — and re-syncs the stored spec when it\n" +
		"already has one. A repository that carries no pipeline is not an error: the app simply\n" +
		"keeps building directly.",
	Example: "  miabi apps resync-pipeline web",
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
		res, err := c.ResyncAppPipeline(ctx, ws, appID)
		if err != nil {
			return err
		}
		if structured() {
			return emit(res)
		}
		path := ""
		if res.Pipeline != nil {
			path = res.Pipeline.SourcePath
		}
		switch {
		case res.Adopted:
			ui.Success("Adopted the pipeline from %s — deploys for %s now run through it", path, ui.Bold(appRef))
		case res.Changed:
			ui.Success("Pipeline updated from %s", path)
		default:
			ui.Info("Already up to date with %s", path)
		}
		return nil
	},
}
