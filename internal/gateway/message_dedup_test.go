package gateway

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/messaging"
)

// dedupInbound builds an Inbound carrying a channel-native message ID, the
// shape a duplicate delivery takes: the same source message ID
// re-presented by the channel.
func dedupInbound(platform, channelID, threadID, sourceID string) Inbound {
	return Inbound{
		Platform: platform,
		Message: messaging.Message{
			ConversationID: messaging.ConversationID{ChannelID: channelID, ThreadID: threadID},
			SourceID:       sourceID,
			Role:           messaging.RoleUser,
			Text:           "hello",
		},
	}
}

// TestMessageDeduplicatorAdmit covers the gate's contract: the first
// delivery within the TTL window is admitted, a repeat of the same
// platform message is rejected, and expiry is judged by an injected
// clock, never a real sleep. The key is platform + channel (conversation)
// + message ID, so a second distinct channel-native ID from the same
// sender is a genuine repeat message, not a delivery duplicate.
func TestMessageDeduplicatorAdmit(t *testing.T) {
	const tg = "telegram"

	tests := []struct {
		name       string
		prepare    []Inbound // deliveries recorded before the one under test
		advanceBy  time.Duration
		admit      Inbound // the delivery being tested
		wantAdmit  bool
		wantStored int // entries the cache must hold after the tested Admit
	}{
		{
			name:      "the first delivery is admitted",
			advanceBy: 0,
			admit:     dedupInbound(tg, "100", "7", "42"),
			wantAdmit: true, wantStored: 1,
		},
		{
			name:      "a repeat delivery within the TTL is rejected",
			prepare:   []Inbound{dedupInbound(tg, "100", "7", "42")},
			advanceBy: defaultDedupTTL - time.Second,
			admit:     dedupInbound(tg, "100", "7", "42"),
			wantAdmit: false, wantStored: 1,
		},
		// The boundary is pinned exactly: TTL elapsed clears the entry,
		// one second shy does not. No sleeps; the clock is injected.
		{
			name:      "the same message after the TTL passes",
			prepare:   []Inbound{dedupInbound(tg, "100", "7", "42")},
			advanceBy: defaultDedupTTL,
			admit:     dedupInbound(tg, "100", "7", "42"),
			wantAdmit: true, wantStored: 1,
		},
		{
			name:      "a repeat in a different conversation is a distinct message",
			prepare:   []Inbound{dedupInbound(tg, "100", "7", "42")},
			advanceBy: 0,
			admit:     dedupInbound(tg, "100", "8", "42"),
			wantAdmit: true, wantStored: 2,
		},
		{
			name:      "a repeat on a different platform is a distinct message",
			prepare:   []Inbound{dedupInbound(tg, "100", "7", "42")},
			advanceBy: 0,
			admit:     dedupInbound("web", "100", "7", "42"),
			wantAdmit: true, wantStored: 2,
		},
		{
			name:      "a second message from the same sender is admitted",
			prepare:   []Inbound{dedupInbound(tg, "100", "7", "42")},
			advanceBy: 0,
			admit:     dedupInbound(tg, "100", "7", "43"),
			wantAdmit: true, wantStored: 2,
		},
		{
			name:      "a message with no source ID cannot be deduplicated",
			prepare:   []Inbound{dedupInbound(tg, "100", "7", "")},
			advanceBy: 0,
			admit:     dedupInbound(tg, "100", "7", ""),
			wantAdmit: true, wantStored: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := time.Unix(1700000000, 0)
			current := base
			now := func() time.Time { return current }
			d := NewMessageDeduplicator(0, 0, now)
			for _, in := range tt.prepare {
				if !d.Admit(in) {
					t.Fatalf("prepare delivery unexpectedly rejected: source %q", in.Message.SourceID)
				}
			}
			current = base.Add(tt.advanceBy)
			if got := d.Admit(tt.admit); got != tt.wantAdmit {
				t.Fatalf("Admit = %v, want %v", got, tt.wantAdmit)
			}
			if got := len(d.entries); got != tt.wantStored {
				t.Fatalf("cache holds %d entries, want %d (repeat rejections must not grow the cache)", got, tt.wantStored)
			}
		})
	}
}

