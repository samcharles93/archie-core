package gateway

import (
	"context"
	"testing"
)

// A chat turn that lands while a compression is being computed is not part of
// what the caller summarised, and must survive it.
//
// applyCompress reads the history, computes a summary, then replaces. The
// replacement used to mean "delete everything that is not in this list", so a
// message saved in that window was deleted without ever being read, let alone
// summarised. That is unrecoverable loss, and it is outside the guarantee the
// failure-safe write-then-delete ordering provides -- the window is in the
// caller, not the store.
func TestCompressKeepsAMessageThatArrivesMidway(t *testing.T) {
	for name, newStore := range compressBackends() {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			inner := newStore(t)
			store := &midCompressStore{SessionStore: inner}

			r := NewRouter(nil, nil, "telegram")
			r.InitSessions(store)
			r.Identity = "archie"
			sessionID, err := r.sessionTracker.resolve(ctx, "telegram", "archie", "chat-1", "")
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}

			seedForCompress(t, store, sessionID, 40)

			// The inbound turn lands after applyCompress has read the
			// history and before it writes the replacement.
			const lateText = "sent while the summary was being computed"
			store.afterRecent = func() {
				if err := inner.SaveMessage(ctx, sessionID, Message{
					From:     "alice",
					Text:     lateText,
					SourceID: "tg-late",
					At:       at(dur(9000)),
				}); err != nil {
					t.Errorf("late SaveMessage: %v", err)
				}
			}

			if _, err := r.applyCompress(ctx, sessionID, DefaultCompressionConfig()); err != nil {
				t.Fatalf("applyCompress: %v", err)
			}

			after, err := inner.RecentMessages(ctx, sessionID, 1000)
			if err != nil {
				t.Fatalf("RecentMessages: %v", err)
			}
			for _, m := range after {
				if m.Text == lateText {
					return
				}
			}
			t.Fatalf("the message that arrived mid-compress was destroyed; history is %d messages",
				len(after))
		})
	}
}

// midCompressStore fires a callback after RecentMessages returns, which is
// the window inside applyCompress between reading the history and replacing
// it.
type midCompressStore struct {
	SessionStore
	afterRecent func()
}

func (m *midCompressStore) RecentMessages(ctx context.Context, sessionID string, n int) ([]Message, error) {
	out, err := m.SessionStore.RecentMessages(ctx, sessionID, n)
	if m.afterRecent != nil {
		fn := m.afterRecent
		m.afterRecent = nil
		fn()
	}
	return out, err
}
