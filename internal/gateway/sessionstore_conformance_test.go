package gateway

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// base is a fixed instant so message timestamps are deterministic; tests
// derive every other instant from it rather than calling time.Now.
var base = time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)

func at(offset time.Duration) time.Time { return base.Add(offset) }

// dur spaces the nth message one second apart from the first.
func dur(n int) time.Duration { return time.Duration(n) * time.Second }

func msg(text string, offset time.Duration) Message {
	return Message{From: "user", Text: text, At: at(offset)}
}

func texts(msgs []Message) []string {
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, m.Text)
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// runSessionStoreSuite runs every SessionStore behavioural contract against
// a store implementation. newStore must return a fresh, empty store.
// supersededIDs returns the canonical IDs of a session's whole history, for
// tests that mean "replace everything". Production callers name only what
// they actually read; a test that wants a clean sweep has to say so.
func supersededIDs(ctx context.Context, t *testing.T, s SessionStore, sessionID string) []string {
	t.Helper()
	count, err := s.MessageCount(ctx, sessionID)
	if err != nil {
		t.Fatalf("MessageCount: %v", err)
	}
	msgs, err := s.RecentMessages(ctx, sessionID, count)
	if err != nil {
		t.Fatalf("RecentMessages: %v", err)
	}
	ids := make([]string, 0, len(msgs))
	for _, m := range msgs {
		ids = append(ids, m.MessageID)
	}
	return ids
}

func runSessionStoreSuite(t *testing.T, newStore func(t *testing.T) SessionStore) {
	t.Helper()

	t.Run("SessionCRUD", func(t *testing.T) { testSessionCRUD(t, newStore) })
	t.Run("NewestFirstOrdering", func(t *testing.T) { testNewestFirstOrdering(t, newStore) })
	t.Run("SaveMessagesAppends", func(t *testing.T) { testSaveMessagesAppends(t, newStore) })
	t.Run("DeleteThenSaveDoesNotReuseKeys", func(t *testing.T) { testDeleteThenSaveDoesNotReuseKeys(t, newStore) })
	t.Run("MessagesOrderByTimestamp", func(t *testing.T) { testMessagesOrderByTimestamp(t, newStore) })
	t.Run("ReplyCannotPrecedeItsPrompt", func(t *testing.T) { testReplyCannotPrecedeItsPrompt(t, newStore) })
	t.Run("UpstreamRedeliveryKeepsOriginalTime", func(t *testing.T) { testUpstreamRedeliveryKeepsOriginalTime(t, newStore) })
	t.Run("SaveMessageStampsMissingTimestamp", func(t *testing.T) { testSaveMessageStampsMissingTimestamp(t, newStore) })
	t.Run("CanonicalMessageIDIsStable", func(t *testing.T) { testCanonicalMessageIDIsStable(t, newStore) })
	t.Run("SourceIDRoundTrips", func(t *testing.T) { testSourceIDRoundTrips(t, newStore) })
	t.Run("SourceIDIsNotSearchable", func(t *testing.T) { testSourceIDIsNotSearchable(t, newStore) })
	t.Run("MessageDeduplication", func(t *testing.T) { testMessageDeduplication(t, newStore) })
	t.Run("ReplaceMessagesIsFailureSafe", func(t *testing.T) { testReplaceMessagesIsFailureSafe(t, newStore) })
	t.Run("ReplaceMessagesBeyondScanPageLimit", func(t *testing.T) { testReplaceMessagesBeyondScanPageLimit(t, newStore) })
	t.Run("ReplaceMessagesKeepsSurvivors", func(t *testing.T) { testReplaceMessagesKeepsSurvivors(t, newStore) })
	t.Run("SessionsAreIsolated", func(t *testing.T) { testSessionsAreIsolated(t, newStore) })
	t.Run("HistoryBeyondScanPageLimit", func(t *testing.T) { testHistoryBeyondScanPageLimit(t, newStore) })
	t.Run("DeleteRecentMessagesReturnsCountDeleted", func(t *testing.T) { testDeleteRecentMessagesReturnsCountDeleted(t, newStore) })
	t.Run("SearchMessagesPaging", func(t *testing.T) { testSearchMessagesPaging(t, newStore) })
	t.Run("SearchMessagesBeyondOldCeiling", func(t *testing.T) { testSearchMessagesBeyondOldCeiling(t, newStore) })
	t.Run("SearchMatchesTextNotMetadata", func(t *testing.T) { testSearchMatchesTextNotMetadata(t, newStore) })
	t.Run("SearchEmptyQueryReturnsEmptyPage", func(t *testing.T) { testSearchEmptyQueryReturnsEmptyPage(t, newStore) })
}

