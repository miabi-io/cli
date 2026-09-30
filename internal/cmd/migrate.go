package cmd

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/miabi-io/cli/internal/api"
	"github.com/miabi-io/cli/internal/ui"
	"github.com/spf13/cobra"
)

func init() {
	f := appMigrateCmd.Flags()
	f.StringVar(&migLocation, "location", "", "the location to move the app to (required)")
	f.StringArrayVar(&migDBs, "db", nil, "database strategy: INSTANCE=move|new|existing:TARGET_INSTANCE (repeatable)")
	f.StringVar(&migCutover, "cutover", "auto", "auto: cut over once the data is copied; manual: wait for `miabi migrations cutover`")
	f.IntVar(&migBandwidth, "bandwidth", 0, "cap the copy in MB/s (0 = unlimited)")
	f.BoolVar(&migPlanOnly, "plan", false, "print the plan and exit without moving anything")
	f.BoolVarP(&migYes, "yes", "y", false, "skip the confirmation prompt")
	f.BoolVar(&migNoWait, "no-wait", false, "return once the move has started")
	f.DurationVar(&migTimeout, "timeout", 6*time.Hour, "max time to wait for the move")
	appMigrateCmd.ValidArgsFunction = completeApps
	appCmd.AddCommand(appMigrateCmd)

	for _, a := range []string{"cutover", "cancel", "rollback", "finalize"} {
		migrationsCmd.AddCommand(migrationActionCmd(a))
	}
	migrationsCmd.AddCommand(migrationShowCmd)
	rootCmd.AddCommand(migrationsCmd)
}

var (
	migLocation  string
	migDBs       []string
	migCutover   string
	migBandwidth int
	migPlanOnly  bool
	migYes       bool
	migNoWait    bool
	migTimeout   time.Duration
)

var appMigrateCmd = &cobra.Command{
	Use:   "migrate [app] --location <location>",
	Short: "Move an application, its volumes and databases to another location (Enterprise)",
	Long: "Plans the move, asks for confirmation, then follows it until the app serves from\n" +
		"the new location. The data is copied while the app keeps running; the app stops\n" +
		"only for the final copy and a deploy. The old copy is kept for 7 days so the move\n" +
		"can be rolled back with `miabi migrations rollback`.\n\n" +
		"A database the app uses alone moves whole by default. One shared with other apps\n" +
		"is restored into a new instance unless --db says otherwise.",
	Example: "  miabi apps migrate web --location eu-east --plan\n" +
		"  miabi apps migrate web --location eu-east --db shop-db=new\n" +
		"  miabi apps migrate web --location eu-east --db shop-db=existing:eu-pg --cutover manual",
	Args: cobra.MaximumNArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		if strings.TrimSpace(migLocation) == "" {
			return fmt.Errorf("--location is required")
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
		body := api.StartMigration{Location: migLocation, CutoverMode: migCutover, BandwidthKBps: migBandwidth * 1024}
		plan, err := c.PlanMigration(ctx, ws, appID, body)
		if err != nil {
			return err
		}
		if len(migDBs) > 0 {
			if body.Databases, err = parseDBChoices(migDBs, plan); err != nil {
				return err
			}
			if plan, err = c.PlanMigration(ctx, ws, appID, body); err != nil {
				return err
			}
		}
		if structured() && migPlanOnly {
			return emit(plan)
		}
		printPlan(appRef, plan)
		if len(plan.Blockers) > 0 {
			return fmt.Errorf("the move is blocked; resolve the blockers above")
		}
		if migPlanOnly {
			return nil
		}
		if !migYes && !structured() && !ui.Confirm(fmt.Sprintf("Move %s to %s?", ui.Bold(appRef), firstNonEmpty(plan.LocationLabel, plan.Location))) {
			ui.Info("Aborted.")
			return nil
		}
		m, err := c.StartMigration(ctx, ws, appID, body)
		if err != nil {
			return err
		}
		if migNoWait {
			if structured() {
				return emit(m)
			}
			ui.Success("Migration #%d of %s started", m.ID, ui.Bold(appRef))
			ui.Info("Follow it: miabi migrations show %d", m.ID)
			return nil
		}
		wctx, cancel := context.WithTimeout(ctx, migTimeout)
		defer cancel()
		sp := ui.NewSpinner(fmt.Sprintf("Migration #%d: %s", m.ID, m.Phase))
		sp.Start()
		final, err := c.WaitForMigration(wctx, ws, m.ID, func(cur *api.Migration) {
			sp.Update(fmt.Sprintf("Migration #%d: %s", cur.ID, phaseLine(cur)))
		})
		sp.Stop()
		if err != nil {
			return err
		}
		if structured() {
			return emit(final)
		}
		return reportMigration(final)
	},
}

