package workflow

import (
	"testing"

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
