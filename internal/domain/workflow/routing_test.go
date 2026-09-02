package workflow

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/workintake"
	"github.com/samcharles93/archie-core/internal/store"
)

func TestRouteDoesNotSilentlyReplaceAnAdmittedWorkflow(t *testing.T) {
	task := &store.Task{Workflow: "skill-review"}
	wf := Route(task, Registry{"implement": Implement()})
	if wf.Name != "none" || len(wf.Stages) != 1 || wf.Stages[0].Name != "fail" {
		t.Fatalf("Route() = %#v, want explicit failure workflow", wf)
	}
}

// TestRoutePrefersTriageOverImplementWhenNoLabelMatches is the regression
// case for archie-core-enfj: an unlabeled task (the common shape for a
// chat-spawned task) with no explicit workflow must reach triage, not the
// heaviest workflow, when triage is registered.
func TestRoutePrefersTriageOverImplementWhenNoLabelMatches(t *testing.T) {
	task := &store.Task{}
	wf := Route(task, Registry{"implement": Implement(), "triage": Triage()})
	if wf.Name != "triage" {
		t.Fatalf("Route() = %q, want %q", wf.Name, "triage")
	}
}

// TestRouteFallsBackToImplementWhenTriageIsNotRegistered pins backward
// compatibility: a registry without a "triage" entry must behave exactly
// as it did before triage existed.
func TestRouteFallsBackToImplementWhenTriageIsNotRegistered(t *testing.T) {
	task := &store.Task{}
	wf := Route(task, Registry{"implement": Implement()})
	if wf.Name != "implement" {
		t.Fatalf("Route() = %q, want %q", wf.Name, "implement")
	}
}

// TestRouteLabelMatchStillWinsOverTriage confirms labels remain the
// preferred, free signal: a labeled task never pays for a triage
// classification call even when triage is registered.
func TestRouteLabelMatchStillWinsOverTriage(t *testing.T) {
	task := &store.Task{Labels: "bug"}
	wf := Route(task, Registry{"tdd": TDD(), "triage": Triage(), "implement": Implement()})
	if wf.Name != "tdd" {
		t.Fatalf("Route() = %q, want %q", wf.Name, "tdd")
	}
}

// TestLoadKindWorkflowsYAMLOverridesDefaultBinding is the first slice of
// docs/prds/eda-playbook-engine.md: which workflow a Kind prefers becomes
// data, not a Go literal. A YAML file rebinding "bug" to "feasibility"
// must be honoured by Route() once loaded.
func TestLoadKindWorkflowsYAMLOverridesDefaultBinding(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "workflow-routing.yaml")
	writeFile(t, path, "bug: feasibility\n")

	kw, err := LoadKindWorkflowsYAML(path)
	if err != nil {
		t.Fatalf("LoadKindWorkflowsYAML() error = %v", err)
	}
	SetKindWorkflows(kw)
	t.Cleanup(func() { SetKindWorkflows(nil) })

	task := &store.Task{Labels: "bug"}
	wf := Route(task, Registry{"tdd": TDD(), "feasibility": Feasibility()})
	if wf.Name != "feasibility" {
		t.Fatalf("Route() = %q, want %q (YAML override not applied)", wf.Name, "feasibility")
	}
}

// TestSetKindWorkflowsNilRestoresDefaults pins the fallback: no file, or
// an explicit nil override, must reproduce today's hardcoded bindings
// unchanged -- required for backward compatibility per the design doc.
func TestSetKindWorkflowsNilRestoresDefaults(t *testing.T) {
	SetKindWorkflows(KindWorkflows{workintake.KindBug: "feasibility"})
	SetKindWorkflows(nil)

	task := &store.Task{Labels: "bug"}
	wf := Route(task, Registry{"tdd": TDD(), "feasibility": Feasibility()})
	if wf.Name != "tdd" {
		t.Fatalf("Route() = %q, want %q (default binding not restored)", wf.Name, "tdd")
	}
}

// TestLoadKindWorkflowsYAMLRejectsUnknownKind ensures a typo'd or invalid
// kind in the YAML is a reported load failure, not a silently-ignored
// binding -- the "schema defines the accepted message; anything else is
// the caller's fault, dropped and logged" rule from the design doc.
func TestLoadKindWorkflowsYAMLRejectsUnknownKind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "workflow-routing.yaml")
	writeFile(t, path, "not-a-real-kind: tdd\n")

	if _, err := LoadKindWorkflowsYAML(path); err == nil {
		t.Fatal("LoadKindWorkflowsYAML() error = nil, want error for unknown kind")
	}
}