// testSessionCRUD covers Save/Get/GetByChannel/Delete/Touch/List, a missing
// Get returning (nil, nil), a missing Delete being a no-op, and Delete also
// removing that session's messages.
func testSessionCRUD(t *testing.T, newStore func(t *testing.T) SessionStore) {
	ctx := context.Background()

	t.Run("save and get round-trips fields", func(t *testing.T) {
		s := newStore(t)
		t.Cleanup(func() { _ = s.Close() })

		sc := SessionContext{
			SessionID:    "sess-1",
			Source:       SessionSource{Platform: "telegram", BotUser: "archie", ChannelID: "chat-42"},
			CreatedAt:    at(0),
			LastActiveAt: at(0),
		}
		if err := s.Save(ctx, sc); err != nil {
			t.Fatalf("Save: %v", err)
		}
		got, err := s.Get(ctx, "sess-1")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got == nil {
			t.Fatal("expected session, got nil")
		}
		if got.SessionID != sc.SessionID || got.Source != sc.Source {
			t.Errorf("got %+v, want %+v", got, sc)
		}
	})

	t.Run("save overwrites an existing session", func(t *testing.T) {
		s := newStore(t)
		t.Cleanup(func() { _ = s.Close() })

		sc1 := SessionContext{
			SessionID: "sess-2",
			Source:    SessionSource{Platform: "discord", BotUser: "archie", ChannelID: "general"},
			CreatedAt: at(0), LastActiveAt: at(0),
		}
		sc2 := SessionContext{
			SessionID: "sess-2",
			Source:    SessionSource{Platform: "web", BotUser: "winter", ChannelID: "ui-1"},
			CreatedAt: at(0), LastActiveAt: at(0),
		}
		if err := s.Save(ctx, sc1); err != nil {
			t.Fatalf("Save: %v", err)
		}
		if err := s.Save(ctx, sc2); err != nil {
			t.Fatalf("Save overwrite: %v", err)
		}
		got, err := s.Get(ctx, "sess-2")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.Source != sc2.Source {
			t.Errorf("got %+v after overwrite, want %+v", got.Source, sc2.Source)
		}
	})

	t.Run("get of a missing session returns nil, nil", func(t *testing.T) {
		s := newStore(t)
		t.Cleanup(func() { _ = s.Close() })

		got, err := s.Get(ctx, "nonexistent")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got != nil {
			t.Errorf("got %+v, want nil", got)
		}
	})

	t.Run("get by channel filters by platform and channel, newest first", func(t *testing.T) {
		s := newStore(t)
		t.Cleanup(func() { _ = s.Close() })

		sessions := []SessionContext{
			{SessionID: "sess-a", Source: SessionSource{Platform: "telegram", BotUser: "archie", ChannelID: "chat-1"}, CreatedAt: at(0), LastActiveAt: at(0)},
			{SessionID: "sess-b", Source: SessionSource{Platform: "telegram", BotUser: "winter", ChannelID: "chat-1"}, CreatedAt: at(time.Minute), LastActiveAt: at(time.Minute)},
			{SessionID: "sess-c", Source: SessionSource{Platform: "telegram", BotUser: "archie", ChannelID: "chat-2"}, CreatedAt: at(2 * time.Minute), LastActiveAt: at(2 * time.Minute)},
		}
		for _, sc := range sessions {
			if err := s.Save(ctx, sc); err != nil {
				t.Fatalf("Save %s: %v", sc.SessionID, err)
			}
		}

		results, err := s.GetByChannel(ctx, "telegram", "chat-1")
		if err != nil {
			t.Fatalf("GetByChannel: %v", err)
		}
		if len(results) != 2 {
			t.Fatalf("got %d sessions for chat-1, want 2", len(results))
		}
		if results[0].SessionID != "sess-b" || results[1].SessionID != "sess-a" {
			t.Errorf("got order %s,%s, want sess-b,sess-a (newest first)", results[0].SessionID, results[1].SessionID)
		}

		results, err = s.GetByChannel(ctx, "telegram", "chat-2")
		if err != nil {
			t.Fatalf("GetByChannel chat-2: %v", err)
		}
		if len(results) != 1 {
			t.Fatalf("got %d sessions for chat-2, want 1", len(results))
		}

		results, err = s.GetByChannel(ctx, "discord", "chat-1")
		if err != nil {
			t.Fatalf("GetByChannel discord: %v", err)
		}
		if len(results) != 0 {
			t.Fatalf("got %d sessions for discord/chat-1, want 0", len(results))
		}
	})

	t.Run("delete removes the session, is a no-op when missing, and drops its messages", func(t *testing.T) {
		s := newStore(t)
		t.Cleanup(func() { _ = s.Close() })

		sc := SessionContext{
			SessionID: "sess-del",
			Source:    SessionSource{Platform: "web", BotUser: "archie", ChannelID: "ui-1"},
			CreatedAt: at(0), LastActiveAt: at(0),
		}
		if err := s.Save(ctx, sc); err != nil {
			t.Fatalf("Save: %v", err)
		}
		if err := s.SaveMessage(ctx, "sess-del", msg("hello", 0)); err != nil {
			t.Fatalf("SaveMessage: %v", err)
		}

		if err := s.Delete(ctx, "sess-del"); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		got, err := s.Get(ctx, "sess-del")
		if err != nil {
			t.Fatalf("Get after delete: %v", err)
		}
		if got != nil {
			t.Errorf("got %+v after delete, want nil", got)
		}
		count, err := s.MessageCount(ctx, "sess-del")
		if err != nil {
			t.Fatalf("MessageCount after delete: %v", err)
		}
		if count != 0 {
			t.Errorf("MessageCount after delete = %d, want 0", count)
		}

		// Delete of a missing session is a no-op.
		if err := s.Delete(ctx, "nonexistent"); err != nil {
			t.Errorf("Delete nonexistent: %v", err)
		}
	})

	t.Run("touch updates LastActiveAt, preserves CreatedAt, and is a no-op when missing", func(t *testing.T) {
		s := newStore(t)
		t.Cleanup(func() { _ = s.Close() })

		// Touch stamps LastActiveAt from the wall clock, not a synthetic
		// instant, so oldTime must be relative to time.Now rather than the
		// fixed base used elsewhere in this suite.
		oldTime := time.Now().UTC().Add(-time.Hour)
		sc := SessionContext{
			SessionID: "sess-touch",
			Source:    SessionSource{Platform: "discord", BotUser: "archie", ChannelID: "dev"},
			CreatedAt: oldTime, LastActiveAt: oldTime,
		}
		if err := s.Save(ctx, sc); err != nil {
			t.Fatalf("Save: %v", err)
		}
		if err := s.Touch(ctx, "sess-touch"); err != nil {
			t.Fatalf("Touch: %v", err)
		}
		got, err := s.Get(ctx, "sess-touch")
		if err != nil {
			t.Fatalf("Get after touch: %v", err)
		}
		if got == nil {
			t.Fatal("expected session after touch")
		}
		if !got.LastActiveAt.After(oldTime) {
			t.Errorf("LastActiveAt = %s, want after %s", got.LastActiveAt, oldTime)
		}
		if got.CreatedAt.Sub(oldTime).Abs() > time.Second {
			t.Errorf("CreatedAt = %s, want preserved near %s", got.CreatedAt, oldTime)
		}

		if err := s.Touch(ctx, "nonexistent"); err != nil {
			t.Errorf("Touch nonexistent: %v", err)
		}
	})

	t.Run("list returns all sessions, newest first", func(t *testing.T) {
		s := newStore(t)
		t.Cleanup(func() { _ = s.Close() })

		all, err := s.List(ctx)
		if err != nil {
			t.Fatalf("List empty: %v", err)
		}
		if len(all) != 0 {
			t.Fatalf("got %d sessions, want 0", len(all))
		}

		ids := []string{"s1", "s2", "s3"}
		for i, id := range ids {
			sc := SessionContext{
				SessionID: id,
				Source:    SessionSource{Platform: "web", BotUser: "archie", ChannelID: "ch-" + id},
				CreatedAt: at(time.Duration(i) * time.Minute), LastActiveAt: at(time.Duration(i) * time.Minute),
			}
			if err := s.Save(ctx, sc); err != nil {
				t.Fatalf("Save %s: %v", id, err)
			}
		}

		all, err = s.List(ctx)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(all) != 3 {
			t.Fatalf("got %d sessions, want 3", len(all))
		}
		want := []string{"s3", "s2", "s1"}
		for i, sc := range all {
			if sc.SessionID != want[i] {
				t.Errorf("List()[%d] = %s, want %s (newest first)", i, sc.SessionID, want[i])
			}
		}
	})
}

