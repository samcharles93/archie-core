package gateway

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	nl "github.com/samcharles93/NellDB"
	"github.com/samcharles93/NellDB/sdk"
)

// SessionStore persists gateway session metadata and conversation
// history. Each session tracks one (platform, bot, channel) combination
// for the lifetime of a connection.
type SessionStore interface {
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

	// ── Message persistence ──────────────────────────────────────────

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

	// SearchMessages returns messages matching query via semantic
	// vector search (NellDB SearchSimilar). Falls back to substring
	// match on message text when vector search is unavailable.
	SearchMessages(ctx context.Context, sessionID, query string, limit int) ([]Message, error)

	// Close shuts down the underlying store.
	Close() error
}

// ── NellDB implementation ──────────────────────────────────────────────────

// nellSessionStore implements SessionStore backed by a NellDB DocDB.
type nellSessionStore struct {
	db     *sdk.DocDB // sessions collection
	msgDB  *sdk.DocDB // messages collection
	store  nl.Store
	nodeID string
	mu     sync.Mutex
}

// NewSessionStore creates a SessionStore backed by an existing NellDB
// store. The store's NodeID is used to stamp records.
func NewSessionStore(st nl.Store, nodeID string) SessionStore {
	return &nellSessionStore{
		db:     sdk.New(st, nodeID, "sessions"),
		msgDB:  sdk.New(st, nodeID, "messages"),
		store:  st,
		nodeID: nodeID,
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

// msgKey builds a sortable document key for a message within a session.
// Format: {sessionID}:{seq} where seq is zero-padded to 20 digits for
// lexicographic ordering.
func msgKey(sessionID string, seq int64) string {
	return fmt.Sprintf("%s:%020d", sessionID, seq)
}

func (s *nellSessionStore) SaveMessage(ctx context.Context, sessionID string, msg Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Get the next sequence number by counting existing messages.
	prefix := sessionID + ":"
	result, err := s.msgDB.AllDocs(ctx, sdk.DocRange{
		StartKey:    prefix,
		EndKey:      prefix + "\xff",
		IncludeDocs: false,
	})
	if err != nil {
		return fmt.Errorf("sessionstore: save message count: %w", err)
	}
	seq := int64(len(result.Rows))

	doc := sdk.Doc{
		sdk.FieldID:  msgKey(sessionID, seq),
		"session_id": sessionID,
		"seq":        seq,
		"from":       msg.From,
		"text":       msg.Text,
		"channel_id": msg.ChannelID,
		"thread_id":  msg.ThreadID,
		"at":         time.Now().UTC().Format(time.RFC3339Nano),
	}
	if _, err := s.msgDB.Put(ctx, doc); err != nil {
		return fmt.Errorf("sessionstore: save message: %w", err)
	}
	return nil
}

func (s *nellSessionStore) RecentMessages(ctx context.Context, sessionID string, n int) ([]Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	prefix := sessionID + ":"
	result, err := s.msgDB.AllDocs(ctx, sdk.DocRange{
		StartKey:    prefix,
		EndKey:      prefix + "\xff",
		IncludeDocs: true,
	})
	if err != nil {
		return nil, fmt.Errorf("sessionstore: recent messages: %w", err)
	}

	// Rows are sorted by key (which includes the seq). Take the last n.
	start := max(len(result.Rows)-n, 0)
	out := make([]Message, 0, n)
	for _, row := range result.Rows[start:] {
		if row.Doc == nil {
			continue
		}
		out = append(out, Message{
			From:      strField(row.Doc, "from"),
			Text:      strField(row.Doc, "text"),
			ChannelID: strField(row.Doc, "channel_id"),
			ThreadID:  strField(row.Doc, "thread_id"),
		})
	}
	return out, nil
}

func (s *nellSessionStore) DeleteRecentMessages(ctx context.Context, sessionID string, n int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	prefix := sessionID + ":"
	result, err := s.msgDB.AllDocs(ctx, sdk.DocRange{
		StartKey:    prefix,
		EndKey:      prefix + "\xff",
		IncludeDocs: false,
	})
	if err != nil {
		return 0, fmt.Errorf("sessionstore: delete recent messages: %w", err)
	}

	toDelete := min(n, len(result.Rows))

	// Delete the last n rows (newest messages).
	for i := len(result.Rows) - toDelete; i < len(result.Rows); i++ {
		if _, err := s.msgDB.Remove(ctx, result.Rows[i].ID); err != nil && !errors.Is(err, sdk.ErrNotFound) {
			return toDelete, fmt.Errorf("sessionstore: delete message %s: %w", result.Rows[i].ID, err)
		}
	}
	return toDelete, nil
}

func (s *nellSessionStore) MessageCount(ctx context.Context, sessionID string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	prefix := sessionID + ":"
	result, err := s.msgDB.AllDocs(ctx, sdk.DocRange{
		StartKey:    prefix,
		EndKey:      prefix + "\xff",
		IncludeDocs: false,
	})
	if err != nil {
		return 0, fmt.Errorf("sessionstore: message count: %w", err)
	}
	return len(result.Rows), nil
}

func (s *nellSessionStore) SaveMessages(ctx context.Context, sessionID string, msgs []Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, msg := range msgs {
		doc := sdk.Doc{
			sdk.FieldID:  msgKey(sessionID, int64(i)),
			"session_id": sessionID,
			"seq":        int64(i),
			"from":       msg.From,
			"text":       msg.Text,
			"channel_id": msg.ChannelID,
			"thread_id":  msg.ThreadID,
			"at":         time.Now().UTC().Format(time.RFC3339Nano),
		}
		if _, err := s.msgDB.Put(ctx, doc); err != nil {
			return fmt.Errorf("sessionstore: save messages: %w", err)
		}
	}
	return nil
}

func (s *nellSessionStore) SearchMessages(ctx context.Context, sessionID, query string, limit int) ([]Message, error) {
	// Substring match on recent messages. Vector search via NellDB
	// SearchSimilar requires an embedding model — deferred to future
	// integration with the memory provider's vector store.
	msgs, err := s.RecentMessages(ctx, sessionID, 200)
	if err != nil {
		return nil, err
	}
	ql := strings.ToLower(query)
	var out []Message
	for _, m := range msgs {
		if strings.Contains(strings.ToLower(m.Text), ql) {
			out = append(out, m)
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}
