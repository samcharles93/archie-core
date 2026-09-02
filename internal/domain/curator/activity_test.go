package curator

import (
	"testing"
	"time"
)

func TestActivityTrackerRecordAndSnapshot(t *testing.T) {
	tr := newActivityTracker()
	at := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	actions := []Action{
		{At: at, Type: "skill.updated", Detail: "rewrote foo", Reason: "stale"},
		{At: at, Type: "memory.written", Detail: "noted bar", Reason: "input"},
	}

	tr.record("curator-a", at, actions)

	got, ok := tr.snapshot("curator-a")
	if !ok {
		t.Fatalf("snapshot(curator-a) ok = false, want true")
	}
	if !got.LastRunAt.Equal(at) {
		t.Fatalf("LastRunAt = %v, want %v", got.LastRunAt, at)
	}
	if got.LastRunActions != len(actions) {
		t.Fatalf("LastRunActions = %d, want %d", got.LastRunActions, len(actions))
	}
	if len(got.Recent) != len(actions) {
		t.Fatalf("Recent len = %d, want %d", len(got.Recent), len(actions))
	}
	// Newest-first within a single run: the last action recorded comes first.
	if got.Recent[0].Type != "memory.written" {
		t.Fatalf("Recent[0].Type = %q, want %q", got.Recent[0].Type, "memory.written")
	}
}

func TestActivityTrackerUnknownCuratorNotFound(t *testing.T) {
	tr := newActivityTracker()
	if _, ok := tr.snapshot("nope"); ok {
		t.Fatalf("snapshot(nope) ok = true, want false")
	}
}

func TestActivityTrackerAccumulatesAcrossRuns(t *testing.T) {
	tr := newActivityTracker()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	tr.record("c", base, []Action{{At: base, Type: "first"}})
	second := base.Add(time.Hour)
	tr.record("c", second, []Action{{At: second, Type: "second"}})

	got, ok := tr.snapshot("c")
	if !ok {
		t.Fatalf("snapshot(c) ok = false, want true")
	}
	if !got.LastRunAt.Equal(second) {
		t.Fatalf("LastRunAt = %v, want %v", got.LastRunAt, second)
	}
	if got.LastRunActions != 1 {
		t.Fatalf("LastRunActions = %d, want 1", got.LastRunActions)
	}
	if len(got.Recent) != 2 {
		t.Fatalf("Recent len = %d, want 2", len(got.Recent))
	}
	if got.Recent[0].Type != "second" {
		t.Fatalf("Recent[0].Type = %q, want %q (newest first)", got.Recent[0].Type, "second")
	}
	if got.Recent[1].Type != "first" {
		t.Fatalf("Recent[1].Type = %q, want %q", got.Recent[1].Type, "first")
	}
}

// A curator engine that has no clock of its own (skillcurator) leaves
// Action.At zero. record must backfill it from the pass's own timestamp so
// it never reaches the wire as time.Time's zero value, which the dashboard
// renders as a nonsense "105694 wks ago".
func TestActivityTrackerBackfillsZeroActionTime(t *testing.T) {
	tr := newActivityTracker()
	at := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	tr.record("c", at, []Action{{Type: "skill.normalized", Detail: "d", Reason: "r"}})

	got, ok := tr.snapshot("c")
	if !ok {
		t.Fatalf("snapshot(c) ok = false, want true")
	}
	if got.Recent[0].At.IsZero() {
		t.Fatalf("Recent[0].At is zero, want backfilled to %v", at)
	}
	if !got.Recent[0].At.Equal(at) {
		t.Fatalf("Recent[0].At = %v, want %v", got.Recent[0].At, at)
	}
}

func TestActivityTrackerCapsRecentActions(t *testing.T) {
	tr := newActivityTracker()
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	for range maxRecentActions + 5 {
		tr.record("c", at, []Action{{At: at, Type: "a"}})
	}

	got, ok := tr.snapshot("c")
	if !ok {
		t.Fatalf("snapshot(c) ok = false, want true")
	}
	if len(got.Recent) != maxRecentActions {
		t.Fatalf("Recent len = %d, want %d (capped)", len(got.Recent), maxRecentActions)
	}
}

func TestActivityTrackerSnapshotIsolatesCallerFromMutation(t *testing.T) {
	tr := newActivityTracker()
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tr.record("c", at, []Action{{At: at, Type: "a"}})

	got, _ := tr.snapshot("c")
	got.Recent[0].Type = "mutated"

	got2, _ := tr.snapshot("c")
	if got2.Recent[0].Type != "a" {
		t.Fatalf("internal state mutated via returned snapshot: got %q, want %q", got2.Recent[0].Type, "a")
	}
}

func TestRegistryRecordsAndExposesActivity(t *testing.T) {
	r := NewRegistry(Registrar{})
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	actions := []Action{{At: at, Type: "skill.updated", Detail: "d", Reason: "r"}}

	r.RecordActivity("a", at, actions)

	got, ok := r.Activity("a")
	if !ok {
		t.Fatalf("Activity(a) ok = false, want true")
	}
	if !got.LastRunAt.Equal(at) {
		t.Fatalf("LastRunAt = %v, want %v", got.LastRunAt, at)
	}
	if len(got.Recent) != 1 {
		t.Fatalf("Recent len = %d, want 1", len(got.Recent))
	}

	if _, ok := r.Activity("unknown"); ok {
		t.Fatalf("Activity(unknown) ok = true, want false")
	}
}
