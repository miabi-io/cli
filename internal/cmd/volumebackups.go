package cmd

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/miabi-io/cli/internal/api"
	"github.com/miabi-io/cli/internal/ui"
	"github.com/spf13/cobra"
)

func init() {
	volBackupRunCmd.Flags().BoolVar(&volBackupWait, "wait", false, "block until the backup finishes, and exit non-zero if it fails")
	volBackupRunCmd.Flags().DurationVar(&volBackupTimeout, "timeout", 30*time.Minute, "max time to wait with --wait")
	volBackupRestoreCmd.Flags().BoolVarP(&volBackupYes, "yes", "y", false, "skip the confirmation prompt")
	volBackupRmCmd.Flags().BoolVarP(&volBackupYes, "yes", "y", false, "skip the confirmation prompt")

	for _, c := range []*cobra.Command{volBackupsCmd, volBackupRunCmd, volBackupRestoreCmd, volBackupLogsCmd, volBackupRmCmd} {
		c.ValidArgsFunction = completeVolumes
	}

	volBackupsCmd.AddCommand(volBackupRunCmd, volBackupRestoreCmd, volBackupLogsCmd, volBackupRmCmd)
	volCmd.AddCommand(volBackupsCmd)
}

var (
	volBackupWait    bool
	volBackupTimeout time.Duration
	volBackupYes     bool
)

var volBackupsCmd = &cobra.Command{
	Use:     "backups <volume>",
	Aliases: []string{"backup"},
	Short:   "List a volume's backups",
	Long: "Backs a volume's contents up to the workspace's S3 target, and restores them.\n" +
		"Configure S3 in the panel's workspace backup settings first — without it every\n" +
		"backup call is refused.\n\n" +
		"Backups are addressed by the numeric ID the listing shows.",
	Example: "  miabi volumes backups web-data\n" +
		"  miabi volumes backups run web-data --wait\n" +
		"  miabi volumes backups restore web-data 42",
	Args: cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		ctx := context.Background()
		c, ws, id, err := volumeConn(ctx, args[0])
		if err != nil {
			return err
		}
		backups, err := c.VolumeBackups(ctx, ws, id)
		if err != nil {
			return err
		}
		if structured() {
			return emit(backups)
		}
		if len(backups) == 0 {
			// An empty history and an unconfigured workspace look identical here,
			// and only one of them is fixed by running a backup.
			if ok, serr := c.VolumeBackupConfigured(ctx, ws, id); serr == nil && !ok {
				ui.Info("No backups: this workspace has no S3 target configured. Set one in the panel's backup settings.")
				return nil
			}
			ui.Info("No backups yet. Take one: miabi volumes backups run %s", args[0])
			return nil
		}
		sort.Slice(backups, func(i, j int) bool { return backups[i].CreatedAt.After(backups[j].CreatedAt) })
		t := ui.NewTable("ID", "STATUS", "TRIGGER", "SIZE", "ARCHIVE", "DURATION", "AGE")
		for _, b := range backups {
			t.Row(itoa(int(b.ID)), ui.Status(b.Status), b.Trigger, humanBytes(b.SizeBytes),
				archiveCell(b), backupDuration(b), ui.Age(b.CreatedAt))
		}
		t.Print()
		return nil
	},
}

var volBackupRunCmd = &cobra.Command{
	Use:   "run <volume> [--wait]",
	Short: "Back a volume up to S3 now",
	Long: "Records a backup and hands it to the panel's worker, so it returns while the\n" +
		"run is still pending. --wait polls until it settles and exits non-zero on\n" +
		"failure, which is what a CI step wants.",
	Example: "  miabi volumes backups run web-data\n" +
		"  miabi volumes backups run web-data --wait --timeout 1h",
	Args: cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		ctx := context.Background()
		c, ws, id, err := volumeConn(ctx, args[0])
		if err != nil {
			return err
		}
		b, err := c.RunVolumeBackup(ctx, ws, id)
		if err != nil {
			return err
		}
		if !volBackupWait {
			if structured() {
				return emit(b)
			}
			ui.Success("Backup #%d of %s started (%s)", b.ID, ui.Bold(args[0]), ui.Status(b.Status))
			ui.Info("Follow it: miabi volumes backups %s", args[0])
			return nil
		}

		wctx, cancel := context.WithTimeout(ctx, volBackupTimeout)
		defer cancel()
		sp := ui.NewSpinner(fmt.Sprintf("Backup #%d: %s", b.ID, b.Status))
		sp.Start()
		final, err := c.WaitForVolumeBackup(wctx, ws, id, b.ID, func(status string) {
			sp.Update(fmt.Sprintf("Backup #%d: %s", b.ID, status))
		})
		sp.Stop()
		if err != nil {
			return fmt.Errorf("waiting for backup #%d: %w", b.ID, err)
		}
		if structured() {
			_ = emit(final)
		}
		if api.IsBackupFailure(final.Status) {
			if final.Error != "" {
				ui.Fail("Backup #%d failed: %s", final.ID, final.Error)
			}
			ui.Info("Full log: miabi volumes backups logs %s %d", args[0], final.ID)
			// Non-zero exit so CI fails the step.
			return fmt.Errorf("backup #%d failed", final.ID)
		}
		if !structured() {
			ui.Success("Backup #%d completed %s", final.ID, ui.Dim("("+humanBytes(final.SizeBytes)+")"))
		}
		return nil
	},
}

