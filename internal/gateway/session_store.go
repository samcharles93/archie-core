package gateway

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
)

// SessionStore persists gateway session metadata and conversation history.
// Each session tracks one platform, bot, and channel combination.
type SessionStore interface {
	SessionLifecycle
	MessageHistory
}

// SessionLifecycle manages session CRUD and listing.
type SessionLifecycle interface {
	// Save inserts or replaces a session.
	Save(ctx context.Context, s SessionContext) error
	// Get returns a session by ID, or nil when it does not exist.
	Get(ctx context.Context, sessionID string) (*SessionContext, error)
	// GetByChannel returns matching sessions newest first.
	GetByChannel(ctx context.Context, platform, channelID string) ([]SessionContext, error)
	// Delete removes a session. Deleting an absent session is a no-op.
	Delete(ctx context.Context, sessionID string) error
	// Touch marks a session active at the current time.
	Touch(ctx context.Context, sessionID string) error
	// List returns all sessions newest first.
	List(ctx context.Context) ([]SessionContext, error)
}

// MessageHistory manages conversation messages within a session.
type MessageHistory interface {
	// SaveMessage appends one message with a strictly increasing timestamp.
	SaveMessage(ctx context.Context, sessionID string, msg Message) error
	// RecentMessages returns the most recent messages in chronological order.
	RecentMessages(ctx context.Context, sessionID string, n int) ([]Message, error)
	// DeleteRecentMessages removes up to n messages and returns the count.
	DeleteRecentMessages(ctx context.Context, sessionID string, n int) (deleted int, err error)
	// MessageCount returns the number of stored messages in a session.
	MessageCount(ctx context.Context, sessionID string) (int, error)
	// SaveMessages appends messages used to inherit branch history.
	SaveMessages(ctx context.Context, sessionID string, msgs []Message) error
	// ReplaceMessages writes replacements before deleting superseded messages.
	// Records absent from both inputs remain untouched.
	ReplaceMessages(ctx context.Context, sessionID string, msgs []Message, superseded []string) error
	// SearchMessages searches the session's full history.
	SearchMessages(ctx context.Context, sessionID string, q MessageQuery) (MessagePage, error)
	// Close releases the database.
	Close() error
}

// MessageQuery is one page request against a session's message history. Limit
// is clamped to MaxMessagePageSize and Offset is never negative.
type MessageQuery struct {
	Query string
	Limit int
	// Offset is the number of ranked matches to skip.
	Offset int
}

// MessagePage is one page of search results.
type MessagePage struct {
	// Messages are ordered by relevance.
	Messages []Message
	// NextOffset is meaningful only when HasMore is true.
	NextOffset int
	HasMore    bool
	// Truncated is retained in the transport contract and is always false for
	// SQLite because it can count the complete result set.
	Truncated bool
}

// Page-size bounds for MessageQuery.
const (
	DefaultMessagePageSize     = 50
	MaxMessagePageSize         = 500
	MaxMessageSearchQueryBytes = 4096
	MaxMessageSearchQueryTerms = 64
)

// NewSessionStoreMemory returns an in-memory SQLite SessionStore for tests.
func NewSessionStoreMemory() SessionStore {
	store, err := NewSQLiteSessionStoreMemory()
	if err != nil {
		panic(err)
	}
	return store
}

// sessionRecency is the later of a session's creation and activity times.
func sessionRecency(sc SessionContext) time.Time {
	if sc.LastActiveAt.After(sc.CreatedAt) {
		return sc.LastActiveAt
	}
	return sc.CreatedAt
}

// messageIDNamespace scopes deterministic canonical message identifiers.
var messageIDNamespace = uuid.MustParse("6f8d2b1e-9c4a-4f37-8a56-1d0e7b3c9a42")

func newMessageID(sessionID, sourceID string) string {
	if sourceID == "" {
		return uuid.NewString()
	}
	return uuid.NewSHA1(messageIDNamespace, []byte(sessionID+"\x00"+sourceID)).String()
}

func stamp(msg Message) time.Time {
	if msg.At.IsZero() {
		return time.Now().UTC()
	}
	return msg.At.UTC()
}

// CanonicalMessageID returns the stable ID for an upstream message.
func CanonicalMessageID(sessionID, sourceID string) string {
	if sourceID == "" {
		return ""
	}
	return newMessageID(sessionID, sourceID)
}

// PriorReply returns the reply already produced for an upstream message.
func PriorReply(history []Message, sessionID, sourceID, identity string) string {
	id := CanonicalMessageID(sessionID, sourceID)
	if id == "" {
		return ""
	}
	for i, message := range history {
		if message.MessageID != id {
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

func isCompressionSummary(text string) bool {
	return strings.HasPrefix(text, DefaultCompressionConfig().SummaryMarker)
}
