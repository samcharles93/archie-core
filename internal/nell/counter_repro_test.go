package nell

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	nell "github.com/samcharles93/NellDB"
	"github.com/samcharles93/NellDB/logstore"
	"github.com/samcharles93/NellDB/sdk"

	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/store"
)

// TestCountersSurviveReopen reproduces a total dispatch outage.
//
// The adapter's auto-increment counters live at "meta:task_id_counter" and
// "meta:event_id_counter". The SDK reserves the "meta:" prefix for its own
// bookkeeping (meta:clock, meta:vector) and skips those ids when reindex()
// rebuilds the in-memory rev cache on open. The record itself is still in the
// log, so after a restart Get returns the counter doc complete with its _rev
// while the rev cache has no entry for it -- and DocDB.putIn rejects exactly
// that combination ("incomingRev != \"\" && !exists") with ErrConflict.
//
// The effect is permanent and survives restarts: every EnqueueIssue and every
// InsertEvent fails, so no task is ever written and no work is dispatched.
func TestCountersSurviveReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "archie.db")

	first, err := OpenStore(path, "test-node")
	if err != nil {
		t.Fatal(err)
	}
	firstEventID, err := first.InsertEvent(t.Context(), events.Event{Kind: "test", Detail: "first"})
	if err != nil {
		t.Fatalf("first InsertEvent on a fresh store: %v", err)
	}
	if firstEventID != 1 {
		t.Fatalf("first event ID = %d, want 1 from a fresh store", firstEventID)
	}
	if _, err := first.EnqueueIssue(t.Context(), "acme", "app", 1, "one", "", "", ""); err != nil {
		t.Fatalf("first EnqueueIssue on a fresh store: %v", err)
	}
	firstTask := mustTaskByIssue(t, first, "acme", "app", 1)
	if firstTask.ID != 1 {
		t.Fatalf("first task ID = %d, want 1 from a fresh store", firstTask.ID)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopen, exactly as a daemon restart does.
	second, err := OpenStore(path, "test-node")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })

	secondEventID, err := second.InsertEvent(t.Context(), events.Event{Kind: "test", Detail: "after restart"})
	if err != nil {
		if errors.Is(err, sdk.ErrConflict) {
			t.Fatalf("InsertEvent after reopen returned ErrConflict: the event log is permanently unwritable: %v", err)
		}
		t.Fatalf("InsertEvent after reopen: %v", err)
	}
	if secondEventID != 2 {
		t.Fatalf("second event ID = %d, want 2 (sequence must continue across reopen, not restart)", secondEventID)
	}

	if _, err := second.EnqueueIssue(t.Context(), "acme", "app", 2, "two", "", "", ""); err != nil {
		if errors.Is(err, sdk.ErrConflict) {
			t.Fatalf("EnqueueIssue after reopen returned ErrConflict: no task can ever be queued again: %v", err)
		}
		t.Fatalf("EnqueueIssue after reopen: %v", err)
	}
	secondTask := mustTaskByIssue(t, second, "acme", "app", 2)
	if secondTask.ID != 2 {
		t.Fatalf("second task ID = %d, want 2 (sequence must continue across reopen, not restart)", secondTask.ID)
	}

	// Both events must survive: a sequence that restarted at 1 would have
	// overwritten event:1 via the same "event:<id>" key, and EventsSince
	// would return one event instead of two.
	got, err := second.EventsSince(t.Context(), 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("EventsSince returned %d events, want 2 (the pre-reopen event must not be overwritten)", len(got))
	}
	if got[0].ID != 1 || got[0].Detail != "first" || got[1].ID != 2 || got[1].Detail != "after restart" {
		t.Fatalf("events after reopen = %+v, want IDs 1 and 2 with their original details", got)
	}
}

func mustTaskByIssue(t *testing.T, st store.TaskStore, owner, repo string, number int) *store.Task {
	t.Helper()
	task, err := st.TaskByIssue(t.Context(), owner, repo, number)
	if err != nil {
		t.Fatal(err)
	}
	if task == nil {
		t.Fatalf("TaskByIssue(%s, %s, %d) returned nothing", owner, repo, number)
	}
	return task
}

