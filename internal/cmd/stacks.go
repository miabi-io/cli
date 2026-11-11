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
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/miabi-io/cli/internal/api"
	"github.com/miabi-io/cli/internal/ui"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// composeFileNames is the order Docker Compose itself looks in.
var composeFileNames = []string{"compose.yaml", "compose.yml", "docker-compose.yaml", "docker-compose.yml"}

const maxImportBytes = 1 << 20

var (
	stackImportFile      string
	stackImportEnvFiles  []string
	stackImportNoEnvFile bool
	stackImportName      string
	stackImportLocation  string
	stackImportDeploy    bool
	stackImportStrict    bool
	stackWait            bool
	stackTimeout         time.Duration
	stackDeployOutdated  bool
)

func init() {
	f := stackImportCmd.Flags()
	f.StringVarP(&stackImportFile, "file", "f", "", "Compose file, or '-' for stdin (default: compose.yaml, compose.yml, docker-compose.yaml or docker-compose.yml)")
	f.StringArrayVar(&stackImportEnvFiles, "env-file", nil, "env file to send instead of the .env beside the Compose file; repeat to merge, a later key wins")
	f.BoolVar(&stackImportNoEnvFile, "no-env-file", false, "send no env file")
	f.StringVar(&stackImportName, "name", "", "stack name (default: the Compose file's name:, else the project directory's name)")
	f.StringVar(&stackImportLocation, "location", "", "location the stack runs in (default: the workspace default)")
	f.BoolVar(&stackImportDeploy, "deploy", false, "deploy every imported app after the import")
	f.BoolVar(&stackImportStrict, "strict", false, "exit non-zero when a service was skipped or a warning was raised")
	addStackWaitFlags(stackImportCmd)

	stackDeployCmd.Flags().BoolVar(&stackDeployOutdated, "outdated", false, "deploy only the apps whose configuration changed since their last deploy")
	addStackWaitFlags(stackDeployCmd)

	for _, c := range []*cobra.Command{stackGetCmd, stackDeployCmd} {
		c.ValidArgsFunction = completeStacks
	}
	stacksCmd.AddCommand(stackImportCmd, stackLsCmd, stackGetCmd, stackDeployCmd)
	addHostShim(stacksCmd)
	rootCmd.AddCommand(stacksCmd)
}

func addStackWaitFlags(c *cobra.Command) {
	c.Flags().BoolVar(&stackWait, "wait", false, "wait for every deployment to finish; exit non-zero if one fails")
	c.Flags().DurationVar(&stackTimeout, "timeout", 10*time.Minute, "how long --wait waits")
}

var stacksCmd = &cobra.Command{
	Use:     "stacks",
	Aliases: []string{"stack", "stk"},
	Short:   "Manage the workspace's stacks",
	Long: "Import a Docker Compose project as a stack, list stacks and deploy them.\n" +
		"Managing the Miabi installation on this host moved to `miabi system`.",
	Example: "  miabi stacks import\n" +
		"  miabi stacks import -f prod.compose.yml --env-file prod.env --name shop --deploy --wait\n" +
		"  miabi stacks ls\n" +
		"  miabi stacks deploy shop --outdated --wait",
}

