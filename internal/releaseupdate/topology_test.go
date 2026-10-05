package releaseupdate

import (
	"reflect"
	"testing"
)

func TestMigrationPlan(t *testing.T) {
	topology := Topology{Units: []string{"state", "gateway", "ui", "daemon"}, ConfigSections: []string{"services.state"}}
	for _, tc := range []struct {
		name            string
		units, sections map[string]bool
		want            []string
	}{
		{"unknown", nil, nil, []string{"configure [services.state]", "install and enable state.service", "install and enable gateway.service", "install and enable ui.service", "install and enable daemon.service"}},
		{"missing", map[string]bool{"state": true, "daemon": true}, map[string]bool{"services.state": true}, []string{"install and enable gateway.service", "install and enable ui.service"}},
		{"complete", map[string]bool{"state": true, "gateway": true, "ui": true, "daemon": true}, map[string]bool{"services.state": true}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := topology.MigrationPlan(tc.units, tc.sections); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
