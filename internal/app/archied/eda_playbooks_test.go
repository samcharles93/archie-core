package archied

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/eda/module"
	"github.com/samcharles93/archie-core/internal/domain/eda/playbook"
)

type stubPlaybookLedger struct{}

func (stubPlaybookLedger) RecordPlaybookDispatch(context.Context, string, string, string, string) error {
	return nil
}

func (stubPlaybookLedger) DeletePlaybookDispatches(context.Context, string) error { return nil }

func loadBootPlaybooks(t *testing.T, document string) *playbook.Store {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pb.yaml"), []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	b := &boot{cfg: config.Config{EDAPlaybookDir: dir}, modules: module.New()}
	if err := b.loadEDAPlaybooks(b.cfg, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatalf("loadEDAPlaybooks: %v", err)
	}
	return b.playbooks
}

const actionPlaybookDoc = `trigger:
  kind: bug
actions:
  - position: module
    kind: log
    args:
      message: '"build started"'
`

// An action playbook never runs without the dispatch ledger, and the
// operator is told which ones are inert rather than finding out by silence.
func TestPlaybookLedger(t *testing.T) {
	tests := []struct {
		name       string
		source     any
		document   string
		wantLedger bool
		wantWarn   bool
	}{
		{name: "ledger wired", source: stubPlaybookLedger{}, document: actionPlaybookDoc, wantLedger: true},
		{name: "no ledger warns per action playbook", source: struct{}{}, document: actionPlaybookDoc, wantWarn: true},
		{name: "no ledger, workflow playbook only", source: struct{}{}, document: "trigger:\n  kind: bug\nactions:\n  - position: workflow\n    workflow: tdd\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			got := playbookLedger(tt.source, loadBootPlaybooks(t, tt.document), slog.New(slog.NewTextHandler(&buf, nil)))
			if (got != nil) != tt.wantLedger {
				t.Fatalf("ledger = %v, want wired=%v", got, tt.wantLedger)
			}
			out := buf.String()
			warned := strings.Contains(out, "level=WARN") && strings.Contains(out, "pb.yaml") && strings.Contains(out, "will not run")
			if warned != tt.wantWarn {
				t.Fatalf("warning output = %q, want warned=%v", out, tt.wantWarn)
			}
		})
	}
}
