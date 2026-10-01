package cmd

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/miabi-io/cli/internal/api"
)

// A server that predates ?wait answers with a pending deployment; --wait must
// then fall back to polling.
func TestSettleDeploymentPollsWhenServerDidNotWait(t *testing.T) {
	polled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/workspaces/prod/apps/7/deployments/5" {
			t.Errorf("unexpected request %s", r.URL.Path)
		}
		polled = true
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":{"id":5,"number":3,"status":"succeeded"}}`)
	}))
	defer srv.Close()
	c, err := api.New(api.Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}

	pending := &api.Deployment{ID: 5, Number: 3, Status: "pending"}
	final, err := settleDeployment(context.Background(), c, "prod", 7, pending, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !polled || final.Status != api.StatusSucceeded {
		t.Errorf("polled=%v final=%+v", polled, final)
	}

	settled := &api.Deployment{ID: 5, Number: 3, Status: api.StatusCanary}
	polled = false
	if got, err := settleDeployment(context.Background(), c, "prod", 7, settled, time.Now().Add(time.Minute)); err != nil || got != settled || polled {
		t.Errorf("a settled deployment must be used as-is: %v, %v, polled=%v", got, err, polled)
	}
}
