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
	"bufio"
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/miabi-io/cli/internal/api"
	"github.com/miabi-io/cli/internal/config"
	"github.com/miabi-io/cli/internal/ui"
	"github.com/spf13/cobra"
)

const maxMigrationTTL = 168 * time.Hour

var migrationPoll = 3 * time.Second

// importFlags are the options `workspace migrate` and `workspace import` share.
type importFlags struct {
	newName     string
	noData      bool
	deploy      bool
	freeze      bool
	rehearse    bool
	retire      bool
	srcInsecure bool
	srcCA       string
	yes         bool
	noWait      bool
	timeout     time.Duration
}

func (o *importFlags) register(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringVar(&o.newName, "new-name", "", "name of the workspace created on the target (default: the source's name)")
	f.BoolVar(&o.noData, "no-data", false, "move the configuration only, without database dumps and volume archives")
	f.BoolVar(&o.deploy, "deploy", false, "deploy the applications on the target once restored")
	f.BoolVar(&o.freeze, "freeze", false, "stop the source's apps and refuse changes there before the copy, for a consistent cut-over")
	f.BoolVar(&o.rehearse, "rehearse", false, "dry run: configuration only, no data, freeze or deploy")
	f.BoolVar(&o.retire, "retire", false, "mark the source workspace as migrated once the import succeeds")
	f.BoolVar(&o.srcInsecure, "source-insecure-skip-tls-verify", false, "let the target skip TLS verification when it reaches the source")
	f.StringVar(&o.srcCA, "source-ca", "", "PEM CA bundle the target trusts when it reaches the source")
	f.BoolVarP(&o.yes, "yes", "y", false, "skip the confirmation prompt")
	f.BoolVar(&o.noWait, "no-wait", false, "return once the migration has started")
	f.DurationVar(&o.timeout, "timeout", 6*time.Hour, "max time to wait for the migration")
}

func (o *importFlags) body(code, sourceWS string) (api.MigrationImport, error) {
	if o.rehearse && (o.freeze || o.deploy || o.retire) {
		return api.MigrationImport{}, fmt.Errorf("--rehearse moves the configuration only; drop --freeze, --deploy and --retire")
	}
	src := api.MigrationSource{Code: code, InsecureTLS: o.srcInsecure}
	if o.srcCA != "" {
		pem, err := os.ReadFile(o.srcCA)
		if err != nil {
			return api.MigrationImport{}, fmt.Errorf("read --source-ca: %w", err)
		}
		src.CABundle = string(pem)
	}
	return api.MigrationImport{
		MigrationSource: src,
		SourceWorkspace: sourceWS,
		NewWorkspace:    strings.TrimSpace(o.newName),
		RestoreData:     !o.noData && !o.rehearse,
		DeployApps:      o.deploy,
		Freeze:          o.freeze,
		Rehearse:        o.rehearse,
		RetireSource:    o.retire,
	}, nil
}

func init() {
	wsMigrationStartCmd.Flags().DurationVar(&wmExpires, "expires", 24*time.Hour, "how long the code stays valid (1h to 168h)")
	for _, c := range []*cobra.Command{wsMigrationStartCmd, wsMigrationStatusCmd, wsMigrationCancelCmd} {
		c.Flags().BoolVar(&wmInstance, "instance", false, "the install's migration, opening every workspace (platform admin)")
	}
	for _, c := range []*cobra.Command{wsMigrationStartCmd, wsMigrationCancelCmd} {
		c.Flags().BoolVarP(&wmYes, "yes", "y", false, "skip the confirmation prompt")
	}
	wsMigrationCmd.AddCommand(wsMigrationStartCmd, wsMigrationStatusCmd, wsMigrationCancelCmd)

	mf := wsMigrateCmd.Flags()
	mf.StringVar(&wsmFrom, "from", "", "context of the Miabi the workspace leaves (required)")
	mf.StringVar(&wsmTo, "to", "", "context of the Miabi the workspace moves to (required)")
	mf.DurationVar(&wsmExpires, "expires", 2*time.Hour, "lifetime of the migration started on the source; raise it for a large move")
	wsmOpts.register(wsMigrateCmd)
	for _, name := range []string{"from", "to"} {
		_ = wsMigrateCmd.MarkFlagRequired(name)
		_ = wsMigrateCmd.RegisterFlagCompletionFunc(name, completeContexts)
	}

	inf := wsImportCmd.Flags()
	inf.StringVar(&wsiCode, "code", "", "the migration code from the source (leaks into shell history; prefer --code-file)")
	inf.StringVar(&wsiCodeFile, "code-file", "", "read the migration code from a file (\"-\" for stdin)")
	wsImportCmd.MarkFlagsMutuallyExclusive("code", "code-file")
	wsImportCmd.MarkFlagsOneRequired("code", "code-file")
	wsiOpts.register(wsImportCmd)

	wsUnfreezeCmd.Flags().BoolVarP(&wsuYes, "yes", "y", false, "skip the confirmation prompt")

	workspaceCmd.AddCommand(wsMigrationCmd, wsMigrateCmd, wsImportCmd, wsUnfreezeCmd)
}

