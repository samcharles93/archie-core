package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/events"
)

func TestOpenLimitsSQLiteToOneConnection(t *testing.T) {
	s := openTest(t)
	if got := s.db.Stats().MaxOpenConnections; got != 1 {
		t.Fatalf("MaxOpenConnections = %d, want 1", got)
	}
}

func TestOpenAppliesPragmasWithTrailingQueryDelimiter(t *testing.T) {
	s, err := Open(t.Context(), filepath.Join(t.TempDir(), "tasks.db")+"?")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	var mode string
	if err := s.db.QueryRowContext(t.Context(), `PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", mode)
	}
}

func TestOpenHonorsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "tasks.db"))
	if s != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("Open = (%+v, %v), want (nil, context.Canceled)", s, err)
	}
}

func TestOpenMigratesUnversionedTaskSchemas(t *testing.T) {
	tests := []struct {
		name    string
		columns string
	}{
		{name: "legacy", columns: "id INTEGER PRIMARY KEY"},
		{name: "partially migrated", columns: "id INTEGER PRIMARY KEY, watch_comment_id INTEGER NOT NULL DEFAULT 0, source TEXT NOT NULL DEFAULT 'forge'"},
		{name: "fully migrated", columns: "id INTEGER PRIMARY KEY, watch_comment_id INTEGER NOT NULL DEFAULT 0, retry_count INTEGER NOT NULL DEFAULT 0, source TEXT NOT NULL DEFAULT 'forge', identity TEXT NOT NULL DEFAULT ''"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "tasks.db")
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(t.Context(), `CREATE TABLE tasks (`+tt.columns+`)`); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}

			s, err := Open(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = s.Close() }()
			var version int
			if err := s.db.QueryRowContext(t.Context(), `PRAGMA user_version`).Scan(&version); err != nil {
				t.Fatal(err)
			}
			if version != taskSchemaVersion {
				t.Fatalf("user_version = %d, want %d", version, taskSchemaVersion)
			}
			rows, err := s.db.QueryContext(t.Context(), `PRAGMA table_info(tasks)`)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := rows.Close(); err != nil {
					t.Errorf("close table_info rows: %v", err)
				}
			}()
			got := map[string]bool{}
			for rows.Next() {
				var cid, notNull, primaryKey int
				var name, columnType string
				var defaultValue any
				if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
					t.Fatal(err)
				}
				got[name] = true
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"watch_comment_id", "retry_count", "source", "identity"} {
				if !got[name] {
					t.Errorf("column %q was not migrated", name)
				}
			}
		})
	}
}

// TestOpenMigratesLegacyEventsAttemptColumn covers the highest-risk step in
// attempt provenance: adding events.attempt to a database that predates it.
// eventsSchema is CREATE TABLE IF NOT EXISTS, so against an existing
// archie.db that statement is a no-op and the migrator arm is the ONLY thing
// that adds the column. A test that opens a fresh tempdir passes whether or
// not the arm exists, so the failure this pins is the one production would
// hit: `table events has no column named attempt`.
func TestOpenMigratesLegacyEventsAttemptColumn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = raw.ExecContext(t.Context(), `
		CREATE TABLE events (
			id        INTEGER PRIMARY KEY AUTOINCREMENT,
			at        TEXT NOT NULL,
			kind      TEXT NOT NULL,
			task_id   INTEGER NOT NULL DEFAULT 0,
			repo      TEXT NOT NULL DEFAULT '',
			issue     INTEGER NOT NULL DEFAULT 0,
			workflow  TEXT NOT NULL DEFAULT '',
			stage     TEXT NOT NULL DEFAULT '',
			detail    TEXT NOT NULL DEFAULT '',
			data      TEXT NOT NULL DEFAULT '{}'
		);
		INSERT INTO events (at, kind, task_id, stage, detail)
		VALUES ('2026-01-01T00:00:00Z', 'stage_start', 7, 'prepare', 'pre-existing row');
		PRAGMA user_version = 3`)
	if err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	// The row written before the column existed survives and reads as
	// unattributed -- zero is not a first attempt.
	legacy, err := s.TaskEvents(t.Context(), 7)
	if err != nil {
		t.Fatalf("TaskEvents after legacy migration: %v", err)
	}
	if len(legacy) != 1 || legacy[0].Detail != "pre-existing row" {
		t.Fatalf("legacy events = %+v, want the preserved row", legacy)
	}
	if legacy[0].Attempt != 0 {
		t.Errorf("legacy event attempt = %d, want 0 (unattributed)", legacy[0].Attempt)
	}

	// Writing the new column is what actually fails in production when the
	// migrator arm is missing.
	if _, err := s.InsertEvent(t.Context(), events.Event{
		Kind: "stage_finish", TaskID: 7, Stage: "prepare", Attempt: 2,
		Data: map[string]any{"duration_ms": 1200},
	}); err != nil {
		t.Fatalf("InsertEvent with attempt after legacy migration: %v", err)
	}

	timeline, err := s.TaskEvents(t.Context(), 7)
	if err != nil {
		t.Fatalf("TaskEvents after insert: %v", err)
	}
	if len(timeline) != 2 {
		t.Fatalf("TaskEvents = %d events, want 2", len(timeline))
	}
	if got := timeline[1].Attempt; got != 2 {
		t.Errorf("attempt round-trip after legacy migration = %d, want 2", got)
	}
	if got := timeline[0].Attempt; got != 0 {
		t.Errorf("legacy row attempt changed to %d, want 0", got)
	}
}

// TestOpenMigratesResourceHistoryToPerKindRequestIDs covers the v4 step: the
// history table's global request_id UNIQUE becomes per-kind uniqueness. A
// global constraint is strictly stricter than the per-kind one, so no legacy
// row can fail the rebuild -- but the legacy table must not leave a request
// ID another kind already used refusing the write (archie-core-fcvd).
func TestOpenMigratesResourceHistoryToPerKindRequestIDs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = raw.ExecContext(t.Context(), `
		CREATE TABLE resources (
		 kind TEXT PRIMARY KEY, value BLOB NOT NULL, version INTEGER NOT NULL, updated_at TEXT NOT NULL
		);
		CREATE TABLE resource_history (
			id INTEGER PRIMARY KEY AUTOINCREMENT, kind TEXT NOT NULL, value BLOB NOT NULL,
			version INTEGER NOT NULL, actor TEXT NOT NULL, source TEXT NOT NULL,
			request_id TEXT NOT NULL UNIQUE, expected_version INTEGER NOT NULL,
			current_version INTEGER NOT NULL, at TEXT NOT NULL
		);
		INSERT INTO resources (kind,value,version,updated_at)
		VALUES ('settings','{"v":1}',1,'2026-01-01T00:00:00Z');
		INSERT INTO resource_history (kind,value,version,actor,source,request_id,expected_version,current_version,at)
		VALUES ('settings','{"v":1}',1,'a','test','shared',0,0,'2026-01-01T00:00:00Z');
		PRAGMA user_version = 3`)
	if err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(t.Context(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	// The row written before the migration survives.
	resource, err := s.Resource(t.Context(), "settings")
	if err != nil {
		t.Fatalf("Resource after migration: %v", err)
	}
	if string(resource.Value) != `{"v":1}` {
		t.Fatalf("legacy history row = %s, want the preserved value", resource.Value)
	}

	// The column constraint is gone -- no auto-index (an index sqlite_master
	// carries with a NULL sql) is left on the table -- and per-kind
	// uniqueness is its replacement.
	var autoIndexes int
	if err := s.db.QueryRowContext(t.Context(), `SELECT count(*) FROM sqlite_master WHERE type='index' AND tbl_name='resource_history' AND sql IS NULL`).Scan(&autoIndexes); err != nil {
		t.Fatal(err)
	}
	if autoIndexes != 0 {
		t.Fatalf("resource_history still carries %d column-constraint index(es), want none", autoIndexes)
	}
	var perKind int
	if err := s.db.QueryRowContext(t.Context(), `SELECT count(*) FROM sqlite_master WHERE type='index' AND name='idx_resource_history_kind_request'`).Scan(&perKind); err != nil {
		t.Fatal(err)
	}
	if perKind != 1 {
		t.Fatalf("idx_resource_history_kind_request exists %d times, want 1", perKind)
	}

	// The dedup key itself: a request ID another kind already used writes the
	// new kind instead of failing the global constraint or replaying the old
	// resource.
	second, err := s.PutResource(t.Context(), ResourceWrite{Kind: "other", Value: []byte(`{"v":9}`), Actor: "a", Source: "test", RequestID: "shared", ExpectedVersion: 0, At: time.Now()})
	if err != nil {
		t.Fatalf("PutResource with a request ID another kind already used: %v", err)
	}
	if second.Kind != "other" || second.Version != 1 {
		t.Fatalf("second write = (%s, v%d), want the %q write itself", second.Kind, second.Version, "other")
	}
}

func TestOpenRejectsNewerSchemaVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), fmt.Sprintf(`PRAGMA user_version = %d`, taskSchemaVersion+1)); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(t.Context(), path)
	if s != nil || err == nil || !strings.Contains(err.Error(), "newer than supported") {
		t.Fatalf("Open = (%+v, %v), want newer-schema error", s, err)
	}
}

func TestPersistenceAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.db")
	ctx := t.Context()
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnqueueIssue(ctx, "acme", "todo", 1, "survive restart", "", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	task, err := s.TaskByIssue(ctx, "acme", "todo", 1)
	if err != nil || task == nil {
		t.Fatalf("TaskByIssue after reopen = (%+v, %v)", task, err)
	}
	if task.Title != "survive restart" {
		t.Fatalf("Title = %q, want survive restart", task.Title)
	}
}

func TestConcurrentEnqueueAndClaim(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	const count = 20
	var wg sync.WaitGroup
	for number := range count {
		wg.Go(func() {
			if _, err := s.EnqueueIssue(ctx, "acme", "repo", number, "title", "", "", ""); err != nil {
				t.Errorf("EnqueueIssue(%d): %v", number, err)
			}
		})
	}
	wg.Wait()

	claimed := make(map[int64]struct{}, count)
	var mu sync.Mutex
	for range count {
		wg.Go(func() {
			task, err := s.ClaimNext(ctx)
			if err != nil {
				t.Errorf("ClaimNext: %v", err)
				return
			}
			if task == nil {
				t.Error("ClaimNext returned nil task")
				return
			}
			mu.Lock()
			claimed[task.ID] = struct{}{}
			mu.Unlock()
		})
	}
	wg.Wait()
	if len(claimed) != count {
		t.Fatalf("claimed %d unique tasks, want %d", len(claimed), count)
	}
}

func TestChatIssueNumbersAreDurableAcrossStoreInstances(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.db")
	stores := make([]*Store, 2)
	for i := range stores {
		var err error
		stores[i], err = Open(t.Context(), path)
		if err != nil {
			t.Fatal(err)
		}
		defer func(s *Store) { _ = s.Close() }(stores[i])
	}

	const count = 20
	numbers := make(map[int]struct{}, count)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := range count {
		wg.Go(func() {
			task, err := stores[i%len(stores)].EnqueueChatTask(
				t.Context(), "acme", "widget", "chat task", "", "", "archie",
			)
			if err != nil {
				t.Errorf("EnqueueChatTask: %v", err)
				return
			}
			mu.Lock()
			numbers[task.IssueNumber] = struct{}{}
			mu.Unlock()
		})
	}
	wg.Wait()
	if len(numbers) != count {
		t.Fatalf("allocated %d unique issue numbers, want %d", len(numbers), count)
	}
	for number := range numbers {
		if number < 1_000_000_000_000_000 || number > 1<<53-1 {
			t.Errorf("synthetic issue number %d is outside the reserved JSON-safe range", number)
		}
	}
}

func TestTasksIncludesIdentity(t *testing.T) {
	s := openTest(t)
	want, err := s.EnqueueChatTask(t.Context(), "acme", "widget", "chat task", "", "", "reviewer")
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Tasks(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != want.ID || got[0].Identity != "reviewer" {
		t.Fatalf("Tasks = %+v, want task %d with identity reviewer", got, want.ID)
	}
}

func TestStoreOperationsHonorContextCancellation(t *testing.T) {
	s := openTest(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if task, err := s.ClaimNext(ctx); task != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("ClaimNext = (%+v, %v), want (nil, context.Canceled)", task, err)
	}
	if task, err := s.TaskByIssue(ctx, "acme", "repo", 1); task != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("TaskByIssue = (%+v, %v), want (nil, context.Canceled)", task, err)
	}
}

func TestOversizedEventPayload(t *testing.T) {
	s := openTest(t)
	detail := strings.Repeat("x", 5000)
	if _, err := s.InsertEvent(t.Context(), events.Event{Kind: events.KindLog, Detail: detail}); err != nil {
		t.Fatal(err)
	}
	got, err := s.EventsSince(t.Context(), "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Detail != detail[:4000] {
		t.Fatalf("stored detail length = %d, want 4000", len(got[0].Detail))
	}
}

func TestInsertEventWithUnmarshalableData(t *testing.T) {
	s := openTest(t)
	if _, err := s.InsertEvent(t.Context(), events.Event{
		Kind: events.KindLog,
		Data: map[string]any{"channel": make(chan int)},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := s.EventsSince(t.Context(), "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("events = %d, want 1", len(got))
	}
	if message, ok := got[0].Data["marshal_error"].(string); !ok || message == "" {
		t.Fatalf("marshal_error = %#v, want non-empty string", got[0].Data["marshal_error"])
	}
}

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	return s
}

// TestEventsSinceOrdersByAt pins the total-order invariant: the feed cursor
// must page by at, not by row id. Inserting a later-timestamped event before
// an earlier one must still return the earlier one first, because row id is
// insertion order, not chronology.
func TestEventsSinceOrdersByAt(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	later := time.Date(2026, 1, 1, 0, 0, 1, 0, time.UTC)
	earlier := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := s.InsertEvent(ctx, events.Event{Kind: "log", At: later, Detail: "later"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertEvent(ctx, events.Event{Kind: "log", At: earlier, Detail: "earlier"}); err != nil {
		t.Fatal(err)
	}

	got, err := s.EventsSince(ctx, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("EventsSince returned %d events, want 2", len(got))
	}
	if got[0].Detail != "earlier" || got[1].Detail != "later" {
		t.Fatalf("order = %q then %q; want earlier then later (at order, not insertion order)", got[0].Detail, got[1].Detail)
	}
}

// TestInsertEventWritesFixedWidthTimeKey pins the write format the total
// order depends on: a whole-second timestamp and a full-precision one must
// render to the same length, or lexicographic order stops being chronological
// order. time.RFC3339Nano trims trailing zeros, so today this fails.
func TestInsertEventWritesFixedWidthTimeKey(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	whole := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	precise := time.Date(2026, 1, 1, 0, 0, 0, 123456789, time.UTC)
	for _, e := range []events.Event{
		{Kind: "log", At: whole, Detail: "whole"},
		{Kind: "log", At: precise, Detail: "precise"},
	} {
		if _, err := s.InsertEvent(ctx, e); err != nil {
			t.Fatal(err)
		}
	}

	rows, err := s.db.QueryContext(ctx, `SELECT at FROM events ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var ats []string
	for rows.Next() {
		var at string
		if err := rows.Scan(&at); err != nil {
			t.Fatal(err)
		}
		ats = append(ats, at)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(ats) != 2 {
		t.Fatalf("stored %d event rows, want 2", len(ats))
	}
	if len(ats[0]) != len(ats[1]) {
		t.Fatalf("at renders %d and %d chars (%q vs %q); want a fixed-width key", len(ats[0]), len(ats[1]), ats[0], ats[1])
	}
}

// TestEventsSincePagesIdenticalTimestampsWithoutSkipOrRepeat is the
// regression guard for the id tie-break: many events sharing one timestamp
// must still page exactly once each, in a stable order, no matter what their
// ids are. It walks the returned cursor to exhaustion with a page size smaller
// than the set, so every page boundary is exercised.
func TestEventsSincePagesIdenticalTimestampsWithoutSkipOrRepeat(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	const total = 50
	inserted := make(map[int64]string, total)
	for i := range total {
		detail := fmt.Sprintf("event-%d", i)
		id, err := s.InsertEvent(ctx, events.Event{Kind: "log", At: at, Detail: detail})
		if err != nil {
			t.Fatal(err)
		}
		inserted[id] = detail
	}

	var got []events.Event
	cursor := ""
	for {
		page, err := s.EventsSince(ctx, cursor, 7)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		got = append(got, page...)
		last := page[len(page)-1]
		cursor = storecontract.EventCursor(last.At, last.ID)
	}

	if len(got) != total {
		t.Fatalf("paged %d events, want %d (a skip or a repeat)", len(got), total)
	}
	seen := make(map[int64]bool, total)
	for i, e := range got {
		if _, ok := inserted[e.ID]; !ok {
			t.Fatalf("event %d (%d) was never inserted", i, e.ID)
		}
		if seen[e.ID] {
			t.Fatalf("event %d (%d) delivered twice", i, e.ID)
		}
		seen[e.ID] = true
		if i > 0 {
			prev := got[i-1]
			if storecontract.EventCursor(prev.At, prev.ID) >= storecontract.EventCursor(e.At, e.ID) {
				t.Fatalf("order regressed at %d: %q then %q", i,
					storecontract.EventCursor(prev.At, prev.ID), storecontract.EventCursor(e.At, e.ID))
			}
		}
	}
}

// TestTasksBindingColumnsRoundTrip confirms the binding_id and
// binding_version columns added in store schema 2 survive an INSERT and a
// full SELECT round-trip through the Task read paths (scanTask,
// TaskByIssue, TaskByID, Tasks).
func TestTasksBindingColumnsRoundTrip(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	task, err := s.EnqueueChatTask(ctx, "acme", "widget", "binding task", "body", "implement", "")
	if err != nil {
		t.Fatalf("EnqueueChatTask: %v", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE tasks SET binding_id=?, binding_version=? WHERE id=?`,
		"rbind0000000001", 3, task.ID); err != nil {
		t.Fatalf("UPDATE binding provenance: %v", err)
	}

	for name, read := range map[string]func() (*workflow.Task, error){
		"scanTask-via-TaskByIssue": func() (*workflow.Task, error) { return s.TaskByIssue(ctx, "acme", "widget", task.IssueNumber) },
		"TaskByID":                 func() (*workflow.Task, error) { return s.TaskByID(ctx, task.ID) },
	} {
		got, err := read()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got == nil {
			t.Fatalf("%s: nil task", name)
		}
		if got.BindingID != "rbind0000000001" || got.BindingVersion != 3 {
			t.Fatalf("%s: BindingID=%q BindingVersion=%d, want rbind0000000001/3",
				name, got.BindingID, got.BindingVersion)
		}
	}

	listed, err := s.Tasks(ctx, 10)
	if err != nil {
		t.Fatalf("Tasks: %v", err)
	}
	if len(listed) == 0 || listed[0].BindingID != "rbind0000000001" || listed[0].BindingVersion != 3 {
		t.Fatalf("Tasks() round-trip: %+v", listed)
	}
}

func TestClip(t *testing.T) {
	tests := []struct {
		name  string
		input string
		n     int
		want  string
	}{
		{"ascii truncate", "hello", 3, "hel"},
		{"ascii below n", "hello", 10, "hello"},
		{"empty zero", "", 0, ""},
		{"empty positive", "", 5, ""},
		{"2-byte fits exactly", "café", 5, "café"},
		{"2-byte cut off", "café", 4, "caf"},
		{"clean ASCII boundary", "café", 3, "caf"},
		{"4-byte emoji fits", "🍕pizza", 4, "🍕"},
		{"4-byte emoji cut off", "🍕pizza", 3, ""},
		{"2-byte ñ fits", "niño", 5, "niño"},
		{"2-byte ñ cut off", "niño", 4, "niñ"},
		{"3-byte CJK fits", "日本語", 6, "日本"},
		{"3-byte CJK cut off", "日本語", 5, "日"},
		{"mixed multi-byte", "a🍕b", 5, "a🍕"},
		{"mixed multi-byte cut off", "a🍕b", 4, "a"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := clip(tt.input, tt.n)
			if got != tt.want {
				t.Errorf("clip(%q, %d) = %q, want %q", tt.input, tt.n, got, tt.want)
			}
			// Verify output is valid UTF-8.
			if !utf8.ValidString(got) {
				t.Errorf("clip(%q, %d) produced invalid UTF-8: %q", tt.input, tt.n, got)
			}
		})
	}
}

func TestClearTerminalTasks(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	statuses := []string{workflow.StatusMerged, workflow.StatusParked, workflow.StatusRejected, workflow.StatusClosedWontDo, workflow.StatusQueued}
	for i, status := range statuses {
		if _, err := s.EnqueueIssue(ctx, "acme", "widget", i+1, "t", "b", "", ""); err != nil {
			t.Fatal(err)
		}
		task, err := s.TaskByIssue(ctx, "acme", "widget", i+1)
		if err != nil || task == nil {
			t.Fatalf("TaskByIssue(%d) = (%+v, %v)", i+1, task, err)
		}
		if status != workflow.StatusQueued {
			if err := s.Transition(ctx, task.ID, workflow.StatusQueued, status, ""); err != nil {
				t.Fatal(err)
			}
		}
	}

	n, err := s.ClearTerminalTasks(ctx)
	if err != nil || n != 3 {
		t.Fatalf("ClearTerminalTasks = (%d, %v), want (3, nil)", n, err)
	}

	counts, err := s.StatusCounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if counts[workflow.StatusQueued] != 1 {
		t.Fatalf("expected 1 queued, got %d", counts[workflow.StatusQueued])
	}
	if counts[workflow.StatusParked] != 1 {
		t.Fatalf("recoverable parked task was cleared: count = %d", counts[workflow.StatusParked])
	}
	for _, status := range []string{workflow.StatusMerged, workflow.StatusRejected, workflow.StatusClosedWontDo} {
		if counts[status] != 0 {
			t.Fatalf("expected 0 for %s, got %d", status, counts[status])
		}
	}

	// Idempotent: second call removes nothing.
	n, err = s.ClearTerminalTasks(ctx)
	if err != nil || n != 0 {
		t.Fatalf("second ClearTerminalTasks = (%d, %v), want (0, nil)", n, err)
	}
}

func TestArchiveTaskIsGuardedAndScoped(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	for number := 1; number <= 2; number++ {
		if _, err := s.EnqueueIssue(ctx, "acme", "widget", number, "t", "b", "", ""); err != nil {
			t.Fatal(err)
		}
	}
	task, err := s.TaskByIssue(ctx, "acme", "widget", 1)
	if err != nil || task == nil {
		t.Fatalf("TaskByIssue = (%+v, %v)", task, err)
	}
	if err := s.Transition(ctx, task.ID, workflow.StatusQueued, workflow.StatusMerged, "done"); err != nil {
		t.Fatal(err)
	}

	audit := events.Event{Kind: events.KindTaskArchiveRequested, TaskID: task.ID}
	if _, err := s.ArchiveTask(ctx, task.ID, workflow.StatusQueued, audit); !errors.Is(err, ErrStaleTransition) {
		t.Fatalf("ArchiveTask stale guard = %v, want ErrStaleTransition", err)
	}
	if got, err := s.TaskByID(ctx, task.ID); err != nil || got == nil {
		t.Fatalf("stale archive removed task: (%+v, %v)", got, err)
	}
	eventID, err := s.ArchiveTask(ctx, task.ID, workflow.StatusMerged, audit)
	if err != nil {
		t.Fatalf("ArchiveTask = %v", err)
	}
	if eventID == 0 {
		t.Fatal("ArchiveTask returned no durable event ID")
	}
	if got, err := s.TaskByID(ctx, task.ID); err != nil || got != nil {
		t.Fatalf("archived task = (%+v, %v), want nil", got, err)
	}
	if other, err := s.TaskByIssue(ctx, "acme", "widget", 2); err != nil || other == nil {
		t.Fatalf("archive removed another task: (%+v, %v)", other, err)
	}
}

func TestArchiveAuditFailurePreservesTask(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	if _, err := s.EnqueueIssue(ctx, "acme", "widget", 1, "t", "", "", ""); err != nil {
		t.Fatal(err)
	}
	task, _ := s.TaskByIssue(ctx, "acme", "widget", 1)
	if err := s.Transition(ctx, task.ID, workflow.StatusQueued, workflow.StatusMerged, "done"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `
		CREATE TRIGGER fail_archive_audit BEFORE INSERT ON events
		WHEN NEW.kind = 'task_archive_requested'
		BEGIN SELECT RAISE(FAIL, 'audit failed'); END`); err != nil {
		t.Fatal(err)
	}

	if _, err := s.ArchiveTask(ctx, task.ID, workflow.StatusMerged, events.Event{
		Kind: events.KindTaskArchiveRequested, TaskID: task.ID,
	}); err == nil {
		t.Fatal("ArchiveTask succeeded despite forced audit failure")
	}
	if got, err := s.TaskByID(ctx, task.ID); err != nil || got == nil {
		t.Fatalf("audit failure deleted task: (%+v, %v)", got, err)
	}
}

func TestEnqueueIsIdempotent(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	ins, err := s.EnqueueIssue(ctx, "acme", "todo", 1, "add tests", "body", "widget", "")
	if err != nil || !ins {
		t.Fatalf("first enqueue = (%v, %v)", ins, err)
	}
	ins, err = s.EnqueueIssue(ctx, "acme", "todo", 1, "add tests", "body", "widget", "")
	if err != nil || ins {
		t.Fatalf("duplicate enqueue must be a no-op, got (%v, %v)", ins, err)
	}
}

func TestEventLogRoundTrip(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	id1, err := s.InsertEvent(ctx, events.Event{Kind: "stage_start", TaskID: 1, Stage: "plan", Attempt: 1, At: at})
	if err != nil || id1 == 0 {
		t.Fatalf("insert = (%d, %v)", id1, err)
	}
	id2, err := s.InsertEvent(ctx, events.Event{
		Kind: "stage_finish", TaskID: 1, Workflow: "implement", Stage: "plan", Attempt: 2, At: at,
		Data: map[string]any{"duration_ms": 1200},
	})
	if err != nil || id2 <= id1 {
		t.Fatalf("second insert = (%d, %v)", id2, err)
	}

	// Two events sharing a timestamp must still page exactly once each via the
	// id tie-break: the cursor carries (at, id), so resuming after id1 yields
	// only id2.
	evs, err := s.EventsSince(ctx, storecontract.EventCursor(at, id1), 10)
	if err != nil || len(evs) != 1 || evs[0].Kind != "stage_finish" {
		t.Fatalf("EventsSince = (%+v, %v)", evs, err)
	}
	if evs[0].Data["duration_ms"] != float64(1200) {
		t.Fatalf("data round-trip = %v", evs[0].Data)
	}
	if evs[0].Attempt != 2 {
		t.Fatalf("EventsSince attempt round-trip = %d, want 2", evs[0].Attempt)
	}

	timeline, err := s.TaskEvents(ctx, 1)
	if err != nil || len(timeline) != 2 {
		t.Fatalf("TaskEvents = (%d, %v)", len(timeline), err)
	}
	// Two attempts of one task must stay distinguishable in its timeline.
	if timeline[0].Attempt != 1 || timeline[1].Attempt != 2 {
		t.Fatalf("TaskEvents attempts = (%d, %d), want (1, 2)", timeline[0].Attempt, timeline[1].Attempt)
	}

	stats, err := s.StageStats(ctx)
	if err != nil || len(stats) != 1 || stats[0].AvgMs != 1200 || stats[0].Stage != "plan" {
		t.Fatalf("StageStats = (%+v, %v)", stats, err)
	}
}

// TestTransitionRejectsStaleFrom proves that Transition rejects a call
// whose `from` parameter does not match the task's current status.
// Today Transition ignores the `from` guard, so the call succeeds when
// it should fail — this test captures that bug.
func TestTransitionRejectsStaleFrom(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	if _, err := s.EnqueueIssue(ctx, "acme", "widget", 1, "t", "b", "", ""); err != nil {
		t.Fatal(err)
	}
	task, err := s.ClaimNext(ctx)
	if err != nil || task == nil {
		t.Fatalf("claim = (%v, %v)", task, err)
	}
	// Task is now workflow.StatusRunning. Transition with from=workflow.StatusQueued
	// must fail because the task is not queued.
	err = s.Transition(ctx, task.ID, workflow.StatusQueued, workflow.StatusPROpen, "stale from")
	if err == nil {
		t.Fatal("Transition with stale 'from' (workflow.StatusQueued) on a running task must return an error, but got nil")
	}

	// Verify the task was NOT changed.
	got, err := s.TaskByID(ctx, task.ID)
	if err != nil || got == nil {
		t.Fatalf("TaskByID = (%+v, %v)", got, err)
	}
	if got.Status != workflow.StatusRunning {
		t.Fatalf("task status changed to %q despite stale from guard; want %q", got.Status, workflow.StatusRunning)
	}
}

// TestTransitionPreventsDoubleTransition proves that two callers racing
// to transition the same task from the same `from` status cannot both
// succeed — the second must get an error because the first already
// changed the status.
func TestTransitionPreventsDoubleTransition(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	if _, err := s.EnqueueIssue(ctx, "acme", "widget", 1, "t", "b", "", ""); err != nil {
		t.Fatal(err)
	}
	task, err := s.ClaimNext(ctx)
	if err != nil || task == nil {
		t.Fatalf("claim = (%v, %v)", task, err)
	}

	// First transition: running → pr_open should succeed.
	if err := s.Transition(ctx, task.ID, workflow.StatusRunning, workflow.StatusPROpen, "first"); err != nil {
		t.Fatalf("first transition = %v", err)
	}

	// Second transition: the task is now workflow.StatusPROpen, so
	// from=workflow.StatusRunning must fail.
	err = s.Transition(ctx, task.ID, workflow.StatusRunning, workflow.StatusMerged, "second")
	if err == nil {
		t.Fatal("second Transition with stale 'from' (workflow.StatusRunning) on a pr_open task must return an error, but got nil")
	}

	// Verify the task kept the first transition's status.
	got, err := s.TaskByID(ctx, task.ID)
	if err != nil || got == nil {
		t.Fatalf("TaskByID = (%+v, %v)", got, err)
	}
	if got.Status != workflow.StatusPROpen {
		t.Fatalf("task status = %q, want %q (second transition must not overwrite)", got.Status, workflow.StatusPROpen)
	}
}

func TestTransitionToParkedPersistsReasonOnTask(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()

	if _, err := s.EnqueueIssue(ctx, "acme", "widget", 1, "task", "", "", ""); err != nil {
		t.Fatal(err)
	}
	task, err := s.ClaimNext(ctx)
	if err != nil || task == nil {
		t.Fatalf("ClaimNext = (%+v, %v)", task, err)
	}
	const reason = "managed worker unavailable"
	if err := s.Transition(ctx, task.ID, workflow.StatusRunning, workflow.StatusParked, reason); err != nil {
		t.Fatal(err)
	}

	got, err := s.TaskByID(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ParkReason != reason {
		t.Errorf("ParkReason = %q, want %q", got.ParkReason, reason)
	}
}

// TestRequeueRejectsStaleFrom proves that Requeue rejects a call whose
// fromStatus does not match the task's current status.
func TestRequeueRejectsStaleFrom(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	if _, err := s.EnqueueIssue(ctx, "acme", "widget", 1, "t", "b", "", ""); err != nil {
		t.Fatal(err)
	}
	task, err := s.ClaimNext(ctx)
	if err != nil || task == nil {
		t.Fatalf("claim = (%v, %v)", task, err)
	}
	// Task is workflow.StatusRunning. Requeue with fromStatus=workflow.StatusParked must
	// fail because the task is not parked.
	err = s.Requeue(ctx, task.ID, workflow.StatusParked, "implement")
	if err == nil {
		t.Fatal("Requeue with stale fromStatus (workflow.StatusParked) on a running task must return an error, but got nil")
	}

	// Verify the task was NOT changed.
	got, err := s.TaskByID(ctx, task.ID)
	if err != nil || got == nil {
		t.Fatalf("TaskByID = (%+v, %v)", got, err)
	}
	if got.Status != workflow.StatusRunning {
		t.Fatalf("task status changed to %q despite stale fromStatus guard; want %q", got.Status, workflow.StatusRunning)
	}
}

func TestRetryTaskAtomicallyRequeuesAndIncrements(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	if _, err := s.EnqueueIssue(ctx, "acme", "todo", 1, "t", "", "", ""); err != nil {
		t.Fatal(err)
	}
	task, err := s.ClaimNext(ctx)
	if err != nil || task == nil {
		t.Fatalf("ClaimNext = (%+v, %v)", task, err)
	}
	if err := s.Transition(ctx, task.ID, workflow.StatusRunning, workflow.StatusParked, "failed"); err != nil {
		t.Fatal(err)
	}

	if err := s.RetryTask(ctx, task.ID, workflow.StatusParked, ""); err != nil {
		t.Fatal(err)
	}
	got, err := s.TaskByID(ctx, task.ID)
	if err != nil || got == nil {
		t.Fatalf("TaskByID = (%+v, %v)", got, err)
	}
	if got.Status != workflow.StatusQueued || got.RetryCount != 1 {
		t.Fatalf("task = status %q retry_count %d, want queued/1", got.Status, got.RetryCount)
	}
	if err := s.RetryTask(ctx, task.ID, workflow.StatusParked, ""); !errors.Is(err, ErrStaleTransition) {
		t.Fatalf("stale RetryTask error = %v, want ErrStaleTransition", err)
	}
}

func TestRetryTaskWriteFailureLeavesParkedCountUnchanged(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	if _, err := s.EnqueueIssue(ctx, "acme", "todo", 1, "t", "", "", ""); err != nil {
		t.Fatal(err)
	}
	task, _ := s.ClaimNext(ctx)
	if err := s.Transition(ctx, task.ID, workflow.StatusRunning, workflow.StatusParked, "failed"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `
		CREATE TRIGGER fail_retry BEFORE UPDATE OF retry_count ON tasks
		BEGIN SELECT RAISE(FAIL, 'retry failed'); END`); err != nil {
		t.Fatal(err)
	}

	if err := s.RetryTask(ctx, task.ID, workflow.StatusParked, ""); err == nil {
		t.Fatal("RetryTask succeeded despite forced write failure")
	}
	got, err := s.TaskByID(ctx, task.ID)
	if err != nil || got == nil {
		t.Fatalf("TaskByID = (%+v, %v)", got, err)
	}
	if got.Status != workflow.StatusParked || got.RetryCount != 0 {
		t.Fatalf("partial retry write: status %q retry_count %d", got.Status, got.RetryCount)
	}
}

func TestRetryCountColumn(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	if _, err := s.EnqueueIssue(ctx, "acme", "todo", 1, "t", "", "", ""); err != nil {
		t.Fatal(err)
	}

	// Verify column exists and defaults to 0 via direct query.
	var count int
	if err := s.db.QueryRowContext(ctx,
		`SELECT retry_count FROM tasks WHERE owner='acme' AND repo='todo' AND issue_number=1`).Scan(&count); err != nil {
		t.Fatalf("retry_count scan failed: %v", err)
	}
	if count != 0 {
		t.Fatalf("default retry_count = %d, want 0", count)
	}
}

func TestClaimTransitionAndRecovery(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	if _, err := s.EnqueueIssue(ctx, "acme", "todo", 7, "t", "", "archie,bug", ""); err != nil {
		t.Fatal(err)
	}
	task, err := s.ClaimNext(ctx)
	if err != nil || task == nil {
		t.Fatalf("claim = (%v, %v)", task, err)
	}
	if task.Status != workflow.StatusRunning || task.Attempt != 1 || task.Labels != "archie,bug" {
		t.Fatalf("claimed task = %+v", task)
	}
	if next, _ := s.ClaimNext(ctx); next != nil {
		t.Fatal("second claim must return nil while task is running")
	}

	// Crash recovery: running goes back to queued, attempt increments on
	// the next claim.
	if n, err := s.RecoverStale(ctx); err != nil || n != 1 {
		t.Fatalf("recover = (%d, %v)", n, err)
	}
	task, err = s.ClaimNext(ctx)
	if err != nil || task == nil || task.Attempt != 2 {
		t.Fatalf("re-claim after recovery = (%+v, %v)", task, err)
	}

	if err := s.Transition(ctx, task.ID, workflow.StatusRunning, workflow.StatusPROpen, "PR #3"); err != nil {
		t.Fatal(err)
	}
	task.PRNumber = 3
	if err := s.Update(ctx, task); err != nil {
		t.Fatal(err)
	}
	open, err := s.OpenPRs(ctx)
	if err != nil || len(open) != 1 || open[0].PRNumber != 3 {
		t.Fatalf("open PRs = (%+v, %v)", open, err)
	}
}

func TestRecoverStalePreservesChatTaskRouting(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	created, err := s.EnqueueChatTask(ctx, "acme", "todo", "chat task", "", "tdd", "reviewer")
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimNext(ctx)
	if err != nil || claimed == nil || claimed.ID != created.ID {
		t.Fatalf("ClaimNext() = (%+v, %v), want task %d", claimed, err, created.ID)
	}
	if n, err := s.RecoverStale(ctx); err != nil || n != 1 {
		t.Fatalf("RecoverStale() = (%d, %v), want (1, nil)", n, err)
	}
	recovered, err := s.TaskByID(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered == nil {
		t.Fatal("TaskByID() returned nil after recovery")
	}
	if recovered.Status != workflow.StatusQueued || recovered.Source != workflow.SourceChat ||
		recovered.Identity != "reviewer" || recovered.Workflow != "tdd" {
		t.Errorf("recovered task = %+v, want queued chat task routed to reviewer/tdd", recovered)
	}
}

func TestLifecycleQueriesPreserveChatTaskRouting(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	waiting, err := s.EnqueueChatTask(ctx, "acme", "todo", "needs approval", "", "feasibility", "reviewer")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Transition(ctx, waiting.ID, workflow.StatusQueued, workflow.StatusWaitingHuman, "await approval"); err != nil {
		t.Fatal(err)
	}
	// Routing metadata must survive a status transition: a chat task that
	// stops for approval still has to come back to the identity that owns it.
	waitingTask, err := s.TaskByID(ctx, waiting.ID)
	if err != nil || waitingTask == nil {
		t.Fatalf("TaskByID = (%+v, %v)", waitingTask, err)
	}
	if waitingTask.Status != workflow.StatusWaitingHuman || waitingTask.Source != workflow.SourceChat ||
		waitingTask.Identity != "reviewer" {
		t.Fatalf("waiting task = %+v, want waiting_human chat/reviewer routing", waitingTask)
	}

	pr, err := s.EnqueueChatTask(ctx, "acme", "todo", "open PR", "", "implement", "builder")
	if err != nil {
		t.Fatal(err)
	}
	pr.PRNumber = 7
	if err := s.Update(ctx, pr); err != nil {
		t.Fatal(err)
	}
	if err := s.Transition(ctx, pr.ID, workflow.StatusQueued, workflow.StatusPROpen, "PR #7"); err != nil {
		t.Fatal(err)
	}
	openPRs, err := s.OpenPRs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(openPRs) != 1 || openPRs[0].Source != workflow.SourceChat ||
		openPRs[0].Identity != "builder" {
		t.Fatalf("OpenPRs() = %+v, want chat/builder routing", openPRs)
	}
}