// testSaveMessagesAppends pins the data-loss defect: a bulk save into a
// session that already holds messages must append, never overwrite.
func testSaveMessagesAppends(t *testing.T, newStore func(t *testing.T) SessionStore) {
	tests := []struct {
		name     string
		existing []string
		bulk     []string
		want     []string
	}{
		{"bulk into empty", nil, []string{"a", "b"}, []string{"a", "b"}},
		{"bulk into non-empty", []string{"m0", "m1"}, []string{"a", "b"}, []string{"m0", "m1", "a", "b"}},
		{"single bulk into non-empty", []string{"m0"}, []string{"a"}, []string{"m0", "a"}},
		{"empty bulk is a no-op", []string{"m0", "m1"}, nil, []string{"m0", "m1"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			s := newStore(t)
			t.Cleanup(func() { _ = s.Close() })

			var offset time.Duration
			for _, text := range tc.existing {
				if err := s.SaveMessage(ctx, "sess", msg(text, offset)); err != nil {
					t.Fatalf("SaveMessage(%q): %v", text, err)
				}
				offset += time.Second
			}

			bulk := make([]Message, 0, len(tc.bulk))
			for _, text := range tc.bulk {
				bulk = append(bulk, msg(text, offset))
				offset += time.Second
			}
			if err := s.SaveMessages(ctx, "sess", bulk); err != nil {
				t.Fatalf("SaveMessages: %v", err)
			}

			got, err := s.RecentMessages(ctx, "sess", 100)
			if err != nil {
				t.Fatalf("RecentMessages: %v", err)
			}
			if !equal(texts(got), tc.want) {
				t.Errorf("got %v, want %v", texts(got), tc.want)
			}
		})
	}
}

// testDeleteThenSaveDoesNotReuseKeys pins the seq-reuse defect: deleting the
// newest messages then saving must not collide with a live key.
func testDeleteThenSaveDoesNotReuseKeys(t *testing.T, newStore func(t *testing.T) SessionStore) {
	tests := []struct {
		name        string
		initial     int
		deleteCount int
		thenSave    []string
		want        []string
	}{
		{"delete one, save one", 3, 1, []string{"new"}, []string{"m0", "m1", "new"}},
		{"delete two, save two", 4, 2, []string{"x", "y"}, []string{"m0", "m1", "x", "y"}},
		{"delete all, save one", 2, 2, []string{"only"}, []string{"only"}},
		{"delete more than exist", 2, 5, []string{"only"}, []string{"only"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			s := newStore(t)
			t.Cleanup(func() { _ = s.Close() })

			var offset time.Duration
			for i := range tc.initial {
				if err := s.SaveMessage(ctx, "sess", msg(fmt.Sprintf("m%d", i), offset)); err != nil {
					t.Fatalf("SaveMessage: %v", err)
				}
				offset += time.Second
			}
			if _, err := s.DeleteRecentMessages(ctx, "sess", tc.deleteCount); err != nil {
				t.Fatalf("DeleteRecentMessages: %v", err)
			}
			for _, text := range tc.thenSave {
				if err := s.SaveMessage(ctx, "sess", msg(text, offset)); err != nil {
					t.Fatalf("SaveMessage(%q): %v", text, err)
				}
				offset += time.Second
			}

			got, err := s.RecentMessages(ctx, "sess", 100)
			if err != nil {
				t.Fatalf("RecentMessages: %v", err)
			}
			if !equal(texts(got), tc.want) {
				t.Errorf("got %v, want %v", texts(got), tc.want)
			}
		})
	}
}

// testMessagesOrderByTimestamp pins the ordering contract: a session's
// history reads back in the order it was written, and a message whose
// timestamp is not ahead of the session's newest is clamped forward rather
// than allowed to sort into the past. Timestamps must be strictly
// increasing within a session.
func testMessagesOrderByTimestamp(t *testing.T, newStore func(t *testing.T) SessionStore) {
	tests := []struct {
		name       string
		writeOrder []time.Duration
	}{
		{"already chronological", []time.Duration{0, time.Second, 2 * time.Second}},
		{"written out of order", []time.Duration{2 * time.Second, 0, time.Second}},
		{"reverse written", []time.Duration{2 * time.Second, time.Second, 0}},
		{"all identical timestamps", []time.Duration{0, 0, 0}},
		{"coarse sender clock", []time.Duration{time.Second, time.Second, time.Second}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			s := newStore(t)
			t.Cleanup(func() { _ = s.Close() })

			want := make([]string, 0, len(tc.writeOrder))
			for i, offset := range tc.writeOrder {
				text := fmt.Sprintf("t%d", i)
				if err := s.SaveMessage(ctx, "sess", msg(text, offset)); err != nil {
					t.Fatalf("SaveMessage: %v", err)
				}
				want = append(want, text)
			}

			got, err := s.RecentMessages(ctx, "sess", 100)
			if err != nil {
				t.Fatalf("RecentMessages: %v", err)
			}
			if !equal(texts(got), want) {
				t.Errorf("history = %v, want write order %v", texts(got), want)
			}
			for i := 1; i < len(got); i++ {
				if !got[i].At.After(got[i-1].At) {
					t.Errorf("message %d at %s does not follow message %d at %s",
						i, got[i].At, i-1, got[i-1].At)
				}
			}
		})
	}
}

