package archied

import (
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/domain/workintake"
	"github.com/samcharles93/archie-core/internal/store"
	"github.com/samcharles93/archie-core/internal/webui"
)

// TestBuildDaemonCarriesWorkflowRoutingBindings pins the composition hop in the
// cross-process routing fix: the resolved bindings loaded by loadWorkflowRouting
// must survive buildDaemon onto the daemon that puts them into taskrun.Request.
//
// Without this, a nil here reverts the entire fix while every other test stays
// green: the daemon test sets d.KindWorkflows itself, and the worker test drives
// routeTask directly, so neither observes how the daemon obtained them. That is
// the "tested in isolation, never verified wired" shape this repo has been bitten
// by before.
func TestBuildDaemonCarriesWorkflowRoutingBindings(t *testing.T) {
	storeA, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "store-a.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storeA.Close() })
	storeB := openSecondStore(t)

	b := &boot{
		cfg:            config.Config{},
		log:            slog.New(slog.DiscardHandler),
		st:             storeA,
		stateStore:     storeB,
		web:            &webui.Server{},
		kindWorkflows:  workflow.KindWorkflows{workintake.KindBug: "custom-bug"},
		labelWorkflows: workflow.LabelWorkflows{"security": "security-review"},
	}
	b.buildDaemon()

	if got := b.d.KindWorkflows[workintake.KindBug]; got != "custom-bug" {
		t.Fatalf("Daemon.KindWorkflows[bug] = %q, want %q; buildDaemon dropped the kind routing bindings, so the worker would route with built-in defaults", got, "custom-bug")
	}
	if got := b.d.LabelWorkflows["security"]; got != "security-review" {
		t.Fatalf("Daemon.LabelWorkflows[security] = %q, want %q; buildDaemon dropped the label routing bindings", got, "security-review")
	}
}