// parseDBChoices reads --db INSTANCE=STRATEGY[:TARGET], naming instances as the plan does.
func parseDBChoices(specs []string, plan *api.MigrationPlan) ([]api.MigrationDBChoice, error) {
	byName := map[string]api.MigrationDBItem{}
	for _, d := range plan.Databases {
		byName[d.InstanceName] = d
	}
	var out []api.MigrationDBChoice
	for _, spec := range specs {
		name, strat, ok := strings.Cut(spec, "=")
		if !ok {
			return nil, fmt.Errorf("--db %q: want INSTANCE=move|new|existing:TARGET", spec)
		}
		d, found := byName[name]
		if !found {
			return nil, fmt.Errorf("--db %q: the app does not use an instance named %s", spec, name)
		}
		choice := api.MigrationDBChoice{InstanceID: d.InstanceID}
		kind, target, _ := strings.Cut(strat, ":")
		switch kind {
		case "move":
			choice.Strategy = "move"
		case "new":
			choice.Strategy = "new_instance"
		case "existing":
			choice.Strategy = "existing_instance"
			id, err := strconv.ParseUint(target, 10, 64)
			if err != nil || id == 0 {
				return nil, fmt.Errorf("--db %q: existing needs the target instance id, e.g. existing:42", spec)
			}
			choice.TargetInstanceID = uint(id)
		default:
			return nil, fmt.Errorf("--db %q: unknown strategy %q", spec, kind)
		}
		out = append(out, choice)
	}
	return out, nil
}

func printPlan(app string, p *api.MigrationPlan) {
	ui.Info("Moving %s to %s", ui.Bold(app), ui.Bold(firstNonEmpty(p.LocationLabel, p.Location)))
	if len(p.Volumes) > 0 {
		t := ui.NewTable("VOLUME", "ACTION", "SIZE", "CLASS")
		for _, v := range p.Volumes {
			t.Row(v.Name, v.Action, humanBytes(v.UsedBytes), v.StorageClass)
		}
		t.Print()
	}
	if len(p.Databases) > 0 {
		t := ui.NewTable("DATABASE", "ENGINE", "USE", "STRATEGY", "ALSO POSSIBLE")
		for _, d := range p.Databases {
			use := "shared"
			if d.Exclusive {
				use = "this app only"
			}
			t.Row(d.InstanceName, d.Engine+" "+d.Version, use, d.Strategy, strings.Join(d.Strategies, ", "))
		}
		t.Print()
	}
	for _, w := range p.Warnings {
		ui.Warn("%s", w.Message)
	}
	for _, b := range p.Blockers {
		ui.Fail("%s", b.Message)
	}
	if p.CopyBytes > 0 {
		ui.Info("%s to copy", humanBytes(p.CopyBytes))
	}
}

func phaseLine(m *api.Migration) string {
	line := m.Phase
	if m.Progress.Message != "" {
		line += " — " + m.Progress.Message
	}
	return line
}

// reportMigration prints where a settled migration ended, and fails the command when the move did not
// happen, which is what a script wants to know.
func reportMigration(m *api.Migration) error {
	switch m.Status {
	case "cut_over":
		ui.Success("%s now serves from %s", ui.Bold(m.AppName), firstNonEmpty(m.Plan.LocationLabel, m.Plan.Location))
		for _, n := range m.Report.Notes {
			ui.Info("%s", n)
		}
		ui.Info("Roll back: miabi migrations rollback %d · delete the old copy now: miabi migrations finalize %d", m.ID, m.ID)
		return nil
	case "awaiting_cutover":
		if s := m.Progress.EstimatedDowntimeSeconds; s > 0 {
			ui.Info("Data copied. Estimated downtime at cutover: %ds", s)
		}
		ui.Info("Cut over when ready: miabi migrations cutover %d", m.ID)
		return nil
	case "finalized":
		ui.Success("Migration #%d finalized", m.ID)
		return nil
	}
	return fmt.Errorf("migration #%d %s: %s", m.ID, strings.ReplaceAll(m.Status, "_", " "), m.Error)
}

