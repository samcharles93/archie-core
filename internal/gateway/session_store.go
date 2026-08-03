package gateway

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	nl "github.com/samcharles93/NellDB"
	"github.com/samcharles93/NellDB/sdk"
)

// SessionStore persists gateway session metadata and conversation
// history. Each session tracks one (platform, bot, channel) combination
// for the lifetime of a connection.
type SessionStore interface {
	SessionLifecycle
	MessageHistory
}

// SessionLifecycle manages session CRUD and listing.
type SessionLifecycle interface {
	// Save persists a session. If a session with the same ID already
	// exists it is overwritten.
	Save(ctx context.Context, s SessionContext) error

	// Get returns the session with the given ID, or nil when not found.
	Get(ctx context.Context, sessionID string) (*SessionContext, error)

	// GetByChannel returns all sessions for the given platform and
	// channel, newest first.
	GetByChannel(ctx context.Context, platform, channelID string) ([]SessionContext, error)

	// Delete removes a session by ID. Deleting a non-existent session
	// is a no-op.
	Delete(ctx context.Context, sessionID string) error

	// Touch updates the LastActiveAt timestamp of the session to now.
	Touch(ctx context.Context, sessionID string) error

	// List returns all sessions, newest first.
	List(ctx context.Context) ([]SessionContext, error)
}

// MessageHistory manages conversation messages within a session.
type MessageHistory interface {
	// SaveMessage appends a message turn to the session's conversation
	// history. Messages are stored with causal ordering via the HLC.
	SaveMessage(ctx context.Context, sessionID string, msg Message) error

	// RecentMessages returns the most recent n messages for a session,
	// oldest first. Used to build the LLM conversation context.
	RecentMessages(ctx context.Context, sessionID string, n int) ([]Message, error)

	// DeleteRecentMessages removes the last n messages from the
	// session's conversation history. Used by /undo and /retry.
	// Returns the number of messages actually deleted.
	DeleteRecentMessages(ctx context.Context, sessionID string, n int) (deleted int, err error)

	// MessageCount returns the total number of messages stored for
	// a session. Used by /compress --preview and /undo.
	MessageCount(ctx context.Context, sessionID string) (int, error)

	// SaveMessages bulk-saves messages for branch inheritance.
	SaveMessages(ctx context.Context, sessionID string, msgs []Message) error

	// SearchMessages returns one page of messages matching the query,
	// searching the session's entire history rather than a recent window.
	SearchMessages(ctx context.Context, sessionID string, q MessageQuery) (MessagePage, error)

	// Close shuts down the underlying store.
	Close() error
}

// MessageQuery is one page request against a session's message history.
// Limit is a page size, not a cap on how much history is considered: the
// whole session is searched and the matches are returned one page at a time.
type MessageQuery struct {
	// Query is the free-text search. Terms are matched against message
	// text; every term must be present.
	Query string
	// Limit is the maximum number of messages in one page. Zero or
	// negative selects DefaultMessagePageSize; values above
	// MaxMessagePageSize are clamped.
	Limit int
	// Offset is the number of matches to skip. Callers should echo
	// MessagePage.NextOffset rather than computing it.
	Offset int
}

// MessagePage is one page of search results.
type MessagePage struct {
	// Messages are this page's matches, most relevant first.
	Messages []Message
	// NextOffset is the Offset that returns the following page. It is
	// only meaningful when HasMore is true.
	NextOffset int
	// HasMore reports whether further pages exist.
	HasMore bool
	// Truncated reports that paging reached MaxSearchResults. Further
	// matches exist in the session but the engine's ranked result set
	// cannot be paged beyond that ceiling, so HasMore is false even
	// though the result set is incomplete. Callers should narrow the
	// query rather than page further.
	Truncated bool
}

// Page-size bounds for MessageQuery.
const (
	DefaultMessagePageSize = 50
	MaxMessagePageSize     = 500
)

// MaxSearchResults is the deepest offset+limit a text search can reach.
// NellDB ranks matches and clamps the returned set to MaxTextSearchLimit,
// and the ranked list has no cursor, so this is a hard engine ceiling
// rather than a policy choice. Pages that reach it are marked Truncated.
const MaxSearchResults = nl.MaxTextSearchLimit

// ── NellDB implementation ──────────────────────────────────────────────────

// nellSessionStore implements SessionStore backed by a NellDB DocDB.
type nellSessionStore struct {
	db     *sdk.DocDB // sessions collection
	store  nl.Store
	nodeID string
	mu     sync.Mutex
	// msgDBs caches one DocDB per session message collection, keyed by
	// session ID. Guarded by mu.
	msgDBs map[string]*sdk.DocDB
	// msgSeq disambiguates message IDs written within the same nanosecond.
	msgSeq atomic.Uint64
}

