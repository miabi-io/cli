package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/miabi-io/cli/internal/api"
	"github.com/miabi-io/cli/internal/ui"
	"github.com/spf13/cobra"
)

func init() {
	// Instance lifecycle.
	dbCreateCmd.Flags().StringVar(&dbEngine, "engine", "", "engine: postgres | mysql | mariadb | redis | mongodb | libsql (required)")
	dbCreateCmd.Flags().StringVar(&dbVersion, "version", "", "engine version (default: the engine's default)")
	dbCreateCmd.Flags().IntVar(&dbSizeMB, "size-mb", 0, "data-volume capacity in MB (0 = unspecified)")
	dbCreateCmd.Flags().UintVar(&dbNode, "node", 0, "node/server id to place on (0 = local)")
	_ = dbCreateCmd.MarkFlagRequired("engine")

	dbLogsCmd.Flags().BoolVarP(&dbLogsFollow, "follow", "f", false, "stream new logs (default: print the current logs and exit)")
	dbLogsCmd.Flags().IntVar(&dbLogsTail, "tail", 200, "number of trailing lines to show first")

	dbUpgradeCmd.Flags().StringVar(&dbUpgradeTo, "to", "", "target engine version (required)")
	dbUpgradeCmd.Flags().BoolVar(&dbUpgradeStopApps, "stop-apps", false, "auto-stop apps using the database during a copy upgrade")
	_ = dbUpgradeCmd.MarkFlagRequired("to")

	dbRmCmd.Flags().BoolVarP(&dbRmYes, "yes", "y", false, "skip the confirmation prompt")

	// Logical-database subcommands.
	dbCreateLogicalCmd.Flags().StringVar(&dbLogicalApp, "app", "", "attach the new database to this app (inject connection env)")
	dbRmLogicalCmd.Flags().BoolVarP(&dbLogicalRmYes, "yes", "y", false, "skip the confirmation prompt")
	dbDatabasesCmd.AddCommand(dbCreateLogicalCmd, dbConnLogicalCmd, dbRmLogicalCmd)

	// Attach instance-ref completion to every command that takes one.
	for _, c := range []*cobra.Command{dbGetCmd, dbStartCmd, dbStopCmd, dbRestartCmd, dbLogsCmd, dbCredsCmd, dbUpgradeCmd, dbRmCmd, dbDatabasesCmd, dbCreateLogicalCmd, dbConnLogicalCmd, dbRmLogicalCmd} {
		c.ValidArgsFunction = completeDatabases
	}

	dbCmd.AddCommand(dbLsCmd, dbEnginesCmd, dbCreateCmd, dbGetCmd, dbStartCmd, dbStopCmd, dbRestartCmd, dbLogsCmd, dbCredsCmd, dbUpgradeCmd, dbRmCmd, dbDatabasesCmd)
	rootCmd.AddCommand(dbCmd)
}

var (
	dbEngine          string
	dbVersion         string
	dbSizeMB          int
	dbNode            uint
	dbLogsFollow      bool
	dbLogsTail        int
	dbUpgradeTo       string
	dbUpgradeStopApps bool
	dbRmYes           bool
	dbLogicalApp      string
	dbLogicalRmYes    bool
)

var dbCmd = &cobra.Command{
	Use:     "db",
	Aliases: []string{"database", "databases"},
	Short:   "Manage databases (PostgreSQL, MySQL, MariaDB, Redis, MongoDB, libSQL)",
	Long: "Provision and operate managed database instances, and manage the logical\n" +
		"databases hosted on them. Instances are addressed by name or numeric id.",
	Example: "  miabi db ls\n" +
		"  miabi db create shop --engine postgres --version 16\n" +
		"  miabi db logs shop --tail 100\n" +
		"  miabi db databases create shop app_prod --app web",
}

// dbConn resolves the connection context, workspace, and instance id from a
// single positional argument (name or id) — the shared preamble for db commands.
func dbConn(ctx context.Context, ref string) (*api.Client, string, uint, error) {
	c, eff, err := newClient()
	if err != nil {
		return nil, "", 0, err
	}
	ws, err := workspaceRef(ctx, c, eff)
	if err != nil {
		return nil, "", 0, err
	}
	id, err := c.ResolveDatabaseID(ctx, ws, ref)
	if err != nil {
		return nil, "", 0, err
	}
	return c, ws, id, nil
}

