package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A page served in front of the panel (SSO gateway, WAF, wrong host) must be
// named as such, not reported as a JSON decode failure.
func TestClaimLoginTokenRejectsHTMLResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<!doctype html><html><head><title>Sign in</title></head><body>hi</body></html>"))
	}))
	defer srv.Close()

	c, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.ClaimLoginToken(context.Background(), "code")
	if err == nil {
		t.Fatal("expected an error for an HTML response")
	}
	for _, want := range []string{"not the Miabi API", "text/html", "Sign in"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// An action with no payload must not read a gateway's 200 page as success.
func TestNoPayloadCallRejectsHTMLResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><title>Checking your browser</title></html>"))
	}))
	defer srv.Close()

	c, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.StopApp(context.Background(), "ws", 1); err == nil {
		t.Fatal("expected an error for an HTML response")
	}
}

func TestRedirectsAreRefused(t *testing.T) {
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><title>Login</title></html>"))
	}))
	defer elsewhere.Close()

	tests := []struct {
		name   string
		to     string
		status int
		want   string
	}{
		{"off-origin", elsewhere.URL + "/login", http.StatusFound, "intercepting"},
		{"method rewritten", "/api/v1/auth/login-token/claim", http.StatusMovedPermanently, "rewritten as GET"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, tt.to, tt.status)
			}))
			defer srv.Close()

			c, err := New(Options{BaseURL: srv.URL})
			if err != nil {
				t.Fatal(err)
			}
			_, err = c.ClaimLoginToken(context.Background(), "code")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want it to mention %q", err, tt.want)
			}
		})
	}
}

// A failed action must fail the command, even though it decodes no payload.
func TestNoPayloadCallSurfacesAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"success":false,"error":{"code":"forbidden","message":"not allowed"}}`))
	}))
	defer srv.Close()

	c, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	err = c.StopApp(context.Background(), "ws", 1)
	if err == nil || !strings.Contains(err.Error(), "forbidden: not allowed") {
		t.Fatalf("error = %v, want the API's forbidden error", err)
	}
}

// A bodiless failure still has to be reported as one.
func TestEmptyBodyFailureIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	c, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.StopApp(context.Background(), "ws", 1); err == nil || !strings.Contains(err.Error(), "HTTP 502") {
		t.Fatalf("error = %v, want HTTP 502", err)
	}
}

// The envelope path stays intact: a normal API response still decodes.
func TestEnvelopeStillDecodes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"token":"tok-123"}}`))
	}))
	defer srv.Close()

	c, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	tok, err := c.ClaimLoginToken(context.Background(), "code")
	if err != nil {
		t.Fatal(err)
	}
	if tok.Token != "tok-123" {
		t.Errorf("token = %q, want tok-123", tok.Token)
	}
}

// The volume get/delete endpoints address volumes by numeric id, so a name has
// to be resolved from the listing before any call is made.
func TestResolveVolumeID(t *testing.T) {
	var listed int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/volumes") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		listed++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":[{"id":7,"name":"web-data"},{"id":9,"name":"cache"}]}`))
	}))
	defer srv.Close()

	c, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	id, err := c.ResolveVolumeID(ctx, "ws", "cache")
	if err != nil {
		t.Fatalf("resolve by name: %v", err)
	}
	if id != 9 {
		t.Errorf("id = %d, want 9", id)
	}

	// A numeric reference is taken as the id itself — no listing needed.
	before := listed
	if id, err = c.ResolveVolumeID(ctx, "ws", "42"); err != nil || id != 42 {
		t.Fatalf("resolve by id = %d, %v; want 42, nil", id, err)
	}
	if listed != before {
		t.Errorf("a numeric reference listed volumes %d extra time(s)", listed-before)
	}

	if _, err = c.ResolveVolumeID(ctx, "ws", "nope"); err == nil || !strings.Contains(err.Error(), `volume "nope" not found`) {
		t.Fatalf("error = %v, want a not-found error naming the volume", err)
	}
}

// A volume that has never been measured must decode with a nil UsedMeasuredAt,
// which is what separates "empty" from "unknown" in the listing.
func TestVolumeDetailDecodes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"id":7,"name":"web-data","size_bytes":1048576,
			"used_bytes":0,"driver":"nfs","access_mode":"rwx","exists":true,"in_use":true,
			"used_by":[{"app_id":3,"app_name":"web","path":"/var/lib/data"}]}}`))
	}))
	defer srv.Close()

	c, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	v, err := c.Volume(context.Background(), "ws", 7)
	if err != nil {
		t.Fatal(err)
	}
	if v.UsedMeasuredAt != nil {
		t.Errorf("UsedMeasuredAt = %v, want nil for a never-measured volume", v.UsedMeasuredAt)
	}
	if !v.InUse || len(v.UsedBy) != 1 || v.UsedBy[0].Path != "/var/lib/data" {
		t.Errorf("usage = %+v, want one mount at /var/lib/data", v.UsedBy)
	}
	if v.AccessMode != "rwx" || v.SizeBytes != 1048576 {
		t.Errorf("volume = %+v, want the embedded fields decoded", v.Volume)
	}
}