// NewSessionStore creates a SessionStore backed by an existing NellDB
// store. The store's NodeID is used to stamp records.
func NewSessionStore(st nl.Store, nodeID string) SessionStore {
	return &nellSessionStore{
		db:     sdk.New(st, nodeID, "sessions"),
		store:  st,
		nodeID: nodeID,
		msgDBs: make(map[string]*sdk.DocDB),
	}
}

// NewSessionStoreMemory creates an in-memory SessionStore suitable for
// tests.
func NewSessionStoreMemory(nodeID string) SessionStore {
	return NewSessionStore(nl.NewMemoryStore(nodeID), nodeID)
}

func (s *nellSessionStore) Save(ctx context.Context, sc SessionContext) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	doc := sessionToDoc(sc)
	if _, err := s.db.Put(ctx, doc); err != nil {
		return fmt.Errorf("sessionstore: save: %w", err)
	}
	return nil
}

func (s *nellSessionStore) Get(ctx context.Context, sessionID string) (*SessionContext, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.db.Get(ctx, sessionID)
	if err != nil {
		if errors.Is(err, sdk.ErrNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("sessionstore: get: %w", err)
	}
	sc := docToSession(doc)
	return &sc, nil
}

func (s *nellSessionStore) GetByChannel(ctx context.Context, platform, channelID string) ([]SessionContext, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := s.listAllLocked(ctx)
	if err != nil {
		return nil, err
	}
	var out []SessionContext
	for _, sc := range all {
		if sc.Source.Platform == platform && sc.Source.ChannelID == channelID {
			out = append(out, sc)
		}
	}
	return out, nil
}

func (s *nellSessionStore) Delete(ctx context.Context, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Remove(ctx, sessionID)
	if err != nil && !errors.Is(err, sdk.ErrNotFound) {
		return fmt.Errorf("sessionstore: delete: %w", err)
	}
	// Drop the session's message collection too, so deleting a session does
	// not orphan its history.
	db := s.messagesFor(sessionID)
	result, err := db.AllDocs(ctx, sdk.DocRange{IncludeDocs: false})
	if err != nil {
		return fmt.Errorf("sessionstore: delete messages: %w", err)
	}
	for _, row := range result.Rows {
		if isMetaKey(row.ID) {
			continue
		}
		if _, err := db.Remove(ctx, row.ID); err != nil && !errors.Is(err, sdk.ErrNotFound) {
			return fmt.Errorf("sessionstore: delete message %s: %w", row.ID, err)
		}
	}
	delete(s.msgDBs, sessionID)
	return nil
}

func (s *nellSessionStore) Touch(ctx context.Context, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	doc, err := s.db.Get(ctx, sessionID)
	if err != nil {
		if errors.Is(err, sdk.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("sessionstore: touch: %w", err)
	}
	doc["last_active"] = time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := s.db.Put(ctx, doc); err != nil {
		return fmt.Errorf("sessionstore: touch: %w", err)
	}
	return nil
}

func (s *nellSessionStore) List(ctx context.Context) ([]SessionContext, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listAllLocked(ctx)
}

func (s *nellSessionStore) Close() error {
	return s.store.Close()
}

// ── Internal helpers ───────────────────────────────────────────────────────

func (s *nellSessionStore) listAllLocked(ctx context.Context) ([]SessionContext, error) {
	result, err := s.db.AllDocs(ctx, sdk.DocRange{IncludeDocs: true})
	if err != nil {
		return nil, fmt.Errorf("sessionstore: list: %w", err)
	}
	var out []SessionContext
	for _, row := range result.Rows {
		if row.Doc == nil || isMetaKey(row.ID) {
			continue
		}
		out = append(out, docToSession(row.Doc))
	}
	// Newest first.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

func sessionToDoc(sc SessionContext) sdk.Doc {
	doc := sdk.Doc{
		sdk.FieldID:   sc.SessionID,
		"session_id":  sc.SessionID,
		"platform":    sc.Source.Platform,
		"bot_user":    sc.Source.BotUser,
		"channel_id":  sc.Source.ChannelID,
		"created_at":  sc.CreatedAt.UTC().Format(time.RFC3339Nano),
		"last_active": sc.LastActiveAt.UTC().Format(time.RFC3339Nano),
	}
	if sc.Source.ThreadID != "" {
		doc["thread_id"] = sc.Source.ThreadID
	}
	if sc.Title != "" {
		doc["title"] = sc.Title
	}
	if sc.ParentSessionID != "" {
		doc["parent_session_id"] = sc.ParentSessionID
	}
	if sc.BranchName != "" {
		doc["branch_name"] = sc.BranchName
	}
	return doc
}

func docToSession(doc sdk.Doc) SessionContext {
	sc := SessionContext{
		SessionID: strField(doc, "session_id"),
		Source: SessionSource{
			Platform:  strField(doc, "platform"),
			BotUser:   strField(doc, "bot_user"),
			ChannelID: strField(doc, "channel_id"),
			ThreadID:  strField(doc, "thread_id"),
		},
		Title:           strField(doc, "title"),
		ParentSessionID: strField(doc, "parent_session_id"),
		BranchName:      strField(doc, "branch_name"),
	}
	if v := strField(doc, "created_at"); v != "" {
		sc.CreatedAt, _ = time.Parse(time.RFC3339Nano, v)
	}
	if v := strField(doc, "last_active"); v != "" {
		sc.LastActiveAt, _ = time.Parse(time.RFC3339Nano, v)
	}
	return sc
}

func strField(doc sdk.Doc, key string) string {
	s, _ := doc[key].(string)
	return s
}

// isMetaKey returns true for internal NellDB meta documents (e.g. counters).
func isMetaKey(key string) bool {
	return strings.HasPrefix(key, "meta:")
}

// ── Message persistence ──────────────────────────────────────────────────────

// msgCollection is the NellDB collection holding one session's messages.
// Each session gets its own collection so scans, counts, deletes and text
// search are naturally scoped to that conversation: NellDB rooms map
// directly onto collections, and ScanSince/TextSearch operate on exactly
// one collection with no session predicate of their own.
func msgCollection(sessionID string) string {
	return "messages:" + sessionID
}

// messagesFor returns the cached DocDB for a session's message collection,
// creating it on first use. Callers must hold s.mu: sdk.New reindexes the
// collection from the log store, so the cache keeps repeat access cheap.
func (s *nellSessionStore) messagesFor(sessionID string) *sdk.DocDB {
	if db, ok := s.msgDBs[sessionID]; ok {
		return db
	}
	db := sdk.New(s.store, s.nodeID, msgCollection(sessionID))
	s.msgDBs[sessionID] = db
	return db
}

// msgID builds a time-ordered, collision-free document ID. Ordering is
// driven by the _ts field, but a matching ID order keeps equal-timestamp
// ties stable across scans and cursors. The counter disambiguates messages
// that land within the same nanosecond.
func (s *nellSessionStore) msgID(at time.Time) string {
	return fmt.Sprintf("%020d-%08d", at.UnixNano(), s.msgSeq.Add(1))
}

// messageToDoc renders a message for persistence. at must already be
// resolved to a concrete instant.
func (s *nellSessionStore) messageToDoc(msg Message, at time.Time) sdk.Doc {
	return sdk.Doc{
		sdk.FieldID: s.msgID(at),
		// NellDB orders scans on _ts, preferring it over the record HLC.
		nl.TimestampField: at.Format(time.RFC3339Nano),
		"from":            msg.From,
		"text":            msg.Text,
		"channel_id":      msg.ChannelID,
		"thread_id":       msg.ThreadID,
	}
}

func docToMessage(doc sdk.Doc) Message {
	msg := Message{
		From:      strField(doc, "from"),
		Text:      strField(doc, "text"),
		ChannelID: strField(doc, "channel_id"),
		ThreadID:  strField(doc, "thread_id"),
	}
	if v := strField(doc, nl.TimestampField); v != "" {
		msg.At, _ = time.Parse(time.RFC3339Nano, v)
	}
	return msg
}

// stamp resolves a message's application time, defaulting a zero At to now
// so every record carries a usable ordering key.
func stamp(msg Message) time.Time {
	if msg.At.IsZero() {
		return time.Now().UTC()
	}
	return msg.At.UTC()
}

func (s *nellSessionStore) SaveMessage(ctx context.Context, sessionID string, msg Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveMessageLocked(ctx, sessionID, msg)
}

func (s *nellSessionStore) saveMessageLocked(ctx context.Context, sessionID string, msg Message) error {
	doc := s.messageToDoc(msg, stamp(msg))
	if _, err := s.messagesFor(sessionID).Put(ctx, doc); err != nil {
		return fmt.Errorf("sessionstore: save message: %w", err)
	}
	return nil
}

func (s *nellSessionStore) SaveMessages(ctx context.Context, sessionID string, msgs []Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Each message gets its own time-ordered ID, so a bulk save appends to
	// whatever the session already holds instead of overwriting from zero.
	for _, msg := range msgs {
		if err := s.saveMessageLocked(ctx, sessionID, msg); err != nil {
			return err
		}
	}
	return nil
}

func (s *nellSessionStore) RecentMessages(ctx context.Context, sessionID string, n int) ([]Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n <= 0 {
		return nil, nil
	}
	// Newest-first, then reversed: the caller wants the last n messages in
	// chronological order.
	rows, err := s.newestRows(ctx, sessionID, n)
	if err != nil {
		return nil, fmt.Errorf("sessionstore: recent messages: %w", err)
	}
	out := make([]Message, 0, len(rows))
	for _, row := range slices.Backward(rows) {
		if row.Doc == nil {
			continue
		}
		out = append(out, docToMessage(row.Doc))
	}
	return out, nil
}

// newestRows returns up to n rows for a session, newest first. ScanSince
// clamps its own Limit to an internal page maximum, so this walks the
// cursor until n rows are collected or the collection is exhausted --
// without it, callers asking for more than one page (e.g. /compress passing
// the full message count) would silently get a truncated answer.
// Callers must hold s.mu.
func (s *nellSessionStore) newestRows(ctx context.Context, sessionID string, n int) ([]sdk.DocRow, error) {
	db := s.messagesFor(sessionID)
	out := make([]sdk.DocRow, 0, min(n, DefaultMessagePageSize))
	cursor := ""
	for len(out) < n {
		page, err := db.ScanSince(ctx, sdk.MessageRange{
			Limit:  n - len(out),
			Desc:   true,
			Cursor: cursor,
		})
		if err != nil {
			return nil, err
		}
		if len(page.Rows) == 0 {
			break
		}
		out = append(out, page.Rows...)
		if page.Cursor == "" {
			break
		}
		cursor = page.Cursor
	}
	return out, nil
}

func (s *nellSessionStore) DeleteRecentMessages(ctx context.Context, sessionID string, n int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n <= 0 {
		return 0, nil
	}
	db := s.messagesFor(sessionID)
	rows, err := s.newestRows(ctx, sessionID, n)
	if err != nil {
		return 0, fmt.Errorf("sessionstore: delete recent messages: %w", err)
	}
	deleted := 0
	for _, row := range rows {
		if _, err := db.Remove(ctx, row.ID); err != nil && !errors.Is(err, sdk.ErrNotFound) {
			return deleted, fmt.Errorf("sessionstore: delete message %s: %w", row.ID, err)
		}
		deleted++
	}
	return deleted, nil
}

func (s *nellSessionStore) MessageCount(ctx context.Context, sessionID string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.messageCountLocked(ctx, sessionID)
}

func (s *nellSessionStore) messageCountLocked(ctx context.Context, sessionID string) (int, error) {
	result, err := s.messagesFor(sessionID).AllDocs(ctx, sdk.DocRange{IncludeDocs: false})
	if err != nil {
		return 0, fmt.Errorf("sessionstore: message count: %w", err)
	}
	count := 0
	for _, row := range result.Rows {
		if !isMetaKey(row.ID) {
			count++
		}
	}
	return count, nil
}

// SearchMessages runs a collection-scoped text search over the session's
// entire history. Limit bounds one page; Offset walks the ranked result set.
// An empty query matches nothing and returns an empty page.
func (s *nellSessionStore) SearchMessages(ctx context.Context, sessionID string, q MessageQuery) (MessagePage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if strings.TrimSpace(q.Query) == "" {
		return MessagePage{}, nil
	}
	limit := q.Limit
	if limit <= 0 {
		limit = DefaultMessagePageSize
	}
	limit = min(limit, MaxMessagePageSize)
	offset := max(q.Offset, 0)

	// TextSearch returns the top-scoring matches in a deterministic order,
	// so one extra result past the page tells us whether more remain. The
	// request is capped at the engine ceiling; reaching it is reported as
	// Truncated rather than passed off as the end of the results.
	want := offset + limit + 1
	truncated := want > MaxSearchResults
	results, err := s.messagesFor(sessionID).TextSearch(ctx, q.Query, min(want, MaxSearchResults))
	if err != nil {
		return MessagePage{}, fmt.Errorf("sessionstore: search messages: %w", err)
	}
	// A full result set at the ceiling means matches were dropped.
	if len(results) >= MaxSearchResults {
		truncated = true
	}
	if offset >= len(results) {
		return MessagePage{Truncated: truncated}, nil
	}

	hasMore := len(results) > offset+limit
	results = results[offset:min(offset+limit, len(results))]
	out := make([]Message, 0, len(results))
	for _, result := range results {
		out = append(out, docToMessage(result.Doc))
	}
	return MessagePage{
		Messages:   out,
		NextOffset: offset + len(out),
		HasMore:    hasMore,
		Truncated:  truncated && !hasMore,
	}, nil
}
