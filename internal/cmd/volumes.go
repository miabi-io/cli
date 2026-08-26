package cmd

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/miabi-io/cli/internal/api"
	"github.com/miabi-io/cli/internal/ui"
	"github.com/spf13/cobra"
)

func init() {
	volCreateCmd.Flags().IntVar(&volSizeMB, "size-mb", 0, "declared capacity in MB (0 = unbounded)")
	volCreateCmd.Flags().UintVar(&volNode, "node", 0, "node/server id to place the volume on (0 = local)")
	volCreateCmd.Flags().StringVar(&volDriver, "driver", "", "storage driver: local (default) | nfs | cifs | host")
	volCreateCmd.Flags().StringArrayVar(&volDriverOpt, "driver-opt", nil, "driver option as key=value (repeatable)")
	volCreateCmd.Flags().StringArrayVar(&volDriverOptFile, "driver-opt-file", nil, "driver option as key=path, value read from the file (\"-\" reads stdin) — keeps a share password out of your shell history")

	volRmCmd.Flags().BoolVarP(&volRmYes, "yes", "y", false, "skip the confirmation prompt")

	volAttachCmd.Flags().StringVar(&volApp, "app", "", "application to mount it into (default: the app bound with 'miabi use')")
	volAttachCmd.Flags().StringVar(&volPath, "path", "", "mount path inside the container (required)")
	_ = volAttachCmd.MarkFlagRequired("path")
	volDetachCmd.Flags().StringVar(&volApp, "app", "", "application to unmount it from (default: the app bound with 'miabi use')")

	for _, c := range []*cobra.Command{volAttachCmd, volDetachCmd} {
		_ = c.RegisterFlagCompletionFunc("app", completeApps)
	}

	for _, c := range []*cobra.Command{volGetCmd, volRmCmd, volAttachCmd, volDetachCmd} {
		c.ValidArgsFunction = completeVolumes
	}

	volCmd.AddCommand(volLsCmd, volCreateCmd, volGetCmd, volRmCmd, volStorageCmd, volAttachCmd, volDetachCmd)
	rootCmd.AddCommand(volCmd)
}

var (
	volSizeMB        int
	volNode          uint
	volDriver        string
	volDriverOpt     []string
	volDriverOptFile []string
	volRmYes         bool
	volApp           string
	volPath          string
)

var volCmd = &cobra.Command{
	Use:     "volumes",
	Aliases: []string{"volume", "vol"},
	Short:   "Manage persistent storage volumes",
	Long: "Create, inspect and delete a workspace's managed volumes, and mount them into\n" +
		"applications. Volumes are addressed by name (or numeric id) and are immutable:\n" +
		"capacity and driver options are fixed at creation, so there is no `set`.\n\n" +
		"A volume's SIZE is the capacity you declared; USED is what the panel last\n" +
		"measured on disk. A volume declared under `kind: Volume` in a manifest is owned\n" +
		"by `miabi apply` — change it there, not here.",
	Example: "  miabi volumes ls\n" +
		"  miabi volumes create web-data --size-mb 5120\n" +
		"  miabi volumes attach web-data --app web --path /var/lib/data\n" +
		"  miabi volumes get web-data\n" +
		"  miabi volumes storage",
}

// volumeConn resolves the connection context, workspace and volume id from a
// single positional argument (name or id) — the shared preamble for volume
// commands. The id is resolved client-side because the get/delete endpoints
// address volumes numerically, not by name.
func volumeConn(ctx context.Context, ref string) (*api.Client, string, uint, error) {
	c, eff, err := newClient()
	if err != nil {
		return nil, "", 0, err
	}
	ws, err := workspaceRef(ctx, c, eff)
	if err != nil {
		return nil, "", 0, err
	}
	id, err := c.ResolveVolumeID(ctx, ws, ref)
	if err != nil {
		return nil, "", 0, err
	}
	return c, ws, id, nil
}

var volLsCmd = &cobra.Command{
	Use:     "ls",
	Aliases: []string{"list"},
	Short:   "List volumes in the workspace",
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
		vols, err := c.Volumes(ctx, ws)
		if err != nil {
			return err
		}
		if structured() {
			return emit(vols)
		}
		if len(vols) == 0 {
			ui.Info("No volumes in this workspace. Add one: miabi volumes create NAME --size-mb 1024")
			return nil
		}
		t := ui.NewTable("NAME", "SIZE", "USED", "DRIVER", "ACCESS", "NODE", "AGE")
		for _, v := range vols {
			t.Row(v.Name, volumeSize(v.SizeBytes), volumeUsed(v), volumeDriver(v), v.AccessMode, volumeNode(v), ui.Age(v.CreatedAt))
		}
		t.Print()
		return nil
	},
}

