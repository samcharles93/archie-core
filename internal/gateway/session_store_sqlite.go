package gateway

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// ── SQLite implementation ───────────────────────────────────────────────────
//
// sqliteSessionStore is the durable, transactional SessionStore described by
// docs/architecture/messaging-and-work-intake.md (canonical session/message
// store uses durable SQLite round trips). It coexists with nellSessionStore;
// nothing in this file changes that implementation or the SessionStore
// interface.
//
// Schema:
//
//   - sessions holds one row per SessionContext, round-tripping every field
//     including ParentSessionID and BranchName.
//   - messages holds one row per message, keyed by the application-generated
//     canonical MessageID (session-scoped unique, not the table's rowid --
//     the integer rowid backs the FTS5 external-content index instead).
//   - messages_fts is an external-content FTS5 index over (sender, text)
//     only, kept in sync by AFTER INSERT/UPDATE/DELETE triggers on messages.
//     Because the sync is driven by triggers on the table itself rather than
//     application code, it cannot drift regardless of which Go method wrote
//     the row.
type sqliteSessionStore struct {
	db *sql.DB
	// mu serializes every write (Save, Delete, message mutations) so the
	// strictly-increasing per-session timestamp invariant holds under
	// concurrent callers: assigning the next timestamp is a read-then-write
	// over two statements, which needs an application-level lock even
	// though SQLite itself serializes at the connection level.
	mu sync.Mutex
}

const sqliteSessionSchema = `
CREATE TABLE IF NOT EXISTS sessions (
	session_id        TEXT PRIMARY KEY,
	platform          TEXT NOT NULL DEFAULT '',
	bot_user          TEXT NOT NULL DEFAULT '',
	channel_id        TEXT NOT NULL DEFAULT '',
	thread_id         TEXT NOT NULL DEFAULT '',
	title             TEXT NOT NULL DEFAULT '',
	parent_session_id TEXT NOT NULL DEFAULT '',
	branch_name       TEXT NOT NULL DEFAULT '',
	created_at        INTEGER NOT NULL DEFAULT 0,
	last_active_at    INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_sessions_channel ON sessions(platform, channel_id);

CREATE TABLE IF NOT EXISTS messages (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	message_id TEXT NOT NULL,
	session_id TEXT NOT NULL,
	source_id  TEXT NOT NULL DEFAULT '',
	sender     TEXT NOT NULL DEFAULT '',
	text       TEXT NOT NULL DEFAULT '',
	ts         INTEGER NOT NULL,
	UNIQUE(session_id, message_id)
);
CREATE INDEX IF NOT EXISTS idx_messages_session_ts ON messages(session_id, ts);

CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
	sender,
	text,
	content='messages',
	content_rowid='id'
);

CREATE TRIGGER IF NOT EXISTS messages_ai AFTER INSERT ON messages BEGIN
	INSERT INTO messages_fts(rowid, sender, text) VALUES (new.id, new.sender, new.text);
END;
CREATE TRIGGER IF NOT EXISTS messages_ad AFTER DELETE ON messages BEGIN
	INSERT INTO messages_fts(messages_fts, rowid, sender, text) VALUES('delete', old.id, old.sender, old.text);
END;
CREATE TRIGGER IF NOT EXISTS messages_au AFTER UPDATE ON messages BEGIN
	INSERT INTO messages_fts(messages_fts, rowid, sender, text) VALUES('delete', old.id, old.sender, old.text);
	INSERT INTO messages_fts(rowid, sender, text) VALUES (new.id, new.sender, new.text);
END;
`

// OpenSQLiteSessionStore opens (creating if needed) a SQLite-backed
// SessionStore.
func OpenSQLiteSessionStore(path string) (SessionStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("sessionstore: mkdir: %w", err)
	}
	return openSQLiteSessionStore(path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
}

// NewSQLiteSessionStoreMemory returns an in-memory SQLite SessionStore for
// tests.
func NewSQLiteSessionStoreMemory() (SessionStore, error) {
	return openSQLiteSessionStore(":memory:")
}

func openSQLiteSessionStore(dsn string) (SessionStore, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sessionstore: open: %w", err)
	}
	// A second connection to an in-memory database is a distinct, empty
	// database under modernc.org/sqlite, and even for the file-backed case
	// this store's own mutex already serializes every operation -- so a
	// pool beyond one connection buys nothing and risks the memory-mode
	// footgun. Pin it to one.
	db.SetMaxOpenConns(1)

	ctx := context.Background()
	if _, err := db.ExecContext(ctx, sqliteSessionSchema); err != nil {
		return nil, errors.Join(fmt.Errorf("sessionstore: init schema: %w", err), db.Close())
	}
	return &sqliteSessionStore{db: db}, nil
}

func (s *sqliteSessionStore) Close() error {
	return s.db.Close()
}

