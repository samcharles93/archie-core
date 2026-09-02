package workflow

import (
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
