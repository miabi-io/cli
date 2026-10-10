package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/miabi-io/cli/internal/api"
)

var (
	opsReadTools  = []string{"workspace_overview", "list_alerts", "list_events", "get_traffic", "list_backups"}
	opsWriteTools = []string{"ack_alert", "resolve_alert", "run_backup"}
)

func TestOpsToolsGating(t *testing.T) {
	list := func(allowWrite bool) map[string]toolDef {
		resps := drive(t, allowWrite, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
		var res toolsListResult
		remarshal(t, resps[0].Result, &res)
		got := map[string]toolDef{}
		for _, d := range res.Tools {
			got[d.Name] = d
		}
		return got
	}

	ro := list(false)
	for _, name := range opsReadTools {
		d, ok := ro[name]
		if !ok {
			t.Errorf("read-only catalog should include %q", name)
			continue
		}
		if d.Annotations == nil || !d.Annotations.ReadOnlyHint {
			t.Errorf("%q should carry readOnlyHint", name)
		}
	}
	for _, name := range opsWriteTools {
		if _, ok := ro[name]; ok {
			t.Errorf("read-only catalog must not include mutating tool %q", name)
		}
	}

	rw := list(true)
	for _, name := range opsWriteTools {
		d, ok := rw[name]
		if !ok {
			t.Errorf("read-write catalog should include %q", name)
			continue
		}
		if d.Annotations != nil && d.Annotations.ReadOnlyHint {
			t.Errorf("%q must not carry readOnlyHint", name)
		}
	}
}

// fakePanel serves the routes the ops tools touch and records each request as
// "METHOD path?query" plus, for POSTs, the body.
type fakePanel struct {
	reqs   []string
	bodies []string
}

func (f *fakePanel) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.reqs = append(f.reqs, r.Method+" "+r.URL.RequestURI())
		if r.Method == http.MethodPost {
			b, _ := io.ReadAll(r.Body)
			f.bodies = append(f.bodies, string(b))
		}
		data := ""
		switch p := r.URL.Path; {
		case p == "/api/v1/workspaces":
			data = `[{"id":1,"name":"prod"}]`
		case p == "/api/v1/workspaces/prod":
			data = `{"id":1,"name":"prod"}`
		case p == "/api/v1/workspaces/prod/apps":
			data = `[{"id":7,"name":"web"}]`
		case p == "/api/v1/workspaces/prod/apps/web":
			data = `{"id":7,"name":"web"}`
		case p == "/api/v1/workspaces/prod/alerts":
			data = `[{"id":3,"category":"app","severity":"critical","state":"firing","title":"web is down","count":4,"subject_type":"app","subject_ref":"web"}]`
		case p == "/api/v1/workspaces/prod/analytics/summary":
			data = `{"totals":{"requests":2000,"error_rate":0.0125,"avg_latency_ms":40,"p95_latency_ms":120,"p99_latency_ms":300},"status":{"s2xx":1975,"s4xx":5,"s5xx":20},"series":[]}`
		case p == "/api/v1/workspaces/prod/databases":
			data = `[{"id":4,"name":"pg"}]`
		case p == "/api/v1/workspaces/prod/databases/4/databases":
			data = `[{"id":9,"name":"shop"}]`
		case p == "/api/v1/workspaces/prod/databases/4/databases/9/backups" && r.Method == http.MethodPost:
			data = `{"id":51,"number":12,"status":"completed","trigger":"manual","destination":"s3","size_bytes":1024,"comment":"before migration"}`
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":`+data+`}`)
	}
}

// callTool runs one tools/call against a server backed by the fake panel and
// returns the decoded JSON payload.
func callTool(t *testing.T, allowWrite bool, name string, args map[string]any) (*fakePanel, map[string]any, any) {
	t.Helper()
	fp := &fakePanel{}
	srv := httptest.NewServer(fp.handler(t))
	defer srv.Close()
	c, err := api.New(api.Options{BaseURL: srv.URL, Token: "t"})
	if err != nil {
		t.Fatal(err)
	}
	s := New(Options{Client: c, AllowWrite: allowWrite, FallbackWorkspace: "prod", Version: "test"})
	params, _ := json.Marshal(map[string]any{"name": name, "arguments": args})
	resp := s.handle(context.Background(), rpcRequest{JSONRPC: "2.0", ID: json.RawMessage("1"), Method: "tools/call", Params: params})
	var res callToolResult
	remarshal(t, resp.Result, &res)
	if res.IsError {
		t.Fatalf("%s returned an error: %+v", name, res.Content)
	}
	var out any
	if err := json.Unmarshal([]byte(res.Content[0].Text), &out); err != nil {
		t.Fatalf("decode %s payload: %v", name, err)
	}
	obj, _ := out.(map[string]any)
	return fp, obj, out
}

func TestListAlertsTool(t *testing.T) {
	fp, _, out := callTool(t, false, "list_alerts", map[string]any{})
	if !containsReq(fp.reqs, "GET /api/v1/workspaces/prod/alerts?active=true") {
		t.Errorf("active alerts not requested; got %v", fp.reqs)
	}
	alerts, _ := out.([]any)
	if len(alerts) != 1 || alerts[0].(map[string]any)["title"] != "web is down" {
		t.Errorf("unexpected alerts payload %v", out)
	}

	fp, _, _ = callTool(t, false, "list_alerts", map[string]any{"all": true})
	if !containsReq(fp.reqs, "GET /api/v1/workspaces/prod/alerts") {
		t.Errorf("all=true should drop the active filter; got %v", fp.reqs)
	}
}

func TestGetTrafficTool(t *testing.T) {
	fp, got, _ := callTool(t, false, "get_traffic", map[string]any{"app": "web", "range": "24h"})
	if !containsReq(fp.reqs, "GET /api/v1/workspaces/prod/analytics/summary?app=7&range=24h") {
		t.Errorf("summary not requested with the numeric app id; got %v", fp.reqs)
	}
	if got["error_rate_percent"] != 1.25 {
		t.Errorf("error_rate_percent = %v, want 1.25", got["error_rate_percent"])
	}
	if got["requests"] != float64(2000) || got["p95_latency_ms"] != float64(120) {
		t.Errorf("unexpected totals %v", got)
	}
	if st, _ := got["status"].(map[string]any); st["5xx"] != float64(20) {
		t.Errorf("status breakdown = %v", got["status"])
	}
}

func TestRunBackupTool(t *testing.T) {
	fp, got, _ := callTool(t, true, "run_backup", map[string]any{"database": "pg", "comment": "before migration"})
	if !containsReq(fp.reqs, "POST /api/v1/workspaces/prod/databases/4/databases/9/backups") {
		t.Errorf("backup not posted to the sole logical database; got %v", fp.reqs)
	}
	if len(fp.bodies) != 1 || !strings.Contains(fp.bodies[0], `"comment":"before migration"`) {
		t.Errorf("unexpected body %v", fp.bodies)
	}
	if got["status"] != "completed" || got["number"] != float64(12) {
		t.Errorf("unexpected backup %v", got)
	}
}

func containsReq(reqs []string, want string) bool {
	for _, r := range reqs {
		if r == want {
			return true
		}
	}
	return false
}