var volCreateCmd = &cobra.Command{
	Use:   "create <name> [--size-mb N] [--node <id>] [--driver <driver> --driver-opt k=v…]",
	Short: "Create a volume",
	Long: "Creates a managed volume in the workspace. The default driver is `local`: a\n" +
		"node-local volume (rwo) that only one node can mount, so an app backed by one\n" +
		"cannot be replicated across nodes.\n\n" +
		"`nfs` and `cifs` create shared (rwx) storage every replica can mount; both need\n" +
		"--driver-opt device=… and usually --driver-opt o=… for the mount options.\n" +
		"`host` binds an operator-managed path (--driver-opt path=/mnt/…) and is limited\n" +
		"to privileged workspaces. Driver options are encrypted server-side and never\n" +
		"read back — use --driver-opt-file for anything holding a password.",
	Example: "  miabi volumes create web-data --size-mb 5120\n" +
		"  miabi volumes create shared --driver nfs --driver-opt device=:/export --driver-opt o=addr=10.0.0.5,rw\n" +
		"  miabi volumes create shared --driver cifs --driver-opt device=//nas/share --driver-opt-file o=mount-opts.txt",
	Args: cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		opts, err := collectDriverOpts()
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
		v, err := c.CreateVolume(ctx, ws, api.CreateVolumeRequest{
			Name: args[0], ServerID: volNode, SizeMB: volSizeMB, Driver: volDriver, DriverOpts: opts,
		})
		if err != nil {
			return err
		}
		if structured() {
			return emit(v)
		}
		ui.Success("Created volume %s %s", ui.Bold(v.Name), ui.Dim(fmt.Sprintf("(%s, %s, %s)", volumeSize(v.SizeBytes), volumeDriver(*v), v.AccessMode)))
		ui.Info("Mount it: miabi volumes attach %s --app <app> --path /var/lib/data", v.Name)
		return nil
	},
}

var volGetCmd = &cobra.Command{
	Use:   "get <volume>",
	Short: "Show a volume's details and the apps mounting it",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		ctx := context.Background()
		c, ws, id, err := volumeConn(ctx, args[0])
		if err != nil {
			return err
		}
		v, err := c.Volume(ctx, ws, id)
		if err != nil {
			return err
		}
		if structured() {
			return emit(v)
		}
		if v.DisplayName != "" && v.DisplayName != v.Name {
			ui.Detail("%s (%s)", ui.Bold(v.DisplayName), v.Name)
		} else {
			ui.Detail("Name:        %s", ui.Bold(v.Name))
		}
		ui.Detail("Size:        %s declared", volumeSize(v.SizeBytes))
		ui.Detail("Used:        %s", volumeUsed(v.Volume))
		ui.Detail("Driver:      %s (%s)", volumeDriver(v.Volume), v.AccessMode)
		if v.HostPath != "" {
			ui.Detail("Host path:   %s", v.HostPath)
		}
		ui.Detail("Node:        %s", volumeNode(v.Volume))
		ui.Detail("Docker name: %s", v.DockerName)
		if v.Mountpoint != "" {
			ui.Detail("Mountpoint:  %s", v.Mountpoint)
		}
		if v.Imported {
			ui.Detail("Imported:    yes (adopted an existing Docker volume)")
		}
		if !v.Exists {
			ui.Warn("The underlying Docker volume is missing on the node — it is created on the next deploy that mounts it.")
		}
		fmt.Println()
		if len(v.UsedBy) == 0 {
			ui.Info("No application mounts this volume.")
			return nil
		}
		t := ui.NewTable("APPLICATION", "MOUNT PATH")
		for _, u := range v.UsedBy {
			t.Row(u.AppName, u.Path)
		}
		t.Print()
		return nil
	},
}

var volRmCmd = &cobra.Command{
	Use:     "rm <volume>",
	Aliases: []string{"delete"},
	Short:   "Delete a volume and its data (blocked while an app mounts it)",
	Args:    cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		ctx := context.Background()
		c, ws, id, err := volumeConn(ctx, args[0])
		if err != nil {
			return err
		}
		if !volRmYes && !structured() {
			prompt := fmt.Sprintf("Delete volume %s and all its data? This cannot be undone.", ui.Bold(args[0]))
			// Naming the apps first turns a 409 into an informed decision.
			if v, verr := c.Volume(ctx, ws, id); verr == nil && v.InUse {
				names := make([]string, 0, len(v.UsedBy))
				for _, u := range v.UsedBy {
					names = append(names, u.AppName)
				}
				sort.Strings(names)
				ui.Warn("Volume %s is mounted by %d app(s): %s — detach it first.", args[0], len(names), strings.Join(names, ", "))
			}
			if !ui.Confirm(prompt) {
				ui.Info("Aborted.")
				return nil
			}
		}
		if err := c.DeleteVolume(ctx, ws, id); err != nil {
			return err
		}
		ui.Success("Deleted volume %s", ui.Bold(args[0]))
		return nil
	},
}