// testReplyCannotPrecedeItsPrompt pins the concrete inversion the clamp
// exists to prevent: a coarse-resolution inbound message arriving after a
// fine-resolution reply must still sort after it.
func testReplyCannotPrecedeItsPrompt(t *testing.T, newStore func(t *testing.T) SessionStore) {
	ctx := context.Background()
	s := newStore(t)
	t.Cleanup(func() { _ = s.Close() })

	reply := Message{From: "bot", Text: "reply", At: base.Add(10*time.Second + 400*time.Millisecond)}
	if err := s.SaveMessage(ctx, "sess", reply); err != nil {
		t.Fatalf("SaveMessage reply: %v", err)
	}
	next := Message{SourceID: "tg-1", From: "user", Text: "next", At: base.Add(10 * time.Second)}
	if err := s.SaveMessage(ctx, "sess", next); err != nil {
		t.Fatalf("SaveMessage next: %v", err)
	}

	got, err := s.RecentMessages(ctx, "sess", 10)
	if err != nil {
		t.Fatalf("RecentMessages: %v", err)
	}
	if !equal(texts(got), []string{"reply", "next"}) {
		t.Errorf("history = %v, want [reply next]", texts(got))
	}
}

// testUpstreamRedeliveryKeepsOriginalTime pins that replaying an update does
// not shuffle it to the end of the conversation.
func testUpstreamRedeliveryKeepsOriginalTime(t *testing.T, newStore func(t *testing.T) SessionStore) {
	ctx := context.Background()
	s := newStore(t)
	t.Cleanup(func() { _ = s.Close() })

	first := Message{SourceID: "tg-1", From: "u", Text: "first", At: base}
	if err := s.SaveMessage(ctx, "sess", first); err != nil {
		t.Fatalf("SaveMessage: %v", err)
	}
	stored, err := s.RecentMessages(ctx, "sess", 1)
	if err != nil {
		t.Fatalf("RecentMessages: %v", err)
	}
	original := stored[0].At

	if err := s.SaveMessage(ctx, "sess", msg("second", time.Second)); err != nil {
		t.Fatalf("SaveMessage: %v", err)
	}
	if err := s.SaveMessage(ctx, "sess", first); err != nil {
		t.Fatalf("redeliver: %v", err)
	}

	got, err := s.RecentMessages(ctx, "sess", 10)
	if err != nil {
		t.Fatalf("RecentMessages: %v", err)
	}
	if !equal(texts(got), []string{"first", "second"}) {
		t.Errorf("history = %v, want [first second]", texts(got))
	}
	if !got[0].At.Equal(original) {
		t.Errorf("redelivery moved the message from %s to %s", original, got[0].At)
	}
}

// testSaveMessageStampsMissingTimestamp pins that a caller omitting At still
// gets a usable ordering key rather than the zero time. This is the one
// legitimate use of time.Now bounds: the store itself stamps "now" when At
// is zero, so there is no fixed instant to assert against.
func testSaveMessageStampsMissingTimestamp(t *testing.T, newStore func(t *testing.T) SessionStore) {
	ctx := context.Background()
	s := newStore(t)
	t.Cleanup(func() { _ = s.Close() })

	before := time.Now().UTC().Add(-time.Second)
	if err := s.SaveMessage(ctx, "sess", Message{From: "u", Text: "no timestamp"}); err != nil {
		t.Fatalf("SaveMessage: %v", err)
	}
	after := time.Now().UTC().Add(time.Second)

	got, err := s.RecentMessages(ctx, "sess", 1)
	if err != nil {
		t.Fatalf("RecentMessages: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d messages, want 1", len(got))
	}
	if got[0].At.Before(before) || got[0].At.After(after) {
		t.Errorf("At = %s, want within [%s, %s]", got[0].At, before, after)
	}
}

// testCanonicalMessageIDIsStable pins the identity contract: reads carry a
// non-empty, application-generated MessageID; it is not equal to SourceID;
// and it is stable across redelivery of the same SourceID.
func testCanonicalMessageIDIsStable(t *testing.T, newStore func(t *testing.T) SessionStore) {
	ctx := context.Background()
	s := newStore(t)
	t.Cleanup(func() { _ = s.Close() })

	const sourceID = "tg-42"
	if err := s.SaveMessage(ctx, "sess", Message{SourceID: sourceID, From: "u", Text: "hi", At: base}); err != nil {
		t.Fatalf("SaveMessage: %v", err)
	}
	first, err := s.RecentMessages(ctx, "sess", 1)
	if err != nil {
		t.Fatalf("RecentMessages: %v", err)
	}
	if len(first) != 1 {
		t.Fatalf("got %d messages, want 1", len(first))
	}
	if first[0].MessageID == "" {
		t.Fatal("MessageID is empty; reads must carry a canonical ID")
	}
	if first[0].MessageID == sourceID {
		t.Errorf("MessageID = %q, which is the platform identifier; it must be application-generated", sourceID)
	}

	if err := s.SaveMessage(ctx, "sess", Message{SourceID: sourceID, From: "u", Text: "hi", At: base}); err != nil {
		t.Fatalf("redeliver: %v", err)
	}
	again, err := s.RecentMessages(ctx, "sess", 10)
	if err != nil {
		t.Fatalf("RecentMessages: %v", err)
	}
	if len(again) != 1 {
		t.Fatalf("redelivery produced %d messages, want 1", len(again))
	}
	if again[0].MessageID != first[0].MessageID {
		t.Errorf("MessageID changed from %q to %q across redelivery", first[0].MessageID, again[0].MessageID)
	}
}