// A file download bypasses the JSON envelope, so its failure paths have to be
// re-established: an error still answers the envelope, and a gateway's HTML must
// never reach the caller as if it were the file's content.
func TestDownloadVolumeFile(t *testing.T) {
	t.Run("raw body", func(t *testing.T) {
		var gotQuery string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotQuery = r.URL.Query().Get("path")
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write([]byte("\x00\x01binary\n"))
		}))
		defer srv.Close()

		c, err := New(Options{BaseURL: srv.URL})
		if err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		n, err := c.DownloadVolumeFile(context.Background(), "ws", 7, "conf/a b.conf", &buf)
		if err != nil {
			t.Fatal(err)
		}
		if buf.String() != "\x00\x01binary\n" {
			t.Errorf("data = %q, want the bytes verbatim", buf.String())
		}
		if n != int64(buf.Len()) {
			t.Errorf("n = %d, want %d", n, buf.Len())
		}
		if gotQuery != "conf/a b.conf" {
			t.Errorf("path query = %q, want it escaped and decoded back", gotQuery)
		}
	})

	t.Run("error envelope", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"success":false,"error":{"code":"NOT_FOUND","message":"file not found"}}`))
		}))
		defer srv.Close()

		c, err := New(Options{BaseURL: srv.URL})
		if err != nil {
			t.Fatal(err)
		}
		_, err = c.DownloadVolumeFile(context.Background(), "ws", 7, "missing", io.Discard)
		if err == nil || !strings.Contains(err.Error(), "file not found") {
			t.Fatalf("error = %v, want the API's not-found message", err)
		}
	})

	t.Run("html is not a file", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<html><title>Sign in</title></html>"))
		}))
		defer srv.Close()

		c, err := New(Options{BaseURL: srv.URL})
		if err != nil {
			t.Fatal(err)
		}
		_, err = c.DownloadVolumeFile(context.Background(), "ws", 7, "app.conf", io.Discard)
		if err == nil || !strings.Contains(err.Error(), "not the Miabi API") {
			t.Fatalf("error = %v, want a 200 HTML page refused instead of returned", err)
		}
	})
}

// The upload is multipart: the panel takes the destination directory from the
// "path" field and the file name from the part's filename.
func TestUploadVolumeFileSendsMultipart(t *testing.T) {
	var (
		gotDir      string
		gotName     string
		gotContent  string
		gotBoundary bool
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBoundary = strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data")
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse multipart: %v", err)
		}
		gotDir = r.FormValue("path")
		f, hdr, err := r.FormFile("file")
		if err != nil {
			t.Errorf("form file: %v", err)
		} else {
			defer f.Close()
			gotName = hdr.Filename
			b := make([]byte, hdr.Size)
			_, _ = f.Read(b)
			gotContent = string(b)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"path":"conf/app.conf"}}`))
	}))
	defer srv.Close()

	c, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	dest, err := c.UploadVolumeFile(context.Background(), "ws", 7, "conf", "app.conf", strings.NewReader("listen 80;\n"))
	if err != nil {
		t.Fatal(err)
	}
	if dest != "conf/app.conf" {
		t.Errorf("dest = %q, want the path the panel reported", dest)
	}
	if !gotBoundary || gotDir != "conf" || gotName != "app.conf" || gotContent != "listen 80;\n" {
		t.Errorf("multipart = (%t, dir %q, name %q, content %q)", gotBoundary, gotDir, gotName, gotContent)
	}
}