// ── Sessions ────────────────────────────────────────────────────────────────

func (s *sqliteSessionStore) Save(ctx context.Context, sc SessionContext) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions (session_id, platform, bot_user, channel_id, thread_id,
			title, parent_session_id, branch_name, created_at, last_active_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET
			platform = excluded.platform,
			bot_user = excluded.bot_user,
			channel_id = excluded.channel_id,
			thread_id = excluded.thread_id,
			title = excluded.title,
			parent_session_id = excluded.parent_session_id,
			branch_name = excluded.branch_name,
			created_at = excluded.created_at,
			last_active_at = excluded.last_active_at`,
		sc.SessionID, sc.Source.Platform, sc.Source.BotUser, sc.Source.ChannelID, sc.Source.ThreadID,
		sc.Title, sc.ParentSessionID, sc.BranchName, sc.CreatedAt.UnixMilli(), sc.LastActiveAt.UnixMilli())
	if err != nil {
		return fmt.Errorf("sessionstore: save: %w", err)
	}
	return nil
}

func (s *sqliteSessionStore) Get(ctx context.Context, sessionID string) (*SessionContext, error) {
	row := s.db.QueryRowContext(ctx, sessionSelect+` WHERE session_id = ?`, sessionID)
	sc, err := scanSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("sessionstore: get: %w", err)
	}
	return sc, nil
}

func (s *sqliteSessionStore) GetByChannel(ctx context.Context, platform, channelID string) ([]SessionContext, error) {
	rows, err := s.db.QueryContext(ctx,
		sessionSelect+` WHERE platform = ? AND channel_id = ? ORDER BY created_at DESC`,
		platform, channelID)
	if err != nil {
		return nil, fmt.Errorf("sessionstore: get by channel: %w", err)
	}
	return scanSessions(rows)
}

func (s *sqliteSessionStore) Delete(ctx context.Context, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sessionstore: delete: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE session_id = ?`, sessionID); err != nil {
		return fmt.Errorf("sessionstore: delete messages: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE session_id = ?`, sessionID); err != nil {
		return fmt.Errorf("sessionstore: delete session: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sessionstore: delete: commit: %w", err)
	}
	return nil
}

func (s *sqliteSessionStore) Touch(ctx context.Context, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET last_active_at = ? WHERE session_id = ?`,
		time.Now().UTC().UnixMilli(), sessionID)
	if err != nil {
		return fmt.Errorf("sessionstore: touch: %w", err)
	}
	return nil
}

func (s *sqliteSessionStore) List(ctx context.Context) ([]SessionContext, error) {
	rows, err := s.db.QueryContext(ctx, sessionSelect+` ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("sessionstore: list: %w", err)
	}
	return scanSessions(rows)
}

const sessionSelect = `
	SELECT session_id, platform, bot_user, channel_id, thread_id,
		title, parent_session_id, branch_name, created_at, last_active_at
	FROM sessions`

// rowScanner is satisfied by both *sql.Row and *sql.Rows, letting scanSession
// serve single-row and multi-row queries alike.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanSession(row rowScanner) (*SessionContext, error) {
	var (
		sc                    SessionContext
		createdAt, lastActive int64
	)
	err := row.Scan(&sc.SessionID, &sc.Source.Platform, &sc.Source.BotUser, &sc.Source.ChannelID,
		&sc.Source.ThreadID, &sc.Title, &sc.ParentSessionID, &sc.BranchName, &createdAt, &lastActive)
	if err != nil {
		return nil, err
	}
	sc.CreatedAt = time.UnixMilli(createdAt).UTC()
	sc.LastActiveAt = time.UnixMilli(lastActive).UTC()
	return &sc, nil
}

