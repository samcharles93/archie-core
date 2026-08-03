package gateway

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// TestMessageTimestampRoundTrips pins that At survives persistence, since
// the previous implementation wrote a timestamp and never read it back.
// Resolution is milliseconds: _ts is stored as Unix milliseconds so the
// text index does not tokenize it. This is a NellDB-specific persistence
// detail rather than a cross-implementation contract, so it stays here
// rather than in the shared SessionStore suite.
func TestMessageTimestampRoundTrips(t *testing.T) {
	ctx := context.Background()
	s := NewSessionStoreMemory("test-node")
	t.Cleanup(func() { _ = s.Close() })

	want := at(90*time.Minute + 123*time.Millisecond)
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

// TestSearchTruncationIsReported pins that hitting the engine's ranked-result
// ceiling is surfaced as Truncated rather than passed off as "no more
// results", so a caller can tell an exhausted result set from a cut-off one.
// MaxSearchResults is a NellDB engine ceiling (nl.MaxTextSearchLimit), so
// this test is pinned against the NellDB implementation rather than the
// shared cross-implementation suite.
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

// TestTruncatedReflectsResultSetNotRequest pins that a deep offset over a
// small result set is reported as exhausted, not truncated. Anchored on
// MaxSearchResults, the NellDB engine ceiling.
func TestTruncatedReflectsResultSetNotRequest(t *testing.T) {
	tests := []struct {
		name          string
		matches       int
		offset        int
		limit         int
		wantTruncated bool
	}{
		{"deep offset over tiny result set", 3, MaxSearchResults, 50, false},
		{"deep offset over empty result set", 0, MaxSearchResults, 50, false},
		{"shallow offset over small result set", 3, 0, 50, false},
		{"offset just past a small result set", 3, 10, 50, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			s := NewSessionStoreMemory("test-node")
			t.Cleanup(func() { _ = s.Close() })

			for i := range tc.matches {
				if err := s.SaveMessage(ctx, "sess", msg(fmt.Sprintf("needle %d", i), time.Duration(i)*time.Second)); err != nil {
					t.Fatalf("SaveMessage: %v", err)
				}
			}
			page, err := s.SearchMessages(ctx, "sess", MessageQuery{
				Query: "needle", Limit: tc.limit, Offset: tc.offset,
			})
			if err != nil {
				t.Fatalf("SearchMessages: %v", err)
			}
			if page.Truncated != tc.wantTruncated {
				t.Errorf("Truncated = %v, want %v", page.Truncated, tc.wantTruncated)
			}
		})
	}
}

// TestMessageDBCacheIsBounded pins that touching many sessions does not grow
// the DocDB cache without limit, and that eviction costs nothing but time.
// This exercises nellSessionStore internals directly, so it is pinned
// against the NellDB implementation rather than the shared suite.
func TestMessageDBCacheIsBounded(t *testing.T) {
	ctx := context.Background()
	s := NewSessionStoreMemory("test-node")
	t.Cleanup(func() { _ = s.Close() })

	const sessions = maxCachedMessageDBs * 3
	for i := range sessions {
		id := fmt.Sprintf("sess-%d", i)
		if err := s.SaveMessage(ctx, id, msg(fmt.Sprintf("in %s", id), time.Duration(i)*time.Second)); err != nil {
			t.Fatalf("SaveMessage: %v", err)
		}
	}

	ns, ok := s.(*nellSessionStore)
	if !ok {
		t.Fatalf("store is %T, want *nellSessionStore", s)
	}
	ns.mu.Lock()
	cached, ordered := len(ns.msgDBs), len(ns.msgOrder)
	ns.mu.Unlock()

	if cached > maxCachedMessageDBs {
		t.Errorf("cache holds %d entries, want at most %d", cached, maxCachedMessageDBs)
	}
	if cached != ordered {
		t.Errorf("cache map has %d entries but LRU order has %d", cached, ordered)
	}

	// An evicted session must still read back correctly.
	got, err := s.RecentMessages(ctx, "sess-0", 10)
	if err != nil {
		t.Fatalf("RecentMessages after eviction: %v", err)
	}
	if !equal(texts(got), []string{"in sess-0"}) {
		t.Errorf("evicted session read back as %v, want [in sess-0]", texts(got))
	}
}

// TestMessageIDsAreUnique pins that generated identities do not collide.
// Ordering no longer rests on ID shape -- timestamps are strictly
// increasing within a session -- but identity must still be unique, and the
// previous counter-based scheme could wrap and reuse a key.
func TestMessageIDsAreUnique(t *testing.T) {
	const count = 10_000
	seen := make(map[string]struct{}, count)
	for range count {
		id := newMessageID("sess", "")
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate message ID %q", id)
		}
		seen[id] = struct{}{}
	}
}