var migrationsCmd = &cobra.Command{
	Use:     "migrations [app]",
	Aliases: []string{"migration"},
	Short:   "List location migrations, and cut over, cancel, roll back or finalize one",
	Example: "  miabi migrations\n" +
		"  miabi migrations web\n" +
		"  miabi migrations cutover 12",
	Args: cobra.MaximumNArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		ctx := context.Background()
		c, eff, err := newClient()
		if err != nil {
			return err
		}
		ws, err := workspaceRef(ctx, c, eff)
		if err != nil {
			return err
		}
		var list []api.Migration
		if len(args) == 1 {
			appID, _, rerr := resolveAppRef(ctx, c, eff, ws, args[0])
			if rerr != nil {
				return rerr
			}
			list, err = c.AppMigrations(ctx, ws, appID)
		} else {
			list, err = c.Migrations(ctx, ws)
		}
		if err != nil {
			return err
		}
		if structured() {
			return emit(list)
		}
		if len(list) == 0 {
			ui.Info("No migrations.")
			return nil
		}
		t := ui.NewTable("ID", "APP", "TO", "STATUS", "PHASE", "AGE")
		for _, m := range list {
			t.Row(itoa(int(m.ID)), m.AppName, firstNonEmpty(m.Plan.LocationLabel, m.Plan.Location), ui.Status(m.Status), m.Phase, ui.Age(m.CreatedAt))
		}
		t.Print()
		return nil
	},
}

var migrationShowCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "Show a migration's progress and report",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		ctx := context.Background()
		c, ws, id, err := migrationConn(ctx, args[0])
		if err != nil {
			return err
		}
		m, err := c.Migration(ctx, ws, id)
		if err != nil {
			return err
		}
		if structured() {
			return emit(m)
		}
		ui.Info("#%d %s → %s: %s (%s)", m.ID, ui.Bold(m.AppName), firstNonEmpty(m.Plan.LocationLabel, m.Plan.Location), ui.Status(m.Status), phaseLine(m))
		if len(m.Progress.Items) > 0 {
			t := ui.NewTable("KIND", "NAME", "STATUS", "COPIED")
			for _, it := range m.Progress.Items {
				copied := humanBytes(it.Bytes)
				if it.Total > 0 {
					copied += " / " + humanBytes(it.Total)
				}
				t.Row(it.Kind, it.Name, it.Status, copied)
			}
			t.Print()
		}
		if m.Error != "" {
			ui.Fail("%s", m.Error)
		}
		for _, n := range m.Report.Notes {
			ui.Info("%s", n)
		}
		return nil
	},
}

func migrationActionCmd(action string) *cobra.Command {
	short := map[string]string{
		"cutover":  "Stop the app, copy the last changes and finish a migration waiting for its cutover",
		"cancel":   "Cancel a migration before the app goes down",
		"rollback": "Put the app back at its old location (changes since the cutover are lost)",
		"finalize": "Delete the old copy now; the migration can no longer be rolled back",
	}[action]
	var yes bool
	cmd := &cobra.Command{
		Use:   action + " <id>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			ctx := context.Background()
			c, ws, id, err := migrationConn(ctx, args[0])
			if err != nil {
				return err
			}
			if (action == "rollback" || action == "finalize") && !yes && !structured() {
				if !ui.Confirm(fmt.Sprintf("%s migration #%d? %s.", strings.ToUpper(action[:1])+action[1:], id, short)) {
					ui.Info("Aborted.")
					return nil
				}
			}
			m, err := c.MigrationAction(ctx, ws, id, action)
			if err != nil {
				return err
			}
			if structured() {
				return emit(m)
			}
			ui.Success("Migration #%d: %s requested", m.ID, action)
			ui.Info("Follow it: miabi migrations show %d", m.ID)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return cmd
}

func migrationConn(ctx context.Context, arg string) (*api.Client, string, uint, error) {
	id, err := strconv.ParseUint(arg, 10, 64)
	if err != nil || id == 0 {
		return nil, "", 0, fmt.Errorf("invalid migration id %q", arg)
	}
	c, eff, err := newClient()
	if err != nil {
		return nil, "", 0, err
	}
	ws, err := workspaceRef(ctx, c, eff)
	if err != nil {
		return nil, "", 0, err
	}
	return c, ws, uint(id), nil
}
