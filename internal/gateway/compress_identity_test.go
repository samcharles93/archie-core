package gateway

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// compressBackends provides the persistent store used by compression tests.
func compressBackends() map[string]func(t *testing.T) SessionStore {
	return map[string]func(t *testing.T) SessionStore{
		"sqlite": func(t *testing.T) SessionStore {
			s, err := NewSQLiteSessionStoreMemory()
			if err != nil {
				t.Fatalf("NewSQLiteSessionStoreMemory: %v", err)
			}
			t.Cleanup(func() { _ = s.Close() })
			return s
		},
	}
}

// routerOn builds a Router backed by store with an active session, and
// returns the session ID.
func routerOn(t *testing.T, store SessionStore, channelID string) (*Router, string) {
	t.Helper()
	r := NewRouter(nil, nil, "telegram")
	r.InitSessions(store)
	r.Identity = "archie"

	sessionID, err := r.sessionTracker.resolve(context.Background(), "telegram", "archie", channelID, "")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	return r, sessionID
}

// seedForCompress fills a session with enough large messages to trip the
// default threshold, each carrying an upstream SourceID as Telegram does.
func seedForCompress(t *testing.T, store SessionStore, sessionID string, n int) []Message {
	t.Helper()
	// Large enough that n messages exceed 50% of a 128k context window
	// (tokenEstimate is len/4, so n*bodyLen/4 must clear 64k).
	body := make([]byte, 8000)
	for i := range body {
		body[i] = 'x'
	}

	for i := range n {
		m := Message{
			From:     "alice",
			Text:     fmt.Sprintf("m%d %s", i, body),
			SourceID: fmt.Sprintf("tg-%d", i),
			At:       at(dur(i)),
		}
		if err := store.SaveMessage(context.Background(), sessionID, m); err != nil {
			t.Fatalf("SaveMessage %d: %v", i, err)
		}
	}

	stored, err := store.RecentMessages(context.Background(), sessionID, n)
	if err != nil {
		t.Fatalf("RecentMessages: %v", err)
	}
	if len(stored) != n {
		t.Fatalf("seeded %d messages, store has %d", n, len(stored))
	}
	return stored
}

// A message that survives compression is the same message. Rebuilding the
// protected head and tail as fresh records re-mints their canonical
// MessageID, drops the upstream SourceID and resets the timestamp, which
// silently breaks redelivery dedup for the rest of the session's life.
//
// Architecture: messaging-and-work-intake.md lines 88-90 (immutable
// application-generated MessageID) and 112-114 (immutable records).
func TestCompressPreservesRetainedMessageIdentity(t *testing.T) {
	for name, newStore := range compressBackends() {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			store := newStore(t)
			r, sessionID := routerOn(t, store, "chat-1")

			before := seedForCompress(t, store, sessionID, 40)
			byText := make(map[string]Message, len(before))
			for _, m := range before {
				byText[m.Text] = m
			}

			if _, err := r.applyCompress(ctx, sessionID, DefaultCompressionConfig()); err != nil {
				t.Fatalf("applyCompress: %v", err)
			}

			after, err := store.RecentMessages(ctx, sessionID, 1000)
			if err != nil {
				t.Fatalf("RecentMessages: %v", err)
			}

			retained := 0
			for _, m := range after {
				orig, ok := byText[m.Text]
				if !ok {
					continue // the summary, which is legitimately new
				}
				retained++
				if m.MessageID != orig.MessageID {
					t.Errorf("retained %q: MessageID = %q, want the original %q",
						shortText(m.Text), m.MessageID, orig.MessageID)
				}
				if m.SourceID != orig.SourceID {
					t.Errorf("retained %q: SourceID = %q, want the original %q",
						shortText(m.Text), m.SourceID, orig.SourceID)
				}
				if !m.At.Equal(orig.At) {
					t.Errorf("retained %q: At = %s, want the original %s",
						shortText(m.Text), m.At, orig.At)
				}
			}
			if retained == 0 {
				t.Fatal("no original message survived compression: nothing was protected")
			}
		})
	}
}

// The consequence of losing identity: after a compression, a redelivered
// upstream message appends a duplicate instead of being recognised. Telegram
// replays its undelivered queue after a restart, so this is the difference
// between a quiet reboot and a doubled conversation.
func TestCompressKeepsRedeliveryIdempotent(t *testing.T) {
	for name, newStore := range compressBackends() {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			store := newStore(t)
			r, sessionID := routerOn(t, store, "chat-1")

			before := seedForCompress(t, store, sessionID, 40)
			last := before[len(before)-1]

			// Redelivery is a no-op before compression.
			if err := store.SaveMessage(ctx, sessionID, Message{
				From: last.From, Text: last.Text, SourceID: last.SourceID, At: last.At,
			}); err != nil {
				t.Fatalf("redeliver before compress: %v", err)
			}
			preCount, err := store.MessageCount(ctx, sessionID)
			if err != nil {
				t.Fatalf("MessageCount: %v", err)
			}
			if preCount != len(before) {
				t.Fatalf("redelivery before compress appended: count = %d, want %d", preCount, len(before))
			}

			if _, err := r.applyCompress(ctx, sessionID, DefaultCompressionConfig()); err != nil {
				t.Fatalf("applyCompress: %v", err)
			}
			compressed, err := store.MessageCount(ctx, sessionID)
			if err != nil {
				t.Fatalf("MessageCount: %v", err)
			}

			// The same redelivery must still be a no-op afterwards.
			if err := store.SaveMessage(ctx, sessionID, Message{
				From: last.From, Text: last.Text, SourceID: last.SourceID, At: last.At,
			}); err != nil {
				t.Fatalf("redeliver after compress: %v", err)
			}
			got, err := store.MessageCount(ctx, sessionID)
			if err != nil {
				t.Fatalf("MessageCount: %v", err)
			}
			if got != compressed {
				t.Errorf("redelivery after compress appended a duplicate: count = %d, want %d",
					got, compressed)
			}
		})
	}
}

// The summary describes the conversation; it is not something the user said.
// CompressHistory emits it with role "system", and the write-back must not
// let that fall through to the user, or the model replays "[Earlier
// conversation has been summarised...]" as if the human typed it.
func TestCompressSummaryIsNotAttributedToTheUser(t *testing.T) {
	for name, newStore := range compressBackends() {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			store := newStore(t)
			r, sessionID := routerOn(t, store, "chat-1")

			seedForCompress(t, store, sessionID, 40)
			if _, err := r.applyCompress(ctx, sessionID, DefaultCompressionConfig()); err != nil {
				t.Fatalf("applyCompress: %v", err)
			}

			after, err := store.RecentMessages(ctx, sessionID, 1000)
			if err != nil {
				t.Fatalf("RecentMessages: %v", err)
			}

			marker := DefaultCompressionConfig().SummaryMarker
			found := false
			for _, m := range after {
				if !strings.Contains(m.Text, marker) {
					continue
				}
				found = true
				if m.From != r.Identity {
					t.Errorf("summary From = %q, want the bot identity %q: consumers "+
						"reconstruct the role by comparing From against the identity, so "+
						"any other value replays the summary as a user turn", m.From, r.Identity)
				}
			}
			if !found {
				t.Fatal("no summary message found after compression")
			}
		})
	}
}

func shortText(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}