func scanSessions(rows *sql.Rows) ([]SessionContext, error) {
	defer func() { _ = rows.Close() }()
	var out []SessionContext
	for rows.Next() {
		sc, err := scanSession(rows)
		if err != nil {
			return nil, fmt.Errorf("sessionstore: scan session: %w", err)
		}
		out = append(out, *sc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sessionstore: scan sessions: %w", err)
	}
	return out, nil
}

// ── Messages ─────────────────────────────────────────────────────────────────

// execer is satisfied by both *sql.DB and *sql.Tx, so message-writing
// helpers work identically for a single-statement call (SaveMessage) and a
// multi-statement transaction (SaveMessages, ReplaceMessages).
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func (s *sqliteSessionStore) SaveMessage(ctx context.Context, sessionID string, msg Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := saveMessage(ctx, s.db, sessionID, msg)
	return err
}

// saveMessage persists one message and reports the canonical ID it was
// stored under (or was already stored under, for a redelivered message).
//
// A message carrying a canonical ID keeps it -- re-saving history read back
// must not mint a new identity. An existing record with that ID is left
// untouched: stored messages are immutable, so a redelivered edit is a
// no-op rather than an overwrite.
func saveMessage(ctx context.Context, ex execer, sessionID string, msg Message) (string, error) {
	id := msg.MessageID
	if id == "" {
		id = newMessageID(sessionID, msg.SourceID)
	}

	var exists int
	err := ex.QueryRowContext(ctx,
		`SELECT 1 FROM messages WHERE session_id = ? AND message_id = ?`, sessionID, id).Scan(&exists)
	switch {
	case err == nil:
		return id, nil
	case !errors.Is(err, sql.ErrNoRows):
		return "", fmt.Errorf("sessionstore: read existing message: %w", err)
	}

	at, err := nextTimestampSQL(ctx, ex, sessionID, stamp(msg))
	if err != nil {
		return "", err
	}
	_, err = ex.ExecContext(ctx, `
		INSERT INTO messages (message_id, session_id, source_id, sender, text, ts)
		VALUES (?, ?, ?, ?, ?, ?)`,
		id, sessionID, msg.SourceID, msg.From, msg.Text, at.UnixMilli())
	if err != nil {
		return "", fmt.Errorf("sessionstore: save message: %w", err)
	}
	return id, nil
}

// nextTimestampSQL picks the timestamp a message is persisted under, kept
// strictly increasing within a session so history stays an append-only
// linear sequence. Channels report time at wildly different resolutions --
// a Telegram message carries whole seconds while a locally generated reply
// carries milliseconds -- so the sender's clock is honoured whenever it is
// already ahead of the session's newest, and otherwise advanced by the
// smallest representable step.
func nextTimestampSQL(ctx context.Context, ex execer, sessionID string, want time.Time) (time.Time, error) {
	var last sql.NullInt64
	err := ex.QueryRowContext(ctx,
		`SELECT MAX(ts) FROM messages WHERE session_id = ?`, sessionID).Scan(&last)
	if err != nil {
		return time.Time{}, fmt.Errorf("sessionstore: read newest message: %w", err)
	}
	ms := want.UnixMilli()
	if last.Valid && ms <= last.Int64 {
		ms = last.Int64 + 1
	}
	return time.UnixMilli(ms).UTC(), nil
}

func (s *sqliteSessionStore) SaveMessages(ctx context.Context, sessionID string, msgs []Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sessionstore: save messages: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Each message gets its own time-ordered ID, so a bulk save appends to
	// whatever the session already holds instead of overwriting from zero.
	for _, msg := range msgs {
		if _, err := saveMessage(ctx, tx, sessionID, msg); err != nil {
			return fmt.Errorf("sessionstore: save messages: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sessionstore: save messages: commit: %w", err)
	}
	return nil
}

// ReplaceMessages writes msgs and removes exactly the messages named by
// superseded, atomically: both happen inside one transaction, so a caller
// either sees the old history or the new one, never an in-between state.
//
// Only the named messages are removed. Deleting the complement of msgs swept
// up anything saved since the caller read the history -- a chat turn arriving
// mid-compression was destroyed without ever being read.
//
// A replacement message that carries a canonical ID lands on the same row it
// already occupied (saveMessage's immutability no-op), so an ID in both lists
// is kept.
func (s *sqliteSessionStore) ReplaceMessages(
	ctx context.Context,
	sessionID string,
	msgs []Message,
	superseded []string,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sessionstore: replace messages: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	survivors := make(map[string]struct{}, len(msgs))
	for _, msg := range msgs {
		id, err := saveMessage(ctx, tx, sessionID, msg)
		if err != nil {
			return fmt.Errorf("sessionstore: replace messages: %w", err)
		}
		survivors[id] = struct{}{}
	}

	doomed := make([]any, 0, len(superseded))
	for _, id := range superseded {
		if _, kept := survivors[id]; kept {
			continue
		}
		doomed = append(doomed, id)
	}
	if len(doomed) > 0 {
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(doomed)), ",")
		query := `DELETE FROM messages WHERE session_id = ? AND message_id IN (` + placeholders + `)`
		args := append([]any{sessionID}, doomed...)
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return fmt.Errorf("sessionstore: replace messages: delete old: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sessionstore: replace messages: commit: %w", err)
	}
	return nil
}