// TestCounterMigrationContinuesSequence covers the upgrade path for a database
// written before the counters moved off the "meta:" prefix.
//
// The old document's next_id is load-bearing: restarting the sequence at 1
// would hand out IDs that already belong to live tasks, so two different tasks
// would answer to the same /approve. The old document cannot be rewritten --
// that is the failure this whole change exists to fix -- so migration reads it
// and seeds the new key.
func TestCounterMigrationContinuesSequence(t *testing.T) {
	const seededNextID = 42

	path := filepath.Join(t.TempDir(), "archie.db")

	// Seed a log in the pre-rename shape. A first Put carries no _rev, which
	// is the one write to a "meta:" document that still succeeds.
	seed, err := logstore.OpenLog(path, "test-node")
	if err != nil {
		t.Fatal(err)
	}
	legacy := sdk.New(seed, "test-node", "tasks")
	if _, err := legacy.Put(t.Context(), sdk.Doc{
		sdk.FieldID: "meta:task_id_counter",
		"next_id":   int64(seededNextID),
	}); err != nil {
		t.Fatal(err)
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}

	st, err := OpenStore(path, "test-node")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	if _, err := st.EnqueueIssue(t.Context(), "acme", "app", 7, "seven", "", "", ""); err != nil {
		t.Fatal(err)
	}
	task, err := st.TaskByIssue(t.Context(), "acme", "app", 7)
	if err != nil {
		t.Fatal(err)
	}
	if task == nil {
		t.Fatal("TaskByIssue returned nothing for the task just enqueued")
	}
	if task.ID != seededNextID {
		t.Errorf("task ID = %d, want %d carried over from the pre-rename counter", task.ID, seededNextID)
	}
}

func TestCounterMigrationFailurePreventsStoreOpening(t *testing.T) {
	tests := []struct {
		name       string
		legacyID   string
		currentID  string
		collection string
	}{
		{name: "task counter", legacyID: legacyTaskCounterID, currentID: taskCounterID, collection: "tasks"},
		{name: "event counter", legacyID: legacyEventCounterID, currentID: eventCounterID, collection: "events"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			base := nell.NewMemoryStore("test-node")
			t.Cleanup(func() { _ = base.Close() })
			legacy := sdk.New(base, "test-node", tc.collection)
			if _, err := legacy.Put(t.Context(), sdk.Doc{
				sdk.FieldID: tc.legacyID,
				"next_id":   int64(42),
			}); err != nil {
				t.Fatal(err)
			}

			// The current counter must be readable (NotFound is fine) so the
			// migration reaches the write; only the write of the new key fails.
			store := &failCounterMigrationStore{Store: base, failPutID: tc.currentID}
			adapter, err := openAdapter(store, "test-node")
			if err == nil {
				_ = adapter.Close()
				t.Fatal("openAdapter returned nil error after counter migration failed")
			}
			if !strings.Contains(err.Error(), "write current counter") {
				t.Fatalf("openAdapter error = %v, want the migration write to fail", err)
			}
			if adapter != nil {
				t.Fatal("openAdapter returned a usable adapter after counter migration failed")
			}
		})
	}
}

// TestCounterMigrationReadFailurePreventsStoreOpening pins the fail-closed
// behaviour for reads, not just the migration write: if the current or legacy
// counter cannot be read, the store must not open and start allocating from a
// fresh sequence.
func TestCounterMigrationReadFailurePreventsStoreOpening(t *testing.T) {
	for _, tc := range []struct {
		name   string
		failID string
	}{
		{name: "current counter read", failID: taskCounterID},
		{name: "legacy counter read", failID: legacyTaskCounterID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := nell.NewMemoryStore("test-node")
			t.Cleanup(func() { _ = base.Close() })

			store := &failCounterMigrationStore{Store: base, failGetID: tc.failID}
			adapter, err := openAdapter(store, "test-node")
			if err == nil {
				_ = adapter.Close()
				t.Fatalf("openAdapter succeeded despite an injected %s failure", tc.failID)
			}
			if !strings.Contains(err.Error(), "read") {
				t.Fatalf("openAdapter error = %v, want the counter read to fail", err)
			}
			if adapter != nil {
				t.Fatal("openAdapter returned a usable adapter after a counter read failed")
			}
		})
	}
}