var dbLsCmd = &cobra.Command{
	Use:     "ls",
	Aliases: []string{"list"},
	Short:   "List database instances in the workspace",
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
		dbs, err := c.Databases(ctx, ws)
		if err != nil {
			return err
		}
		if structured() {
			return emit(dbs)
		}
		t := ui.NewTable("NAME", "ENGINE", "VERSION", "STATUS", "ADDRESS", "SIZE", "AGE")
		for _, d := range dbs {
			t.Row(d.Name, d.Engine, d.Version, ui.Status(d.Status), fmt.Sprintf("%s:%d", d.Host, d.Port), humanBytes(d.SizeBytes), ui.Age(d.CreatedAt))
		}
		t.Print()
		return nil
	},
}

var dbEnginesCmd = &cobra.Command{
	Use:   "engines",
	Short: "List available database engines and their default versions",
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
		engines, err := c.DatabaseEngines(ctx, ws)
		if err != nil {
			return err
		}
		if structured() {
			return emit(engines)
		}
		t := ui.NewTable("ENGINE", "DEFAULT VERSION", "IMAGE")
		for _, e := range engines {
			t.Row(e.Engine, e.Version, e.Image)
		}
		t.Print()
		return nil
	},
}

var dbCreateCmd = &cobra.Command{
	Use:     "create <name> --engine <engine> [--version <v>] [--size-mb N] [--node <id>]",
	Short:   "Provision a database instance",
	Example: "  miabi db create shop --engine postgres --version 16 --size-mb 2048",
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
		d, err := c.CreateDatabase(ctx, ws, api.CreateDatabaseRequest{
			Name: args[0], Engine: dbEngine, Version: dbVersion, ServerID: dbNode, SizeMB: dbSizeMB,
		})
		if err != nil {
			return err
		}
		if structured() {
			return emit(d)
		}
		ui.Success("Provisioning %s %s (%s)", ui.Bold(d.Name), ui.Dim(fmt.Sprintf("(%s %s)", d.Engine, d.Version)), ui.Status(d.Status))
		ui.Info("Watch it come up: miabi db logs %s", d.Name)
		return nil
	},
}

var dbGetCmd = &cobra.Command{
	Use:   "get <db>",
	Short: "Show a database instance's details",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		c, ws, id, err := dbConn(ctx, args[0])
		if err != nil {
			return err
		}
		d, err := c.Database(ctx, ws, id)
		if err != nil {
			return err
		}
		if structured() {
			return emit(d)
		}
		ui.Detail("%s (%s): %s", ui.Bold(d.DisplayName), d.Name, ui.Status(d.Status))
		ui.Detail("Engine:  %s %s", d.Engine, d.Version)
		ui.Detail("Address: %s:%d", d.Host, d.Port)
		if d.AdminUser != "" {
			ui.Detail("Admin:   %s", d.AdminUser)
		}
		if d.SizeBytes > 0 {
			ui.Detail("Size:    %s", humanBytes(d.SizeBytes))
		}
		node := d.ServerName
		if node == "" {
			node = "local"
		}
		ui.Detail("Node:    %s", node)
		if !d.CreatedAt.IsZero() {
			age := ui.Age(d.CreatedAt)
			if age != "just now" {
				age += " ago"
			}
			ui.Detail("Created: %s %s", d.CreatedAt.Local().Format("2006-01-02 15:04"), ui.Dim("("+age+")"))
		}
		return nil
	},
}

// lifecycleCmd builds a start/stop/restart command sharing one shape.
func lifecycleCmd(use, short, verb string, fn func(*api.Client, context.Context, string, uint) error) *cobra.Command {
	return &cobra.Command{
		Use:   use + " <db>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			c, ws, id, err := dbConn(ctx, args[0])
			if err != nil {
				return err
			}
			if err := fn(c, ctx, ws, id); err != nil {
				return err
			}
			ui.Success("%s %s", verb, ui.Bold(args[0]))
			return nil
		},
	}
}

var dbStartCmd = lifecycleCmd("start", "Start a database instance", "Started",
	func(c *api.Client, ctx context.Context, ws string, id uint) error {
		return c.StartDatabase(ctx, ws, id)
	})
