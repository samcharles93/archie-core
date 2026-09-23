package archieplaybooks

import (
	"io"
	"path/filepath"
	"strings"
	"testing"
)

// LintEDA refuses exactly what the daemon's EDA loader refuses at startup.
func TestLintEDA(t *testing.T) {
	tests := []struct {
		name     string
		document string
		wantErr  string
	}{
		{name: "valid action playbook", document: "trigger:\n  kind: bug\nactions:\n  - position: module\n    kind: log\n    args:\n      message: '\"hi\"'\n"},
		{name: "unknown kind", document: "trigger:\n  kind: bug\nactions:\n  - position: module\n    kind: nope\n", wantErr: "nope"},
		{name: "wrongly typed arg", document: "trigger:\n  kind: bug\nactions:\n  - position: module\n    kind: log\n    args:\n      message: '123'\n", wantErr: `args["message"]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, filepath.Join(dir, "pb.yaml"), tt.document)
			result := LintEDA(dir, io.Discard)
			if tt.wantErr == "" {
				if result.ExitCode != 0 {
					t.Fatalf("LintEDA exit = %d, findings %q, want clean", result.ExitCode, result.Findings)
				}
				return
			}
			joined := strings.Join(result.Findings, "\n")
			if result.ExitCode != 1 || !strings.Contains(joined, "pb.yaml") || !strings.Contains(joined, tt.wantErr) {
				t.Fatalf("LintEDA = exit %d, findings %q, want exit 1 naming pb.yaml and %q", result.ExitCode, joined, tt.wantErr)
			}
		})
	}
}