// TestMessageDeduplicatorCapacity proves the cache is bounded: at
// capacity, an insert evicts expired entries first, then the oldest
// still-live delivery, never growing past the bound.
func TestMessageDeduplicatorCapacity(t *testing.T) {
	const tg = "telegram"
	current := time.Unix(1700000000, 0)
	now := func() time.Time { return current }
	d := NewMessageDeduplicator(defaultDedupTTL, 2, now)

	if !d.Admit(dedupInbound(tg, "1", "", "a")) {
		t.Fatal("first delivery rejected")
	}
	if !d.Admit(dedupInbound(tg, "1", "", "b")) {
		t.Fatal("second delivery rejected")
	}
	// Inserting a third message at the bound evicts the oldest live
	// delivery (a), which is then admitted again.
	if !d.Admit(dedupInbound(tg, "1", "", "c")) {
		t.Fatal("third delivery rejected")
	}
	if len(d.entries) != 2 {
		t.Fatalf("cache holds %d entries, want the bound of 2", len(d.entries))
	}
	if _, stillTracked := d.entries[dedupKey{Platform: tg, Conversation: "1", SourceID: "a"}]; stillTracked {
		t.Fatal("expected a to have been evicted as the oldest entry")
	}
	if !d.Admit(dedupInbound(tg, "1", "", "a")) {
		t.Fatal("an evicted entry must be re-admitted on its next delivery")
	}
	if d.Admit(dedupInbound(tg, "1", "", "c")) {
		t.Fatal("c is still live inside the TTL (b, the oldest entry, was evicted to make room for a's second delivery)")
	}

	// Expired eviction beats LRU: every entry is well past the TTL, so
	// the next insert drops the expired tail rather than recent entries.
	current = now().Add(defaultDedupTTL + time.Hour)
	if !d.Admit(dedupInbound(tg, "2", "", "d")) {
		t.Fatal("delivery after full expiry rejected")
	}
	if len(d.entries) != 1 {
		t.Fatalf("cache holds %d entries after expiry sweep, want 1", len(d.entries))
	}
	if !d.Admit(dedupInbound(tg, "1", "", "a")) {
		t.Fatal("an expired entry must be admitted again on its next delivery")
	}
}

// TestMessageDeduplicatorConcurrent drives the gate from many goroutines
// at once, as the Router does: one telegram worker, several web sessions
// and webhook posts all dispatch through the same Router. Exactly one
// racing delivery may be admitted, and the -race run proves the guard.
func TestMessageDeduplicatorConcurrent(t *testing.T) {
	current := time.Unix(1700000000, 0)
	now := func() time.Time { return current }
	d := NewMessageDeduplicator(0, 0, now)

	t.Run("racing repeats of one delivery", func(t *testing.T) {
		in := dedupInbound("telegram", "1", "", "7")
		const workers = 32
		var admitted atomic.Int64
		var wg sync.WaitGroup
		wg.Add(workers)
		for range workers {
			go func() {
				defer wg.Done()
				if d.Admit(in) {
					admitted.Add(1)
				}
			}()
		}
		wg.Wait()
		if got := admitted.Load(); got != 1 {
			t.Fatalf("%d goroutines raced the same delivery and %d were admitted, want exactly 1", workers, got)
		}
	})

	t.Run("racing distinct deliveries stay bounded", func(t *testing.T) {
		fresh := NewMessageDeduplicator(0, 16, now)
		const workers = 64
		var wg sync.WaitGroup
		wg.Add(workers)
		for i := range workers {
			go func(i int) {
				defer wg.Done()
				fresh.Admit(dedupInbound("telegram", "1", "", strconv.Itoa(i)))
			}(i)
		}
		wg.Wait()
		if len(fresh.entries) != 16 {
			t.Fatalf("cache holds %d entries, want the bound of 16", len(fresh.entries))
		}
	})
}

// countingLLM is an LLMResponder stub that counts invocations.
type countingLLM struct {
	reply string
	calls *int64
}