var dbStopCmd = lifecycleCmd("stop", "Stop a database instance", "Stopped",
	func(c *api.Client, ctx context.Context, ws string, id uint) error { return c.StopDatabase(ctx, ws, id) })
var dbRestartCmd = lifecycleCmd("restart", "Restart a database instance", "Restarted",
	func(c *api.Client, ctx context.Context, ws string, id uint) error {
		return c.RestartDatabase(ctx, ws, id)
	})

var dbLogsCmd = &cobra.Command{
	Use:   "logs <db> [--follow] [--tail N]",
	Short: "Show a database instance's container logs",
	Args:  cobra.ExactArgs(1),
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
		id, err := c.ResolveDatabaseID(ctx, ws, args[0])
		if err != nil {
			return err
		}
		follow := dbLogsFollow
		url := fmt.Sprintf("%s/api/v1/workspaces/%s/databases/%d/logs?tail=%d&follow=%t",
			strings.TrimRight(eff.URL, "/"), ws, id, dbLogsTail, follow)
		return streamRuntimeLogs(ctx, url, eff.Token)
	},
}

var dbCredsCmd = &cobra.Command{
	Use:     "credentials <db>",
	Aliases: []string{"creds"},
	Short:   "Reveal a database instance's admin connection (admin only)",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		c, ws, id, err := dbConn(ctx, args[0])
		if err != nil {
			return err
		}
		info, err := c.DatabaseCredentials(ctx, ws, id)
		if err != nil {
			return err
		}
		if structured() {
			return emit(info)
		}
		printConnection(info)
		return nil
	},
}

var dbUpgradeCmd = &cobra.Command{
	Use:     "upgrade <db> --to <version> [--stop-apps]",
	Short:   "Upgrade a database instance's engine version",
	Example: "  miabi db upgrade shop --to 17 --stop-apps",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		c, ws, id, err := dbConn(ctx, args[0])
		if err != nil {
			return err
		}
		d, err := c.UpgradeDatabase(ctx, ws, id, api.UpgradeDatabaseRequest{Version: dbUpgradeTo, StopApps: dbUpgradeStopApps})
		if err != nil {
			return err
		}
		if structured() {
			return emit(d)
		}
		ui.Success("Upgrading %s to %s (%s)", ui.Bold(args[0]), dbUpgradeTo, ui.Status(d.Status))
		return nil
	},
}

var dbRmCmd = &cobra.Command{
	Use:     "rm <db>",
	Aliases: []string{"delete"},
	Short:   "Delete a database instance (destroys its data)",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		c, ws, id, err := dbConn(ctx, args[0])
		if err != nil {
			return err
		}
		if !dbRmYes && !structured() {
			prompt := fmt.Sprintf("Delete database %s and all its data? This cannot be undone.", ui.Bold(args[0]))

			if inst, ierr := c.Database(ctx, ws, id); ierr == nil && inst.Status == "running" {
				prompt = fmt.Sprintf("Database %s is currently running. Deleting it stops and removes its container and all its data. This cannot be undone.\nDelete it?", ui.Bold(args[0]))
			}
			if !ui.Confirm(prompt) {
				ui.Info("Aborted")
				return nil
			}
		}
		if err := c.DeleteDatabaseInstance(ctx, ws, id); err != nil {
			return err
		}
		ui.Success("Deleted %s", ui.Bold(args[0]))
		return nil
	},
}

// --- logical databases -----------------------------------------------------

var dbDatabasesCmd = &cobra.Command{
	Use:     "databases <db>",
	Aliases: []string{"dbs"},
	Short:   "List the databases hosted on an instance",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		c, ws, id, err := dbConn(ctx, args[0])
		if err != nil {
			return err
		}
		dbs, err := c.LogicalDatabases(ctx, ws, id)
		if err != nil {
			return err
		}
		if structured() {
			return emit(dbs)
		}
		t := ui.NewTable("NAME", "USER", "STATUS", "SIZE", "AGE")
		for _, d := range dbs {
			t.Row(d.Name, d.Username, ui.Status(d.Status), humanBytes(d.SizeBytes), ui.Age(d.CreatedAt))
		}
		t.Print()
		return nil
	},
}

