package cmd

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/miabi-io/cli/internal/api"
	"github.com/miabi-io/cli/internal/ui"
	"github.com/spf13/cobra"
)

func init() {
	volLsFilesCmd.Flags().StringVar(&volFilePath, "path", "", "only list entries under this directory")
	volCpCmd.Flags().BoolVarP(&volCpForce, "force", "f", false, "overwrite the local destination file if it exists")
	volRmFileCmd.Flags().BoolVarP(&volFileRmYes, "yes", "y", false, "skip the confirmation prompt")

	volLsFilesCmd.ValidArgsFunction = completeVolumes
	volRmFileCmd.ValidArgsFunction = completeVolumeFiles

	volCmd.AddCommand(volLsFilesCmd, volCpCmd, volRmFileCmd)
}

var (
	volFilePath  string
	volCpForce   bool
	volFileRmYes bool
)

var volLsFilesCmd = &cobra.Command{
	Use:     "ls-files <volume> [--path <dir>]",
	Aliases: []string{"files"},
	Short:   "List the files stored in a volume",
	Long: "Lists a volume's contents recursively, relative to its root. The panel reads\n" +
		"the volume with a short-lived helper container, so the first call on a node may\n" +
		"pause while that image is pulled, and the listing is capped at 5000 entries.",
	Example: "  miabi volumes ls-files web-data\n" +
		"  miabi volumes ls-files web-data --path uploads",
	Args: cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		ctx := context.Background()
		c, ws, id, err := volumeConn(ctx, args[0])
		if err != nil {
			return err
		}
		all, err := c.VolumeFiles(ctx, ws, id)
		if err != nil {
			return err
		}
		// Truncation is a property of what the panel sent, not of what --path keeps:
		// measuring it after the filter hides it for every filtered listing.
		truncated := len(all) >= api.MaxListedVolumeFiles
		files := filterFiles(all, volFilePath)
		sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
		if structured() {
			return emit(files)
		}
		if len(files) == 0 {
			if volFilePath != "" {
				ui.Info("Nothing under %s in %s.", volFilePath, args[0])
			} else {
				ui.Info("Volume %s is empty.", args[0])
			}
			if truncated {
				ui.Warn("The listing hit the panel's %d-entry cap — entries under that path may exist beyond it.", api.MaxListedVolumeFiles)
			}
			return nil
		}
		t := ui.NewTable("PATH", "SIZE", "MODIFIED")
		for _, f := range files {
			t.Row(filePathCell(f), fileSizeCell(f), fileAgeCell(f))
		}
		t.Print()
		if truncated {
			ui.Warn("The listing hit the panel's %d-entry cap — some files are not shown.", api.MaxListedVolumeFiles)
		}
		return nil
	},
}

var volCpCmd = &cobra.Command{
	Use:   "cp <src> <dst>",
	Short: "Copy a file into or out of a volume",
	Long: "Copies one file, with exactly one side qualified as <volume>:<path> — the\n" +
		"other side is a local path (\"-\" is stdin when uploading, stdout when\n" +
		"downloading). Paths inside a volume are relative to its root, so a leading\n" +
		"slash is optional.\n\n" +
		"Uploading overwrites the file in the volume; downloading refuses to clobber an\n" +
		"existing local file unless --force. Both directions stream, so file size is\n" +
		"bounded by disk rather than memory; only an upload from stdin is buffered,\n" +
		"since its size is not knowable in advance. The panel rejects uploads over\n" +
		"512 MiB.",
	Example: "  miabi volumes cp ./nginx.conf web-data:/conf/nginx.conf\n" +
		"  miabi volumes cp web-data:/conf/nginx.conf ./nginx.conf\n" +
		"  miabi volumes cp web-data:/dump.sql - | gzip > dump.sql.gz",
	Args: cobra.ExactArgs(2),
	RunE: func(_ *cobra.Command, args []string) error {
		srcVol, srcPath, srcRemote := splitVolumeRef(args[0])
		dstVol, dstPath, dstRemote := splitVolumeRef(args[1])
		switch {
		case srcRemote == dstRemote && srcRemote:
			return fmt.Errorf("cannot copy between two volumes — download to a local file first")
		case srcRemote == dstRemote:
			return fmt.Errorf("one side must name a volume as <volume>:<path>")
		}
		ctx := context.Background()
		if srcRemote {
			return downloadFromVolume(ctx, srcVol, srcPath, args[1])
		}
		return uploadToVolume(ctx, args[0], dstVol, dstPath)
	},
}

