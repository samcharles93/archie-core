package gateway

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func newTestSQLiteStore(t *testing.T) SessionStore {
	t.Helper()
	st, err := NewSQLiteSessionStoreMemory()
	if err != nil {
		t.Fatalf("NewSQLiteSessionStoreMemory: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func sqliteBase() time.Time { return time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC) }

func TestSQLiteSessionStoreRecentTurnsReturnsReplayableToolHistory(t *testing.T) {
	st := newTestSQLiteStore(t)
	ledger, ok := st.(TurnLedger)
	if !ok {
		t.Fatalf("store is %T, want TurnLedger", st)
	}
	turn, claim, err := ledger.ClaimTurn(t.Context(), TurnRecord{
		TurnID: "turn-history", SessionID: "session-history", SourceID: "source-history", OwnerID: "owner",
		ToolCalls: []ToolCallEvent{{Name: "shell", Output: "exit 0"}},
	})
	if err != nil || claim != TurnClaimOwned {
		t.Fatalf("ClaimTurn() = %#v, %v, %v", turn, claim, err)
	}
	turn.Status = TurnStatusCompleted
	turn.ResponseText = "answer"
	turn.UpdatedAt = time.Now().UTC()
	if err := ledger.SaveTurn(t.Context(), turn); err != nil {
		t.Fatalf("SaveTurn() error = %v", err)
	}
	history, ok := st.(TurnHistory)
	if !ok {
		t.Fatalf("store is %T, want TurnHistory", st)
	}
	got, err := history.RecentTurns(t.Context(), "session-history", 10)
	if err != nil || len(got) != 1 || len(got[0].ToolCalls) != 1 || got[0].ResponseText != "answer" {
		t.Fatalf("RecentTurns() = %#v, %v; want one replayable turn", got, err)
	}
}

// TestOpenSQLiteSessionStoreMigratesPreExistingTurnsTable simulates a
// database created before the tool_calls column existed: CREATE TABLE IF NOT
// EXISTS leaves such a table alone, so opening it must add the column rather
// than fail or silently keep the old shape.
func TestOpenSQLiteSessionStoreMigratesPreExistingTurnsTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	raw, err := sql.Open("sqlite", sqliteSessionDSN(path))
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	if _, err := raw.ExecContext(t.Context(), `CREATE TABLE turns (
		turn_id             TEXT PRIMARY KEY,
		session_id          TEXT NOT NULL,
		source_id           TEXT NOT NULL DEFAULT '',
		status              TEXT NOT NULL,
		attempt             INTEGER NOT NULL DEFAULT 0,
		owner_id            TEXT NOT NULL DEFAULT '',
		input_message_id    TEXT NOT NULL DEFAULT '',
		assistant_message_id TEXT NOT NULL DEFAULT '',
		partial_text       TEXT NOT NULL DEFAULT '',
		response_text      TEXT NOT NULL DEFAULT '',
		error              TEXT NOT NULL DEFAULT '',
		created_at         INTEGER NOT NULL,
		updated_at         INTEGER NOT NULL
	)`); err != nil {
		t.Fatalf("create legacy turns table: %v", err)
	}
	if _, err := raw.ExecContext(t.Context(), `INSERT INTO turns (
		turn_id, session_id, source_id, status, attempt, owner_id,
		input_message_id, assistant_message_id, partial_text, response_text,
		error, created_at, updated_at
	) VALUES ('legacy-turn', 'legacy-session', 'legacy-source', 'completed', 1, 'owner',
		'', 'assistant-1', '', 'legacy answer', '', 0, 0)`); err != nil {
		t.Fatalf("insert legacy turn row: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close legacy handle: %v", err)
	}

	store, err := OpenSQLiteSessionStore(path)
	if err != nil {
		t.Fatalf("OpenSQLiteSessionStore() on legacy schema error = %v", err)
	}
	defer func() { _ = store.Close() }()
	ledger, ok := store.(TurnLedger)
	if !ok {
		t.Fatalf("store is %T, want TurnLedger", store)
	}
	turn, ok, err := ledger.GetTurn(t.Context(), "legacy-turn")
	if err != nil || !ok {
		t.Fatalf("GetTurn() after migration = %#v, %v, %v; want the legacy row", turn, ok, err)
	}
	if turn.ResponseText != "legacy answer" || len(turn.ToolCalls) != 0 {
		t.Fatalf("migrated turn = %#v, want legacy text with no tool calls", turn)
	}

	// Reopening a second time must not fail on the "duplicate column name"
	// the migration produces once the column already exists.
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	reopened, err := OpenSQLiteSessionStore(path)
	if err != nil {
		t.Fatalf("second OpenSQLiteSessionStore() error = %v", err)
	}
	_ = reopened.Close()
}

func TestSQLiteSessionStoreRejectsExcessiveSearchQueries(t *testing.T) {
	st := newTestSQLiteStore(t)
	tests := []struct {
		name  string
		query string
	}{
		{name: "bytes", query: strings.Repeat("a", 4097)},
		{name: "terms", query: strings.Repeat("word ", 65)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := st.SearchMessages(t.Context(), "sess", MessageQuery{Query: tt.query}); err == nil {
				t.Fatal("SearchMessages accepted an excessive query")
			}
		})
	}
}

func TestOpenSQLiteSessionStoreAppliesPragmasWithTrailingQueryDelimiter(t *testing.T) {
	st, err := OpenSQLiteSessionStore(filepath.Join(t.TempDir(), "messages.db") + "?")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	sqliteStore, ok := st.(*sqliteSessionStore)
	if !ok {
		t.Fatalf("store type = %T, want *sqliteSessionStore", st)
	}
	var mode string
	if err := sqliteStore.db.QueryRowContext(t.Context(), `PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", mode)
	}
}

func TestSQLiteSessionStoreThreadIDRoundTrip(t *testing.T) {
	st := newTestSQLiteStore(t)
	want := SessionContext{
		SessionID: "sess-thread",
		Source: SessionSource{
			Platform: "telegram", BotUser: "archie", ChannelID: "-100123", ThreadID: "5",
		},
		CreatedAt: sqliteBase(), LastActiveAt: sqliteBase(),
	}
	if err := st.Save(t.Context(), want); err != nil {
		t.Fatal(err)
	}
	got, err := st.Get(t.Context(), want.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Source.ThreadID != want.Source.ThreadID || got.Source.ChannelID != want.Source.ChannelID {
		t.Fatalf("Get = %+v, want thread %q channel %q", got, want.Source.ThreadID, want.Source.ChannelID)
	}
}

func TestMessageIDsAreUnique(t *testing.T) {
	const count = 10_000
	seen := make(map[string]struct{}, count)
	for range count {
		id := newMessageID("sess", "")
		if _, exists := seen[id]; exists {
			t.Fatalf("duplicate message ID %q", id)
		}
		seen[id] = struct{}{}
	}
}

func TestCanonicalMessageIDComponentsAreInjectivelyEncoded(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		firstSession  string
		firstSource   string
		secondSession string
		secondSource  string
	}{
		{
			name:          "separator can occur in session",
			firstSession:  "a\x00b",
			firstSource:   "c",
			secondSession: "a",
			secondSource:  "b\x00c",
		},
		{
			name:          "empty session does not alias prefixed source",
			firstSession:  "",
			firstSource:   "a\x00b",
			secondSession: "\x00a",
			secondSource:  "b",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			first := CanonicalMessageID(tc.firstSession, tc.firstSource)
			second := CanonicalMessageID(tc.secondSession, tc.secondSource)
			if first == second {
				t.Fatalf("CanonicalMessageID(%q, %q) = CanonicalMessageID(%q, %q) = %q",
					tc.firstSession, tc.firstSource, tc.secondSession, tc.secondSource, first)
			}
		})
	}
}

func TestSQLiteSessionStoreRecognisesLegacyCanonicalIDOnRedelivery(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	ctx := t.Context()
	const (
		sessionID = "a\x00b"
		sourceID  = "source"
	)
	legacyID := uuid.NewSHA1(messageIDNamespace, []byte(sessionID+"\x00"+sourceID)).String()
	if _, err := sqliteStoreDB(t, store).ExecContext(ctx, `
		INSERT INTO messages (message_id, session_id, source_id, sender, text, ts)
		VALUES (?, ?, ?, 'alice', 'original', ?)`,
		legacyID, sessionID, sourceID, time.Now().Add(-time.Minute).UnixMilli()); err != nil {
		t.Fatalf("seed legacy message: %v", err)
	}

	if err := store.SaveMessage(ctx, sessionID, Message{
		MessageID: CanonicalMessageID(sessionID, sourceID),
		SourceID:  sourceID, From: "alice", Text: "redelivered",
	}); err != nil {
		t.Fatalf("SaveMessage(redelivery): %v", err)
	}
	messages, err := store.RecentMessages(ctx, sessionID, 10)
	if err != nil {
		t.Fatalf("RecentMessages: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("redelivery produced %d messages, want 1", len(messages))
	}
	if messages[0].MessageID != legacyID || messages[0].Text != "original" {
		t.Fatalf("legacy message changed on redelivery: %#v", messages[0])
	}
}

func TestSQLiteSessionStoreFindPriorReplyRecognisesLegacyCanonicalID(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	ctx := t.Context()
	const (
		sessionID = "a\x00b"
		sourceID  = "source"
		identity  = "archie"
	)
	legacyID := uuid.NewSHA1(messageIDNamespace, []byte(sessionID+"\x00"+sourceID)).String()
	inputAt := time.Now().Add(-time.Minute).UnixMilli()
	statements := []struct {
		id     string
		source string
		sender string
		text   string
		at     int64
	}{
		{id: legacyID, source: sourceID, sender: "alice", text: "question", at: inputAt},
		{id: "reply", sender: identity, text: "answer", at: inputAt + 1},
	}
	for _, row := range statements {
		if _, err := sqliteStoreDB(t, store).ExecContext(ctx, `
			INSERT INTO messages (message_id, session_id, source_id, sender, text, ts)
			VALUES (?, ?, ?, ?, ?, ?)`,
			row.id, sessionID, row.source, row.sender, row.text, row.at); err != nil {
			t.Fatalf("seed %s: %v", row.id, err)
		}
	}

	replayStore, ok := store.(TurnReplayStore)
	if !ok {
		t.Fatalf("store is %T, want TurnReplayStore", store)
	}
	got, err := replayStore.FindPriorReply(ctx, sessionID, sourceID, identity)
	if err != nil {
		t.Fatalf("FindPriorReply: %v", err)
	}
	if got != "answer" {
		t.Fatalf("FindPriorReply = %q, want legacy reply", got)
	}
}

func TestSQLiteSessionStoreCapsFutureMessageTimestamp(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	ctx := t.Context()
	now := time.Now().UTC()
	if err := store.SaveMessage(ctx, "sess", Message{
		From: "alice", Text: "future", At: now.Add(24 * time.Hour),
	}); err != nil {
		t.Fatalf("SaveMessage(future): %v", err)
	}
	if err := store.SaveMessage(ctx, "sess", Message{
		From: "alice", Text: "current", At: now,
	}); err != nil {
		t.Fatalf("SaveMessage(current): %v", err)
	}

	messages, err := store.RecentMessages(ctx, "sess", 10)
	if err != nil {
		t.Fatalf("RecentMessages: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("RecentMessages returned %d messages, want 2", len(messages))
	}
	if messages[0].At.After(time.Now().UTC().Add(time.Minute)) {
		t.Fatalf("future timestamp was persisted as %s", messages[0].At)
	}
	if !messages[1].At.After(messages[0].At) {
		t.Fatalf("current message timestamp %s does not follow capped future timestamp %s",
			messages[1].At, messages[0].At)
	}
}

func TestSQLiteSessionStoreRepairsPersistedFutureTimestampsOnReopen(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "future.db")
	store, err := OpenSQLiteSessionStore(path)
	if err != nil {
		t.Fatalf("OpenSQLiteSessionStore: %v", err)
	}
	now := time.Now().UTC()
	rows := []struct {
		id   string
		text string
		at   time.Time
	}{
		{id: "past", text: "past", at: now.Add(-time.Minute)},
		{id: "future-1", text: "future-1", at: now.Add(24 * time.Hour)},
		{id: "future-2", text: "future-2", at: now.Add(25 * time.Hour)},
	}
	for _, row := range rows {
		if _, err := sqliteStoreDB(t, store).ExecContext(ctx, `
			INSERT INTO messages (message_id, session_id, sender, text, ts)
			VALUES (?, 'sess', 'alice', ?, ?)`, row.id, row.text, row.at.UnixMilli()); err != nil {
			t.Fatalf("seed %s: %v", row.id, err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close seeded store: %v", err)
	}

	reopened, err := OpenSQLiteSessionStore(path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if err := reopened.SaveMessage(ctx, "sess", Message{
		From: "alice", Text: "current", At: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("SaveMessage(current): %v", err)
	}

	messages, err := reopened.RecentMessages(ctx, "sess", 10)
	if err != nil {
		t.Fatalf("RecentMessages: %v", err)
	}
	want := []string{"past", "future-1", "future-2", "current"}
	if got := texts(messages); !equal(got, want) {
		t.Fatalf("history = %v, want %v", got, want)
	}
	for i := 1; i < len(messages); i++ {
		if !messages[i].At.After(messages[i-1].At) {
			t.Fatalf("timestamp %d (%s) does not follow %d (%s)",
				i, messages[i].At, i-1, messages[i-1].At)
		}
	}
	if messages[len(messages)-1].At.After(time.Now().UTC().Add(time.Second)) {
		t.Fatalf("repaired history remains pinned in the future: last timestamp %s",
			messages[len(messages)-1].At)
	}
}

func TestSQLiteMessageTimestampRoundTrips(t *testing.T) {
	st := newTestSQLiteStore(t)
	want := sqliteBase().Add(90*time.Minute + 123*time.Millisecond)
	if err := st.SaveMessage(t.Context(), "sess", Message{From: "user", Text: "hello", At: want}); err != nil {
		t.Fatal(err)
	}
	got, err := st.RecentMessages(t.Context(), "sess", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[0].At.Equal(want) {
		t.Fatalf("RecentMessages = %+v, want timestamp %s", got, want)
	}
}

func TestSQLiteMessageTimestampsIncreaseAcrossStoreHandles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "messages.db")
	stores := make([]SessionStore, 2)
	for i := range stores {
		var err error
		stores[i], err = OpenSQLiteSessionStore(path)
		if err != nil {
			t.Fatal(err)
		}
		defer func(store SessionStore) { _ = store.Close() }(stores[i])
	}

	const perStore = 100
	wantTime := sqliteBase()
	var wg sync.WaitGroup
	for storeIndex, st := range stores {
		for messageIndex := range perStore {
			wg.Go(func() {
				err := st.SaveMessage(t.Context(), "sess", Message{
					MessageID: fmt.Sprintf("%d-%d", storeIndex, messageIndex),
					From:      "user",
					Text:      "concurrent",
					At:        wantTime,
				})
				if err != nil {
					t.Errorf("SaveMessage: %v", err)
				}
			})
		}
	}
	wg.Wait()

	messages, err := stores[0].RecentMessages(t.Context(), "sess", perStore*len(stores))
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != perStore*len(stores) {
		t.Fatalf("messages = %d, want %d", len(messages), perStore*len(stores))
	}
	for i := 1; i < len(messages); i++ {
		if !messages[i].At.After(messages[i-1].At) {
			t.Fatalf("timestamp %d (%s) is not after timestamp %d (%s)", i, messages[i].At, i-1, messages[i-1].At)
		}
	}
}

// TestSQLiteSessionStore_DeleteIsAtomic pins invariant 4: Delete removes the
// session and its messages together.
func TestSQLiteSessionStore_DeleteIsAtomic(t *testing.T) {
	ctx := context.Background()
	st := newTestSQLiteStore(t)

	sc := SessionContext{
		SessionID: "sess-del", Source: SessionSource{Platform: "web", ChannelID: "c"},
		CreatedAt: sqliteBase(), LastActiveAt: sqliteBase(),
	}
	if err := st.Save(ctx, sc); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := st.SaveMessage(ctx, "sess-del", Message{From: "u", Text: "hi", At: sqliteBase()}); err != nil {
		t.Fatalf("SaveMessage: %v", err)
	}

	if err := st.Delete(ctx, "sess-del"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	got, err := st.Get(ctx, "sess-del")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != nil {
		t.Error("expected session to be gone")
	}
	count, err := st.MessageCount(ctx, "sess-del")
	if err != nil {
		t.Fatalf("MessageCount: %v", err)
	}
	if count != 0 {
		t.Errorf("expected messages to be gone, got count %d", count)
	}

	// Deleting a non-existent session is a no-op.
	if err := st.Delete(ctx, "nonexistent"); err != nil {
		t.Errorf("Delete nonexistent: %v", err)
	}
}

// TestSQLiteSessionStore_SearchPagingAndTruncated pins invariant 6: Limit is
// a page size (not a cap), paging is honoured via Offset/NextOffset/HasMore,
// and Truncated is always false (SQLite has a real total count).
func TestSQLiteSessionStore_SearchPagingAndTruncated(t *testing.T) {
	ctx := context.Background()
	st := newTestSQLiteStore(t)

	const total = 30
	for i := range total {
		m := Message{From: "u", Text: "needle common", At: sqliteBase().Add(time.Duration(i) * time.Second)}
		if err := st.SaveMessage(ctx, "sess", m); err != nil {
			t.Fatalf("SaveMessage %d: %v", i, err)
		}
	}

	seen := map[string]bool{}
	offset := 0
	pages := 0
	for {
		page, err := st.SearchMessages(ctx, "sess", MessageQuery{Query: "needle", Limit: 7, Offset: offset})
		if err != nil {
			t.Fatalf("SearchMessages offset=%d: %v", offset, err)
		}
		if page.Truncated {
			t.Errorf("expected Truncated=false for SQLite backend")
		}
		for _, m := range page.Messages {
			if seen[m.MessageID] {
				t.Errorf("duplicate message across pages: %s", m.MessageID)
			}
			seen[m.MessageID] = true
		}
		pages++
		if !page.HasMore {
			break
		}
		offset = page.NextOffset
		if pages > total {
			t.Fatal("paging did not terminate")
		}
	}
	if len(seen) != total {
		t.Errorf("expected to see all %d matches across pages, got %d", total, len(seen))
	}
}

func TestSQLiteSessionStore_ErrorPaths(t *testing.T) {
	ctx := context.Background()
	st := newTestSQLiteStore(t)
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	t.Run("Save after close", func(t *testing.T) {
		if err := st.Save(ctx, SessionContext{SessionID: "x"}); err == nil {
			t.Error("expected error saving to a closed store")
		}
	})
	t.Run("Get after close", func(t *testing.T) {
		if _, err := st.Get(ctx, "x"); err == nil {
			t.Error("expected error getting from a closed store")
		}
	})
	t.Run("SaveMessage after close", func(t *testing.T) {
		if err := st.SaveMessage(ctx, "sess", Message{From: "u", Text: "hi"}); err == nil {
			t.Error("expected error saving message to a closed store")
		}
	})
	t.Run("SearchMessages after close", func(t *testing.T) {
		if _, err := st.SearchMessages(ctx, "sess", MessageQuery{Query: "hi"}); err == nil {
			t.Error("expected error searching a closed store")
		}
	})
	t.Run("Delete after close", func(t *testing.T) {
		if err := st.Delete(ctx, "sess"); err == nil {
			t.Error("expected error deleting from a closed store")
		}
	})
	t.Run("ReplaceMessages after close", func(t *testing.T) {
		if err := st.ReplaceMessages(ctx, "sess", nil, nil); err == nil {
			t.Error("expected error replacing messages on a closed store")
		}
	})
}

func TestOpenSQLiteSessionStore_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/sub/dir/sessions.db"

	st, err := OpenSQLiteSessionStore(path)
	if err != nil {
		t.Fatalf("OpenSQLiteSessionStore: %v", err)
	}
	defer func() { _ = st.Close() }()

	ctx := context.Background()
	sc := SessionContext{SessionID: "s1", Source: SessionSource{Platform: "web", ChannelID: "c"}, CreatedAt: sqliteBase(), LastActiveAt: sqliteBase()}
	if err := st.Save(ctx, sc); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := st.Get(ctx, "s1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("expected session to persist to the created file")
	}
}

// ── Adversarial file-backed coverage (bead 8u8.1) ───────────────────────────
//
// These tests are deliberately SQLite-only. They prove two properties the
// shared 19-group conformance suite cannot: file-backed durability across
// close+reopen (with the trigger-maintained FTS index intact and a clean
// PRAGMA quick_check), and transaction rollback under injected
// mid-operation failures. They reach into sqliteSessionStore internals -- the
// raw *sql.DB -- to install deterministic abort triggers and run PRAGMA
// checks, which the SessionStore interface does not expose.

// sqliteStoreDB exposes the raw handle behind a store for SQLite-specific
// tests. The store pins its pool to one connection and serializes writes
// with its own mutex, so a test touching this handle between operations is
// safe and deterministic.
func sqliteStoreDB(t *testing.T, st SessionStore) *sql.DB {
	t.Helper()
	s, ok := st.(*sqliteSessionStore)
	if !ok {
		t.Fatalf("store is %T, want *sqliteSessionStore", st)
	}
	return s.db
}

// installAbortTrigger deterministically fails the statement that matches
// clause by raising ABORT from a BEFORE trigger. Because the trigger fires
// mid-transaction (after earlier statements in the same transaction have
// already run), a correct store must roll the whole transaction back for the
// abort to be invisible to later reads.
func installAbortTrigger(t *testing.T, st SessionStore, clause string) {
	t.Helper()
	_, err := sqliteStoreDB(t, st).ExecContext(context.Background(),
		`CREATE TRIGGER test_forced_abort `+clause+` BEGIN
			SELECT RAISE(ABORT, 'forced test abort');
		END`)
	if err != nil {
		t.Fatalf("install abort trigger: %v", err)
	}
}

// TestSQLiteSessionStore_CloseReopenDurability proves that a file-backed
// store persists SessionContext fields and canonical message identity
// (MessageID, SourceID, sender, text, timestamp) across a close+reopen
// cycle, that the trigger-maintained FTS index survives with it, and that
// PRAGMA quick_check reports the file intact.
func TestSQLiteSessionStore_CloseReopenDurability(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sessions.db")

	st, err := OpenSQLiteSessionStore(path)
	if err != nil {
		t.Fatalf("OpenSQLiteSessionStore: %v", err)
	}

	wantSC := SessionContext{
		SessionID: "sess-durable",
		Source: SessionSource{
			Platform: "telegram", BotUser: "archie", ChannelID: "chat-42", ThreadID: "topic-7",
		},
		Title:           "durable title",
		ParentSessionID: "sess-parent",
		BranchName:      "feature/x",
		CreatedAt:       sqliteBase(),
		LastActiveAt:    sqliteBase().Add(time.Minute),
	}
	if err := st.Save(ctx, wantSC); err != nil {
		t.Fatalf("Save: %v", err)
	}

	wantMsgs := []Message{
		{MessageID: "canonical-1", SourceID: "tg-1001", From: "alice", Text: "first durable message", At: sqliteBase().Add(time.Second)},
		{MessageID: "canonical-2", SourceID: "tg-1002", From: "bob", Text: "second durable message", At: sqliteBase().Add(2 * time.Second)},
	}
	for _, m := range wantMsgs {
		if err := st.SaveMessage(ctx, "sess-durable", m); err != nil {
			t.Fatalf("SaveMessage %q: %v", m.MessageID, err)
		}
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	st2, err := OpenSQLiteSessionStore(path)
	if err != nil {
		t.Fatalf("reopen OpenSQLiteSessionStore: %v", err)
	}
	defer func() { _ = st2.Close() }()

	got, err := st2.Get(ctx, "sess-durable")
	if err != nil {
		t.Fatalf("Get after reopen: %v", err)
	}
	if got == nil {
		t.Fatal("expected session to survive close+reopen")
	}
	if *got != wantSC {
		t.Errorf("session after reopen:\n got %+v\nwant %+v", *got, wantSC)
	}

	hist, err := st2.RecentMessages(ctx, "sess-durable", 10)
	if err != nil {
		t.Fatalf("RecentMessages after reopen: %v", err)
	}
	if len(hist) != len(wantMsgs) {
		t.Fatalf("history after reopen: got %d messages, want %d", len(hist), len(wantMsgs))
	}
	for i, m := range hist {
		want := wantMsgs[i]
		if m.MessageID != want.MessageID || m.SourceID != want.SourceID ||
			m.From != want.From || m.Text != want.Text || !m.At.Equal(want.At) {
			t.Errorf("message %d after reopen:\n got %+v\nwant %+v", i, m, want)
		}
	}

	// The FTS index is trigger-maintained, so it must survive reopen too.
	page, err := st2.SearchMessages(ctx, "sess-durable", MessageQuery{Query: "durable"})
	if err != nil {
		t.Fatalf("SearchMessages after reopen: %v", err)
	}
	if len(page.Messages) != len(wantMsgs) {
		t.Errorf("search after reopen: got %d matches, want %d", len(page.Messages), len(wantMsgs))
	}

	var check string
	if err := sqliteStoreDB(t, st2).QueryRowContext(ctx, "PRAGMA quick_check").Scan(&check); err != nil {
		t.Fatalf("PRAGMA quick_check: %v", err)
	}
	if check != "ok" {
		t.Errorf("PRAGMA quick_check = %q, want ok", check)
	}
}

// TestSQLiteSessionStore_DeleteAbortRollsBack pins that Delete rolls its
// transaction back when a statement fails mid-operation: the messages DELETE
// has already run inside the transaction when the sessions DELETE trips the
// abort trigger, so the rollback must restore the session, its history, its
// count, and its search results.
func TestSQLiteSessionStore_DeleteAbortRollsBack(t *testing.T) {
	ctx := context.Background()
	st := newTestSQLiteStore(t)

	sc := SessionContext{
		SessionID: "sess-del-abort",
		Source:    SessionSource{Platform: "web", ChannelID: "c"},
		Title:     "keep me",
		CreatedAt: sqliteBase(), LastActiveAt: sqliteBase(),
	}
	if err := st.Save(ctx, sc); err != nil {
		t.Fatalf("Save: %v", err)
	}
	seed := []Message{
		{From: "alice", Text: "alpha", At: sqliteBase()},
		{From: "bob", Text: "beta", At: sqliteBase().Add(time.Second)},
		{From: "carol", Text: "gamma", At: sqliteBase().Add(2 * time.Second)},
	}
	for _, m := range seed {
		if err := st.SaveMessage(ctx, "sess-del-abort", m); err != nil {
			t.Fatalf("SaveMessage: %v", err)
		}
	}

	installAbortTrigger(t, st, `BEFORE DELETE ON sessions WHEN old.session_id = 'sess-del-abort'`)

	if err := st.Delete(ctx, "sess-del-abort"); err == nil {
		t.Fatal("expected Delete to fail from the forced abort trigger")
	}

	got, err := st.Get(ctx, "sess-del-abort")
	if err != nil {
		t.Fatalf("Get after failed Delete: %v", err)
	}
	if got == nil || *got != sc {
		t.Errorf("session after failed Delete: got %+v, want %+v", got, sc)
	}

	hist, err := st.RecentMessages(ctx, "sess-del-abort", 10)
	if err != nil {
		t.Fatalf("RecentMessages after failed Delete: %v", err)
	}
	if len(hist) != len(seed) {
		t.Fatalf("history after failed Delete: got %d messages, want %d", len(hist), len(seed))
	}
	for i, m := range hist {
		if m.Text != seed[i].Text || m.From != seed[i].From {
			t.Errorf("message %d after failed Delete: got %+v, want text %q from %q", i, m, seed[i].Text, seed[i].From)
		}
	}

	count, err := st.MessageCount(ctx, "sess-del-abort")
	if err != nil {
		t.Fatalf("MessageCount after failed Delete: %v", err)
	}
	if count != len(seed) {
		t.Errorf("count after failed Delete: got %d, want %d", count, len(seed))
	}

	// The FTS index must match the rolled-back history.
	page, err := st.SearchMessages(ctx, "sess-del-abort", MessageQuery{Query: "beta"})
	if err != nil {
		t.Fatalf("SearchMessages after failed Delete: %v", err)
	}
	if len(page.Messages) != 1 {
		t.Errorf("search after failed Delete: got %d matches, want 1", len(page.Messages))
	}
}

// TestSQLiteSessionStore_ReplaceMessagesAbortRollsBack pins that
// ReplaceMessages rolls its transaction back when the delete-old step fails
// mid-operation: the replacement inserts have already landed in the
// transaction when the abort fires, so the rollback must remove the
// replacement and restore the original history, count, and search results.
func TestSQLiteSessionStore_ReplaceMessagesAbortRollsBack(t *testing.T) {
	ctx := context.Background()
	st := newTestSQLiteStore(t)

	seed := []Message{
		{MessageID: "m-0", SourceID: "s-0", From: "alice", Text: "alpha", At: sqliteBase()},
		{MessageID: "m-1", SourceID: "s-1", From: "bob", Text: "beta", At: sqliteBase().Add(time.Second)},
		{MessageID: "m-2", SourceID: "s-2", From: "carol", Text: "gamma", At: sqliteBase().Add(2 * time.Second)},
	}
	if err := st.SaveMessages(ctx, "sess-replace-abort", seed); err != nil {
		t.Fatalf("SaveMessages: %v", err)
	}

	// Guard an original row so the abort fires only after the replacement
	// inserts have landed inside the transaction.
	installAbortTrigger(t, st, `BEFORE DELETE ON messages WHEN old.message_id = 'm-1'`)

	replacement := []Message{{From: "summarizer", Text: "merged summary", At: sqliteBase().Add(time.Hour)}}
	if err := st.ReplaceMessages(ctx, "sess-replace-abort", replacement, supersededIDs(ctx, t, st, "sess-replace-abort")); err == nil {
		t.Fatal("expected ReplaceMessages to fail from the forced abort trigger")
	}

	hist, err := st.RecentMessages(ctx, "sess-replace-abort", 10)
	if err != nil {
		t.Fatalf("RecentMessages after failed ReplaceMessages: %v", err)
	}
	if len(hist) != len(seed) {
		t.Fatalf("history after failed ReplaceMessages: got %d messages, want %d", len(hist), len(seed))
	}
	for i, m := range hist {
		if m.MessageID != seed[i].MessageID || m.Text != seed[i].Text || m.From != seed[i].From {
			t.Errorf("message %d after failed ReplaceMessages: got %+v, want %+v", i, m, seed[i])
		}
	}

	count, err := st.MessageCount(ctx, "sess-replace-abort")
	if err != nil {
		t.Fatalf("MessageCount after failed ReplaceMessages: %v", err)
	}
	if count != len(seed) {
		t.Errorf("count after failed ReplaceMessages: got %d, want %d", count, len(seed))
	}

	page, err := st.SearchMessages(ctx, "sess-replace-abort", MessageQuery{Query: "beta"})
	if err != nil {
		t.Fatalf("SearchMessages after failed ReplaceMessages: %v", err)
	}
	if len(page.Messages) != 1 {
		t.Errorf("search 'beta' after failed ReplaceMessages: got %d matches, want 1", len(page.Messages))
	}
	page, err = st.SearchMessages(ctx, "sess-replace-abort", MessageQuery{Query: "summary"})
	if err != nil {
		t.Fatalf("SearchMessages after failed ReplaceMessages: %v", err)
	}
	if len(page.Messages) != 0 {
		t.Errorf("search 'summary' after failed ReplaceMessages: got %d matches, want 0 (replacement must be rolled back)", len(page.Messages))
	}
}

// ── FTS lifecycle and literal-query semantics (bead 8u8.4) ──────────────────
//
// The shared 19-group conformance suite (sessionstore_conformance_test.go)
// proves behaviour through the SessionStore interface, but two properties are
// SQLite-specific and cannot be expressed there: the trigger-maintained FTS5
// index must stay consistent as history mutates, and free-text queries must
// survive hostile input. These tests are SQLite-only and assert exact
// results -- not merely the absence of panics.

// sameTexts compares two text slices as sets: same length, same members,
// order-insensitive. FTS pages are ranked by bm25, so order is a ranking
// detail rather than part of the match contract.
func sameTexts(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]int, len(a))
	for _, s := range a {
		seen[s]++
	}
	for _, s := range b {
		seen[s]--
		if seen[s] < 0 {
			return false
		}
	}
	return true
}

// ftsIntegrityErr runs the documented FTS5 external-content integrity
// check (`INSERT INTO ...(table, rank) VALUES('integrity-check', 1)`) against
// the store's raw handle and returns any error it reports. A consistent
// index reports no error; a stale entry -- a content row deleted without the
// AFTER DELETE trigger firing -- reports "database disk image is malformed".
// Search/count joins can never surface such an entry because they only see
// index rows whose content row still exists, so this is the one check that
// detects an orphaned index.
func ftsIntegrityErr(t *testing.T, st SessionStore) error {
	t.Helper()
	_, err := sqliteStoreDB(t, st).ExecContext(context.Background(),
		`INSERT INTO messages_fts(messages_fts, rank) VALUES('integrity-check', 1)`)
	return err
}

// TestSQLiteSessionStore_FTSStaysCorrectThroughMutations pins that the FTS5
// index follows every message mutation: DeleteRecentMessages removes the
// newest documents, ReplaceMessages swaps the whole document set, and Delete
// clears the session -- with search results and counts tracking the surviving
// history and never leaking across sessions.
func TestSQLiteSessionStore_FTSStaysCorrectThroughMutations(t *testing.T) {
	// Sessions "a" and "b" share the term "needle", so any cross-session
	// leak in search or count is caught; "marker" lives only in "b".
	seedA := []string{"needle alpha", "filler one", "needle beta", "filler two"}
	seedB := []string{"needle gamma", "distinct marker"}

	tests := []struct {
		name        string
		mutate      func(ctx context.Context, t *testing.T, st SessionStore) error
		wantA       []string // session a history after the mutation
		wantANeedle []string // session a search "needle"
		wantACount  int
		wantAMarker []string // session a search "marker" (only in b)
		wantBNeedle []string // session b search "needle" (must stay intact)
		wantBCount  int
	}{
		{
			name: "DeleteRecentMessages prunes the index newest-first",
			mutate: func(ctx context.Context, t *testing.T, st SessionStore) error {
				deleted, err := st.DeleteRecentMessages(ctx, "a", 2)
				if err == nil && deleted != 2 {
					t.Errorf("deleted %d messages, want 2", deleted)
				}
				return err
			},
			wantA:       []string{"needle alpha", "filler one"},
			wantANeedle: []string{"needle alpha"},
			wantACount:  2,
			wantAMarker: nil,
			wantBNeedle: []string{"needle gamma"},
			wantBCount:  2,
		},
		{
			name: "ReplaceMessages swaps the document set",
			mutate: func(ctx context.Context, t *testing.T, st SessionStore) error {
				return st.ReplaceMessages(ctx, "a", []Message{{
					From: "summarizer", Text: "merged needle summary", At: sqliteBase().Add(time.Hour),
				}}, supersededIDs(ctx, t, st, "a"))
			},
			wantA:       []string{"merged needle summary"},
			wantANeedle: []string{"merged needle summary"},
			wantACount:  1,
			wantAMarker: nil,
			wantBNeedle: []string{"needle gamma"},
			wantBCount:  2,
		},
		{
			name: "Delete clears the session's index",
			mutate: func(ctx context.Context, t *testing.T, st SessionStore) error {
				return st.Delete(ctx, "a")
			},
			wantA:       nil,
			wantANeedle: nil,
			wantACount:  0,
			wantAMarker: nil,
			wantBNeedle: []string{"needle gamma"},
			wantBCount:  2,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Subtest-local context: mutation closures receive it as a
			// parameter instead of capturing the outer function's.
			ctx := context.Background()
			st := newTestSQLiteStore(t)

			for i, text := range seedA {
				if err := st.SaveMessage(ctx, "a", Message{From: "u", Text: text, At: sqliteBase().Add(time.Duration(i) * time.Second)}); err != nil {
					t.Fatalf("SaveMessage a %q: %v", text, err)
				}
			}
			for i, text := range seedB {
				if err := st.SaveMessage(ctx, "b", Message{From: "u", Text: text, At: sqliteBase().Add(time.Duration(i) * time.Second)}); err != nil {
					t.Fatalf("SaveMessage b %q: %v", text, err)
				}
			}

			if err := tc.mutate(ctx, t, st); err != nil {
				t.Fatalf("mutate: %v", err)
			}

			// The index must be internally consistent with the surviving
			// history, not merely join-visible: an orphaned entry would be
			// invisible to the search/count joins below, so check the index
			// itself after every mutation.
			if err := ftsIntegrityErr(t, st); err != nil {
				t.Fatalf("FTS5 integrity check after %q: %v", tc.name, err)
			}

			hist, err := st.RecentMessages(ctx, "a", 100)
			if err != nil {
				t.Fatalf("RecentMessages a: %v", err)
			}
			if !equal(texts(hist), tc.wantA) {
				t.Errorf("session a history = %v, want %v", texts(hist), tc.wantA)
			}

			assertSearch := func(session, term string, want []string) {
				t.Helper()
				page, err := st.SearchMessages(ctx, session, MessageQuery{Query: term, Limit: 100})
				if err != nil {
					t.Fatalf("SearchMessages(%s, %q): %v", session, term, err)
				}
				if !sameTexts(texts(page.Messages), want) {
					t.Errorf("search %s %q = %v, want %v", session, term, texts(page.Messages), want)
				}
				if page.Truncated || page.HasMore {
					t.Errorf("search %s %q: Truncated=%v HasMore=%v, want false/false", session, term, page.Truncated, page.HasMore)
				}
			}

			assertSearch("a", "needle", tc.wantANeedle)
			assertSearch("a", "marker", tc.wantAMarker)
			assertSearch("b", "needle", tc.wantBNeedle)

			for _, sc := range []struct {
				session string
				want    int
			}{
				{"a", tc.wantACount}, {"b", tc.wantBCount},
			} {
				count, err := st.MessageCount(ctx, sc.session)
				if err != nil {
					t.Fatalf("MessageCount %s: %v", sc.session, err)
				}
				if count != sc.want {
					t.Errorf("MessageCount %s = %d, want %d", sc.session, count, sc.want)
				}
			}

			histB, err := st.RecentMessages(ctx, "b", 100)
			if err != nil {
				t.Fatalf("RecentMessages b: %v", err)
			}
			if !equal(texts(histB), seedB) {
				t.Errorf("session b history = %v, want untouched %v", texts(histB), seedB)
			}
		})
	}
}

// TestSQLiteSessionStore_FTSIntegrityCheckDetectsOrphanedIndex proves the
// integrity check used by the lifecycle test is load-bearing: with the AFTER
// DELETE trigger temporarily dropped, a store mutation leaves a stale
// messages_fts entry that the check must report, while the same mutation with
// the trigger intact leaves a consistent index.
func TestSQLiteSessionStore_FTSIntegrityCheckDetectsOrphanedIndex(t *testing.T) {
	t.Run("stale index entry fails the check", func(t *testing.T) {
		ctx := context.Background()
		st := newTestSQLiteStore(t)

		for i, text := range []string{"needle one", "needle two"} {
			if err := st.SaveMessage(ctx, "sess", Message{From: "u", Text: text, At: sqliteBase().Add(time.Duration(i) * time.Second)}); err != nil {
				t.Fatalf("SaveMessage: %v", err)
			}
		}

		// Bypass the AFTER DELETE trigger so the index keeps a row for the
		// deleted message.
		if _, err := sqliteStoreDB(t, st).ExecContext(ctx, `DROP TRIGGER messages_ad`); err != nil {
			t.Fatalf("drop delete trigger: %v", err)
		}
		if _, err := st.DeleteRecentMessages(ctx, "sess", 1); err != nil {
			t.Fatalf("DeleteRecentMessages: %v", err)
		}

		if err := ftsIntegrityErr(t, st); err == nil {
			t.Fatal("FTS5 integrity check passed despite a stale index entry (delete trigger bypassed)")
		}
	})

	t.Run("trigger-maintained delete keeps the index consistent", func(t *testing.T) {
		ctx := context.Background()
		st := newTestSQLiteStore(t)

		for i, text := range []string{"needle one", "needle two"} {
			if err := st.SaveMessage(ctx, "sess", Message{From: "u", Text: text, At: sqliteBase().Add(time.Duration(i) * time.Second)}); err != nil {
				t.Fatalf("SaveMessage: %v", err)
			}
		}
		if _, err := st.DeleteRecentMessages(ctx, "sess", 1); err != nil {
			t.Fatalf("DeleteRecentMessages: %v", err)
		}

		if err := ftsIntegrityErr(t, st); err != nil {
			t.Fatalf("FTS5 integrity check failed after trigger-maintained delete: %v", err)
		}
	})
}

// TestSQLiteSessionStore_HostileQueriesAreLiteral pins that free-text input
// is never executed as FTS5 query syntax: column filters, the NEAR operator,
// prefix wildcards, and boolean operators must all be treated as literal
// terms (or, for a bare "*", an empty phrase) without syntax errors. Each
// row asserts the exact match set, not merely the absence of a panic.
func TestSQLiteSessionStore_HostileQueriesAreLiteral(t *testing.T) {
	ctx := context.Background()
	st := newTestSQLiteStore(t)

	seed := []string{
		"text:hello here", // phrase (text, hello)
		"hello there",     // would match a raw "text:hello" column filter
		"hello world",
		`hello "world quote`, // unmatched-quote input must still find this
		"near a b",           // phrase (near, a) AND token b
		"a far b",            // would match a raw NEAR(a b) operator
		"star * symbol",      // "*" is a separator: never a token, never matches
		"needle or elsewhere",
		"needle elsewhere", // would match a raw OR query
		"needle",
		"alpha beta gamma", // both AND terms present
		"alpha alone",
		"beta alone",
	}
	for i, text := range seed {
		if err := st.SaveMessage(ctx, "sess", Message{From: "u", Text: text, At: sqliteBase().Add(time.Duration(i) * time.Second)}); err != nil {
			t.Fatalf("SaveMessage %q: %v", text, err)
		}
	}

	tests := []struct {
		name  string
		query string
		want  []string
	}{
		{"column syntax is a literal phrase, not a column filter", "text:hello", []string{"text:hello here"}},
		{"unmatched quote is literal, not a syntax error", `hello "world`, []string{"hello world", `hello "world quote`}},
		{"NEAR syntax is literal, not the NEAR operator", "NEAR(a b)", []string{"near a b"}},
		{"bare wildcard is an empty phrase, not a prefix operator", "*", nil},
		{"OR is a literal term, not the boolean operator", "needle OR elsewhere", []string{"needle or elsewhere"}},
		{"multi-term query pins AND semantics", "alpha beta", []string{"alpha beta gamma"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			page, err := st.SearchMessages(ctx, "sess", MessageQuery{Query: tc.query, Limit: 100})
			if err != nil {
				t.Fatalf("SearchMessages(%q): %v", tc.query, err)
			}
			if !sameTexts(texts(page.Messages), tc.want) {
				t.Errorf("query %q matched %v, want %v", tc.query, texts(page.Messages), tc.want)
			}
			if page.Truncated || page.HasMore {
				t.Errorf("query %q: Truncated=%v HasMore=%v, want false/false", tc.query, page.Truncated, page.HasMore)
			}
		})
	}
}

// TestFTSMatchQueryEscapesHostileInput pins the exact MATCH expression the
// store generates: every whitespace-separated term becomes a quoted phrase
// (embedded quotes doubled), joined by explicit AND, so user input can never
// reach the FTS5 parser as query syntax.
func TestFTSMatchQueryEscapesHostileInput(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", ""},
		{"   ", ""},
		{"alpha beta", `"alpha" AND "beta"`},
		{"needle OR elsewhere", `"needle" AND "OR" AND "elsewhere"`},
		{"text:hello", `"text:hello"`},
		{`hello "world`, `"hello" AND """world"`},
		{"NEAR(a b)", `"NEAR(a" AND "b)"`},
		{"*", `"*"`},
	}
	for _, tc := range tests {
		if got := ftsMatchQuery(tc.in); got != tc.want {
			t.Errorf("ftsMatchQuery(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
