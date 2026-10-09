// This file enforces the dashboard's process boundary
// (docs/prds/external-agent-harness.md, "Session contract", Transport): the
// dashboard must not link the container or daemon internals, because only the
// daemon owns containers. Go imports are atomic, so a single reference
// relinks a runtime, and the boundary holds only while something re-measures
// it. archie-core-h3s0 names this check; it was purged with the test suite and
// is restored here.

package main

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestDashboardDoesNotLinkContainerRuntime(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "go", "list", "-deps", ".").Output()
	if err != nil {
		t.Fatalf("go list -deps .: %v", err)
	}
	linked := map[string]bool{}
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		linked[line] = true
	}
	for _, pkg := range []string{
		"github.com/samcharles93/archie-core/internal/container",
		"github.com/samcharles93/archie-core/internal/app/archied",
		"github.com/samcharles93/archie-core/internal/agentexec",
	} {
		t.Run(pkg, func(t *testing.T) {
			if linked[pkg] {
				t.Errorf("cmd/archie-ui links %s: only the daemon owns containers", pkg)
			}
		})
	}
}
