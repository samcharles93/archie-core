package archied

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/eda/module"
)

// TestLoadEDAPlaybooksWarnsOnActionPlaybook pins the D1 visible warning: an
// action playbook loads and validates but has no run path yet, so the daemon
// logs a warning (not debug) naming it and stating plainly that action
// playbooks do not execute yet -- rather than silently loading a playbook that
// can never be routed.
func TestLoadEDAPlaybooksWarnsOnActionPlaybook(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pb.yaml"), []byte(`trigger:
  kind: bug
actions:
  - position: module
    kind: log
    args:
      message: '"build started"'
`), 0o600); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	b := &boot{cfg: config.Config{EDAPlaybookDir: dir}, modules: module.New()}
	if err := b.loadEDAPlaybooks(b.cfg, log); err != nil {
		t.Fatalf("loadEDAPlaybooks: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "pb.yaml") {
		t.Errorf("warning output does not name the action playbook: %q", out)
	}
	if !strings.Contains(out, "do not execute yet") {
		t.Errorf("warning output does not state action playbooks do not execute yet: %q", out)
	}
	if strings.Contains(out, "level=DEBUG") {
		t.Errorf("warning output was logged at debug, want a visible warning: %q", out)
	}
}
