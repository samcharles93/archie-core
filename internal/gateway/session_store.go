package gateway

import (
	"encoding/binary"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/samcharles93/archie-core/internal/domain/messaging"
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

// messageIDNamespaceV2 isolates the injective encoding used for inputs that
// contain the legacy separator. Ordinary platform/session IDs keep their
// existing canonical IDs, so an upgrade cannot duplicate redelivered messages.
var messageIDNamespaceV2 = uuid.MustParse("e6ac7868-cf49-4e65-a5a1-a1549fd12f67")

func newMessageID(sessionID, sourceID string) string {
	if sourceID == "" {
		return uuid.NewString()
	}
	current, _ := canonicalMessageIDs(sessionID, sourceID)
	return current
}

func canonicalMessageIDs(sessionID, sourceID string) (current, legacy string) {
	legacy = uuid.NewSHA1(messageIDNamespace, []byte(sessionID+"\x00"+sourceID)).String()
	if strings.ContainsRune(sessionID, '\x00') || strings.ContainsRune(sourceID, '\x00') {
		return uuid.NewSHA1(messageIDNamespaceV2, encodeCanonicalPair(sessionID, sourceID)).String(), legacy
	}
	return legacy, ""
}

func encodeCanonicalPair(first, second string) []byte {
	encoded := make([]byte, 8+len(first)+len(second))
	binary.BigEndian.PutUint64(encoded, uint64(len(first)))
	copy(encoded[8:], first)
	copy(encoded[8+len(first):], second)
	return encoded
}

func stamp(msg messaging.Message) time.Time {
	now := time.Now().UTC()
	if msg.At.IsZero() || msg.At.After(now) {
		// At orders the canonical conversation; it is not an authoritative
		// copy of a platform clock. Persisting a future platform timestamp
		// would force every later message beyond it to preserve monotonicity.
		return now
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
// The assistant check reads the record's Role, which its producer set when
// the message was written, so this needs no bot identity.
func PriorReply(history []messaging.Message, sessionID, sourceID string) string {
	if sourceID == "" {
		return ""
	}
	id, legacyID := canonicalMessageIDs(sessionID, sourceID)
	for i, message := range history {
		if string(message.ID) != id && string(message.ID) != legacyID {
			continue
		}
		if i+1 < len(history) && history[i+1].Role == messaging.RoleAssistant &&
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