// testSourceIDRoundTrips pins the SourceID contract: the channel-native
// identifier must round-trip verbatim through persistence, stay distinct
// from the canonical application-generated MessageID, survive redelivery
// (where the canonical ID is derived from it), and read back empty for a
// message written without one -- the shape of documents persisted before
// source_id was a stored field.
func testSourceIDRoundTrips(t *testing.T, newStore func(t *testing.T) SessionStore) {
	ctx := context.Background()
	s := newStore(t)
	t.Cleanup(func() { _ = s.Close() })

	const sourceID = "tg-777"
	if err := s.SaveMessage(ctx, "sess", Message{SourceID: sourceID, From: "u", Text: "hi", At: base}); err != nil {
		t.Fatalf("SaveMessage: %v", err)
	}

	first, err := s.RecentMessages(ctx, "sess", 10)
	if err != nil {
		t.Fatalf("RecentMessages: %v", err)
	}
	if len(first) != 1 {
		t.Fatalf("got %d messages, want 1", len(first))
	}
	if first[0].SourceID != sourceID {
		t.Errorf("SourceID = %q after save, want %q", first[0].SourceID, sourceID)
	}
	if first[0].MessageID == "" {
		t.Fatal("MessageID is empty; reads must carry a canonical ID")
	}
	if first[0].MessageID == sourceID {
		t.Errorf("MessageID = %q equals SourceID; the canonical ID must be application-generated", first[0].MessageID)
	}

	// Redelivery of the same upstream message must keep its SourceID and
	// canonical identity and must not append a second turn.
	if err := s.SaveMessage(ctx, "sess", Message{SourceID: sourceID, From: "u", Text: "hi", At: base}); err != nil {
		t.Fatalf("redeliver: %v", err)
	}
	again, err := s.RecentMessages(ctx, "sess", 10)
	if err != nil {
		t.Fatalf("RecentMessages after redelivery: %v", err)
	}
	if len(again) != 1 {
		t.Fatalf("redelivery produced %d messages, want 1", len(again))
	}
	if again[0].SourceID != sourceID {
		t.Errorf("SourceID = %q after redelivery, want %q", again[0].SourceID, sourceID)
	}
	if again[0].MessageID != first[0].MessageID {
		t.Errorf("MessageID changed from %q to %q across redelivery", first[0].MessageID, again[0].MessageID)
	}

	// A message with no upstream identity -- the shape of a document written
	// before source_id was persisted -- must read back with an empty
	// SourceID, not a zero-value artifact or an error.
	if err := s.SaveMessage(ctx, "sess", Message{From: "u", Text: "local", At: at(time.Second)}); err != nil {
		t.Fatalf("SaveMessage without SourceID: %v", err)
	}
	got, err := s.RecentMessages(ctx, "sess", 10)
	if err != nil {
		t.Fatalf("RecentMessages: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d messages, want 2", len(got))
	}
	if got[1].SourceID != "" {
		t.Errorf("SourceID = %q for a message saved without one, want empty", got[1].SourceID)
	}
}

// testSourceIDIsNotSearchable pins that the channel-native SourceID is
// persistence metadata, not conversation content: a query for it -- ASCII
// or non-ASCII -- must match nothing. SQLite's FTS index covers sender and
// text only.
func testSourceIDIsNotSearchable(t *testing.T, newStore func(t *testing.T) SessionStore) {
	tests := []struct {
		name     string
		sourceID string
	}{
		{"ascii", "tg-900"},
		{"utf8", "идентификатор-42"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			s := newStore(t)
			t.Cleanup(func() { _ = s.Close() })

			m := Message{SourceID: tc.sourceID, From: "u", Text: "hello world", At: base}
			if err := s.SaveMessage(ctx, "sess", m); err != nil {
				t.Fatalf("SaveMessage: %v", err)
			}

			// The ID must actually have been persisted -- the
			// non-searchability assertion is vacuous against a store that
			// drops it on write -- so pin the read-back first.
			got, err := s.RecentMessages(ctx, "sess", 10)
			if err != nil {
				t.Fatalf("RecentMessages: %v", err)
			}
			if len(got) != 1 {
				t.Fatalf("got %d messages, want 1", len(got))
			}
			if got[0].SourceID != tc.sourceID {
				t.Fatalf("SourceID = %q after save, want %q", got[0].SourceID, tc.sourceID)
			}

			page, err := s.SearchMessages(ctx, "sess", MessageQuery{Query: tc.sourceID, Limit: 10})
			if err != nil {
				t.Fatalf("SearchMessages: %v", err)
			}
			if len(page.Messages) != 0 {
				t.Errorf("search for SourceID %q matched %v, want no matches", tc.sourceID, texts(page.Messages))
			}
		})
	}
}

// testMessageDeduplication pins that redelivering the same upstream message
// updates in place instead of appending a duplicate turn, that stored
// messages are immutable, and that messages without a SourceID are never
// deduplicated.
func testMessageDeduplication(t *testing.T, newStore func(t *testing.T) SessionStore) {
	tests := []struct {
		name  string
		saves []Message
		want  []string
	}{
		{
			name: "same upstream ID saved twice",
			saves: []Message{
				{SourceID: "tg-100", From: "u", Text: "hello", At: at(0)},
				{SourceID: "tg-100", From: "u", Text: "hello", At: at(0)},
			},
			want: []string{"hello"},
		},
		{
			name: "distinct upstream IDs both persist",
			saves: []Message{
				{SourceID: "tg-100", From: "u", Text: "first", At: at(0)},
				{SourceID: "tg-101", From: "u", Text: "second", At: at(dur(1))},
			},
			want: []string{"first", "second"},
		},
		{
			name: "redelivery with edited text leaves the record intact",
			saves: []Message{
				{SourceID: "tg-100", From: "u", Text: "original", At: at(0)},
				{SourceID: "tg-100", From: "u", Text: "edited", At: at(0)},
			},
			want: []string{"original"},
		},
		{
			name: "messages without an upstream ID are never deduplicated",
			saves: []Message{
				{From: "u", Text: "same", At: at(0)},
				{From: "u", Text: "same", At: at(0)},
			},
			want: []string{"same", "same"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			s := newStore(t)
			t.Cleanup(func() { _ = s.Close() })

			for _, m := range tc.saves {
				if err := s.SaveMessage(ctx, "sess", m); err != nil {
					t.Fatalf("SaveMessage: %v", err)
				}
			}
			got, err := s.RecentMessages(ctx, "sess", 100)
			if err != nil {
				t.Fatalf("RecentMessages: %v", err)
			}
			if !equal(texts(got), tc.want) {
				t.Errorf("history = %v, want %v", texts(got), tc.want)
			}
		})
	}
}