var (
	wmExpires  time.Duration
	wmInstance bool
	wmYes      bool

	wsmFrom    string
	wsmTo      string
	wsmExpires time.Duration
	wsmOpts    importFlags

	wsiCode     string
	wsiCodeFile string
	wsiOpts     importFlags

	wsuYes bool
)

var wsMigrationCmd = &cobra.Command{
	Use:   "migration",
	Short: "Open a workspace for another Miabi to import (source side)",
	Long: "Starting a migration returns a migration code, shown once, that another Miabi imports the\n" +
		"workspace with. A workspace code is started by the workspace's owner and works for one\n" +
		"completed import; an instance code (--instance, platform admin) opens every workspace until\n" +
		"cancelled. Both expire. Starting again replaces the open code.",
	Example: "  miabi workspace migration start --expires 6h\n" +
		"  miabi workspace migration status\n" +
		"  miabi workspace migration cancel",
}

// migrationScope is the workspace a migration command addresses, "" for the install's migration.
func migrationScope(ctx context.Context, c *api.Client, eff *config.Effective) (string, error) {
	if wmInstance {
		return "", nil
	}
	return workspaceRef(ctx, c, eff)
}

func scopeLabel(ws string) string {
	if ws == "" {
		return "every workspace on this Miabi"
	}
	return "workspace " + ui.Bold(ws)
}

func instanceFlag(ws string) string {
	if ws == "" {
		return " --instance"
	}
	return " -w " + ws
}

var wsMigrationStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start a migration and print its code, shown once",
	Args:  cobra.NoArgs,
	RunE: func(_ *cobra.Command, _ []string) error {
		hours, err := ttlHours(wmExpires)
		if err != nil {
			return err
		}
		ctx := context.Background()
		c, eff, err := newClient()
		if err != nil {
			return err
		}
		ws, err := migrationScope(ctx, c, eff)
		if err != nil {
			return err
		}
		if !wmYes && !structured() {
			if o, err := c.WorkspaceMigration(ctx, ws); err == nil && o.Window != nil &&
				!ui.Confirm(fmt.Sprintf("A migration of %s is already open. Replace its code? An import still using it fails.", scopeLabel(ws))) {
				ui.Info("Aborted.")
				return nil
			}
		}
		m, err := c.StartWorkspaceMigration(ctx, ws, api.MigrationStartRequest{ExpiresInHours: hours})
		if err != nil {
			return err
		}
		m.SourceURL = firstNonEmpty(m.SourceURL, eff.URL)
		if structured() {
			return emit(m)
		}
		printMigrationStarted(m, ws)
		return nil
	},
}

func printMigrationStarted(m *api.MigrationStarted, ws string) {
	ui.Success("Migration started for %s, open until %s", scopeLabel(ws), m.ExpiresAt.Local().Format("2006-01-02 15:04"))
	ui.Detail("")
	ui.Detail("  %s", m.Code)
	ui.Detail("")
	use := "It works for one completed import"
	if ws == "" {
		use = "It works for any number of imports"
	}
	ui.Warn("The code is shown once. Whoever holds it can copy %s, secrets included. %s, until it expires or you cancel it: miabi workspace migration cancel%s",
		scopeLabel(ws), use, instanceFlag(ws))
	pick := ""
	if ws == "" {
		pick = " -w <workspace>"
	}
	ui.Info("On the target Miabi, import it (paste the code when asked):")
	ui.Detail("  miabi --context <target> workspace import --code-file -%s", pick)
	ui.Info("or paste it on the target's Import from another Miabi page (/workspaces/import).")
}

var wsMigrationStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the open migration, whether the workspace is frozen, and past imports",
	Args:  cobra.NoArgs,
	RunE: func(_ *cobra.Command, _ []string) error {
		ctx := context.Background()
		c, eff, err := newClient()
		if err != nil {
			return err
		}
		ws, err := migrationScope(ctx, c, eff)
		if err != nil {
			return err
		}
		o, err := c.WorkspaceMigration(ctx, ws)
		if err != nil {
			return err
		}
		if structured() {
			return emit(o)
		}
		printMigrationOverview(o, ws)
		return nil
	},
}

func printMigrationOverview(o *api.MigrationOverview, ws string) {
	if !o.Enabled {
		ui.Warn("Workspace migration is off on this Miabi (MIABI_MIGRATION_ENABLED is not set).")
	}
	if w := o.Window; w != nil {
		ui.Info("Migration open for %s since %s, until %s (code last used: %s)",
			scopeLabel(ws), w.StartedAt.Local().Format("2006-01-02 15:04"), localTime(w.ExpiresAt), lastUsed(w.LastUsedAt))
	} else {
		ui.Info("No migration open for %s. Start one: miabi workspace migration start%s", scopeLabel(ws), instanceFlag(ws))
	}
	if o.Frozen {
		ui.Warn("%s is frozen%s. Roll back: miabi workspace unfreeze -w %s", ui.Bold(ws), reasonSuffix(o.FrozenReason), ws)
	}
	if len(o.Exports) == 0 {
		return
	}
	t := ui.NewTable("ID", "WORKSPACE", "STATUS", "PHASE", "DATA", "FREEZE", "IMPORTED", "BY", "AGE")
	for _, e := range o.Exports {
		t.Row(itoa(int(e.ID)), e.Workspace, ui.Status(e.Status), firstNonEmpty(e.Phase, "-"), yesNo(e.Data), yesNo(e.Freeze),
			yesNo(e.Completed), firstNonEmpty(e.ImportedBy, "-"), ui.Age(e.CreatedAt))
	}
	t.Print()
	for _, e := range o.Exports {
		if e.Error != "" {
			ui.Warn("#%d: %s", e.ID, e.Error)
		}
	}
}