var volRmFileCmd = &cobra.Command{
	Use:     "rm-file <volume> <path>",
	Aliases: []string{"delete-file"},
	Short:   "Delete a file or directory from a volume",
	Long: "Removes one entry from the volume. A directory is removed recursively with\n" +
		"everything under it, which is why this asks before acting.",
	Example: "  miabi volumes rm-file web-data conf/nginx.conf\n" +
		"  miabi volumes rm-file web-data uploads --yes",
	Args: cobra.ExactArgs(2),
	RunE: func(_ *cobra.Command, args []string) error {
		ctx := context.Background()
		c, ws, id, err := volumeConn(ctx, args[0])
		if err != nil {
			return err
		}
		target := strings.TrimPrefix(args[1], "/")
		if target == "" {
			return fmt.Errorf("a path inside the volume is required")
		}
		if !volFileRmYes && !structured() {
			prompt := fmt.Sprintf("Delete %s from volume %s?", ui.Bold(target), args[0])
			// A directory takes everything under it, so say how much that is.
			if files, ferr := c.VolumeFiles(ctx, ws, id); ferr == nil {
				if n := countUnder(files, target); n > 0 {
					prompt = fmt.Sprintf("Delete %s from volume %s, and the %d entries under it? This cannot be undone.",
						ui.Bold(target), args[0], n)
				}
			}
			if !ui.Confirm(prompt) {
				ui.Info("Aborted.")
				return nil
			}
		}
		if err := c.DeleteVolumeFile(ctx, ws, id, target); err != nil {
			return err
		}
		ui.Success("Deleted %s from %s", ui.Bold(target), ui.Bold(args[0]))
		return nil
	},
}

// downloadFromVolume writes one file out of a volume to dst ("-" is stdout).
func downloadFromVolume(ctx context.Context, volRef, remotePath, dst string) error {
	remotePath = strings.TrimPrefix(remotePath, "/")
	if remotePath == "" {
		return fmt.Errorf("a path inside the volume is required, e.g. %s:/conf/app.conf", volRef)
	}
	c, ws, id, err := volumeConn(ctx, volRef)
	if err != nil {
		return err
	}
	// Resolve the destination before the transfer: a name collision should fail
	// before spending minutes pulling the bytes down.
	local := dst
	if local != "-" {
		if info, serr := os.Stat(local); serr == nil {
			if info.IsDir() {
				local = filepath.Join(local, path.Base(remotePath))
			}
		}
		if info, serr := os.Stat(local); serr == nil && !info.IsDir() && !volCpForce {
			return fmt.Errorf("%s already exists — pass --force to overwrite it", local)
		}
	}

	sp := ui.NewSpinner(fmt.Sprintf("Downloading %s from %s", remotePath, volRef))
	if local == "-" {
		_, err := c.DownloadVolumeFile(ctx, ws, id, remotePath, os.Stdout)
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(local), ".miabi-download-*")
	if err != nil {
		return err
	}
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}()

	sp.Start()
	n, err := c.DownloadVolumeFile(ctx, ws, id, remotePath, tmp)
	sp.Stop()
	if err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), local); err != nil {
		return err
	}
	ui.Success("Wrote %s %s", ui.Bold(local), ui.Dim("("+humanBytes(n)+")"))
	return nil
}

