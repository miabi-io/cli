package cmd

import (
	"testing"

	"github.com/miabi-io/cli/internal/api"
)

func TestParseDBChoices(t *testing.T) {
	plan := &api.MigrationPlan{Databases: []api.MigrationDBItem{
		{InstanceID: 3, InstanceName: "shop-db"},
		{InstanceID: 5, InstanceName: "cache"},
	}}
	got, err := parseDBChoices([]string{"shop-db=existing:42", "cache=move"}, plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != (api.MigrationDBChoice{InstanceID: 3, Strategy: "existing_instance", TargetInstanceID: 42}) ||
		got[1] != (api.MigrationDBChoice{InstanceID: 5, Strategy: "move"}) {
		t.Errorf("got %+v", got)
	}
	for _, bad := range []string{"shop-db", "nope=move", "shop-db=teleport", "shop-db=existing", "shop-db=existing:x"} {
		if _, err := parseDBChoices([]string{bad}, plan); err == nil {
			t.Errorf("%q should be refused", bad)
		}
	}
}
