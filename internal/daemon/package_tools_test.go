package daemon

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/storepkg"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
)

type acceptedTools map[string]storepkg.Authority

func (a acceptedTools) AcceptedAuthority(_ context.Context, name string) (storepkg.Authority, error) {
	if authority, ok := a[name]; ok {
		return authority, nil
	}
	return storepkg.Authority{}, errors.New("not installed")
}

// TestToolAllowlistFollowsPackageAuthority pins that a package workflow is
// offered only the tools accepted for its package, and none when the package
// is missing or unaccepted, while an operator workflow keeps its profile's list.
func TestToolAllowlistFollowsPackageAuthority(t *testing.T) {
	d := &Daemon{Log: slog.Default(), PackageAuthorities: acceptedTools{
		"triage":   {Tools: []string{"search", "fetch"}},
		"unsigned": {},
	}}
	tests := []struct {
		name    string
		yaml    string
		profile []string
		want    []string
	}{
		{"operator workflow without a profile list", "id: a\n", nil, nil},
		{"operator workflow keeps its profile list", "id: a\n", []string{"x"}, []string{"x"}},
		{"package workflow gets its accepted tools", "package: triage\nid: a\n", nil, []string{"search", "fetch"}},
		{"profile narrows the accepted tools", "package: triage\nid: a\n", []string{"fetch", "shell"}, []string{"fetch"}},
		{"profile cannot widen the accepted tools", "package: triage\nid: a\n", []string{"shell"}, []string{}},
		{"package with nothing accepted", "package: unsigned\nid: a\n", nil, []string{}},
		{"package that is gone", "package: gone\nid: a\n", nil, []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := d.toolAllowlist(context.Background(), &workflow.Task{WorkflowDefinitionYAML: tt.yaml}, tt.profile)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %#v, want %#v", got, tt.want)
			}
		})
	}
}