// testReplaceMessagesIsFailureSafe pins the /compress contract: the
// replacement set must be the only history left.
func testReplaceMessagesIsFailureSafe(t *testing.T, newStore func(t *testing.T) SessionStore) {
	tests := []struct {
		name        string
		existing    []string
		replacement []string
		want        []string
	}{
		{"replace many with one summary", []string{"m0", "m1", "m2", "m3"}, []string{"summary"}, []string{"summary"}},
		{"replace all with several", []string{"m0", "m1"}, []string{"a", "b"}, []string{"a", "b"}},
		{"replace into empty session", nil, []string{"a"}, []string{"a"}},
		{"replace with nothing clears history", []string{"m0", "m1"}, nil, nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			s := newStore(t)
			t.Cleanup(func() { _ = s.Close() })

			for i, text := range tc.existing {
				if err := s.SaveMessage(ctx, "sess", msg(text, dur(i))); err != nil {
					t.Fatalf("SaveMessage: %v", err)
				}
			}

			replacement := make([]Message, 0, len(tc.replacement))
			for i, text := range tc.replacement {
				replacement = append(replacement, msg(text, dur(len(tc.existing)+i)))
			}
			if err := s.ReplaceMessages(ctx, "sess", replacement, supersededIDs(ctx, t, s, "sess")); err != nil {
				t.Fatalf("ReplaceMessages: %v", err)
			}

			got, err := s.RecentMessages(ctx, "sess", 100)
			if err != nil {
				t.Fatalf("RecentMessages: %v", err)
			}
			if !equal(texts(got), tc.want) {
				t.Errorf("history = %v, want %v", texts(got), tc.want)
			}
			count, err := s.MessageCount(ctx, "sess")
			if err != nil {
				t.Fatalf("MessageCount: %v", err)
			}
			if count != len(tc.want) {
				t.Errorf("MessageCount = %d, want %d", count, len(tc.want))
			}
		})
	}
}

// testReplaceMessagesBeyondScanPageLimit pins that replacement works for
// histories larger than 1000 messages.
func testReplaceMessagesBeyondScanPageLimit(t *testing.T, newStore func(t *testing.T) SessionStore) {
	ctx := context.Background()
	s := newStore(t)
	t.Cleanup(func() { _ = s.Close() })

	const total = 1200
	for i := range total {
		if err := s.SaveMessage(ctx, "sess", msg(fmt.Sprintf("m%d", i), dur(i))); err != nil {
			t.Fatalf("SaveMessage: %v", err)
		}
	}
	if err := s.ReplaceMessages(ctx, "sess", []Message{msg("summary", dur(total))}, supersededIDs(ctx, t, s, "sess")); err != nil {
		t.Fatalf("ReplaceMessages: %v", err)
	}
	got, err := s.RecentMessages(ctx, "sess", 100)
	if err != nil {
		t.Fatalf("RecentMessages: %v", err)
	}
	if !equal(texts(got), []string{"summary"}) {
		t.Errorf("history = %v, want [summary]", texts(got))
	}
}

// testReplaceMessagesKeepsSurvivors pins the read-modify-write case:
// re-writing messages that carry upstream IDs must not delete them, since
// they land on the documents they already occupied.
func testReplaceMessagesKeepsSurvivors(t *testing.T, newStore func(t *testing.T) SessionStore) {
	ctx := context.Background()
	s := newStore(t)
	t.Cleanup(func() { _ = s.Close() })

	for i, text := range []string{"keep-a", "keep-b", "drop"} {
		m := Message{SourceID: "tg-" + text, From: "u", Text: text, At: at(dur(i))}
		if err := s.SaveMessage(ctx, "sess", m); err != nil {
			t.Fatalf("SaveMessage: %v", err)
		}
	}

	history, err := s.RecentMessages(ctx, "sess", 10)
	if err != nil {
		t.Fatalf("RecentMessages: %v", err)
	}
	if err := s.ReplaceMessages(ctx, "sess", history[:2], supersededIDs(ctx, t, s, "sess")); err != nil {
		t.Fatalf("ReplaceMessages: %v", err)
	}

	got, err := s.RecentMessages(ctx, "sess", 10)
	if err != nil {
		t.Fatalf("RecentMessages: %v", err)
	}
	if !equal(texts(got), []string{"keep-a", "keep-b"}) {
		t.Errorf("history = %v, want [keep-a keep-b]", texts(got))
	}
}

// testSessionsAreIsolated pins per-session scoping: one session's history
// and search results must never include another's.
func testSessionsAreIsolated(t *testing.T, newStore func(t *testing.T) SessionStore) {
	ctx := context.Background()
	s := newStore(t)
	t.Cleanup(func() { _ = s.Close() })

	if err := s.SaveMessage(ctx, "alpha", msg("alpha secret", 0)); err != nil {
		t.Fatalf("SaveMessage alpha: %v", err)
	}
	if err := s.SaveMessage(ctx, "beta", msg("beta secret", time.Second)); err != nil {
		t.Fatalf("SaveMessage beta: %v", err)
	}

	for _, tc := range []struct{ session, want string }{
		{"alpha", "alpha secret"},
		{"beta", "beta secret"},
	} {
		got, err := s.RecentMessages(ctx, tc.session, 100)
		if err != nil {
			t.Fatalf("RecentMessages(%s): %v", tc.session, err)
		}
		if !equal(texts(got), []string{tc.want}) {
			t.Errorf("session %s history = %v, want [%s]", tc.session, texts(got), tc.want)
		}

		page, err := s.SearchMessages(ctx, tc.session, MessageQuery{Query: "secret", Limit: 10})
		if err != nil {
			t.Fatalf("SearchMessages(%s): %v", tc.session, err)
		}
		if !equal(texts(page.Messages), []string{tc.want}) {
			t.Errorf("session %s search = %v, want [%s]", tc.session, texts(page.Messages), tc.want)
		}
	}

	count, err := s.MessageCount(ctx, "alpha")
	if err != nil {
		t.Fatalf("MessageCount: %v", err)
	}
	if count != 1 {
		t.Errorf("MessageCount(alpha) = %d, want 1", count)
	}
}