func (c *countingLLM) respond(ctx context.Context, in Inbound) (string, error) {
	atomic.AddInt64(c.calls, 1)
	return c.reply, nil
}

// TestRouterRejectsRedeliveredMessage wires the gate where its owner
// lives: the Router declines a second delivery of the same platform
// message without reaching the LLM again, honours the TTL with an
// injected clock, and a nil Dedup disables the gate.
func TestRouterRejectsRedeliveredMessage(t *testing.T) {
	t.Run("the second delivery is declined without reaching the LLM", func(t *testing.T) {
		current := time.Unix(1700000000, 0)
		now := func() time.Time { return current }
		var calls int64
		r := NewRouter(nil, (&countingLLM{reply: "pong", calls: &calls}).respond, "web")
		r.Dedup = NewMessageDeduplicator(0, 0, now)
		in := dedupInbound("telegram", "100", "7", "42")

		if reply, err := r.Route(context.Background(), in); err != nil || reply != "pong" {
			t.Fatalf("first delivery: reply %q, err %v, want pong", reply, err)
		}
		if reply, _ := r.Route(context.Background(), in); reply == "pong" {
			t.Fatal("the repeat delivery was answered again instead of being declined")
		}
		if calls != 1 {
			t.Fatalf("LLM called %d times for one accepted delivery, want 1", calls)
		}

		// The streamed repeat must render the gate's prose, never the turn:
		// the declines happen before any stream exists.
		chat := &LocalChatAdapter{Router: r}
		deltas, kind, text, err := drainStreamed(t, chat, in)
		if err != nil {
			t.Fatalf("streamed repeat delivery: unexpected error %v", err)
		} else if text == "pong" {
			t.Fatal("the streamed repeat delivery was streamed again instead of being declined")
		}
		if calls != 1 || len(deltas) != 0 {
			t.Fatalf("streamed repeat reached the stream (calls %d, deltas %d), want none", calls, len(deltas))
		}
		if kind != "done" || text != dedupReply {
			t.Fatalf("streamed repeat terminal = (%s, %q), want the dedup prose", kind, text)
		}
	})

	t.Run("after the TTL the same message is processed again", func(t *testing.T) {
		current := time.Unix(1700000000, 0)
		now := func() time.Time { return current }
		var calls int64
		r := NewRouter(nil, (&countingLLM{reply: "pong", calls: &calls}).respond, "web")
		r.Dedup = NewMessageDeduplicator(defaultDedupTTL, 0, now)
		in := dedupInbound("telegram", "100", "7", "42")
		if _, err := r.Route(context.Background(), in); err != nil {
			t.Fatal(err)
		}
		current = now().Add(defaultDedupTTL)
		if reply, err := r.Route(context.Background(), in); err != nil || reply != "pong" {
			t.Fatalf("post-TTL delivery: reply %q, err %v, want pong", reply, err)
		}
	})

	t.Run("nil Dedup disables the gate", func(t *testing.T) {
		var calls int64
		r := NewRouter(nil, (&countingLLM{reply: "pong", calls: &calls}).respond, "web")
		r.Dedup = nil
		in := dedupInbound("telegram", "100", "7", "42")
		for range 2 {
			if _, err := r.Route(context.Background(), in); err != nil {
				t.Fatal(err)
			}
		}
		if calls != 2 {
			t.Fatalf("with dedup disabled, LLM should run for both deliveries, got %d calls", calls)
		}
	})
}

// TestNewRouterWiresTheDefaultGate holds the composition root to the
// contract: the production router constructor wires the gate, so a
// deployment cannot silently process every redelivery.
func TestNewRouterWiresTheDefaultGate(t *testing.T) {
	r := NewRouter(nil, nil, "web")
	if r.Dedup == nil {
		t.Fatal("NewRouter must wire the default deduplicator")
	}
	if r.Dedup.ttl != defaultDedupTTL || r.Dedup.cap != defaultDedupCapacity {
		t.Fatalf("default gate configured with ttl %v cap %d, want the package defaults", r.Dedup.ttl, r.Dedup.cap)
	}
}