var volStorageCmd = &cobra.Command{
	Use:   "storage",
	Short: "Show the workspace's declared vs measured storage usage",
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
		s, err := c.WorkspaceStorage(ctx, ws)
		if err != nil {
			return err
		}
		if structured() {
			return emit(s)
		}
		ui.Detail("Volumes:  %d", s.VolumeCount)
		ui.Detail("Declared: %s", volumeSize(s.DeclaredBytes))
		if s.MeasuredAt == nil {
			ui.Detail("Measured: %s", ui.Dim("never measured"))
		} else {
			age := ui.Age(*s.MeasuredAt)
			if age != "just now" {
				age += " ago"
			}
			ui.Detail("Measured: %s %s", volumeSize(s.UsedBytes), ui.Dim("(measured "+age+")"))
		}
		if s.LimitMB < 0 {
			ui.Detail("Limit:    unlimited")
		} else {
			ui.Detail("Limit:    %s", volumeSize(int64(s.LimitMB)*1024*1024))
		}
		return nil
	},
}

var volAttachCmd = &cobra.Command{
	Use:   "attach <volume> [--app <app>] --path <mount path>",
	Short: "Mount a volume into an application",
	Long: "Mounts the volume into the application at --path. The app is flagged as needing\n" +
		"a redeploy — the mount takes effect on its next deploy, not immediately.\n\n" +
		"A node-local (rwo) volume cannot be mounted into a replicated app, and cannot be\n" +
		"mounted into an app placed on a different node; the panel refuses both.",
	Example: "  miabi volumes attach web-data --app web --path /var/lib/data",
	Args:    cobra.ExactArgs(1),
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
		volID, err := c.ResolveVolumeID(ctx, ws, args[0])
		if err != nil {
			return err
		}
		appID, appRef, err := resolveAppRef(ctx, c, eff, ws, volApp)
		if err != nil {
			return err
		}
		if err := c.AttachVolume(ctx, ws, appID, api.AttachVolumeRequest{VolumeID: volID, Path: volPath}); err != nil {
			return err
		}
		ui.Success("Mounted %s at %s in %s", ui.Bold(args[0]), volPath, ui.Bold(appRef))
		ui.Info("Redeploy to apply it: miabi apps deploy %s", appRef)
		return nil
	},
}

var volDetachCmd = &cobra.Command{
	Use:     "detach <volume> [--app <app>]",
	Short:   "Unmount a volume from an application (its data is kept)",
	Example: "  miabi volumes detach web-data --app web",
	Args:    cobra.ExactArgs(1),
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
		volID, err := c.ResolveVolumeID(ctx, ws, args[0])
		if err != nil {
			return err
		}
		appID, appRef, err := resolveAppRef(ctx, c, eff, ws, volApp)
		if err != nil {
			return err
		}
		if err := c.DetachVolume(ctx, ws, appID, volID); err != nil {
			return err
		}
		ui.Success("Unmounted %s from %s", ui.Bold(args[0]), ui.Bold(appRef))
		ui.Info("Redeploy to apply it: miabi apps deploy %s", appRef)
		return nil
	},
}

// collectDriverOpts merges --driver-opt and --driver-opt-file into the option
// map. The file form exists so a CIFS password never reaches the shell history.
func collectDriverOpts() (map[string]string, error) {
	opts := map[string]string{}
	for _, spec := range volDriverOpt {
		k, v, ok := strings.Cut(spec, "=")
		if k = strings.TrimSpace(k); !ok || k == "" {
			return nil, fmt.Errorf("--driver-opt must be key=value, got %q", spec)
		}
		opts[k] = v
	}
	for _, spec := range volDriverOptFile {
		k, path, ok := strings.Cut(spec, "=")
		if k = strings.TrimSpace(k); !ok || k == "" || path == "" {
			return nil, fmt.Errorf("--driver-opt-file must be key=path, got %q", spec)
		}
		v, err := readFileOrStdin(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		opts[k] = v
	}
	if len(opts) == 0 {
		return nil, nil
	}
	return opts, nil
}

// volumeSize renders a declared capacity: 0 means "no ceiling was declared",
// which humanBytes would otherwise print as "-".
func volumeSize(n int64) string {
	if n <= 0 {
		return "unlimited"
	}
	return humanBytes(n)
}

// volumeUsed renders measured usage. A volume that has never been measured is
// not an empty volume, so it must not read as 0.
func volumeUsed(v api.Volume) string {
	if v.UsedMeasuredAt == nil {
		return "-"
	}
	if v.UsedBytes <= 0 {
		return "0B"
	}
	return humanBytes(v.UsedBytes)
}

// volumeDriver names the driver, defaulting a blank one to local (an older panel
// may not send the column).
func volumeDriver(v api.Volume) string {
	if v.Driver == "" {
		return "local"
	}
	return v.Driver
}

func volumeNode(v api.Volume) string {
	if v.ServerName != "" {
		return v.ServerName
	}
	if v.ServerID == 0 {
		return "local"
	}
	return itoa(int(v.ServerID))
}

// completeVolumes tab-completes volume names in the active workspace.
func completeVolumes(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
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
	vols, err := c.Volumes(ctx, ws)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var out []string
	for _, v := range vols {
		if toComplete == "" || strings.HasPrefix(v.Name, toComplete) {
			out = append(out, v.Name)
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}
