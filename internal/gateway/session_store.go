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

	// ReplaceMessages writes msgs and removes exactly the messages named by
	// superseded. It is the /compress primitive.
	//
	// The caller names what it is replacing rather than the store inferring
	// it from the complement of msgs. A caller computes a replacement from a
	// history it read, and a message that arrives in between is not part of
	// that: inferring the delete set destroyed such a message without ever
	// having read it, which is unrecoverable. Anything in neither list is
	// left alone.
	//
	// The replacement is written before the removal, so an interrupted call
	// leaves both sets present -- recoverable duplicates -- rather than an
	// empty session. IDs appearing in both msgs and superseded are kept.
	ReplaceMessages(ctx context.Context, sessionID string, msgs []Message, superseded []string) error

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
	turns  *sdk.DocDB // durable chat-turn ledger
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
		turns:  sdk.New(st, nodeID, "turns"),
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

func (s *nellSessionStore) ClaimTurn(ctx context.Context, initial TurnRecord) (TurnRecord, TurnClaim, error) {
	if initial.TurnID == "" {
		return TurnRecord{}, "", fmt.Errorf("sessionstore: turn ID is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	existing, err := s.turns.Get(ctx, initial.TurnID)
	if errors.Is(err, sdk.ErrNotFound) {
		initial.Status = TurnStatusRunning
		initial.Attempt = 1
		if initial.CreatedAt.IsZero() {
			initial.CreatedAt = now
		}
		initial.UpdatedAt = now
		if _, err := s.turns.Put(ctx, turnToDoc(initial)); err != nil {
			return TurnRecord{}, "", fmt.Errorf("sessionstore: claim turn: %w", err)
		}
		return initial, TurnClaimOwned, nil
	}
	if err != nil {
		return TurnRecord{}, "", fmt.Errorf("sessionstore: read turn: %w", err)
	}
	turn := docToTurn(existing)
	switch turn.Status {
	case TurnStatusCompleted:
		return turn, TurnClaimCompleted, nil
	case TurnStatusAccepted, TurnStatusRunning, TurnStatusPartial:
		return turn, TurnClaimInProgress, nil
	case TurnStatusFailed, TurnStatusCancelled:
		if turn.AssistantMessageID != "" {
			turn.Status = TurnStatusCompleted
			turn.Error = ""
			turn.UpdatedAt = now
			if _, err := s.turns.Put(ctx, withTurnRevision(existing, turn)); err != nil {
				return TurnRecord{}, "", fmt.Errorf("sessionstore: finalize turn: %w", err)
			}
			return turn, TurnClaimCompleted, nil
		}
		turn.Status = TurnStatusRunning
		turn.Attempt++
		turn.OwnerID = initial.OwnerID
		turn.Error = ""
		turn.UpdatedAt = now
		if _, err := s.turns.Put(ctx, withTurnRevision(existing, turn)); err != nil {
			return TurnRecord{}, "", fmt.Errorf("sessionstore: retry turn: %w", err)
		}
		return turn, TurnClaimOwned, nil
	default:
		return turn, TurnClaimInProgress, nil
	}
}

func (s *nellSessionStore) RecoverTurns(ctx context.Context, ownerID string) error {
	if ownerID == "" {
		return fmt.Errorf("sessionstore: recovery owner ID is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	result, err := s.turns.AllDocs(ctx, sdk.DocRange{IncludeDocs: true})
	if err != nil {
		return fmt.Errorf("sessionstore: recover turns: list: %w", err)
	}
	now := time.Now().UTC()
	for _, row := range result.Rows {
		if row.Doc == nil {
			continue
		}
		turn := docToTurn(row.Doc)
		if turn.Status != TurnStatusAccepted && turn.Status != TurnStatusRunning && turn.Status != TurnStatusPartial {
			continue
		}
		if turn.OwnerID == ownerID {
			continue
		}
		turn.Status = TurnStatusFailed
		turn.Error = "recovered after process restart"
		turn.UpdatedAt = now
		if _, err := s.turns.Put(ctx, withTurnRevision(row.Doc, turn)); err != nil {
			return fmt.Errorf("sessionstore: recover turn %s: %w", turn.TurnID, err)
		}
	}
	return nil
}

func (s *nellSessionStore) GetTurn(ctx context.Context, turnID string) (TurnRecord, bool, error) {
	if turnID == "" {
		return TurnRecord{}, false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.turns.Get(ctx, turnID)
	if errors.Is(err, sdk.ErrNotFound) {
		return TurnRecord{}, false, nil
	}
	if err != nil {
		return TurnRecord{}, false, fmt.Errorf("sessionstore: get turn: %w", err)
	}
	return docToTurn(doc), true, nil
}

func (s *nellSessionStore) SaveTurn(ctx context.Context, turn TurnRecord) error {
	if turn.TurnID == "" {
		return fmt.Errorf("sessionstore: turn ID is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.turns.Get(ctx, turn.TurnID)
	if errors.Is(err, sdk.ErrNotFound) {
		return fmt.Errorf("sessionstore: save turn: turn not found")
	}
	if err != nil {
		return fmt.Errorf("sessionstore: save turn: read: %w", err)
	}
	currentTurn := docToTurn(current)
	if currentTurn.OwnerID != turn.OwnerID || currentTurn.Attempt != turn.Attempt {
		return fmt.Errorf("%w: %s", ErrTurnOwnershipLost, turn.TurnID)
	}
	if turn.UpdatedAt.IsZero() {
		turn.UpdatedAt = time.Now().UTC()
	}
	if _, err := s.turns.Put(ctx, withTurnRevision(current, turn)); err != nil {
		return fmt.Errorf("sessionstore: save turn: %w", err)
	}
	return nil
}

func (s *nellSessionStore) ListRecoverableTurns(ctx context.Context) ([]TurnRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result, err := s.turns.AllDocs(ctx, sdk.DocRange{IncludeDocs: true})
	if err != nil {
		return nil, fmt.Errorf("sessionstore: list recoverable turns: %w", err)
	}
	turns := make([]TurnRecord, 0)
	for _, row := range result.Rows {
		if row.Doc == nil {
			continue
		}
		turn := docToTurn(row.Doc)
		switch turn.Status {
		case TurnStatusAccepted, TurnStatusRunning, TurnStatusPartial:
			turns = append(turns, turn)
		}
	}
	slices.SortFunc(turns, func(a, b TurnRecord) int {
		return a.UpdatedAt.Compare(b.UpdatedAt)
	})
	return turns, nil
}

func turnToDoc(turn TurnRecord) sdk.Doc {
	return sdk.Doc{
		sdk.FieldID:            turn.TurnID,
		"session_id":           turn.SessionID,
		"source_id":            turn.SourceID,
		"status":               string(turn.Status),
		"attempt":              turn.Attempt,
		"owner_id":             turn.OwnerID,
		"input_message_id":     turn.InputMessageID,
		"assistant_message_id": turn.AssistantMessageID,
		"partial_text":         turn.PartialText,
		"response_text":        turn.ResponseText,
		"error":                turn.Error,
		"created_at":           turn.CreatedAt.UnixMilli(),
		"updated_at":           turn.UpdatedAt.UnixMilli(),
	}
}

func withTurnRevision(current sdk.Doc, turn TurnRecord) sdk.Doc {
	doc := turnToDoc(turn)
	if revision, ok := current[sdk.FieldRev]; ok {
		doc[sdk.FieldRev] = revision
	}
	return doc
}

func docToTurn(doc sdk.Doc) TurnRecord {
	turn := TurnRecord{
		TurnID:             strField(doc, sdk.FieldID),
		SessionID:          strField(doc, "session_id"),
		SourceID:           strField(doc, "source_id"),
		Status:             TurnStatus(strField(doc, "status")),
		OwnerID:            strField(doc, "owner_id"),
		InputMessageID:     strField(doc, "input_message_id"),
		AssistantMessageID: strField(doc, "assistant_message_id"),
		PartialText:        strField(doc, "partial_text"),
		ResponseText:       strField(doc, "response_text"),
		Error:              strField(doc, "error"),
	}
	if attempt, ok := numField(doc, "attempt"); ok {
		turn.Attempt = int(attempt)
	}
	if ms, ok := numField(doc, "created_at"); ok {
		turn.CreatedAt = time.UnixMilli(ms).UTC()
	}
	if ms, ok := numField(doc, "updated_at"); ok {
		turn.UpdatedAt = time.UnixMilli(ms).UTC()
	}
	return turn
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

	turns, err := s.turns.AllDocs(ctx, sdk.DocRange{IncludeDocs: true})
	if err != nil {
		return fmt.Errorf("sessionstore: delete turns: %w", err)
	}
	for _, row := range turns.Rows {
		if row.Doc == nil || strField(row.Doc, "session_id") != sessionID {
			continue
		}
		if _, err := s.turns.Remove(ctx, row.ID); err != nil && !errors.Is(err, sdk.ErrNotFound) {
			return fmt.Errorf("sessionstore: delete turn %s: %w", row.ID, err)
		}
	}

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
//
// The later of the two timestamps, matching the SQLite side's
// MAX(last_active_at, created_at). Preferring LastActiveAt whenever it was
// set diverged from that whenever it predated CreatedAt -- a clock step, or
// a record written by an older version -- and the two backends then ordered
// the same sessions differently.
func sessionRecency(sc SessionContext) time.Time {
	if sc.LastActiveAt.After(sc.CreatedAt) {
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
// saveMessageLocked appends a message, clamping its timestamp so history
// stays a strictly increasing sequence.
func (s *nellSessionStore) saveMessageLocked(ctx context.Context, sessionID string, msg Message) (string, error) {
	return s.saveMessageAt(ctx, sessionID, msg, true)
}

// saveMessageAt writes a message, optionally without the monotonic clamp.
//
// The clamp belongs to the append path: an inbound message must not sort
// ahead of the one that provoked it. It is wrong for a caller rewriting
// history, which is placing records deliberately -- a compression summary
// stands in for the span it replaced and belongs where that span was, not at
// the end. Clamping it there put the summary after the messages it was meant
// to precede, so the model read the recent conversation and was only then
// told that earlier context had been summarised.
func (s *nellSessionStore) saveMessageAt(ctx context.Context, sessionID string, msg Message, clamp bool) (string, error) {
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

	at := stamp(msg)
	if clamp {
		var err error
		if at, err = s.nextTimestamp(ctx, sessionID, db, at); err != nil {
			return "", err
		}
	} else if ms := at.UnixMilli(); ms > s.lastTS[sessionID] {
		// Keep the cache honest so a later append still sorts after this.
		s.lastTS[sessionID] = ms
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

func (s *nellSessionStore) ReplaceMessages(
	ctx context.Context,
	sessionID string,
	msgs []Message,
	superseded []string,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	db := s.messagesFor(sessionID)
	// Only what the caller named is removed. Deriving this from the store's
	// current contents would sweep up messages saved since the caller read
	// the history -- messages it never saw and never summarised.
	doomed := make(map[string]struct{}, len(superseded))
	for _, id := range superseded {
		doomed[id] = struct{}{}
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
		// No clamp: the caller is reconstructing a history and has already
		// decided where each record belongs.
		id, err := s.saveMessageAt(ctx, sessionID, msg, false)
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

func (s *nellSessionStore) FindPriorReply(ctx context.Context, sessionID, sourceID, identity string) (string, error) {
	if sourceID == "" {
		return "", nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	id := CanonicalMessageID(sessionID, sourceID)
	db := s.messagesFor(sessionID)
	if _, err := db.Get(ctx, id); err != nil {
		if errors.Is(err, sdk.ErrNotFound) {
			return "", nil
		}
		return "", fmt.Errorf("sessionstore: find source message: %w", err)
	}
	count, err := s.messageCountLocked(ctx, sessionID)
	if err != nil {
		return "", err
	}
	rows, err := s.newestRows(ctx, sessionID, count)
	if err != nil {
		return "", fmt.Errorf("sessionstore: find prior reply: %w", err)
	}
	for i := len(rows) - 1; i >= 0; i-- {
		if rows[i].Doc == nil || strField(rows[i].Doc, sdk.FieldID) != id {
			continue
		}
		if i == 0 || rows[i-1].Doc == nil {
			return "", nil
		}
		reply := docToMessage(rows[i-1].Doc)
		if reply.From == identity && !isCompressionSummary(reply.Text) {
			return reply.Text, nil
		}
		return "", nil
	}
	return "", nil
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

// CanonicalMessageID returns the identity the store assigns to a message
// carrying sourceID in this session, or "" when there is no upstream
// reference to derive one from.
//
// It exists so a caller can ask "have I already handled this upstream
// message?" without duplicating the derivation. Duplicating it would let the
// two drift, and a lookup that silently never matches looks exactly like a
// message that has never been seen.
func CanonicalMessageID(sessionID, sourceID string) string {
	if sourceID == "" {
		return ""
	}
	return newMessageID(sessionID, sourceID)
}

// PriorReply returns the reply already produced for an upstream message, or
// "" when it has not been answered yet.
//
// Making the store idempotent stopped redelivery duplicating the inbound
// *record*, but nothing consulted that: the chat path saved the message,
// ignored the fact that nothing changed, and called the model again. That
// meant a second billed turn, tool side effects run twice, and a second
// assistant reply appended -- replies carry no upstream reference, so they
// always get a fresh identity and always append.
//
// An answered message is one immediately followed by a turn from identity.
// A message with no reply after it was never answered -- the turn may have
// died partway -- and re-running is the right outcome there, since the user
// is still owed an answer.
//
// history is the recent window the caller already loaded, so a redelivery of
// something older than that window is not recognised. Channels redeliver only
// their recent undelivered queue, so this covers the case that occurs.
func PriorReply(history []Message, sessionID, sourceID, identity string) string {
	id := CanonicalMessageID(sessionID, sourceID)
	if id == "" {
		return ""
	}
	for i, m := range history {
		if m.MessageID != id {
			continue
		}
		if i+1 < len(history) && history[i+1].From == identity &&
			!isCompressionSummary(history[i+1].Text) {
			return history[i+1].Text
		}
		return ""
	}
	return ""
}

// isCompressionSummary reports whether a record is a compression summary
// rather than a turn in the conversation.
//
// The summary is attributed to the bot so it replays to the model with the
// right role, which makes it indistinguishable from a reply by sender alone.
// Treating it as one meant a redelivered but unanswered question was
// "answered" with the compression banner and the model was never called --
// the user's question silently swallowed.
func isCompressionSummary(text string) bool {
	return strings.HasPrefix(text, DefaultCompressionConfig().SummaryMarker)
}
