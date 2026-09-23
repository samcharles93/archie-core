package workflow

import (
	"path/filepath"
	"strings"
	"testing"
)

// A key-level finding names the file and the line of the offending key, so
// the lint tool reports it compiler-style instead of leaving the author to
// search the file.
func TestBindingFindingsCarryTheKeyLine(t *testing.T) {
	tests := []struct {
		name    string
		content string
		load    func(path string) error
		line    string
	}{
		{
			name:    "playbook dir, empty workflow name",
			content: "# routes\nsecurity: security-review\ndocs: \"\"\n",
			load: func(path string) error {
				_, _, err := LoadPlaybookDirs([]string{filepath.Dir(path)})
				return err
			},
			line: ":3:",
		},
		{
			name:    "playbook dir, kind with no workflow name",
			content: "security: security-review\n\n\nbug: \"\"\n",
			load: func(path string) error {
				_, _, err := LoadPlaybookDirs([]string{filepath.Dir(path)})
				return err
			},
			line: ":4:",
		},
		{
			name:    "label file, kind-owned label",
			content: "security: security-review\nfeature: other\n",
			load: func(path string) error {
				_, err := LoadLabelWorkflowsYAML(path)
				return err
			},
			line: ":2:",
		},
		{
			name:    "kind file, unknown kind",
			content: "bug: tdd\n\nnot-a-kind: x\n",
			load: func(path string) error {
				_, err := LoadKindWorkflowsYAML(path)
				return err
			},
			line: ":3:",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "routes.yaml")
			writeFile(t, path, tt.content)
			err := tt.load(path)
			if err == nil {
				t.Fatal("load error = nil, want a finding")
			}
			if want := path + tt.line; !strings.Contains(err.Error(), want) {
				t.Fatalf("error = %q, want it to name %q", err, want)
			}
		})
	}
}