// TestInvalidLegacyCounterPreventsStoreOpening: a legacy counter whose
// next_id is not a positive integer is corrupt and must refuse to open rather
// than hand out IDs that are already live.
func TestInvalidLegacyCounterPreventsStoreOpening(t *testing.T) {
	base := nell.NewMemoryStore("test-node")
	t.Cleanup(func() { _ = base.Close() })
	legacy := sdk.New(base, "test-node", "tasks")
	if _, err := legacy.Put(t.Context(), sdk.Doc{
		sdk.FieldID: legacyTaskCounterID,
		"next_id":   int64(0),
	}); err != nil {
		t.Fatal(err)
	}

	adapter, err := openAdapter(base, "test-node")
	if err == nil {
		_ = adapter.Close()
		t.Fatal("openAdapter accepted a legacy counter with next_id < 1")
	}
}

// TestMalformedCurrentCounterFailsAllocation: a corrupted current counter
// (doc exists, but next_id is missing or not positive) must fail allocation
// with an error instead of emitting a zero/negative task ID.
func TestMalformedCurrentCounterFailsAllocation(t *testing.T) {
	st := OpenMemory("test-node")
	t.Cleanup(func() { _ = st.Close() })

	adapter, ok := st.(*Adapter)
	if !ok {
		t.Fatal("OpenMemory returned a non-Adapter store")
	}
	if _, err := adapter.tasks.Put(t.Context(), sdk.Doc{
		sdk.FieldID: taskCounterID,
		"next_id":   int64(0),
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := st.EnqueueIssue(t.Context(), "acme", "app", 1, "one", "", "", ""); err == nil {
		t.Fatal("EnqueueIssue succeeded with a malformed counter; it must fail rather than emit an invalid ID")
	}
}

// TestMalformedCurrentCounterPreventsStoreOpening: a persisted database whose
// current counter is already corrupt must fail closed at open, before any
// caller can allocate from it.
func TestMalformedCurrentCounterPreventsStoreOpening(t *testing.T) {
	path := filepath.Join(t.TempDir(), "archie.db")
	seedLegacyCounters(t, path, map[string]int64{
		taskCounterID: 0,
	})

	if _, err := OpenStore(path, "test-node"); err == nil {
		t.Fatal("OpenStore accepted a current counter with next_id < 1")
	}
}

// TestCounterMigrationCoversBothCountersAndReopens checks the full upgrade
// path: both sequences carry forward, the legacy documents stay untouched,
// and a second reopen is a no-op rather than a re-migration.
func TestCounterMigrationCoversBothCountersAndReopens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "archie.db")
	seedLegacyCounters(t, path, map[string]int64{
		legacyTaskCounterID:  42,
		legacyEventCounterID: 100,
	})

	first, err := OpenStore(path, "test-node")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.EnqueueIssue(t.Context(), "acme", "app", 7, "seven", "", "", ""); err != nil {
		t.Fatal(err)
	}
	if task := mustTaskByIssue(t, first, "acme", "app", 7); task.ID != 42 {
		t.Fatalf("task ID = %d, want 42 carried over from the pre-rename task counter", task.ID)
	}
	if id, err := first.InsertEvent(t.Context(), events.Event{Kind: "test", Detail: "migrated"}); err != nil {
		t.Fatal(err)
	} else if id != 100 {
		t.Fatalf("event ID = %d, want 100 carried over from the pre-rename event counter", id)
	}
	adapter, ok := first.(*Adapter)
	if !ok {
		t.Fatal("OpenStore returned a non-Adapter store")
	}
	assertCounterDoc(t, adapter, "tasks", taskCounterID, 43)
	assertCounterDoc(t, adapter, "events", eventCounterID, 101)
	// Legacy documents are inert but must not be rewritten or deleted.
	assertCounterDoc(t, adapter, "tasks", legacyTaskCounterID, 42)
	assertCounterDoc(t, adapter, "events", legacyEventCounterID, 100)
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := OpenStore(path, "test-node")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	if _, err := second.EnqueueIssue(t.Context(), "acme", "app", 8, "eight", "", "", ""); err != nil {
		t.Fatal(err)
	}
	if task := mustTaskByIssue(t, second, "acme", "app", 8); task.ID != 43 {
		t.Fatalf("task ID after second reopen = %d, want 43 (migration must be idempotent)", task.ID)
	}
}

