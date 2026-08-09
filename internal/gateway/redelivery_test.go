package gateway

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPriorReplyRecognisesLegacyCanonicalMessageID(t *testing.T) {
	t.Parallel()

	const (
		sessionID = "a\x00b"
		sourceID  = "source"
		identity  = "archie"
	)
	legacyID := uuid.NewSHA1(messageIDNamespace, []byte(sessionID+"\x00"+sourceID)).String()
	history := []Message{
		{MessageID: legacyID, SourceID: sourceID, From: "alice", Text: "question"},
		{From: identity, Text: "answer"},
	}
	if got := PriorReply(history, sessionID, sourceID, identity); got != "answer" {
		t.Fatalf("PriorReply = %q, want legacy reply", got)
	}
}

// Making the store idempotent only stopped the duplicate *record*. The chat
// path saved the inbound message, ignored the fact that nothing changed, and
// went on to call the model again -- a second billed turn, any tool side
// effects run twice, and a second assistant reply appended (the reply carries
// no SourceID, so it always gets a fresh identity and always appends).
//
// PriorReply is the check that closes that: if this upstream message has
// already been answered, the answer is already in the history.
func TestPriorReply(t *testing.T) {
	const (
		sessionID = "chat-1"
		identity  = "archie"
	)

	// history is built from (sourceID, from, text) triples; an empty
	// sourceID means a locally generated message, as replies are.
	type turn struct {
		sourceID string
		from     string
		text     string
	}

	tests := []struct {
		name     string
		history  []turn
		sourceID string
		want     string
	}{
		{
			name: "an answered message returns its reply",
			history: []turn{
				{"tg-1", "alice", "ping"},
				{"", identity, "pong"},
			},
			sourceID: "tg-1",
			want:     "pong",
		},
		{
			name: "the reply for the right message is chosen",
			history: []turn{
				{"tg-1", "alice", "first"},
				{"", identity, "first reply"},
				{"tg-2", "alice", "second"},
				{"", identity, "second reply"},
			},
			sourceID: "tg-2",
			want:     "second reply",
		},
		{
			name: "an unanswered message is handled normally",
			history: []turn{
				{"tg-1", "alice", "ping"},
			},
			sourceID: "tg-1",
			want:     "",
		},
		{
			// The turn died before replying. Re-running is correct here:
			// the user is owed an answer.
			name: "a message followed only by another user turn is unanswered",
			history: []turn{
				{"tg-1", "alice", "ping"},
				{"tg-2", "alice", "still there?"},
			},
			sourceID: "tg-1",
			want:     "",
		},
		{
			name: "an unseen message is handled normally",
			history: []turn{
				{"tg-1", "alice", "ping"},
				{"", identity, "pong"},
			},
			sourceID: "tg-99",
			want:     "",
		},
		{
			// Without an upstream ID there is nothing to match on, so the
			// message cannot be recognised as a redelivery.
			name: "no source id means no dedup",
			history: []turn{
				{"tg-1", "alice", "ping"},
				{"", identity, "pong"},
			},
			sourceID: "",
			want:     "",
		},
		{
			name:     "empty history",
			history:  nil,
			sourceID: "tg-1",
			want:     "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			history := make([]Message, 0, len(tc.history))
			for i, h := range tc.history {
				history = append(history, Message{
					MessageID: newMessageIDFor(sessionID, h.sourceID, i),
					SourceID:  h.sourceID,
					From:      h.from,
					Text:      h.text,
					At:        time.Now().Add(time.Duration(i) * time.Second),
				})
			}

			got := PriorReply(history, sessionID, tc.sourceID, identity)
			if got != tc.want {
				t.Errorf("PriorReply = %q, want %q", got, tc.want)
			}
		})
	}
}

// newMessageIDFor mirrors what the store assigns: a derived ID for messages
// carrying an upstream reference, a unique one otherwise.
func newMessageIDFor(sessionID, sourceID string, i int) string {
	if sourceID == "" {
		return "local-" + string(rune('a'+i))
	}
	return CanonicalMessageID(sessionID, sourceID)
}

// The canonical ID must be the same one the store derives, or the lookup
// silently never matches and the dedup quietly stops working.
func TestCanonicalMessageIDMatchesTheStore(t *testing.T) {
	const sessionID = "chat-1"
	got := CanonicalMessageID(sessionID, "tg-7")
	want := newMessageID(sessionID, "tg-7")
	if got != want {
		t.Fatalf("CanonicalMessageID = %q, want the store's %q", got, want)
	}
	if CanonicalMessageID(sessionID, "") != "" {
		t.Error("CanonicalMessageID with no source id must be empty, not a random UUID")
	}
}