var volBackupRestoreCmd = &cobra.Command{
	Use:   "restore <volume> <backup-id>",
	Short: "Restore a volume's contents from a backup (overwrites its data)",
	Long: "Unpacks the backup's archive back into the volume, overwriting what is there.\n" +
		"The apps mounting it are NOT stopped first — restoring under a running app\n" +
		"gives it a filesystem that changed beneath it, so stop them yourself unless you\n" +
		"know the workload tolerates it.\n\n" +
		"The panel restores inline, so this call blocks until the restore finishes.",
	Example: "  miabi volumes backups restore web-data 42",
	Args:    cobra.ExactArgs(2),
	RunE: func(_ *cobra.Command, args []string) error {
		ctx := context.Background()
		c, ws, id, err := volumeConn(ctx, args[0])
		if err != nil {
			return err
		}
		backupID, err := parseBackupID(args[1])
		if err != nil {
			return err
		}
		b, err := c.FindVolumeBackup(ctx, ws, id, backupID)
		if err != nil {
			return err
		}
		if !api.IsBackupTerminal(b.Status) || api.IsBackupFailure(b.Status) {
			return fmt.Errorf("backup #%d is %s — only a completed backup can be restored", b.ID, b.Status)
		}
		if !volBackupYes && !structured() {
			prompt := fmt.Sprintf("Restore %s from backup #%d (%s, taken %s ago)? This overwrites the volume's current contents.",
				ui.Bold(args[0]), b.ID, humanBytes(b.SizeBytes), ui.Age(b.CreatedAt))
			// Restoring under a running app is the failure mode worth naming.
			if v, verr := c.Volume(ctx, ws, id); verr == nil && v.InUse {
				names := make([]string, 0, len(v.UsedBy))
				for _, u := range v.UsedBy {
					names = append(names, u.AppName)
				}
				sort.Strings(names)
				ui.Warn("%d app(s) currently mount %s: %s — stop them first.", len(names), args[0], strings.Join(names, ", "))
			}
			if !ui.Confirm(prompt) {
				ui.Info("Aborted.")
				return nil
			}
		}
		sp := ui.NewSpinner(fmt.Sprintf("Restoring %s from backup #%d", args[0], b.ID))
		sp.Start()
		err = c.RestoreVolumeBackup(ctx, ws, id, b.ID)
		sp.Stop()
		if err != nil {
			return err
		}
		ui.Success("Restored %s from backup #%d", ui.Bold(args[0]), b.ID)
		ui.Info("Redeploy the apps mounting it so they see the restored data.")
		return nil
	},
}

var volBackupLogsCmd = &cobra.Command{
	Use:     "logs <volume> <backup-id>",
	Short:   "Print a backup run's full log",
	Example: "  miabi volumes backups logs web-data 42",
	Args:    cobra.ExactArgs(2),
	RunE: func(_ *cobra.Command, args []string) error {
		ctx := context.Background()
		c, ws, id, err := volumeConn(ctx, args[0])
		if err != nil {
			return err
		}
		backupID, err := parseBackupID(args[1])
		if err != nil {
			return err
		}
		data, err := c.VolumeBackupLogs(ctx, ws, id, backupID)
		if err != nil {
			return err
		}
		if _, err := os.Stdout.Write(data); err != nil {
			return err
		}
		if len(data) > 0 && !strings.HasSuffix(string(data), "\n") {
			fmt.Println()
		}
		return nil
	},
}

var volBackupRmCmd = &cobra.Command{
	Use:     "rm <volume> <backup-id>",
	Aliases: []string{"delete"},
	Short:   "Delete a backup record",
	Long: "Removes the backup from the history. The archive object is left in the bucket —\n" +
		"the panel has no S3 delete — so reclaim that storage with a bucket lifecycle\n" +
		"rule, not with this command.",
	Args: cobra.ExactArgs(2),
	RunE: func(_ *cobra.Command, args []string) error {
		ctx := context.Background()
		c, ws, id, err := volumeConn(ctx, args[0])
		if err != nil {
			return err
		}
		backupID, err := parseBackupID(args[1])
		if err != nil {
			return err
		}
		if !volBackupYes && !structured() {
			if !ui.Confirm(fmt.Sprintf("Delete backup #%d of %s from the history?", backupID, ui.Bold(args[0]))) {
				ui.Info("Aborted.")
				return nil
			}
		}
		if err := c.DeleteVolumeBackup(ctx, ws, id, backupID); err != nil {
			return err
		}
		ui.Success("Deleted backup #%d %s", backupID, ui.Dim("(its S3 archive is kept)"))
		return nil
	},
}

func parseBackupID(ref string) (uint, error) {
	id, err := strconv.ParseUint(strings.TrimPrefix(ref, "#"), 10, 64)
	if err != nil || id == 0 {
		return 0, fmt.Errorf("%q is not a backup id — use the ID column of `miabi volumes backups <volume>`", ref)
	}
	return uint(id), nil
}

// archiveCell names the object in the bucket, which is what someone reaching for
// the archive by hand actually needs.
func archiveCell(b api.VolumeBackup) string {
	if b.Filename == "" {
		return "-"
	}
	if b.S3Bucket == "" {
		return b.Filename
	}
	return b.S3Bucket + "/" + strings.Trim(b.S3Path+"/"+b.Filename, "/")
}

// backupDuration renders how long a settled run took. A run still in flight has
// no duration yet, and one that never started has none at all.
func backupDuration(b api.VolumeBackup) string {
	if b.StartedAt == nil || b.FinishedAt == nil {
		return "-"
	}
	d := b.FinishedAt.Sub(*b.StartedAt)
	switch {
	case d < 0:
		return "-"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	}
}