// uploadToVolume sends a local file ("-" is stdin) into a volume. The remote
// path names the destination file, whose directory is created as needed.
func uploadToVolume(ctx context.Context, src, volRef, remotePath string) error {
	remotePath = strings.TrimPrefix(remotePath, "/")
	if remotePath == "" || strings.HasSuffix(remotePath, "/") {
		return fmt.Errorf("the destination must name a file, e.g. %s:/conf/app.conf", volRef)
	}

	var (
		body io.Reader
		size int64
	)
	if src == "-" {
		data, err := readAllStdin()
		if err != nil {
			return err
		}
		body, size = bytes.NewReader(data), int64(len(data))
	} else {
		info, err := os.Stat(src)
		if err != nil {
			return err
		}
		if info.IsDir() {
			return fmt.Errorf("%s is a directory — copy one file at a time", src)
		}
		f, err := os.Open(src)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		body, size = f, info.Size()
	}
	if size > api.MaxVolumeUploadBytes {
		return fmt.Errorf("%s is %s — the panel rejects uploads over %s",
			src, humanBytes(size), humanBytes(api.MaxVolumeUploadBytes))
	}
	c, ws, id, err := volumeConn(ctx, volRef)
	if err != nil {
		return err
	}
	subdir, name := path.Split(remotePath)
	sp := ui.NewSpinner(fmt.Sprintf("Uploading %s to %s", name, volRef))
	sp.Start()
	dest, err := c.UploadVolumeFile(ctx, ws, id, strings.TrimSuffix(subdir, "/"), name, body)
	sp.Stop()
	if err != nil {
		return err
	}
	label := src
	if label == "-" {
		label = "stdin"
	}
	ui.Success("Uploaded %s to %s:%s %s", ui.Bold(label), volRef, dest, ui.Dim("("+humanBytes(size)+")"))
	return nil
}

// readAllStdin reads stdin verbatim. Unlike readFileOrStdin it keeps the
// trailing newline: a volume file is copied byte for byte, text or not.
func readAllStdin() ([]byte, error) { return io.ReadAll(os.Stdin) }

// splitVolumeRef splits a "<volume>:<path>" argument. A single-letter prefix is
// a Windows drive, not a volume, so "C:\src" stays a local path.
func splitVolumeRef(arg string) (vol, p string, remote bool) {
	head, tail, ok := strings.Cut(arg, ":")
	if !ok || head == "" {
		return "", arg, false
	}
	if len(head) == 1 && isDriveLetter(head[0]) {
		return "", arg, false
	}
	return head, tail, true
}

func isDriveLetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// filterFiles keeps the entries under dir (the directory itself included).
func filterFiles(files []api.VolumeFile, dir string) []api.VolumeFile {
	dir = strings.Trim(dir, "/")
	if dir == "" {
		return files
	}
	out := make([]api.VolumeFile, 0, len(files))
	for _, f := range files {
		if f.Path == dir || strings.HasPrefix(f.Path, dir+"/") {
			out = append(out, f)
		}
	}
	return out
}

// countUnder counts the entries a recursive delete of dir would take with it.
func countUnder(files []api.VolumeFile, dir string) int {
	dir = strings.Trim(dir, "/")
	n := 0
	for _, f := range files {
		if strings.HasPrefix(f.Path, dir+"/") {
			n++
		}
	}
	return n
}

func filePathCell(f api.VolumeFile) string {
	if f.IsDir {
		return f.Path + "/"
	}
	return f.Path
}

func fileSizeCell(f api.VolumeFile) string {
	if f.IsDir {
		return "-"
	}
	if f.Size == 0 {
		return "0B"
	}
	return humanBytes(f.Size)
}

// fileAgeCell renders the mtime the helper reported. A zero epoch means the
// helper could not stat it, which is not "1970".
func fileAgeCell(f api.VolumeFile) string {
	if f.ModTime <= 0 {
		return "-"
	}
	age := ui.Age(time.Unix(f.ModTime, 0))
	if age != "just now" {
		age += " ago"
	}
	return age
}

// completeVolumeFiles completes the volume name, then its file paths.
func completeVolumeFiles(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) == 0 {
		return completeVolumes(nil, args, toComplete)
	}
	if len(args) > 1 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	ctx := context.Background()
	c, ws, id, err := volumeConn(ctx, args[0])
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	files, err := c.VolumeFiles(ctx, ws, id)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var out []string
	for _, f := range files {
		if strings.HasPrefix(f.Path, toComplete) {
			out = append(out, f.Path)
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}