// testHistoryBeyondScanPageLimit pins that history operations page past a
// 1000-row internal scan clamp instead of silently truncating. /compress
// calls DeleteRecentMessages with the full message count, so a silent cap
// there strands messages while reporting success.
func testHistoryBeyondScanPageLimit(t *testing.T, newStore func(t *testing.T) SessionStore) {
	ctx := context.Background()
	s := newStore(t)
	t.Cleanup(func() { _ = s.Close() })

	const total = 1200
	for i := range total {
		m := msg(fmt.Sprintf("m%d", i), time.Duration(i)*time.Second)
		if err := s.SaveMessage(ctx, "sess", m); err != nil {
			t.Fatalf("SaveMessage: %v", err)
		}
	}

	got, err := s.RecentMessages(ctx, "sess", total)
	if err != nil {
		t.Fatalf("RecentMessages: %v", err)
	}
	if len(got) != total {
		t.Errorf("RecentMessages returned %d, want %d", len(got), total)
	}
	if len(got) > 0 && got[0].Text != "m0" {
		t.Errorf("oldest message = %q, want m0", got[0].Text)
	}

	count, err := s.MessageCount(ctx, "sess")
	if err != nil {
		t.Fatalf("MessageCount: %v", err)
	}
	if count != total {
		t.Errorf("MessageCount = %d, want %d", count, total)
	}

	deleted, err := s.DeleteRecentMessages(ctx, "sess", total)
	if err != nil {
		t.Fatalf("DeleteRecentMessages: %v", err)
	}
	if deleted != total {
		t.Errorf("deleted %d, want %d", deleted, total)
	}
	remaining, err := s.MessageCount(ctx, "sess")
	if err != nil {
		t.Fatalf("MessageCount: %v", err)
	}
	if remaining != 0 {
		t.Errorf("%d messages remain after deleting all", remaining)
	}
}

// testDeleteRecentMessagesReturnsCountDeleted pins that the reported count
// reflects what was actually deleted, including when asked to delete more
// than exists.
func testDeleteRecentMessagesReturnsCountDeleted(t *testing.T, newStore func(t *testing.T) SessionStore) {
	tests := []struct {
		name      string
		saved     int
		request   int
		wantCount int
	}{
		{"delete fewer than exist", 3, 2, 2},
		{"delete exactly what exists", 3, 3, 3},
		{"delete more than exists", 3, 5, 3},
		{"delete from empty session", 0, 5, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			s := newStore(t)
			t.Cleanup(func() { _ = s.Close() })

			for i := range tc.saved {
				if err := s.SaveMessage(ctx, "sess", msg(fmt.Sprintf("m%d", i), dur(i))); err != nil {
					t.Fatalf("SaveMessage: %v", err)
				}
			}
			deleted, err := s.DeleteRecentMessages(ctx, "sess", tc.request)
			if err != nil {
				t.Fatalf("DeleteRecentMessages: %v", err)
			}
			if deleted != tc.wantCount {
				t.Errorf("deleted = %d, want %d", deleted, tc.wantCount)
			}
		})
	}
}

// testSearchMessagesPaging pins that Limit is a page size, not a total cap,
// and that paging walks the whole result set without gaps or repeats.
func testSearchMessagesPaging(t *testing.T, newStore func(t *testing.T) SessionStore) {
	const total = 250 // deliberately above the old hard-coded 200 ceiling

	tests := []struct {
		name      string
		pageSize  int
		wantPages int
	}{
		{"page size 10", 10, 25},
		{"page size 50", 50, 5},
		{"page size larger than result set", 500, 1},
		{"page size 1", 1, total},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			s := newStore(t)
			t.Cleanup(func() { _ = s.Close() })

			for i := range total {
				m := msg(fmt.Sprintf("needle %d", i), time.Duration(i)*time.Second)
				if err := s.SaveMessage(ctx, "sess", m); err != nil {
					t.Fatalf("SaveMessage: %v", err)
				}
			}

			seen := map[string]bool{}
			pages, offset := 0, 0
			for {
				page, err := s.SearchMessages(ctx, "sess", MessageQuery{
					Query: "needle", Limit: tc.pageSize, Offset: offset,
				})
				if err != nil {
					t.Fatalf("SearchMessages: %v", err)
				}
				pages++
				if len(page.Messages) > tc.pageSize {
					t.Fatalf("page returned %d messages, exceeds page size %d", len(page.Messages), tc.pageSize)
				}
				for _, m := range page.Messages {
					if seen[m.Text] {
						t.Errorf("duplicate result across pages: %q", m.Text)
					}
					seen[m.Text] = true
				}
				if !page.HasMore {
					break
				}
				if page.NextOffset <= offset {
					t.Fatalf("NextOffset %d did not advance past %d", page.NextOffset, offset)
				}
				offset = page.NextOffset
				if pages > total+1 {
					t.Fatal("paging did not terminate")
				}
			}

			if pages != tc.wantPages {
				t.Errorf("walked %d pages, want %d", pages, tc.wantPages)
			}
			if len(seen) != total {
				t.Errorf("saw %d unique messages across all pages, want %d", len(seen), total)
			}
		})
	}
}