var stackImportCmd = &cobra.Command{
	Use:   "import",
	Short: "Create a stack from a Docker Compose file",
	Long: "Creates a stack from a Docker Compose file, with one application per service.\n\n" +
		"Without -f, the current directory is searched the way Docker Compose does. The .env\n" +
		"beside the Compose file fills its ${VAR} references and is what env_file: .env loads;\n" +
		"--env-file replaces it. Unlike Docker Compose, your shell environment is never used:\n" +
		"put a variable in an env file to pass it.\n\n" +
		"Nothing is deployed unless --deploy. Services that cannot be imported are reported\n" +
		"and skipped; --strict turns that into a non-zero exit, though the stack is still created.",
	Args: cobra.NoArgs,
	RunE: func(_ *cobra.Command, _ []string) error {
		if stackWait && !stackImportDeploy {
			return errors.New("--wait needs --deploy")
		}
		if stackImportNoEnvFile && len(stackImportEnvFiles) > 0 {
			return errors.New("--no-env-file and --env-file cannot be combined")
		}
		src, err := loadComposeSource(stackImportFile, stackImportEnvFiles, stackImportNoEnvFile)
		if err != nil {
			return err
		}
		name := stackImportName
		if name == "" {
			name = defaultStackName(src.compose, src.dir)
		}
		if name == "" {
			return errors.New("cannot derive a stack name: pass --name")
		}
		ctx, c, ws, err := configContext()
		if err != nil {
			return err
		}
		res, err := c.ImportStack(ctx, ws, api.ImportStackRequest{
			Name: name, Compose: string(src.compose), Env: src.env, Location: stackImportLocation,
		})
		if ae := (*api.APIError)(nil); errors.As(err, &ae) && ae.StatusCode == http.StatusConflict {
			return fmt.Errorf("%w\n\n  Import only creates stacks; pass --name for a new one, or keep an existing one in sync with `miabi apply -f`", err)
		}
		if err != nil {
			return err
		}
		var deploys []api.StackDeployResult
		if stackImportDeploy && res.Stack != nil {
			if deploys, err = c.DeployStack(ctx, ws, res.Stack.ID, false); err != nil {
				return fmt.Errorf("stack %q was created, but its deploy failed to start: %w", res.Stack.Name, err)
			}
		}
		if structured() {
			if err := emit(importOutput{*res, deploys}); err != nil {
				return err
			}
		} else {
			printImport(res, src.composePath, src.envFrom)
		}
		if stackImportDeploy {
			if err := finishStackDeploy(ctx, c, ws, deploys); err != nil {
				return err
			}
		} else if !structured() && res.Stack != nil {
			ui.Info("Deploy it: miabi stacks deploy %s", res.Stack.Name)
		}
		return strictImportErr(res, stackImportStrict)
	},
}

func strictImportErr(res *api.StackImportResult, strict bool) error {
	if strict && (len(res.Skipped) > 0 || len(res.Warnings) > 0) {
		return errors.New("the stack was created, but the import was partial (--strict)")
	}
	return nil
}

type importOutput struct {
	api.StackImportResult `yaml:",inline"`
	Deployments           []api.StackDeployResult `json:"deployments,omitempty" yaml:"deployments,omitempty"`
}

var stackLsCmd = &cobra.Command{
	Use:     "ls",
	Aliases: []string{"list"},
	Short:   "List the workspace's stacks",
	Args:    cobra.NoArgs,
	RunE: func(_ *cobra.Command, _ []string) error {
		ctx, c, ws, err := configContext()
		if err != nil {
			return err
		}
		stacks, err := c.Stacks(ctx, ws)
		if err != nil {
			return err
		}
		if structured() {
			return emit(stacks)
		}
		if len(stacks) == 0 {
			ui.Info("No stacks in this workspace. Import one: miabi stacks import")
			return nil
		}
		t := ui.NewTable("NAME", "APPS", "RUNNING", "FAILED", "UPDATED")
		for _, st := range stacks {
			t.Row(st.Name, st.AppCount, st.Status.Running, st.Status.Failed, ui.Age(st.UpdatedAt))
		}
		t.Print()
		return nil
	},
}

var stackGetCmd = &cobra.Command{
	Use:     "get <stack>",
	Aliases: []string{"show"},
	Short:   "Show a stack and its applications",
	Args:    cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		ctx, c, ws, err := configContext()
		if err != nil {
			return err
		}
		id, err := c.ResolveStackID(ctx, ws, args[0])
		if err != nil {
			return err
		}
		st, err := c.Stack(ctx, ws, id)
		if err != nil {
			return err
		}
		if structured() {
			return emit(st)
		}
		ui.Detail("Name:        %s", ui.Bold(st.Name))
		if st.DisplayName != "" && st.DisplayName != st.Name {
			ui.Detail("Display:     %s", st.DisplayName)
		}
		if st.Description != "" {
			ui.Detail("Description: %s", st.Description)
		}
		ui.Detail("Updated:     %s ago", ui.Age(st.UpdatedAt))
		fmt.Println()
		if len(st.Apps) == 0 {
			ui.Info("The stack has no applications.")
			return nil
		}
		t := ui.NewTable("APP", "IMAGE", "STATUS")
		for _, a := range st.Apps {
			image := a.Image
			if a.Tag != "" {
				image += ":" + a.Tag
			}
			t.Row(a.Name, orDash(image), ui.Status(a.Status))
		}
		t.Print()
		return nil
	},
}

