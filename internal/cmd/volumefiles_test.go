package cmd

import (
	"testing"
	"time"

	"github.com/miabi-io/cli/internal/api"
)

func TestSplitVolumeRef(t *testing.T) {
	tests := []struct {
		arg    string
		vol    string
		path   string
		remote bool
	}{
		{"web-data:/conf/app.conf", "web-data", "/conf/app.conf", true},
		{"web-data:conf/app.conf", "web-data", "conf/app.conf", true},
		{"./local.conf", "", "./local.conf", false},
		{"-", "", "-", false},
		{"/abs/path.conf", "", "/abs/path.conf", false},
		// A numeric id is a valid volume reference, and one character long.
		{"7:/dump.sql", "7", "/dump.sql", true},
		// A Windows drive is not a volume, whatever the platform we parse on.
		{`C:\src\app.conf`, "", `C:\src\app.conf`, false},
		{`c:/src/app.conf`, "", `c:/src/app.conf`, false},
	}
	for _, tt := range tests {
		vol, p, remote := splitVolumeRef(tt.arg)
		if vol != tt.vol || p != tt.path || remote != tt.remote {
			t.Errorf("splitVolumeRef(%q) = (%q, %q, %t), want (%q, %q, %t)",
				tt.arg, vol, p, remote, tt.vol, tt.path, tt.remote)
		}
	}
}

func TestFilterFiles(t *testing.T) {
	files := []api.VolumeFile{
		{Path: "conf", IsDir: true},
		{Path: "conf/app.conf"},
		{Path: "conf-backup/old.conf"},
		{Path: "data.db"},
	}
	// The prefix must match whole path segments: conf-backup is not under conf.
	got := filterFiles(files, "conf")
	if len(got) != 2 || got[0].Path != "conf" || got[1].Path != "conf/app.conf" {
		t.Errorf("filter(conf) = %v, want the directory and its one file", pathsOf(got))
	}
	if got := filterFiles(files, "/conf/"); len(got) != 2 {
		t.Errorf("filter(/conf/) = %v, want slashes to be optional", pathsOf(got))
	}
	if got := filterFiles(files, ""); len(got) != len(files) {
		t.Errorf("filter() dropped entries: %v", pathsOf(got))
	}
}

// The delete prompt has to say how much a recursive directory delete takes; the
// directory entry itself is not part of that count.
func TestCountUnder(t *testing.T) {
	files := []api.VolumeFile{
		{Path: "uploads", IsDir: true},
		{Path: "uploads/a.png"},
		{Path: "uploads/thumbs", IsDir: true},
		{Path: "uploads/thumbs/a.png"},
		{Path: "uploads2/b.png"},
	}
	if n := countUnder(files, "uploads"); n != 3 {
		t.Errorf("countUnder(uploads) = %d, want 3", n)
	}
	if n := countUnder(files, "uploads/a.png"); n != 0 {
		t.Errorf("countUnder(file) = %d, want 0", n)
	}
}

func TestFileCells(t *testing.T) {
	if got := fileSizeCell(api.VolumeFile{IsDir: true, Size: 4096}); got != "-" {
		t.Errorf("dir size = %q, want -", got)
	}
	// An empty file is a fact; humanBytes would render it as "-" like a directory.
	if got := fileSizeCell(api.VolumeFile{Size: 0}); got != "0B" {
		t.Errorf("empty file size = %q, want 0B", got)
	}
	if got := filePathCell(api.VolumeFile{Path: "conf", IsDir: true}); got != "conf/" {
		t.Errorf("dir path = %q, want a trailing slash", got)
	}
	// A helper that could not stat an entry reports epoch 0, which is not 1970.
	if got := fileAgeCell(api.VolumeFile{}); got != "-" {
		t.Errorf("missing mtime = %q, want -", got)
	}
	if got := fileAgeCell(api.VolumeFile{ModTime: time.Now().Add(-48 * time.Hour).Unix()}); got != "2d ago" {
		t.Errorf("mtime = %q, want 2d ago", got)
	}
}

func pathsOf(files []api.VolumeFile) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, f.Path)
	}
	return out
}