func (s *sqliteSessionStore) RecentMessages(ctx context.Context, sessionID string, n int) ([]Message, error) {
	if n <= 0 {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT message_id, source_id, sender, text, ts FROM (
			SELECT message_id, source_id, sender, text, ts FROM messages
			WHERE session_id = ? ORDER BY ts DESC LIMIT ?
		) ORDER BY ts ASC`, sessionID, n)
	if err != nil {
		return nil, fmt.Errorf("sessionstore: recent messages: %w", err)
	}
	return scanMessages(rows)
}

func (s *sqliteSessionStore) DeleteRecentMessages(ctx context.Context, sessionID string, n int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n <= 0 {
		return 0, nil
	}

	res, err := s.db.ExecContext(ctx, `
		DELETE FROM messages WHERE id IN (
			SELECT id FROM messages WHERE session_id = ? ORDER BY ts DESC LIMIT ?
		)`, sessionID, n)
	if err != nil {
		return 0, fmt.Errorf("sessionstore: delete recent messages: %w", err)
	}
	deleted, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("sessionstore: delete recent messages: rows affected: %w", err)
	}
	return int(deleted), nil
}

func (s *sqliteSessionStore) MessageCount(ctx context.Context, sessionID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM messages WHERE session_id = ?`, sessionID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("sessionstore: message count: %w", err)
	}
	return n, nil
}

func scanMessages(rows *sql.Rows) ([]Message, error) {
	defer func() { _ = rows.Close() }()
	var out []Message
	for rows.Next() {
		var (
			msg Message
			ts  int64
		)
		if err := rows.Scan(&msg.MessageID, &msg.SourceID, &msg.From, &msg.Text, &ts); err != nil {
			return nil, fmt.Errorf("sessionstore: scan message: %w", err)
		}
		msg.At = time.UnixMilli(ts).UTC()
		out = append(out, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sessionstore: scan messages: %w", err)
	}
	return out, nil
}

// SearchMessages runs an FTS5 search over the session's entire message
// history, matching message text and sender only -- ts and other metadata
// are not part of the indexed content, so a query like "2026" cannot match
// every message via its timestamp. An empty or whitespace-only query
// returns an empty page without touching the database.
//
// SQLite gives an exact total match count, so paging here has no analogue
// to NellDB's MaxTextSearchLimit ceiling: MessagePage.Truncated is always
// false.
func (s *sqliteSessionStore) SearchMessages(ctx context.Context, sessionID string, q MessageQuery) (MessagePage, error) {
	if strings.TrimSpace(q.Query) == "" {
		return MessagePage{}, nil
	}
	limit := q.Limit
	if limit <= 0 {
		limit = DefaultMessagePageSize
	}
	limit = min(limit, MaxMessagePageSize)
	offset := max(q.Offset, 0)

	match := ftsMatchQuery(q.Query)

	var total int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM messages_fts
		JOIN messages m ON m.id = messages_fts.rowid
		WHERE messages_fts MATCH ? AND m.session_id = ?`, match, sessionID).Scan(&total)
	if err != nil {
		return MessagePage{}, fmt.Errorf("sessionstore: search messages: count: %w", err)
	}
	if offset >= total {
		return MessagePage{}, nil
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT m.message_id, m.source_id, m.sender, m.text, m.ts
		FROM messages_fts
		JOIN messages m ON m.id = messages_fts.rowid
		WHERE messages_fts MATCH ? AND m.session_id = ?
		ORDER BY bm25(messages_fts) ASC
		LIMIT ? OFFSET ?`, match, sessionID, limit, offset)
	if err != nil {
		return MessagePage{}, fmt.Errorf("sessionstore: search messages: %w", err)
	}
	msgs, err := scanSearchMessages(rows)
	if err != nil {
		return MessagePage{}, err
	}

	next := offset + len(msgs)
	return MessagePage{
		Messages:   msgs,
		NextOffset: next,
		HasMore:    next < total,
		Truncated:  false,
	}, nil
}

// scanSearchMessages scans rows shaped (message_id, source_id, sender, text,
// ts) -- the same column order SearchMessages selects, distinct from
// scanMessages' column order because the FTS join names "sender" rather
// than the "from" convention the rest of the file uses.
func scanSearchMessages(rows *sql.Rows) ([]Message, error) {
	defer func() { _ = rows.Close() }()
	var out []Message
	for rows.Next() {
		var (
			msg Message
			ts  int64
		)
		if err := rows.Scan(&msg.MessageID, &msg.SourceID, &msg.From, &msg.Text, &ts); err != nil {
			return nil, fmt.Errorf("sessionstore: scan search result: %w", err)
		}
		msg.At = time.UnixMilli(ts).UTC()
		out = append(out, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sessionstore: scan search results: %w", err)
	}
	return out, nil
}

// ftsMatchQuery turns free text into an FTS5 MATCH expression requiring
// every term to be present. Each term is wrapped as a quoted phrase --
// doubling any embedded quote to escape it -- so user input can never be
// interpreted as FTS5 query syntax (column filters, NEAR, boolean
// operators); every token is a literal string to match, joined with an
// explicit AND.
func ftsMatchQuery(query string) string {
	fields := strings.Fields(query)
	terms := make([]string, 0, len(fields))
	for _, f := range fields {
		escaped := strings.ReplaceAll(f, `"`, `""`)
		terms = append(terms, `"`+escaped+`"`)
	}
	return strings.Join(terms, " AND ")
}