// TestLoadKindWorkflowsYAMLMissingFileReturnsNilNoError treats an unset
// routing file as "use defaults", matching SkillsDir's own "empty means
// built-ins only" convention rather than requiring callers to stat first.
func TestLoadKindWorkflowsYAMLMissingFileReturnsNilNoError(t *testing.T) {
	kw, err := LoadKindWorkflowsYAML("")
	if err != nil {
		t.Fatalf("LoadKindWorkflowsYAML(\"\") error = %v, want nil", err)
	}
	if kw != nil {
		t.Fatalf("LoadKindWorkflowsYAML(\"\") = %#v, want nil", kw)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// ─── Label vocabulary layer (arbitrary labels → workflow) ───────────────

// TestRouteWithArbitraryLabelDispatch is the core of this slice: a forge
// label outside the closed bug/feature/bootstrap kind set (e.g. "security")
// routes to a registered workflow via the loaded LabelWorkflows map --
// the label vocabulary becomes playbook data, not a Go literal, without
// touching the closed Kind/NATS-subject set.
func TestRouteWithArbitraryLabelDispatch(t *testing.T) {
	SetLabelWorkflows(LabelWorkflows{"security": "security-review"})
	t.Cleanup(func() { SetLabelWorkflows(nil) })

	task := &store.Task{Labels: "security"}
	wf := Route(task, Registry{
		"implement":       Implement(),
		"security-review": Workflow{Name: "security-review", Stages: []Stage{{Name: "review", Run: func(_ context.Context, _ *TaskContext) error { return nil }}}},
	})
	if wf.Name != "security-review" {
		t.Fatalf("Route() = %q, want %q (label dispatch not applied)", wf.Name, "security-review")
	}
}

// TestLoadLabelWorkflowsYAMLReadsBindings ensures a label→workflow file
// loads into a map Route() honours.
func TestLoadLabelWorkflowsYAMLReadsBindings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "labels.yaml")
	writeFile(t, path, "security: security-review\ndocs: docs-notify\n")

	lw, err := LoadLabelWorkflowsYAML(path)
	if err != nil {
		t.Fatalf("LoadLabelWorkflowsYAML() error = %v", err)
	}
	if lw["security"] != "security-review" || lw["docs"] != "docs-notify" {
		t.Fatalf("LoadLabelWorkflowsYAML() = %#v, want both bindings", lw)
	}
}

// TestLoadLabelWorkflowsYAMLCollisionIsReported is the collision rule from
// the design doc: two bindings for the same label, or a label colliding
// with the closed kind set, is the caller's fault -- reported as an error,
// nothing silently wins by version or by declaration order.
func TestLoadLabelWorkflowsYAMLCollisionIsReported(t *testing.T) {
	dir := t.TempDir()

	// Same label twice inside one file: neither declared with higher
	// schema/version wins; the load fails.
	path := filepath.Join(dir, "dup.yaml")
	writeFile(t, path, "security: workflow-a\nsecurity: workflow-b\n")
	if _, err := LoadLabelWorkflowsYAML(path); err == nil {
		t.Fatal("LoadLabelWorkflowsYAML() error = nil, want collision error for duplicate label")
	}

	// A label that the built-in kind set owns (bug) is itself a collision:
	// KindForLabels already routes it; shadowing it here would be two
	// authorities claiming one label.
	path2 := filepath.Join(dir, "shadow.yaml")
	writeFile(t, path2, "bug: custom-bug\n")
	if _, err := LoadLabelWorkflowsYAML(path2); err == nil {
		t.Fatal("LoadLabelWorkflowsYAML() error = nil, want error for label owned by kind set")
	}
}

// TestLoadLabelWorkflowsYAMLRejectsEmpties pins the schema gate: an empty
// label or empty workflow name is not an accepted message.
func TestLoadLabelWorkflowsYAMLRejectsEmpties(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.yaml")
	writeFile(t, path, "\"\": security-review\n")
	if _, err := LoadLabelWorkflowsYAML(path); err == nil {
		t.Fatal("LoadLabelWorkflowsYAML() error = nil, want error for empty label")
	}
	path2 := filepath.Join(dir, "empty-wf.yaml")
	writeFile(t, path2, "security: \"\"\n")
	if _, err := LoadLabelWorkflowsYAML(path2); err == nil {
		t.Fatal("LoadLabelWorkflowsYAML() error = nil, want error for empty workflow name")
	}
}

// TestLoadLabelWorkflowsYAMLMissingFileReturnsNilNoError treats an unset
// playbook dir as "no label bindings", matching the kind layer's convention.
func TestLoadLabelWorkflowsYAMLMissingFileReturnsNilNoError(t *testing.T) {
	lw, err := LoadLabelWorkflowsYAML("")
	if err != nil {
		t.Fatalf("LoadLabelWorkflowsYAML(\"\") error = %v, want nil", err)
	}
	if lw != nil {
		t.Fatalf("LoadLabelWorkflowsYAML(\"\") = %#v, want nil", lw)
	}
}

// TestSetLabelWorkflowsNilRestoresDefaults pins that clearing the label map
// restores kind-only routing: an arbitrary label no longer routes.
func TestSetLabelWorkflowsNilRestoresDefaults(t *testing.T) {
	SetLabelWorkflows(LabelWorkflows{"security": "security-review"})
	SetLabelWorkflows(nil)

	task := &store.Task{Labels: "security"}
	wf := Route(task, Registry{"implement": Implement(), "security-review": Workflow{Name: "security-review", Stages: []Stage{{Name: "review", Run: func(_ context.Context, _ *TaskContext) error { return nil }}}}})
	if wf.Name != "implement" {
		t.Fatalf("Route() = %q, want %q (label map not restored to empty)", wf.Name, "implement")
	}
}

// TestRoutePrefersArbitraryLabelOverKind pins precedence: for a task
// carrying both a kind label and an arbitrary label, the arbitrary-label
// binding (explicitly loaded) wins over the kind default.
func TestRoutePrefersArbitraryLabelOverKind(t *testing.T) {
	SetLabelWorkflows(LabelWorkflows{"security": "security-review"})
	t.Cleanup(func() { SetLabelWorkflows(nil) })
	SetKindWorkflows(nil) // ensure defaults
	t.Cleanup(func() { SetKindWorkflows(nil) })

	task := &store.Task{Labels: "bug,security"}
	wf := Route(task, Registry{
		"tdd":             TDD(),
		"implement":       Implement(),
		"security-review": Workflow{Name: "security-review", Stages: []Stage{{Name: "review", Run: func(_ context.Context, _ *TaskContext) error { return nil }}}},
	})
	if wf.Name != "security-review" {
		t.Fatalf("Route() = %q, want %q (arbitrary label should beat kind default)", wf.Name, "security-review")
	}
}
