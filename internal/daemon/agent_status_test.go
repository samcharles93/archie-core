package daemon

import "testing"

func TestAgentStatusSnapshotBeforeObserveIsUnverified(t *testing.T) {
	var s AgentStatus
	if version, installType, ok := s.Snapshot(); ok || version != "" || installType != "" {
		t.Fatalf("Snapshot() = (%q, %q, %v), want zero values and ok=false before any Observe", version, installType, ok)
	}
}

func TestAgentStatusObserveThenSnapshot(t *testing.T) {
	var s AgentStatus
	s.Observe("1.9.11", "container")

	version, installType, ok := s.Snapshot()
	if !ok || version != "1.9.11" || installType != "container" {
		t.Fatalf("Snapshot() = (%q, %q, %v), want (\"1.9.11\", \"container\", true)", version, installType, ok)
	}
}

func TestAgentStatusObserveOverwritesPrevious(t *testing.T) {
	var s AgentStatus
	s.Observe("1.9.10", "container")
	s.Observe("1.9.11", "container")

	version, _, _ := s.Snapshot()
	if version != "1.9.11" {
		t.Fatalf("Snapshot() version = %q, want the most recent observation %q", version, "1.9.11")
	}
}
