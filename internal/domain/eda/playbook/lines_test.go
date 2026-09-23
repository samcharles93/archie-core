package playbook

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// An action-level finding names the line of the offending action, `when`
// or args key, compiler-style, so the lint tool and the language server can
// point at it.
func TestLoadFindingsCarryTheOffendingLine(t *testing.T) {
	const head = "trigger:\n  kind: bug\nactions:\n"
	tests := []struct {
		name    string
		actions string
		line    int
	}{
		{
			name:    "unknown kind on the second action",
			actions: "  - position: module\n    kind: log\n  - position: module\n    kind: nope\n",
			line:    6,
		},
		{
			name:    "when that does not compile",
			actions: "  - position: module\n    kind: log\n    when: 'event.x =='\n",
			line:    6,
		},
		{
			name:    "args value of the wrong type",
			actions: "  - position: module\n    kind: log\n    args:\n      level: '\"info\"'\n      message: '123'\n",
			line:    8,
		},
		{
			name:    "args key the kind does not declare",
			actions: "  - position: module\n    kind: log\n    args:\n      mesage: '\"x\"'\n",
			line:    7,
		},
		{
			name:    "workflow action naming no workflow",
			actions: "  - position: workflow\n    when: 'true'\n",
			line:    4,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := writeFile(t, dir, "pb.yaml", head+tt.actions)
			_, err := Load(dir, testSchemas(t))
			if err == nil {
				t.Fatal("Load = nil, want a finding")
			}
			if want := filepath.Clean(path) + ":" + strconv.Itoa(tt.line) + ":"; !strings.Contains(err.Error(), want) {
				t.Fatalf("Load error = %q, want it to name %q", err, want)
			}
		})
	}
}