// The wait polls the history until the run settles. "running" must not end it:
// that is terminal for a deployment, not for a backup.
func TestWaitForVolumeBackup(t *testing.T) {
	statuses := []string{"pending", "running", "running", "completed"}
	var polls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		s := statuses[min(polls, len(statuses)-1)]
		polls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":[{"id":42,"status":"` + s + `","size_bytes":2048}]}`))
	}))
	defer srv.Close()

	c, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	// Poll fast: the point is the state machine, not the pacing.
	volumeBackupPollInterval = time.Millisecond
	t.Cleanup(func() { volumeBackupPollInterval = 3 * time.Second })

	var seen []string
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	final, err := c.WaitForVolumeBackup(ctx, "ws", 7, 42, func(s string) { seen = append(seen, s) })
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != "completed" || final.SizeBytes != 2048 {
		t.Errorf("final = %+v, want the completed record", final)
	}
	// pending -> running -> completed; the repeated "running" is not re-reported.
	if strings.Join(seen, ",") != "pending,running,completed" {
		t.Errorf("updates = %v, want each change once", seen)
	}
}

// A backup id absent from the history is a user error, not an empty result.
func TestFindVolumeBackupMissing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":[{"id":1,"status":"completed"}]}`))
	}))
	defer srv.Close()

	c, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.FindVolumeBackup(context.Background(), "ws", 7, 99); err == nil ||
		!strings.Contains(err.Error(), "backup 99 not found") {
		t.Fatalf("error = %v, want a not-found naming the id", err)
	}
}

// The point of streaming is that a large body never becomes a []byte. A response
// bigger than any reasonable buffer must still arrive intact, and the client must
// not have grown a copy of it along the way.
func TestDownloadVolumeFileStreams(t *testing.T) {
	const size = 8 << 20
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		chunk := bytes.Repeat([]byte("x"), 64<<10)
		for sent := 0; sent < size; sent += len(chunk) {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	c, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.New()
	n, err := c.DownloadVolumeFile(context.Background(), "ws", 7, "big.bin", h)
	if err != nil {
		t.Fatal(err)
	}
	if n != size {
		t.Fatalf("copied %d bytes, want %d", n, size)
	}
	want := sha256.Sum256(bytes.Repeat([]byte("x"), size))
	if !bytes.Equal(h.Sum(nil), want[:]) {
		t.Error("the streamed bytes do not match what the server sent")
	}
}

// A download's deadline has to outlive the call that starts it, or the copy dies
// on the first read. Closing the body is what releases it.
func TestStreamedDownloadOutlivesTheCallThatOpenedIt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.(http.Flusher).Flush()
		time.Sleep(150 * time.Millisecond)
		_, _ = w.Write([]byte("late"))
	}))
	defer srv.Close()

	c, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if _, err := c.DownloadVolumeFile(context.Background(), "ws", 7, "slow.bin", &buf); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "late" {
		t.Errorf("body = %q, want the bytes that arrived after the headers", buf.String())
	}
}

// An error answered as the envelope must still be reported as the API's message,
// even though the success path never reads the body.
func TestStreamedDownloadStillDecodesAPIErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"success":false,"error":{"code":"FORBIDDEN","message":"not your volume"}}`))
	}))
	defer srv.Close()

	c, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	_, err = c.DownloadVolumeFile(context.Background(), "ws", 7, "x", &buf)
	if err == nil || !strings.Contains(err.Error(), "not your volume") {
		t.Fatalf("error = %v, want the API message", err)
	}
	if buf.Len() != 0 {
		t.Errorf("a failed download wrote %d bytes to the destination", buf.Len())
	}
}
