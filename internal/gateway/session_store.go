package gateway

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
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

	// ReplaceMessages makes msgs the session's entire history. It is the
	// /compress primitive. The replacement is written before the previous
	// history is removed, so an interrupted call leaves both sets
	// present -- recoverable duplicates -- rather than an empty session.
	ReplaceMessages(ctx context.Context, sessionID string, msgs []Message) error

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
	// Query is the free-text search. Every term must be present. Terms
	// match a message's text and its sender, so searching a username
	// returns that user's messages.
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
	//
	// Paging is by offset over a relevance-ranked list, and the ranking
	// uses collection-wide term statistics, so a message saved between
	// two page fetches re-scores the whole list. A walk across an active
	// session can therefore skip or repeat a result; treat the pages as a
	// snapshot-in-motion, not a transactional cursor.
	NextOffset int
	// HasMore reports whether further pages exist.
	HasMore bool
	// Truncated reports that paging reached MaxSearchResults, so HasMore
	// is false even though matches may remain. Callers should narrow the
	// query rather than page further.
	//
	// It is conservative at the boundary: a query matching exactly
	// MaxSearchResults documents is indistinguishable from one matching
	// more, because the engine returns a ranked slice with no total and no
	// cursor. Such a query reports Truncated even though every match was
	// in fact returned.
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
	// session ID. Guarded by mu, bounded by maxCachedMessageDBs.
	msgDBs map[string]*sdk.DocDB
	// msgOrder is msgDBs' LRU order, least-recently-used first.
	msgOrder []string
	// lastTS caches the newest persisted timestamp per session, in Unix
	// milliseconds, so the monotonic clamp costs one store read per
	// session rather than one per write. Guarded by mu.
	lastTS map[string]int64
}