var stackDeployCmd = &cobra.Command{
	Use:   "deploy <stack>",
	Short: "Deploy every application in a stack",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		ctx, c, ws, err := configContext()
		if err != nil {
			return err
		}
		id, err := c.ResolveStackID(ctx, ws, args[0])
		if err != nil {
			return err
		}
		deploys, err := c.DeployStack(ctx, ws, id, stackDeployOutdated)
		if err != nil {
			return err
		}
		if structured() {
			if err := emit(deploys); err != nil {
				return err
			}
		}
		if len(deploys) == 0 && !structured() {
			ui.Info("Nothing to deploy.")
			return nil
		}
		return finishStackDeploy(ctx, c, ws, deploys)
	},
}

func finishStackDeploy(ctx context.Context, c *api.Client, ws string, deploys []api.StackDeployResult) error {
	failed := 0
	for _, d := range deploys {
		if d.Status == api.StatusFailed {
			failed++
			if !structured() {
				ui.Fail("%s: %s", d.AppName, orElse(d.Error, "deploy failed to start"))
			}
		} else if !structured() && !stackWait {
			ui.Success("%s: deployment queued", d.AppName)
		}
	}
	if stackWait {
		deadline := time.Now().Add(stackTimeout)
		for _, d := range deploys {
			if d.Status == api.StatusFailed || d.DeploymentID == 0 {
				continue
			}
			ref := strconv.FormatUint(uint64(d.AppID), 10)
			dep, err := c.Deployment(ctx, ws, ref, d.DeploymentID)
			if err != nil {
				return fmt.Errorf("%s: %w", d.AppName, err)
			}
			final, err := settleDeployment(ctx, c, ws, ref, dep, deadline)
			if err != nil {
				return fmt.Errorf("%s: %w", d.AppName, err)
			}
			switch {
			case api.IsFailure(final.Status):
				failed++
				if !structured() {
					ui.Fail("%s: deployment #%d failed%s", d.AppName, final.Number, suffix(": ", final.Error))
				}
			case !structured():
				ui.Success("%s: deployment #%d %s", d.AppName, final.Number, final.Status)
			}
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d deployment(s) failed", failed, len(deploys))
	}
	return nil
}

func suffix(sep, s string) string {
	if s == "" {
		return ""
	}
	return sep + s
}

func printImport(res *api.StackImportResult, composePath, envFrom string) {
	name := ""
	if res.Stack != nil {
		name = res.Stack.Name
	}
	ui.Success("Created stack %s from %s", ui.Bold(name), composeLabel(composePath))
	if envFrom != "" {
		ui.Info("Env file: %s", envFrom)
	}
	if len(res.Created)+len(res.Volumes) > 0 {
		t := ui.NewTable("CREATED", "KIND")
		for _, a := range res.Created {
			t.Row(a, "application")
		}
		for _, v := range res.Volumes {
			t.Row(v, "volume")
		}
		t.Print()
	}
	if res.PortRequests > 0 {
		ui.Info("%d host port request(s) filed; an admin approves them in the console.", res.PortRequests)
	}
	for _, pc := range res.PortConflicts {
		ui.Warn("%s: host port %d/%s is used by %s; the binding stays pending", pc.Service, pc.HostPort, orElse(pc.Protocol, "tcp"), pc.UsedBy)
	}
	for _, s := range res.Skipped {
		ui.Warn("skipped %s: %s", s.Service, s.Reason)
	}
	for _, w := range res.Warnings {
		ui.Warn("%s", w)
	}
}

func composeLabel(path string) string {
	if path == "-" {
		return "stdin"
	}
	return path
}

type composeSource struct {
	compose     []byte
	composePath string
	// dir is the project directory: where the default .env and the default name come from.
	dir     string
	env     string
	envFrom string
}

func loadComposeSource(file string, envFiles []string, noEnv bool) (*composeSource, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	src := &composeSource{composePath: file, dir: cwd}
	switch file {
	case "-":
		src.compose, err = io.ReadAll(io.LimitReader(os.Stdin, maxImportBytes+1))
		if err != nil {
			return nil, fmt.Errorf("read stdin: %w", err)
		}
	case "":
		path, warn, err := findComposeFile(cwd)
		if err != nil {
			return nil, err
		}
		if warn != "" {
			ui.Warn("%s", warn)
		}
		src.composePath = filepath.Base(path)
		if src.compose, err = os.ReadFile(path); err != nil {
			return nil, err
		}
	default:
		if src.compose, err = os.ReadFile(file); err != nil {
			return nil, err
		}
		if abs, err := filepath.Abs(file); err == nil {
			src.dir = filepath.Dir(abs)
		}
	}
	if len(src.compose) > maxImportBytes {
		return nil, fmt.Errorf("%s is larger than 1 MiB", composeLabel(src.composePath))
	}
	if len(strings.TrimSpace(string(src.compose))) == 0 {
		return nil, fmt.Errorf("%s is empty", composeLabel(src.composePath))
	}
	if noEnv {
		return src, nil
	}
	if len(envFiles) == 0 && file != "-" {
		def := filepath.Join(src.dir, ".env")
		if _, err := os.Stat(def); err == nil {
			envFiles = []string{def}
		}
	}
	src.env, err = mergeEnvFiles(envFiles)
	if err != nil {
		return nil, err
	}
	shown := make([]string, len(envFiles))
	for i, p := range envFiles {
		shown[i] = p
		if rel, err := filepath.Rel(cwd, p); err == nil && !strings.HasPrefix(rel, "..") {
			shown[i] = rel
		}
	}
	src.envFrom = strings.Join(shown, ", ")
	return src, nil
}

// findComposeFile picks the first of Compose's file names in dir, warning when another is also present.
func findComposeFile(dir string) (string, string, error) {
	var found []string
	for _, n := range composeFileNames {
		if info, err := os.Stat(filepath.Join(dir, n)); err == nil && !info.IsDir() {
			found = append(found, n)
		}
	}
	if len(found) == 0 {
		return "", "", fmt.Errorf("no Compose file in %s (looked for %s); pass -f", dir, strings.Join(composeFileNames, ", "))
	}
	warn := ""
	if len(found) > 1 {
		warn = fmt.Sprintf("found %s; using %s", strings.Join(found, " and "), found[0])
	}
	return filepath.Join(dir, found[0]), warn, nil
}

// mergeEnvFiles concatenates the files in order; the server reads a repeated key's last value, as Compose does.
func mergeEnvFiles(paths []string) (string, error) {
	var parts []string
	total := 0
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			return "", err
		}
		total += len(b)
		if total > maxImportBytes {
			return "", errors.New("the env files are larger than 1 MiB together")
		}
		parts = append(parts, strings.TrimRight(string(b), "\n"))
	}
	if len(parts) == 0 {
		return "", nil
	}
	return strings.Join(parts, "\n") + "\n", nil
}

// defaultStackName is the Compose file's top-level name:, else the project directory's name.
func defaultStackName(compose []byte, dir string) string {
	var doc struct {
		Name string `yaml:"name"`
	}
	if yaml.Unmarshal(compose, &doc) == nil {
		if n := slugName(doc.Name); n != "" {
			return n
		}
	}
	return slugName(filepath.Base(dir))
}

var slugInvalid = regexp.MustCompile(`[^a-z0-9]+`)

// slugName matches the server's slug rules, so the name the CLI shows is the one the stack gets.
func slugName(s string) string {
	return strings.Trim(slugInvalid.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), "-"), "-")
}

func completeStacks(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	ctx, c, ws, err := configContext()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	stacks, err := c.Stacks(ctx, ws)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	names := make([]string, 0, len(stacks))
	for _, st := range stacks {
		names = append(names, st.Name)
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}