func seedLegacyCounters(t *testing.T, path string, counters map[string]int64) {
	t.Helper()
	seed, err := logstore.OpenLog(path, "test-node")
	if err != nil {
		t.Fatal(err)
	}
	tasks := sdk.New(seed, "test-node", "tasks")
	events := sdk.New(seed, "test-node", "events")
	for id, next := range counters {
		db := tasks
		switch id {
		case legacyEventCounterID, eventCounterID:
			db = events
		}
		if _, err := db.Put(t.Context(), sdk.Doc{sdk.FieldID: id, "next_id": next}); err != nil {
			t.Fatal(err)
		}
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}
}

func assertCounterDoc(t *testing.T, a *Adapter, collection, id string, wantNext int64) {
	t.Helper()
	var db *sdk.DocDB
	switch collection {
	case "tasks":
		db = a.tasks
	case "events":
		db = a.events
	default:
		t.Fatalf("unknown collection %q", collection)
	}
	doc, err := db.Get(t.Context(), id)
	if err != nil {
		t.Fatalf("Get(%q, %q): %v", collection, id, err)
	}
	if next := intField(doc, "next_id"); next != wantNext {
		t.Errorf("counter %s:%s next_id = %d, want %d", collection, id, next, wantNext)
	}
}

type failCounterMigrationStore struct {
	nell.Store
	failGetID string // Get on this id returns an injected error ("" = never)
	failPutID string // Put of this id returns an injected error once ("" = never)
	failed    bool
}

func (s *failCounterMigrationStore) Put(incoming nell.Record) (bool, nell.Record, error) {
	if !s.failed && incoming.ID == s.failPutID {
		s.failed = true
		return false, nell.Record{}, errors.New("injected counter migration failure")
	}
	return s.Store.Put(incoming)
}

func (s *failCounterMigrationStore) Get(collection, id string) (nell.Record, error) {
	if id == s.failGetID {
		return nell.Record{}, errors.New("injected counter read failure")
	}
	return s.Store.Get(collection, id)
}

// TestTasksOwnedByCounterAreVisible guards the scan filter's blast radius.
//
// Task keys are "<owner>:<repo>:<number>", so matching bookkeeping documents by
// a "counter:" prefix would also match every task in a repository owned by
// "counter". EnqueueIssue would report success and the task would then be
// invisible to ClaimNext, Tasks and StatusCounts: never run, never re-enqueued,
// no error anywhere.
func TestTasksOwnedByCounterAreVisible(t *testing.T) {
	st := OpenMemory("test-node")
	t.Cleanup(func() { _ = st.Close() })

	if _, err := st.EnqueueIssue(t.Context(), "counter", "app", 1, "counter-owned", "", "", ""); err != nil {
		t.Fatal(err)
	}

	tasks, err := st.Tasks(t.Context(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("Tasks() returned %d tasks, want the one just enqueued", len(tasks))
	}

	claimed, err := st.ClaimNext(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if claimed == nil {
		t.Fatal("ClaimNext() skipped a queued task because its owner is named \"counter\"")
	}
}

// TestEnqueueIssuePersistsIdentity guards the field this adapter used to accept
// and silently drop, leaving every forge-polled task unattributed while the
// SQLite store recorded it. Multi-identity dispatch and per-identity listing
// both read it.
func TestEnqueueIssuePersistsIdentity(t *testing.T) {
	tests := []struct {
		name     string
		identity string
	}{
		{name: "named identity", identity: "archie-two"},
		{name: "single-identity deployment", identity: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := OpenMemory("test-node")
			t.Cleanup(func() { _ = st.Close() })

			if _, err := st.EnqueueIssue(t.Context(), "acme", "app", 3, "three", "", "", tc.identity); err != nil {
				t.Fatal(err)
			}
			task, err := st.TaskByIssue(t.Context(), "acme", "app", 3)
			if err != nil {
				t.Fatal(err)
			}
			if task == nil {
				t.Fatal("TaskByIssue returned nothing")
			}
			if task.Identity != tc.identity {
				t.Errorf("Identity = %q, want %q", task.Identity, tc.identity)
			}
		})
	}
}