var dbCreateLogicalCmd = &cobra.Command{
	Use:     "create <db> <name> [--app <app>]",
	Short:   "Create a database on an instance (optionally attach to an app)",
	Example: "  miabi db databases create shop app_prod --app web",
	Args:    cobra.ExactArgs(2),
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
		id, err := c.ResolveDatabaseID(ctx, ws, args[0])
		if err != nil {
			return err
		}
		var appID *uint
		if dbLogicalApp != "" {
			aid, aerr := c.ResolveAppID(ctx, ws, dbLogicalApp)
			if aerr != nil {
				return aerr
			}
			appID = &aid
		}
		res, err := c.CreateLogicalDatabase(ctx, ws, id, api.CreateLogicalDatabaseRequest{Name: args[1], ApplicationID: appID})
		if err != nil {
			return err
		}
		if structured() {
			return emit(res)
		}
		ui.Success("Created database %s (user %s)", ui.Bold(res.Database.Name), res.Database.Username)
		if res.EnvInjected {
			ui.Info("Connection env injected into %s (redeploying)", dbLogicalApp)
		}
		return nil
	},
}

var dbConnLogicalCmd = &cobra.Command{
	Use:     "connection <db> <name>",
	Aliases: []string{"conn", "dsn"},
	Short:   "Reveal a database's connection (admin only)",
	Args:    cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		c, ws, id, err := dbConn(ctx, args[0])
		if err != nil {
			return err
		}
		dbID, err := resolveLogicalDB(ctx, c, ws, id, args[1])
		if err != nil {
			return err
		}
		info, err := c.LogicalDatabaseConnection(ctx, ws, id, dbID)
		if err != nil {
			return err
		}
		if structured() {
			return emit(info)
		}
		printConnection(info)
		return nil
	},
}

var dbRmLogicalCmd = &cobra.Command{
	Use:     "rm <db> <name>",
	Aliases: []string{"delete"},
	Short:   "Delete a database from an instance (destroys its data)",
	Args:    cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		c, ws, id, err := dbConn(ctx, args[0])
		if err != nil {
			return err
		}
		dbID, err := resolveLogicalDB(ctx, c, ws, id, args[1])
		if err != nil {
			return err
		}
		if !dbLogicalRmYes && !structured() {
			if !ui.Confirm(fmt.Sprintf("Delete database %s on %s and all its data?", ui.Bold(args[1]), args[0])) {
				ui.Info("Aborted")
				return nil
			}
		}
		if err := c.DeleteLogicalDatabase(ctx, ws, id, dbID); err != nil {
			return err
		}
		ui.Success("Deleted database %s", ui.Bold(args[1]))
		return nil
	},
}

// resolveLogicalDB maps a logical-database name (or numeric id) to its id.
func resolveLogicalDB(ctx context.Context, c *api.Client, ws string, instID uint, ref string) (uint, error) {
	dbs, err := c.LogicalDatabases(ctx, ws, instID)
	if err != nil {
		return 0, err
	}
	for _, d := range dbs {
		if d.Name == ref || itoa(int(d.ID)) == ref {
			return d.ID, nil
		}
	}
	return 0, fmt.Errorf("database %q not found on this instance", ref)
}

// printConnection renders a revealed connection; the password is shown because
// the command exists to reveal it (admin-gated server-side).
func printConnection(info *api.ConnectionInfo) {
	ui.Detail("Host:     %s", info.Host)
	ui.Detail("Port:     %d", info.Port)
	ui.Detail("User:     %s", info.Username)
	ui.Detail("Password: %s", info.Password)
	if info.Database != "" {
		ui.Detail("Database: %s", info.Database)
	}
	ui.Detail("URI:      %s", info.URI)
}

// completeDatabases tab-completes database instance handles in the active workspace.
func completeDatabases(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
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
	dbs, err := c.Databases(ctx, ws)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var out []string
	for _, d := range dbs {
		if toComplete == "" || strings.HasPrefix(d.Name, toComplete) {
			out = append(out, d.Name)
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// humanBytes renders a byte count as a short human string (B/KB/MB/GB/TB).
func humanBytes(n int64) string {
	if n <= 0 {
		return "-"
	}
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%cB", float64(n)/float64(div), "KMGTPE"[exp])
}
