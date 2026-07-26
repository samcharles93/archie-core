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

// SessionStore persists gateway session metadata. Each session tracks
// one (platform, bot, channel) combination for the lifetime of a
// connection.
type SessionStore interface {
	// Save persists a session. If a session with the same ID already
	// exists it is overwritten.
	Save(ctx context.Context, s SessionContext) error

	// Get returns the session with the given ID, or nil when not found.
	Get(ctx context.Context, sessionID string) (*SessionContext, error)

	// GetByChannel returns all sessions for the given platform and
	// channel, newest first. Useful for finding which bot identities
	// are active in a conversation.
	GetByChannel(ctx context.Context, platform, channelID string) ([]SessionContext, error)

	// Delete removes a session by ID. Deleting a non-existent session
	// is a no-op.
	Delete(ctx context.Context, sessionID string) error

	// Touch updates the LastActiveAt timestamp of the session to now.
	// Returns nil when the session does not exist.
	Touch(ctx context.Context, sessionID string) error

	// List returns all sessions, newest first.
	List(ctx context.Context) ([]SessionContext, error)

	// Close shuts down the underlying store.
	Close() error
}

// ── NellDB implementation ──────────────────────────────────────────────────

// nellSessionStore implements SessionStore backed by a NellDB DocDB.
type nellSessionStore struct {
	db     *sdk.DocDB
	store  nl.Store
	nodeID string
	mu     sync.Mutex
}

// NewSessionStore creates a SessionStore backed by an existing NellDB
// store. The store's NodeID is used to stamp records.
func NewSessionStore(st nl.Store, nodeID string) SessionStore {
	return &nellSessionStore{
		db:     sdk.New(st, nodeID, "sessions"),
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
	all, err := s.listAll(ctx)
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
	return s.listAll(ctx)
}

func (s *nellSessionStore) Close() error {
	return s.store.Close()
}

// ── Internal helpers ───────────────────────────────────────────────────────

func (s *nellSessionStore) listAll(ctx context.Context) ([]SessionContext, error) {
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
