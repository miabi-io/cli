package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := New(Options{BaseURL: srv.URL, Token: "t"})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func writeData(w http.ResponseWriter, data string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"success":true,"data":`+data+`}`)
}

func TestDeploySendsWaitAndReadsTimeoutHeader(t *testing.T) {
	var gotWait string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotWait = r.URL.Query().Get("wait")
		w.Header().Set("X-Miabi-Wait", "timeout")
		writeData(w, `{"id":5,"number":3,"status":"deploying"}`)
	})
	res, err := c.Deploy(context.Background(), "prod", "7", DeployRequest{}, 2*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if gotWait != "900" {
		t.Errorf("wait = %q, want the server's 900s cap", gotWait)
	}
	if res.Deployment == nil || res.Deployment.ID != 5 || !res.WaitTimedOut {
		t.Errorf("unexpected result %+v", res)
	}
}

func TestDeployWithoutWaitOmitsParam(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			t.Errorf("unexpected query %q", r.URL.RawQuery)
		}
		writeData(w, `{"id":5,"number":3,"status":"pending"}`)
	})
	res, err := c.Deploy(context.Background(), "prod", "7", DeployRequest{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.WaitTimedOut || res.Deployment.Status != "pending" {
		t.Errorf("unexpected result %+v", res)
	}
}

// A pipeline-backed app answers with the run; decoding it as a Deployment would
// leave the caller waiting on deployment #0.
func TestDeployDecodesPipelineRun(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeData(w, `{"kind":"pipeline_run","run":{"id":40,"number":6,"status":"pending"}}`)
	})
	res, err := c.Deploy(context.Background(), "prod", "7", DeployRequest{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != "pipeline_run" || res.Deployment != nil || res.Run == nil || res.Run.ID != 40 {
		t.Errorf("unexpected result %+v", res)
	}
}

func TestDeploymentUsesSingleGet(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/workspaces/prod/apps/7/deployments/5" {
			t.Errorf("unexpected request %s", r.URL.Path)
		}
		writeData(w, `{"id":5,"number":3,"status":"canary"}`)
	})
	d, err := c.Deployment(context.Background(), "prod", "7", 5)
	if err != nil {
		t.Fatal(err)
	}
	if d.Status != StatusCanary {
		t.Errorf("status = %q", d.Status)
	}
}

func TestDeploymentFallsBackToListOnOlderServer(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/workspaces/prod/apps/7/deployments/5":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"success":false,"error":{"status_code":404,"code":"NOT_FOUND","message":"route not found"}}`)
		case "/api/v1/workspaces/prod/apps/7/deployments":
			writeData(w, `[{"id":6,"number":4,"status":"pending"},{"id":5,"number":3,"status":"succeeded"}]`)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
		}
	})
	d, err := c.Deployment(context.Background(), "prod", "7", 5)
	if err != nil {
		t.Fatal(err)
	}
	if d.Number != 3 || d.Status != StatusSucceeded {
		t.Errorf("unexpected deployment %+v", d)
	}
}

func TestWaitForDeployStopsAtCanary(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeData(w, `{"id":5,"number":3,"status":"canary"}`)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	d, err := c.WaitForDeploy(ctx, "prod", "7", 5, nil)
	if err != nil {
		t.Fatal(err)
	}
	if d.Status != StatusCanary {
		t.Errorf("status = %q", d.Status)
	}
}

func TestWaitForPipelineRun(t *testing.T) {
	old := deployPollInterval
	deployPollInterval = time.Millisecond
	defer func() { deployPollInterval = old }()

	calls := 0
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/workspaces/prod/pipeline-runs/40" {
			t.Errorf("unexpected request %s", r.URL.Path)
		}
		calls++
		status := "running"
		if calls >= 3 {
			status = "failed"
		}
		writeData(w, `{"id":40,"number":6,"status":"`+status+`","error":"step build failed"}`)
	})
	var seen []string
	r, err := c.WaitForPipelineRun(context.Background(), "prod", 40, func(s string) { seen = append(seen, s) })
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "failed" || r.Error == "" {
		t.Errorf("unexpected run %+v", r)
	}
	if strings.Join(seen, ",") != "running,failed" {
		t.Errorf("updates = %v", seen)
	}
}

func TestScopeRefusalNamesTheRemedy(t *testing.T) {
	tests := []struct {
		scope string
		want  string
	}{
		{"deploy", "miabi login --scopes"},
		{"admin", "web console"},
	}
	for _, tt := range tests {
		c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `{"success":false,"error":{"status_code":403,"code":"FORBIDDEN","message":"API key missing required scope: `+tt.scope+`"}}`)
		})
		_, err := c.Deploy(context.Background(), "prod", "7", DeployRequest{}, 0)
		var se *ScopeError
		if !errors.As(err, &se) || se.Scope != tt.scope {
			t.Fatalf("want a ScopeError for %q, got %v", tt.scope, err)
		}
		if !strings.Contains(err.Error(), tt.want) {
			t.Errorf("error %q does not mention %q", err, tt.want)
		}
		var ae *APIError
		if !errors.As(err, &ae) || ae.StatusCode != http.StatusForbidden {
			t.Errorf("ScopeError should unwrap to the 403 APIError")
		}
	}
}

// A role refusal is also a 403 but is not about the token's scopes.
func TestRoleRefusalIsNotAScopeError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"success":false,"error":{"status_code":403,"code":"FORBIDDEN","message":"insufficient workspace role"}}`)
	})
	_, err := c.Apps(context.Background(), "prod")
	var se *ScopeError
	if err == nil || errors.As(err, &se) {
		t.Errorf("want a plain APIError, got %v", err)
	}
}

func TestResolveWorkspaceWithBoundKey(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/workspaces":
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `{"success":false,"error":{"status_code":403,"message":"this API key is scoped to a workspace and cannot be used on account-level endpoints"}}`)
		case "/api/v1/me":
			_, _ = io.WriteString(w, `{"success":true,"data":{"id":4,"auth":{"method":"api_key","workspace_id":2}}}`)
		case "/api/v1/workspaces/2", "/api/v1/workspaces/ops":
			_, _ = io.WriteString(w, `{"success":true,"data":{"id":2,"name":"ops"}}`)
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"success":false,"error":{"status_code":404,"message":"workspace not found"}}`)
		}
	})
	for _, ref := range []string{"", "ops", "2"} {
		if got, err := c.ResolveWorkspaceName(context.Background(), ref, ""); err != nil || got != "ops" {
			t.Errorf("ResolveWorkspaceName(%q) = %q, %v", ref, got, err)
		}
	}
	if _, err := c.ResolveWorkspaceName(context.Background(), "nope", ""); err == nil {
		t.Error("unknown workspace resolved")
	}
}
