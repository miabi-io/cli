package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestWorkspaceEventsQuery(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.RequestURI(); got != "/api/v1/workspaces/prod/events?page=0&size=100&order=desc" {
			t.Errorf("unexpected request %s", got)
		}
		writeData(w, `[{"id":1,"type":"deploy_failed","severity":"error","message":"boom","app_name":"web"}]`)
	})
	es, err := c.WorkspaceEvents(context.Background(), "prod", 500)
	if err != nil {
		t.Fatal(err)
	}
	if len(es) != 1 || es[0].AppName != "web" {
		t.Errorf("unexpected events %+v", es)
	}
}

func TestResolveLogicalDatabaseID(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeData(w, `[{"id":9,"name":"shop"},{"id":10,"name":"auth"}]`)
	})
	ctx := context.Background()
	if id, err := c.ResolveLogicalDatabaseID(ctx, "prod", 4, "auth"); err != nil || id != 10 {
		t.Errorf("by name: %d, %v", id, err)
	}
	if id, err := c.ResolveLogicalDatabaseID(ctx, "prod", 4, "9"); err != nil || id != 9 {
		t.Errorf("by id: %d, %v", id, err)
	}
	if _, err := c.ResolveLogicalDatabaseID(ctx, "prod", 4, ""); err == nil || !strings.Contains(err.Error(), "shop") {
		t.Errorf("an ambiguous instance should list its databases, got %v", err)
	}
}