func count(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func reasonSuffix(r string) string {
	if r == "" {
		return ""
	}
	return " (" + r + ")"
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

var wsMigrationCancelCmd = &cobra.Command{
	Use:   "cancel",
	Short: "Cancel the open migration: its code stops working",
	Args:  cobra.NoArgs,
	RunE: func(_ *cobra.Command, _ []string) error {
		ctx := context.Background()
		c, eff, err := newClient()
		if err != nil {
			return err
		}
		ws, err := migrationScope(ctx, c, eff)
		if err != nil {
			return err
		}
		if !wmYes && !structured() && !ui.Confirm(fmt.Sprintf("Cancel the migration of %s? Its code stops working, and an import still pulling with it fails.", scopeLabel(ws))) {
			ui.Info("Aborted.")
			return nil
		}
		if err := c.CancelWorkspaceMigration(ctx, ws); err != nil {
			return err
		}
		ui.Success("Migration of %s cancelled; its code no longer works", scopeLabel(ws))
		return nil
	},
}

var wsMigrateCmd = &cobra.Command{
	Use:   "migrate --from <context> --to <context>",
	Short: "Move a workspace from one Miabi to another, using two saved contexts",
	Long: "Starts a migration on --from (you must own the workspace there), has --to import the\n" +
		"workspace into a new workspace with its code, follows the move and prints the report. The\n" +
		"source workspace is --workspace, else the one bound in the --from context. Starting replaces\n" +
		"any migration code already open for the workspace.\n\n" +
		"The code closes itself when the import completes; after a failed, partial or rehearsal run\n" +
		"the CLI cancels it. --freeze stops the source's apps for a consistent copy; a failed move\n" +
		"unfreezes it, and `miabi --context <from> workspace unfreeze` rolls a successful one back.\n" +
		"--retire marks the source as moved once the import succeeds.\n\n" +
		"--source-insecure-skip-tls-verify and --source-ca are what the target uses to reach the\n" +
		"source; the CLI itself trusts each server as its context says.",
	Example: "  miabi workspace migrate --from old --to new --workspace acme --rehearse\n" +
		"  miabi workspace migrate --from old --to new --workspace acme --freeze --deploy\n" +
		"  miabi workspace migrate --from old --to new -w acme --new-name acme-eu --retire --yes",
	Args: cobra.NoArgs,
	RunE: func(_ *cobra.Command, _ []string) error {
		return runWorkspaceMigrate(context.Background())
	},
}

func runWorkspaceMigrate(ctx context.Context) error {
	hours, err := ttlHours(wsmExpires)
	if err != nil {
		return err
	}
	f, err := config.Load()
	if err != nil {
		return err
	}
	src, srcEff, err := contextClient(f, wsmFrom)
	if err != nil {
		return fmt.Errorf("--from: %w", err)
	}
	dst, dstEff, err := contextClient(f, wsmTo)
	if err != nil {
		return fmt.Errorf("--to: %w", err)
	}
	if err := requireMigration(ctx, src, wsmFrom); err != nil {
		return err
	}
	if err := requireMigration(ctx, dst, wsmTo); err != nil {
		return err
	}
	ws, err := workspaceRef(ctx, src, srcEff)
	if err != nil {
		return fmt.Errorf("--from %s: %w", wsmFrom, err)
	}
	body, err := wsmOpts.body("", ws)
	if err != nil {
		return err
	}
	if !wsmOpts.yes && !structured() && !ui.Confirm(migratePrompt(ws, srcEff.URL, dstEff.URL, &wsmOpts)) {
		ui.Info("Aborted.")
		return nil
	}

	m, err := src.StartWorkspaceMigration(ctx, ws, api.MigrationStartRequest{ExpiresInHours: hours})
	if err != nil {
		return fmt.Errorf("start the migration on %s: %w", wsmFrom, err)
	}
	cancel := func() {
		if err := src.CancelWorkspaceMigration(context.Background(), ws); err != nil {
			ui.Warn("Could not cancel the migration on %s (%s); its code expires %s, or run: miabi --context %s workspace migration cancel -w %s",
				wsmFrom, err, m.ExpiresAt.Local().Format("2006-01-02 15:04"), wsmFrom, ws)
			return
		}
		if !structured() {
			ui.Info("Cancelled the migration on %s: its code no longer works.", wsmFrom)
		}
	}
	body.Code = m.Code
	p, err := dst.ProbeMigration(ctx, body.MigrationSource)
	if err != nil {
		cancel()
		return fmt.Errorf("%s could not reach %s with the migration code: %w", wsmTo, wsmFrom, err)
	}
	w, err := pickSourceWorkspace(p, ws)
	if err != nil {
		cancel()
		return err
	}
	if !structured() {
		printMigrationPreview(p, w, &wsmOpts)
	}
	run, err := dst.ImportWorkspace(ctx, body)
	if err != nil {
		cancel()
		return fmt.Errorf("start the import on %s: %w", wsmTo, err)
	}
	final, err := followMigrationRun(ctx, dst, run, &wsmOpts, wsmTo)
	if final == nil || !api.MigrationRunDone(final.Status) {
		// The target pulls with the code until the run ends, so cancelling now would break the move.
		ui.Warn("The migration on %s stays open until %s while the target pulls. A completed import closes it; otherwise cancel it: miabi --context %s workspace migration cancel -w %s",
			wsmFrom, m.ExpiresAt.Local().Format("2006-01-02 15:04"), wsmFrom, ws)
		return err
	}
	err = reportMigrationRun(final, firstNonEmpty(body.NewWorkspace, final.SourceWorkspace, ws), wsmFrom)
	if final.Status != "completed" || body.Rehearse {
		cancel()
	}
	return err
}

func migratePrompt(ws, from, to string, o *importFlags) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Migrate workspace %s", ui.Bold(ws))
	if from != "" {
		fmt.Fprintf(&b, " from %s", from)
	}
	fmt.Fprintf(&b, " to %s", to)
	if o.newName != "" {
		fmt.Fprintf(&b, " as %s", ui.Bold(o.newName))
	}
	switch {
	case o.rehearse:
		b.WriteString(" (rehearsal: configuration only)")
	case o.noData:
		b.WriteString(" (configuration only)")
	}
	if o.freeze {
		b.WriteString(". The source's apps stop for the copy")
	}
	if o.retire {
		b.WriteString(". The source is marked as moved once it succeeds")
	}
	return b.String() + "?"
}

// pickSourceWorkspace is the workspace to import: want, else the one the code was started for, else
// the only one an instance code opens.
func pickSourceWorkspace(p *api.MigrationProbe, want string) (*api.MigrationWorkspace, error) {
	names := make([]string, 0, len(p.Workspaces))
	for _, w := range p.Workspaces {
		names = append(names, w.Name)
	}
	name := firstNonEmpty(want, p.CodeWorkspace)
	if name == "" {
		switch len(p.Workspaces) {
		case 0:
			return nil, fmt.Errorf("the migration code opens no workspace")
		case 1:
			return &p.Workspaces[0], nil
		}
		return nil, fmt.Errorf("the migration code opens %d workspaces (%s); pick one with --workspace", len(names), strings.Join(names, ", "))
	}
	for i := range p.Workspaces {
		if p.Workspaces[i].Name == name {
			return &p.Workspaces[i], nil
		}
	}
	return nil, fmt.Errorf("the migration code does not open workspace %q (it opens: %s)", name, firstNonEmpty(strings.Join(names, ", "), "none"))
}

func printMigrationPreview(p *api.MigrationProbe, w *api.MigrationWorkspace, o *importFlags) {
	name := ui.Bold(w.Name)
	if w.DisplayName != "" && w.DisplayName != w.Name {
		name += " (" + w.DisplayName + ")"
	}
	ui.Info("Source Miabi %s: workspace %s", firstNonEmpty(p.Version, "(unknown version)"), name)
	size := humanBytes(w.SizeBytes) + " of data"
	if o.noData || o.rehearse {
		size = "configuration only"
	}
	ui.Detail("  %s, %s, %s, %s", count(w.Apps, "app"), count(w.Databases, "database"), count(w.Volumes, "volume"), size)
	if w.Frozen {
		ui.Warn("Workspace %s is frozen on the source.", w.Name)
	}
}

var wsImportCmd = &cobra.Command{
	Use:   "import (--code <code> | --code-file <path|->)",
	Short: "Import a workspace from another Miabi with a migration code",
	Long: "Creates a new workspace on the current server and pulls the source workspace into it, then\n" +
		"follows the move and prints the report. The code comes from `miabi workspace migration start`\n" +
		"on the source (or its Migration page); --code-file - asks for it, keeping it out of your shell\n" +
		"history. The source workspace is the code's; with an instance code, --workspace picks one.\n" +
		"The command shows what the code opens before it asks to go ahead.",
	Example: "  miabi workspace import --code-file -\n" +
		"  miabi workspace import --code-file code.txt --freeze --deploy --retire\n" +
		"  miabi workspace import --code-file code.txt -w acme --new-name acme-eu --rehearse",
	Args: cobra.NoArgs,
	RunE: func(_ *cobra.Command, _ []string) error {
		return runWorkspaceImport(context.Background())
	},
}

func runWorkspaceImport(ctx context.Context) error {
	piped := wsiCodeFile == "-" && !stdinIsTerminal()
	if piped && !wsiOpts.yes && !structured() {
		return fmt.Errorf("the code comes on stdin, so the confirmation prompt cannot be answered; pass --yes")
	}
	code, err := readMigrationCode()
	if err != nil {
		return err
	}
	body, err := wsiOpts.body(code, "")
	if err != nil {
		return err
	}
	c, eff, err := newClient()
	if err != nil {
		return err
	}
	if err := requireMigration(ctx, c, eff.Context); err != nil {
		return err
	}
	p, err := c.ProbeMigration(ctx, body.MigrationSource)
	if err != nil {
		return err
	}
	w, err := pickSourceWorkspace(p, flagWorkspace)
	if err != nil {
		return err
	}
	body.SourceWorkspace = w.Name
	if !structured() {
		printMigrationPreview(p, w, &wsiOpts)
	}
	if !wsiOpts.yes && !structured() && !ui.Confirm(migratePrompt(w.Name, "", eff.URL, &wsiOpts)) {
		ui.Info("Aborted.")
		return nil
	}
	run, err := c.ImportWorkspace(ctx, body)
	if err != nil {
		return err
	}
	final, err := followMigrationRun(ctx, c, run, &wsiOpts, "")
	if err != nil || final == nil || !api.MigrationRunDone(final.Status) {
		return err
	}
	return reportMigrationRun(final, firstNonEmpty(body.NewWorkspace, final.SourceWorkspace, w.Name), "")
}

func readMigrationCode() (string, error) {
	code := wsiCode
	switch {
	case wsiCodeFile == "-" && stdinIsTerminal():
		fmt.Fprint(os.Stderr, "Migration code: ")
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && line == "" {
			return "", fmt.Errorf("read the migration code: %w", err)
		}
		code = line
	case wsiCodeFile != "":
		v, err := readFileOrStdin(wsiCodeFile)
		if err != nil {
			return "", fmt.Errorf("read --code-file: %w", err)
		}
		code = v
	}
	code = strings.TrimSpace(code)
	if code == "" {
		return "", fmt.Errorf("the migration code is empty")
	}
	return code, nil
}

func stdinIsTerminal() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// followMigrationRun waits for run to end, printing each phase. It returns the run as last seen; with
// --no-wait that is the run as started.
func followMigrationRun(ctx context.Context, c *api.Client, run *api.MigrationRun, o *importFlags, target string) (*api.MigrationRun, error) {
	on := ""
	if target != "" {
		on = " on " + target
	}
	if o.noWait {
		if structured() {
			return run, emit(run)
		}
		ui.Success("Migration #%d started%s", run.ID, on)
		return run, nil
	}
	if !structured() {
		ui.Info("Migration #%d started%s", run.ID, on)
	}
	wctx, cancel := context.WithTimeout(ctx, o.timeout)
	defer cancel()
	return c.WaitForMigrationRun(wctx, run.ID, migrationPoll, func(r *api.MigrationRun) {
		if !structured() {
			ui.Detail("  %s %s", ui.Status(r.Status), r.Phase)
		}
	})
}

// reportMigrationRun prints how a migration ended and fails the command when it failed, which is what a
// script wants to know. A partial run created the workspace, so it warns rather than fails.
func reportMigrationRun(r *api.MigrationRun, workspace, sourceCtx string) error {
	if structured() {
		if err := emit(r); err != nil {
			return err
		}
	} else {
		printMigrationReport(r)
	}
	switch r.Status {
	case "completed":
		if !structured() {
			ui.Success("Workspace %s migrated (migration #%d)", ui.Bold(workspace), r.ID)
			if r.Migration != nil && r.Migration.Freeze && !r.Migration.SourceRetired {
				ctxFlag := ""
				if sourceCtx != "" {
					ctxFlag = "--context " + sourceCtx + " "
				}
				ui.Info("The source stays frozen. Roll back: miabi %sworkspace unfreeze -w %s", ctxFlag, firstNonEmpty(r.SourceWorkspace, workspace))
			}
		}
		return nil
	case "partial":
		ui.Warn("Migration #%d finished with failures: workspace %s exists, but the items marked failed above did not come across", r.ID, ui.Bold(workspace))
		return nil
	}
	if r.Migration != nil && r.Migration.SourceUnfrozen {
		ui.Info("The source workspace was unfrozen.")
	}
	return fmt.Errorf("migration #%d %s: %s", r.ID, r.Status, firstNonEmpty(r.Error, "see the report above"))
}

func printMigrationReport(r *api.MigrationRun) {
	if len(r.Report.Items) > 0 {
		t := ui.NewTable("KIND", "NAME", "ACTION", "DETAIL")
		for _, it := range r.Report.Items {
			t.Row(it.Kind, it.Name, reportAction(it.Action), it.Detail)
		}
		t.Print()
	}
	for _, n := range r.Report.Notes {
		ui.Info("%s", n)
	}
	if r.Error != "" && r.Status != "failed" {
		ui.Warn("%s", r.Error)
	}
}

var wsUnfreezeCmd = &cobra.Command{
	Use:   "unfreeze",
	Short: "Unfreeze a workspace a migration froze: its apps start again and it accepts changes",
	Long: "Rolls a migration back on the source. Run it against the Miabi the workspace left, e.g.\n" +
		"`miabi --context old workspace unfreeze -w acme`.",
	Args: cobra.NoArgs,
	RunE: func(_ *cobra.Command, _ []string) error {
		ctx := context.Background()
		c, eff, err := newClient()
		if err != nil {
			return err
		}
		ws, err := workspaceRef(ctx, c, eff)
		if err != nil {
			return err
		}
		if !wsuYes && !structured() && !ui.Confirm(fmt.Sprintf("Unfreeze %s? Its apps start again, and changes made here diverge from the migrated copy.", ui.Bold(ws))) {
			ui.Info("Aborted.")
			return nil
		}
		if err := c.UnfreezeWorkspace(ctx, ws); err != nil {
			return err
		}
		ui.Success("Workspace %s unfrozen", ui.Bold(ws))
		return nil
	},
}

// contextClient connects to a saved context by name with its own server, token and TLS trust. The
// global connection flags and MIABI_* env describe a single server, so they do not apply here.
func contextClient(f *config.File, name string) (*api.Client, *config.Effective, error) {
	c := f.Contexts[name]
	if c == nil {
		return nil, nil, fmt.Errorf("context %q not found — see `miabi context list`", name)
	}
	if c.Server.URL == "" || c.Token == "" {
		return nil, nil, fmt.Errorf("context %q has no server or token — run `miabi login --context %s`", name, name)
	}
	eff := &config.Effective{
		Context: name, URL: c.Server.URL, Token: c.Token, CA: c.Server.CA, InsecureSkip: c.Server.InsecureSkip,
		Workspace: c.Workspace, App: c.App,
	}
	if eff.InsecureSkip && !structured() {
		ui.Warn("TLS verification disabled for %s — this connection can be intercepted.", eff.URL)
	}
	cl, err := api.New(api.Options{BaseURL: eff.URL, Token: eff.Token, CAFile: eff.CA, InsecureSkip: eff.InsecureSkip, Verbose: flagVerbose})
	if err != nil {
		return nil, nil, err
	}
	return cl, eff, nil
}

// requireMigration fails early, before a migration starts or a workspace is created, when a server
// does not have workspace migration on.
func requireMigration(ctx context.Context, c *api.Client, name string) error {
	s, err := c.MigrationStatus(ctx)
	if err != nil {
		var ae *api.APIError
		if errors.As(err, &ae) && ae.StatusCode == http.StatusNotFound {
			return fmt.Errorf("context %s: this Miabi predates workspace migration; upgrade it", name)
		}
		return fmt.Errorf("context %s: %w", name, err)
	}
	if !s.Enabled {
		return fmt.Errorf("context %s: workspace migration is off on this Miabi (set MIABI_MIGRATION_ENABLED=true)", name)
	}
	return nil
}

// ttlHours turns --expires into the whole hours the API takes, rounding up.
func ttlHours(d time.Duration) (int, error) {
	if d < time.Hour || d > maxMigrationTTL {
		return 0, fmt.Errorf("--expires must be between 1h and 168h (7 days), got %s", d)
	}
	return int(math.Ceil(d.Hours())), nil
}

func reportAction(a string) string {
	switch a {
	case "failed":
		return ui.Red(a)
	case "skipped":
		return ui.Yellow(a)
	}
	return a
}

func localTime(t *time.Time) string {
	if t == nil || t.IsZero() {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04")
}

func lastUsed(t *time.Time) string {
	if t == nil || t.IsZero() {
		return "never"
	}
	return localTime(t)
}
