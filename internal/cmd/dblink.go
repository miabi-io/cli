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
	dbLinkCmd.Flags().StringVar(&dbLinkApp, "app", "", "application to link to (required)")
	dbLinkCmd.Flags().StringVar(&dbLinkPrefix, "prefix", "", "namespace the injected vars, e.g. ANALYTICS -> ANALYTICS_DB_URL")
	dbLinkCmd.Flags().StringArrayVar(&dbLinkEnv, "env", nil, "rename a field's var as field=NAME, or skip it with field= (fields: url, database_url, host, port, name, user, password); repeatable")
	_ = dbLinkCmd.MarkFlagRequired("app")
	dbUnlinkCmd.Flags().StringVar(&dbLinkApp, "app", "", "application to unlink from (required)")
	_ = dbUnlinkCmd.MarkFlagRequired("app")
	dbLinksCmd.Flags().StringVar(&dbLinkApp, "app", "", "application whose linked databases to list (required)")
	_ = dbLinksCmd.MarkFlagRequired("app")

	for _, c := range []*cobra.Command{dbLinkCmd, dbUnlinkCmd} {
		c.ValidArgsFunction = completeDatabases
	}
	dbCmd.AddCommand(dbLinkCmd, dbUnlinkCmd, dbLinksCmd)
}

var (
	dbLinkApp    string
	dbLinkPrefix string
	dbLinkEnv    []string
)

var dbLinkCmd = &cobra.Command{
	Use:   "link <db> [name] --app <app> [--prefix P] [--env field=NAME]...",
	Short: "Link a database to an app and inject its connection env",
	Long: "Link a database to an app. SQL, MongoDB and libSQL instances link one of their databases\n" +
		"(name required); Redis links the whole instance and can be shared by several apps.\n" +
		"Re-running on an existing link updates its env vars.",
	Example: "  miabi db link main shop --app web\n" +
		"  miabi db link main stats --app web --prefix ANALYTICS\n" +
		"  miabi db link main shop --app api --env url=SPRING_DATASOURCE_URL --env database_url=\n" +
		"  miabi db link cache --app web",
	Args: cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		envMap, err := parseEnvMap(dbLinkEnv)
		if err != nil {
			return err
		}
		ctx := context.Background()
		t, err := resolveLinkTarget(ctx, args)
		if err != nil {
			return err
		}
		req := api.LinkDatabaseRequest{EnvPrefix: dbLinkPrefix, EnvMap: envMap}
		var res *api.LinkDatabaseResult
		if t.dbID == 0 {
			res, err = t.c.LinkDatabaseInstance(ctx, t.ws, t.app, t.instID, req)
		} else {
			res, err = t.c.AttachDatabase(ctx, t.ws, t.app, t.dbID, req)
		}
		if err != nil {
			return err
		}
		if structured() {
			return emit(res)
		}
		ui.Success("Linked %s to %s", ui.Bold(t.label), ui.Bold(dbLinkApp))
		if len(res.EnvVars) > 0 {
			ui.Info("Env: %s (redeploy to apply)", strings.Join(res.EnvVars, ", "))
		}
		return nil
	},
}

var dbUnlinkCmd = &cobra.Command{
	Use:     "unlink <db> [name] --app <app>",
	Short:   "Unlink a database from an app and remove its injected env",
	Example: "  miabi db unlink main shop --app web\n  miabi db unlink cache --app web",
	Args:    cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		t, err := resolveLinkTarget(ctx, args)
		if err != nil {
			return err
		}
		if t.dbID == 0 {
			err = t.c.UnlinkDatabaseInstance(ctx, t.ws, t.app, t.instID)
		} else {
			err = t.c.DetachDatabase(ctx, t.ws, t.app, t.dbID)
		}
		if err != nil {
			return err
		}
		ui.Success("Unlinked %s from %s", ui.Bold(t.label), ui.Bold(dbLinkApp))
		return nil
	},
}

var dbLinksCmd = &cobra.Command{
	Use:   "links --app <app>",
	Short: "List the databases linked to an app and the env vars they inject",
	Args:  cobra.NoArgs,
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
		dbs, err := c.AppDatabases(ctx, ws, dbLinkApp)
		if err != nil {
			return err
		}
		if structured() {
			return emit(dbs)
		}
		t := ui.NewTable("INSTANCE", "DATABASE", "ENGINE", "ENV")
		for _, d := range dbs {
			name := d.Name
			if d.Kind == "instance" {
				name = "-"
			}
			t.Row(d.InstanceName, name, d.Engine, strings.Join(d.EnvVars, ", "))
		}
		t.Print()
		return nil
	},
}

type linkTarget struct {
	c      *api.Client
	ws     string
	app    string
	instID uint
	dbID   uint // 0 for a whole-instance link
	label  string
}

// resolveLinkTarget maps <db> [name] --app to ids. A name is required unless
// the engine links whole instances (Redis).
func resolveLinkTarget(ctx context.Context, args []string) (*linkTarget, error) {
	c, eff, err := newClient()
	if err != nil {
		return nil, err
	}
	ws, err := workspaceRef(ctx, c, eff)
	if err != nil {
		return nil, err
	}
	instID, err := c.ResolveDatabaseID(ctx, ws, args[0])
	if err != nil {
		return nil, err
	}
	inst, err := c.Database(ctx, ws, instID)
	if err != nil {
		return nil, err
	}
	t := &linkTarget{c: c, ws: ws, app: dbLinkApp, instID: instID, label: inst.Name}
	if inst.Engine == "redis" {
		if len(args) > 1 {
			return nil, fmt.Errorf("%s has no named databases; link the instance: miabi db link %s --app %s", inst.Engine, args[0], dbLinkApp)
		}
		return t, nil
	}
	if len(args) < 2 {
		return nil, fmt.Errorf("name the database on %s to link (see: miabi db databases %s)", args[0], args[0])
	}
	if t.dbID, err = resolveLogicalDB(ctx, c, ws, instID, args[1]); err != nil {
		return nil, err
	}
	t.label = inst.Name + "/" + args[1]
	return t, nil
}

// parseEnvMap turns repeated field=NAME flags into an env map; field= skips it.
func parseEnvMap(pairs []string) (map[string]string, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(pairs))
	for _, p := range pairs {
		field, name, ok := strings.Cut(p, "=")
		field = strings.TrimSpace(field)
		if !ok || field == "" {
			return nil, fmt.Errorf("invalid --env %q: want field=NAME (or field= to skip)", p)
		}
		out[field] = strings.TrimSpace(name)
	}
	return out, nil
}