// testSearchMessagesBeyondOldCeiling pins that a match buried more than 200
// messages deep is still found.
func testSearchMessagesBeyondOldCeiling(t *testing.T, newStore func(t *testing.T) SessionStore) {
	ctx := context.Background()
	s := newStore(t)
	t.Cleanup(func() { _ = s.Close() })

	if err := s.SaveMessage(ctx, "sess", msg("distinctive needle", 0)); err != nil {
		t.Fatalf("SaveMessage: %v", err)
	}
	for i := range 300 {
		if err := s.SaveMessage(ctx, "sess", msg(fmt.Sprintf("filler %d", i), time.Duration(i+1)*time.Second)); err != nil {
			t.Fatalf("SaveMessage: %v", err)
		}
	}

	page, err := s.SearchMessages(ctx, "sess", MessageQuery{Query: "distinctive", Limit: 10})
	if err != nil {
		t.Fatalf("SearchMessages: %v", err)
	}
	if !equal(texts(page.Messages), []string{"distinctive needle"}) {
		t.Errorf("got %v, want [distinctive needle]", texts(page.Messages))
	}
}

// testSearchMatchesTextNotMetadata pins that persistence metadata stays out
// of the text index: a query for a year or a channel-id fragment must not
// match everything.
func testSearchMatchesTextNotMetadata(t *testing.T, newStore func(t *testing.T) SessionStore) {
	tests := []struct {
		name  string
		query string
		want  []string
	}{
		{"message text matches", "hello", []string{"hello world"}},
		{"sender matches", "alice", []string{"hello world"}},
		{"timestamp year does not match", "2026", nil},
		{"timestamp month does not match", "08", nil},
		{"channel id does not match", "xyz", nil},
		{"unrelated term does not match", "nonsense", nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			s := newStore(t)
			t.Cleanup(func() { _ = s.Close() })

			if err := s.SaveMessage(ctx, "sess", Message{
				From: "alice", Text: "hello world", ChannelID: "chan-xyz", ThreadID: "thread-xyz", At: base,
			}); err != nil {
				t.Fatalf("SaveMessage: %v", err)
			}
			if err := s.SaveMessage(ctx, "sess", Message{
				From: "bob", Text: "goodbye", ChannelID: "chan-xyz", ThreadID: "thread-xyz", At: at(time.Second),
			}); err != nil {
				t.Fatalf("SaveMessage: %v", err)
			}

			page, err := s.SearchMessages(ctx, "sess", MessageQuery{Query: tc.query, Limit: 10})
			if err != nil {
				t.Fatalf("SearchMessages: %v", err)
			}
			got := texts(page.Messages)
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !equal(got, tc.want) {
				t.Errorf("query %q matched %v, want %v", tc.query, got, tc.want)
			}
		})
	}
}

// testSearchEmptyQueryReturnsEmptyPage pins that an empty query returns an
// empty page rather than the whole history or an error.
func testSearchEmptyQueryReturnsEmptyPage(t *testing.T, newStore func(t *testing.T) SessionStore) {
	tests := []struct {
		name  string
		query string
	}{
		{"empty string", ""},
		{"whitespace only", "   "},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			s := newStore(t)
			t.Cleanup(func() { _ = s.Close() })

			if err := s.SaveMessage(ctx, "sess", msg("hello world", 0)); err != nil {
				t.Fatalf("SaveMessage: %v", err)
			}

			page, err := s.SearchMessages(ctx, "sess", MessageQuery{Query: tc.query, Limit: 10})
			if err != nil {
				t.Fatalf("SearchMessages: %v", err)
			}
			if len(page.Messages) != 0 {
				t.Errorf("got %d messages, want 0 for query %q", len(page.Messages), tc.query)
			}
			if page.HasMore {
				t.Errorf("HasMore = true, want false for query %q", tc.query)
			}
		})
	}
}

// testNewestFirstOrdering pins the observable newest-first contract that List
// and GetByChannel both document.
//
// Recency is LastActiveAt -- that is what Touch updates and what "newest"
// means to someone reading /sessions -- so a session created first but used
// most recently sorts first.
func testNewestFirstOrdering(t *testing.T, newStore func(t *testing.T) SessionStore) {
	ctx := context.Background()
	s := newStore(t)
	t.Cleanup(func() { _ = s.Close() })

	// Created oldest-to-newest, but used in a different order, so ordering
	// by creation and ordering by recency give different answers. IDs also
	// disagree with both, so document order cannot pass by luck.
	seed := []struct {
		id      string
		created time.Duration
		active  time.Duration
	}{
		{"zzz-created-first", 0, 3 * time.Hour},
		{"aaa-created-second", time.Hour, time.Hour},
		{"mmm-created-last", 2 * time.Hour, 2 * time.Hour},
	}
	for _, sd := range seed {
		if err := s.Save(ctx, SessionContext{
			SessionID: sd.id,
			Source: SessionSource{
				Platform:  "telegram",
				BotUser:   "archie",
				ChannelID: "chat-1",
			},
			CreatedAt:    base.Add(sd.created),
			LastActiveAt: base.Add(sd.active),
		}); err != nil {
			t.Fatalf("Save %s: %v", sd.id, err)
		}
	}

	want := []string{"zzz-created-first", "mmm-created-last", "aaa-created-second"}

	assertOrder := func(what string, got []SessionContext) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("%s returned %d sessions, want %d", what, len(got), len(want))
		}
		for i, w := range want {
			if got[i].SessionID != w {
				t.Errorf("%s[%d] = %q, want %q (full order %v)", what, i, got[i].SessionID, w, sessionIDs(got))
			}
		}
	}

	list, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	assertOrder("List", list)

	byChannel, err := s.GetByChannel(ctx, "telegram", "chat-1")
	if err != nil {
		t.Fatalf("GetByChannel: %v", err)
	}
	assertOrder("GetByChannel", byChannel)

	// Touch is the live path that makes a session the newest one. Ordering
	// that ignores it silently resumes a stale conversation.
	if err := s.Touch(ctx, "aaa-created-second"); err != nil {
		t.Fatalf("Touch: %v", err)
	}
	touched, err := s.GetByChannel(ctx, "telegram", "chat-1")
	if err != nil {
		t.Fatalf("GetByChannel after Touch: %v", err)
	}
	if len(touched) == 0 || touched[0].SessionID != "aaa-created-second" {
		t.Fatalf("after Touch, GetByChannel[0] = %v, want the touched session first",
			sessionIDs(touched))
	}
}