// NewSessionStore creates a SessionStore backed by an existing NellDB
// store. The store's NodeID is used to stamp records.
func NewSessionStore(st nl.Store, nodeID string) SessionStore {
	return &nellSessionStore{
		db:     sdk.New(st, nodeID, "sessions"),
		store:  st,
		nodeID: nodeID,
		msgDBs: make(map[string]*sdk.DocDB),
		lastTS: make(map[string]int64),
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

	// Messages go first. If this fails partway the session document still
	// points at the surviving history, so a retry can finish the job;
	// removing the session first would strand those messages unreachable.
	db := s.messagesFor(sessionID)
	result, err := db.AllDocs(ctx, sdk.DocRange{IncludeDocs: false})
	if err != nil {
		return fmt.Errorf("sessionstore: delete messages: %w", err)
	}
	for _, row := range result.Rows {
		if _, err := db.Remove(ctx, row.ID); err != nil && !errors.Is(err, sdk.ErrNotFound) {
			return fmt.Errorf("sessionstore: delete message %s: %w", row.ID, err)
		}
	}
	s.dropMsgDB(sessionID)

	if _, err := s.db.Remove(ctx, sessionID); err != nil && !errors.Is(err, sdk.ErrNotFound) {
		return fmt.Errorf("sessionstore: delete: %w", err)
	}
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
	// Newest first. AllDocs sorts by document ID, which for sessions is the
	// session ID -- reversing that produced reverse-lexicographic order and
	// passed it off as recency, so a restart resolved into an arbitrary old
	// session. Sort on the field that actually means recency, falling back
	// to creation for records never touched.
	slices.SortStableFunc(out, func(a, b SessionContext) int {
		return sessionRecency(b).Compare(sessionRecency(a))
	})
	return out, nil
}

// sessionRecency is the instant a session was last meaningfully used.
func sessionRecency(sc SessionContext) time.Time {
	if !sc.LastActiveAt.IsZero() {
		return sc.LastActiveAt
	}
	return sc.CreatedAt
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

// maxCachedMessageDBs bounds the per-session DocDB cache. Each entry holds
// a rev map with one entry per live message in that collection, so an
// unbounded cache would grow with every session the daemon ever touches.
const maxCachedMessageDBs = 64

// messagesFor returns the cached DocDB for a session's message collection,
// creating it on first use. Callers must hold s.mu: sdk.New reindexes the
// collection from the log store, so the cache keeps repeat access cheap.
// The cache is LRU-bounded; eviction only drops a rev cache that the next
// sdk.New rebuilds, so it costs time rather than correctness.
func (s *nellSessionStore) messagesFor(sessionID string) *sdk.DocDB {
	if db, ok := s.msgDBs[sessionID]; ok {
		s.touchMsgDB(sessionID)
		return db
	}
	db := sdk.New(s.store, s.nodeID, msgCollection(sessionID))
	s.msgDBs[sessionID] = db
	s.msgOrder = append(s.msgOrder, sessionID)
	for len(s.msgOrder) > maxCachedMessageDBs {
		evict := s.msgOrder[0]
		s.msgOrder = s.msgOrder[1:]
		delete(s.msgDBs, evict)
		// lastTS is re-read from the store on next use.
		delete(s.lastTS, evict)
	}
	return db
}

// touchMsgDB moves a session to the most-recently-used end of the cache.
func (s *nellSessionStore) touchMsgDB(sessionID string) {
	if i := slices.Index(s.msgOrder, sessionID); i >= 0 {
		s.msgOrder = append(slices.Delete(s.msgOrder, i, i+1), sessionID)
	}
}

// dropMsgDB removes a session from the cache entirely.
func (s *nellSessionStore) dropMsgDB(sessionID string) {
	delete(s.msgDBs, sessionID)
	delete(s.lastTS, sessionID)
	if i := slices.Index(s.msgOrder, sessionID); i >= 0 {
		s.msgOrder = slices.Delete(s.msgOrder, i, i+1)
	}
}

// msgID builds a time-ordered, collision-free document ID. Ordering is
// driven by the _ts field, but a matching ID order keeps equal-timestamp
// ties stable across scans and cursors. The counter disambiguates messages
// that land within the same nanosecond; it resets on restart, so uniqueness
// across restarts rests on the nanosecond timestamp.
// messageIDNamespace scopes the deterministic derivation below. It is a
// fixed, arbitrary UUID: only its stability matters.
var messageIDNamespace = uuid.MustParse("6f8d2b1e-9c4a-4f37-8a56-1d0e7b3c9a42")

// newMessageID returns the canonical, application-generated identifier a
// message is stored under.
//
// Platform identifiers are correlation metadata, never the canonical ID, so
// a channel-native ID is not used as the key. It is instead an input to a
// deterministic derivation scoped to the session, which keeps the canonical
// ID archie's own while still making redelivery of the same upstream
// message resolve to the same record. Scoping by session means the same
// upstream message copied into a branch gets its own identity.
//
// Messages with no upstream identity get a random ID and always append.
func newMessageID(sessionID, sourceID string) string {
	if sourceID == "" {
		return uuid.NewString()
	}
	return uuid.NewSHA1(messageIDNamespace, []byte(sessionID+"\x00"+sourceID)).String()
}

// messageToDoc renders a message for persistence. at must already be
// resolved to a concrete instant.
//
// Only fields that should be findable by SearchMessages belong here as
// strings: NellDB's text index tokenizes every string value in the payload
// except a small reserved set, so an RFC3339 _ts would make "2026" match
// every message in the session. _ts is therefore written as Unix
// milliseconds, which Record.Timestamp accepts and the indexer ignores
// because it is a number. Channel and thread are session-level constants
// (a session is a platform/bot/channel/thread tuple) and are deliberately
// not persisted per message. source_id is persisted per message because it
// is the external correlation metadata an export must carry, and it is only
// written when present: a message with no upstream identity produces the
// same document it always did. It is stored as the integer values of its
// UTF-8 bytes rather than as a string, because the indexer ignores numbers
// -- a plain string would make external correlation IDs match
// SearchMessages. The byte array survives the log store's JSON round-trip.
// messageID must already be the canonical ID the message is stored under.
func messageToDoc(messageID string, msg Message, at time.Time) sdk.Doc {
	doc := sdk.Doc{
		sdk.FieldID: messageID,
		// NellDB orders scans on _ts, preferring it over the record HLC.
		nl.TimestampField: at.UnixMilli(),
		"from":            msg.From,
		"text":            msg.Text,
	}
	if msg.SourceID != "" {
		doc["source_id"] = utf8ByteValues(msg.SourceID)
	}
	return doc
}

func docToMessage(doc sdk.Doc) Message {
	msg := Message{
		MessageID: strField(doc, sdk.FieldID),
		From:      strField(doc, "from"),
		Text:      strField(doc, "text"),
	}
	if ms, ok := numField(doc, nl.TimestampField); ok {
		msg.At = time.UnixMilli(ms).UTC()
	}
	// source_id is optional: messages written without one never carry the
	// field, and documents persisted before it was stored lack it, so a
	// missing key reads back as "". It is stored as UTF-8 byte values (see
	// sourceIDFromDoc), never as a string, to keep correlation IDs out of
	// the text index.
	msg.SourceID = sourceIDFromDoc(doc)
	return msg
}

// utf8ByteValues renders s as the integer values of its UTF-8 bytes, the
// persisted shape of source_id. Numbers are invisible to NellDB's text
// index, so the correlation ID stays out of SearchMessages, and the array
// survives the log store's JSON round-trip exactly. []byte is deliberately
// not used: JSON would encode it as a base64 string, which is both
// searchable and not the original text.
func utf8ByteValues(s string) []int {
	b := []byte(s)
	out := make([]int, len(b))
	for i, c := range b {
		out[i] = int(c)
	}
	return out
}

// sourceIDFromDoc decodes the persisted source_id representation back into
// the original UTF-8 string. It tolerates every shape a read can produce:
// []int as written in-process, and []any of float64 after the log store's
// JSON round-trip. A missing field -- a document written before source_id
// was stored, or a message with no upstream identity -- reads back empty,
// as does any unrecognized shape (e.g. a stray string, which would be
// searchable and is therefore never echoed).
func sourceIDFromDoc(doc sdk.Doc) string {
	v, ok := doc["source_id"]
	if !ok {
		return ""
	}
	var nums []int64
	switch t := v.(type) {
	case []int:
		nums = make([]int64, len(t))
		for i, n := range t {
			nums[i] = int64(n)
		}
	case []int64:
		nums = t
	case []float64:
		nums = make([]int64, len(t))
		for i, n := range t {
			nums[i] = int64(n)
		}
	case []any:
		nums = make([]int64, len(t))
		for i, item := range t {
			var n int64
			switch x := item.(type) {
			case float64:
				n = int64(x)
			case int64:
				n = x
			case int:
				n = int64(x)
			default:
				return ""
			}
			nums[i] = n
		}
	default:
		return ""
	}
	buf := make([]byte, len(nums))
	for i, n := range nums {
		buf[i] = byte(n)
	}
	return string(buf)
}

// numField reads an integer payload field. JSON round-trips numbers as
// float64, but an in-process Doc still holds whatever the writer put there,
// so both shapes have to be handled.
func numField(doc sdk.Doc, key string) (int64, bool) {
	switch v := doc[key].(type) {
	case int64:
		return v, true
	case float64:
		return int64(v), true
	case int:
		return int64(v), true
	default:
		return 0, false
	}
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
	_, err := s.saveMessageLocked(ctx, sessionID, msg)
	return err
}

// saveMessageLocked persists one message and reports the document ID it was
// written under. Callers must hold s.mu.
func (s *nellSessionStore) saveMessageLocked(ctx context.Context, sessionID string, msg Message) (string, error) {
	db := s.messagesFor(sessionID)
	// A message carrying a canonical ID keeps it: re-saving one that was
	// read back must not mint a new identity, or branch points and related
	// records would lose their referent.
	id := msg.MessageID
	if id == "" {
		id = newMessageID(sessionID, msg.SourceID)
	}

	// Stored messages are immutable. An existing record has been seen
	// before -- a redelivered update, or a re-save of history already
	// held -- so it stands and the write is a no-op. Representing a later
	// edit means adding a related record, not rewriting this one.
	switch _, err := db.Get(ctx, id); {
	case err == nil:
		return id, nil
	case !errors.Is(err, sdk.ErrNotFound):
		return "", fmt.Errorf("sessionstore: read existing message: %w", err)
	}

	at, err := s.nextTimestamp(ctx, sessionID, db, stamp(msg))
	if err != nil {
		return "", err
	}
	if _, err := db.Put(ctx, messageToDoc(id, msg, at)); err != nil {
		return "", fmt.Errorf("sessionstore: save message: %w", err)
	}
	return id, nil
}

// nextTimestamp picks the timestamp a message is persisted under.
//
// Within a session the value is strictly increasing, which is what makes
// history an append-only linear sequence. Channels report time at wildly
// different resolutions -- a Telegram message carries whole seconds while a
// locally generated reply carries milliseconds -- so honouring the sender's
// clock verbatim lets a reply sort ahead of the message that provoked it.
// The sender's time is kept whenever it is already ahead, and otherwise
// advanced by the smallest representable step.
//
// Distinct timestamps within a session also mean the document ID never
// decides ordering, which is why the canonical ID does not encode time.
func (s *nellSessionStore) nextTimestamp(ctx context.Context, sessionID string, db *sdk.DocDB, want time.Time) (time.Time, error) {
	last, err := s.lastTimestamp(ctx, sessionID, db)
	if err != nil {
		return time.Time{}, err
	}
	ms := want.UnixMilli()
	if ms <= last {
		ms = last + 1
	}
	s.lastTS[sessionID] = ms
	return time.UnixMilli(ms).UTC(), nil
}

// lastTimestamp returns the newest persisted timestamp in a session, in
// Unix milliseconds, reading the store only on the first call per session.
func (s *nellSessionStore) lastTimestamp(ctx context.Context, sessionID string, db *sdk.DocDB) (int64, error) {
	if ms, ok := s.lastTS[sessionID]; ok {
		return ms, nil
	}
	page, err := db.ScanSince(ctx, sdk.MessageRange{Limit: 1, Desc: true})
	if err != nil {
		return 0, fmt.Errorf("sessionstore: read newest message: %w", err)
	}
	var ms int64
	if len(page.Rows) > 0 && page.Rows[0].Doc != nil {
		ms, _ = numField(page.Rows[0].Doc, nl.TimestampField)
	}
	s.lastTS[sessionID] = ms
	return ms, nil
}

func (s *nellSessionStore) SaveMessages(ctx context.Context, sessionID string, msgs []Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Each message gets its own time-ordered ID, so a bulk save appends to
	// whatever the session already holds instead of overwriting from zero.
	for _, msg := range msgs {
		if _, err := s.saveMessageLocked(ctx, sessionID, msg); err != nil {
			return err
		}
	}
	return nil
}

func (s *nellSessionStore) ReplaceMessages(ctx context.Context, sessionID string, msgs []Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	db := s.messagesFor(sessionID)
	// Snapshot the current history first, so the delete below removes
	// exactly what was there when the call started and never touches the
	// replacement written after it.
	existing, err := db.AllDocs(ctx, sdk.DocRange{IncludeDocs: false})
	if err != nil {
		return fmt.Errorf("sessionstore: replace messages: list existing: %w", err)
	}
	doomed := make(map[string]struct{}, len(existing.Rows))
	for _, row := range existing.Rows {
		doomed[row.ID] = struct{}{}
	}

	// Write the replacement before removing anything: an interruption here
	// leaves both sets present, which is recoverable, rather than an empty
	// session, which is not.
	//
	// A replacement message that carries an upstream ID lands on the same
	// document it already occupied, so anything just written is removed
	// from the delete set. Without this a read-modify-write caller --
	// ReplaceMessages(sess, history[10:]) -- would rewrite the survivors
	// and then delete them, emptying the session.
	for _, msg := range msgs {
		id, err := s.saveMessageLocked(ctx, sessionID, msg)
		if err != nil {
			return fmt.Errorf("sessionstore: replace messages: %w", err)
		}
		delete(doomed, id)
	}

	for id := range doomed {
		if _, err := db.Remove(ctx, id); err != nil && !errors.Is(err, sdk.ErrNotFound) {
			return fmt.Errorf("sessionstore: replace messages: delete %s: %w", id, err)
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
		switch _, err := db.Remove(ctx, row.ID); {
		case err == nil:
			deleted++
		case errors.Is(err, sdk.ErrNotFound):
			// Removed concurrently; it is gone either way, but it was not
			// this call that removed it, so it is not counted.
		default:
			return deleted, fmt.Errorf("sessionstore: delete message %s: %w", row.ID, err)
		}
	}
	return deleted, nil
}

func (s *nellSessionStore) MessageCount(ctx context.Context, sessionID string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.messageCountLocked(ctx, sessionID)
}

func (s *nellSessionStore) messageCountLocked(ctx context.Context, sessionID string) (int, error) {
	// AllDocs already drops NellDB's internal meta documents.
	result, err := s.messagesFor(sessionID).AllDocs(ctx, sdk.DocRange{IncludeDocs: false})
	if err != nil {
		return 0, fmt.Errorf("sessionstore: message count: %w", err)
	}
	return len(result.Rows), nil
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
	results, err := s.messagesFor(sessionID).TextSearch(ctx, q.Query, min(want, MaxSearchResults))
	if err != nil {
		return MessagePage{}, fmt.Errorf("sessionstore: search messages: %w", err)
	}
	// Truncation is a property of the result set, not of the request: the
	// engine only dropped matches if it returned a full ceiling's worth
	// while we asked for at least that many. Deciding this from `want`
	// alone would flag a deep offset over a small result set as truncated.
	truncated := want >= MaxSearchResults && len(results) >= MaxSearchResults
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
