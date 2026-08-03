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

// TestSaveMessagesAppends pins the data-loss defect: a bulk save into a
// session that already holds messages must append, never overwrite.
func TestSaveMessagesAppends(t *testing.T) {
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
			s := NewSessionStoreMemory("test-node")
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

// TestDeleteThenSaveDoesNotReuseKeys pins the seq-reuse defect: deleting
// the newest messages then saving must not collide with a live key.
func TestDeleteThenSaveDoesNotReuseKeys(t *testing.T) {
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
			s := NewSessionStoreMemory("test-node")
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

// TestMessagesOrderByTimestamp pins chronology: ordering follows Message.At,
// not the order rows happened to be written.
func TestMessagesOrderByTimestamp(t *testing.T) {
	tests := []struct {
		name       string
		writeOrder []time.Duration
		want       []string
	}{
		{"already chronological", []time.Duration{0, time.Second, 2 * time.Second}, []string{"t0", "t1", "t2"}},
		{"written out of order", []time.Duration{2 * time.Second, 0, time.Second}, []string{"t1", "t2", "t0"}},
		{"reverse written", []time.Duration{2 * time.Second, time.Second, 0}, []string{"t2", "t1", "t0"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			s := NewSessionStoreMemory("test-node")
			t.Cleanup(func() { _ = s.Close() })

			for i, offset := range tc.writeOrder {
				if err := s.SaveMessage(ctx, "sess", msg(fmt.Sprintf("t%d", i), offset)); err != nil {
					t.Fatalf("SaveMessage: %v", err)
				}
			}
			got, err := s.RecentMessages(ctx, "sess", 100)
			if err != nil {
				t.Fatalf("RecentMessages: %v", err)
			}
			// want lists texts in the order their timestamps dictate.
			if !equal(texts(got), tc.want) {
				t.Errorf("got %v, want %v", texts(got), tc.want)
			}
		})
	}
}

// TestMessageTimestampRoundTrips pins that At survives persistence, since
// the previous implementation wrote a timestamp and never read it back.
func TestMessageTimestampRoundTrips(t *testing.T) {
	ctx := context.Background()
	s := NewSessionStoreMemory("test-node")
	t.Cleanup(func() { _ = s.Close() })

	want := at(90 * time.Minute)
	if err := s.SaveMessage(ctx, "sess", Message{From: "u", Text: "hello", At: want}); err != nil {
		t.Fatalf("SaveMessage: %v", err)
	}
	got, err := s.RecentMessages(ctx, "sess", 1)
	if err != nil {
		t.Fatalf("RecentMessages: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d messages, want 1", len(got))
	}
	if !got[0].At.Equal(want) {
		t.Errorf("At = %s, want %s", got[0].At, want)
	}
}

// TestSaveMessageStampsMissingTimestamp pins that a caller omitting At still
// gets a usable ordering key rather than the zero time.
func TestSaveMessageStampsMissingTimestamp(t *testing.T) {
	ctx := context.Background()
	s := NewSessionStoreMemory("test-node")
	t.Cleanup(func() { _ = s.Close() })

	before := time.Now().UTC()
	if err := s.SaveMessage(ctx, "sess", Message{From: "u", Text: "no timestamp"}); err != nil {
		t.Fatalf("SaveMessage: %v", err)
	}
	after := time.Now().UTC()

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

// TestSessionsAreIsolated pins per-session collection scoping: one session's
// history and search results must never include another's.
func TestSessionsAreIsolated(t *testing.T) {
	ctx := context.Background()
	s := NewSessionStoreMemory("test-node")
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

// TestSearchMessagesPaging pins that Limit is a page size, not a total cap,
// and that paging walks the whole result set without gaps or repeats.
func TestSearchMessagesPaging(t *testing.T) {
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
			s := NewSessionStoreMemory("test-node")
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

// TestSearchMessagesBeyondOldCeiling pins the specific regression: a match
// older than the most recent 200 messages must still be findable.
func TestSearchMessagesBeyondOldCeiling(t *testing.T) {
	ctx := context.Background()
	s := NewSessionStoreMemory("test-node")
	t.Cleanup(func() { _ = s.Close() })

	// The needle is the oldest message; 300 newer messages bury it well
	// past the old 200-message window.
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

// TestHistoryBeyondScanPageLimit pins that history operations page past
// NellDB's internal 1000-row ScanSince clamp instead of silently truncating.
// /compress calls DeleteRecentMessages with the full message count, so a
// silent cap there strands messages while reporting success.
func TestHistoryBeyondScanPageLimit(t *testing.T) {
	tests := []struct {
		name  string
		total int
	}{
		{"just under the clamp", 999},
		{"exactly the clamp", 1000},
		{"over the clamp", 1200},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			s := NewSessionStoreMemory("test-node")
			t.Cleanup(func() { _ = s.Close() })

			for i := range tc.total {
				m := msg(fmt.Sprintf("m%d", i), time.Duration(i)*time.Second)
				if err := s.SaveMessage(ctx, "sess", m); err != nil {
					t.Fatalf("SaveMessage: %v", err)
				}
			}

			got, err := s.RecentMessages(ctx, "sess", tc.total)
			if err != nil {
				t.Fatalf("RecentMessages: %v", err)
			}
			if len(got) != tc.total {
				t.Errorf("RecentMessages returned %d, want %d", len(got), tc.total)
			}
			if len(got) > 0 && got[0].Text != "m0" {
				t.Errorf("oldest message = %q, want m0", got[0].Text)
			}

			deleted, err := s.DeleteRecentMessages(ctx, "sess", tc.total)
			if err != nil {
				t.Fatalf("DeleteRecentMessages: %v", err)
			}
			if deleted != tc.total {
				t.Errorf("deleted %d, want %d", deleted, tc.total)
			}
			remaining, err := s.MessageCount(ctx, "sess")
			if err != nil {
				t.Fatalf("MessageCount: %v", err)
			}
			if remaining != 0 {
				t.Errorf("%d messages remain after deleting all", remaining)
			}
		})
	}
}

// TestSearchTruncationIsReported pins that hitting the engine's ranked-result
// ceiling is surfaced as Truncated rather than passed off as "no more
// results", so a caller can tell an exhausted result set from a cut-off one.
func TestSearchTruncationIsReported(t *testing.T) {
	ctx := context.Background()
	s := NewSessionStoreMemory("test-node")
	t.Cleanup(func() { _ = s.Close() })

	// More matches than the engine will ever rank.
	for i := range MaxSearchResults + 100 {
		if err := s.SaveMessage(ctx, "sess", msg(fmt.Sprintf("needle %d", i), time.Duration(i)*time.Second)); err != nil {
			t.Fatalf("SaveMessage: %v", err)
		}
	}

	// Walk to the last reachable page.
	offset, pages := 0, 0
	var last MessagePage
	for {
		page, err := s.SearchMessages(ctx, "sess", MessageQuery{
			Query: "needle", Limit: MaxMessagePageSize, Offset: offset,
		})
		if err != nil {
			t.Fatalf("SearchMessages: %v", err)
		}
		last = page
		pages++
		if !page.HasMore {
			break
		}
		offset = page.NextOffset
		if pages > 10 {
			t.Fatal("paging did not terminate")
		}
	}

	if !last.Truncated {
		t.Error("final page Truncated = false, want true when matches exceed MaxSearchResults")
	}
	if last.HasMore {
		t.Error("final page HasMore = true, want false")
	}
}

// TestSearchExhaustedIsNotTruncated pins the complement: a result set that
// fits within the ceiling must not be reported as truncated.
func TestSearchExhaustedIsNotTruncated(t *testing.T) {
	ctx := context.Background()
	s := NewSessionStoreMemory("test-node")
	t.Cleanup(func() { _ = s.Close() })

	for i := range 20 {
		if err := s.SaveMessage(ctx, "sess", msg(fmt.Sprintf("needle %d", i), time.Duration(i)*time.Second)); err != nil {
			t.Fatalf("SaveMessage: %v", err)
		}
	}
	page, err := s.SearchMessages(ctx, "sess", MessageQuery{Query: "needle", Limit: 100})
	if err != nil {
		t.Fatalf("SearchMessages: %v", err)
	}
	if page.Truncated {
		t.Error("Truncated = true, want false for a result set inside the ceiling")
	}
	if page.HasMore {
		t.Error("HasMore = true, want false")
	}
	if len(page.Messages) != 20 {
		t.Errorf("got %d messages, want 20", len(page.Messages))
	}
}

// TestDeleteSessionDropsMessages pins that removing a session also removes
// its message collection rather than orphaning it.
func TestDeleteSessionDropsMessages(t *testing.T) {
	ctx := context.Background()
	s := NewSessionStoreMemory("test-node")
	t.Cleanup(func() { _ = s.Close() })

	if err := s.SaveMessage(ctx, "sess", msg("hello", 0)); err != nil {
		t.Fatalf("SaveMessage: %v", err)
	}
	if err := s.Delete(ctx, "sess"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	count, err := s.MessageCount(ctx, "sess")
	if err != nil {
		t.Fatalf("MessageCount: %v", err)
	}
	if count != 0 {
		t.Errorf("MessageCount after Delete = %d, want 0", count)
	}
}
