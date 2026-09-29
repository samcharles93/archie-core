package gateway

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/messaging"
)

// fragment builds one rapid-fire text message carrying the per-person
// identity the batch key and the dedup gate read: the channel that brought
// it, its conversation, a stable sender, and the channel-native message ID.
// Two fragments from the same sender differ only in source ID and text.
func fragment(n int, sender, text string) Inbound {
	return Inbound{
		Platform: "telegram",
		Message: messaging.Message{
			SourceID:       fmt.Sprintf("tg-%d", n),
			ConversationID: messaging.ConversationID{ChannelID: "chat:1"},
			Sender:         sender,
			SenderID:       sender,
			Role:           messaging.RoleUser,
			Text:           text,
		},
	}
}

// routeOutcome records one RouteStream caller's reply and how its own
// stream rendered, so the dispatcher can be told apart from fragments whose
// turn another call covered.
type routeOutcome struct {
	reply  string
	err    error
	deltas int
}

// batchOf reads the open batch for a key, if one is open. Test-only: it is
// how tests see a join land without racing a blocking Collect call.
func batchOf(ag *TextBatchAggregator, key batchKey) *textBatch {
	ag.mu.Lock()
	defer ag.mu.Unlock()
	return ag.batches[key]
}

// fragmentCount reports how many fragments key's open batch holds, or -1
// when no batch is open. The read is taken under the aggregate's lock.
func fragmentCount(ag *TextBatchAggregator, key batchKey) int {
	ag.mu.Lock()
	defer ag.mu.Unlock()
	if bat := ag.batches[key]; bat != nil {
		return len(bat.fragments)
	}
	return -1
}

// waitForFragments polls until key's batch holds want fragments.
func waitForFragments(t *testing.T, ag *TextBatchAggregator, key batchKey, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if fragmentCount(ag, key) >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("the batch never registered %d fragments for %v", want, key)
}

// waitForSettled polls until key's batch has settled and left the map.
func waitForSettled(t *testing.T, ag *TextBatchAggregator, key batchKey) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		ag.mu.Lock()
		gone := ag.batches[key] == nil
		ag.mu.Unlock()
		if gone {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("the batch for %v never settled", key)
}

// fakeBatchClock is the batcher's injectable clock: Now plus close timers
// that fire when Advance moves past their deadline. Batch close times are
// tested with this, never wall-clock sleeps; the same shape the curator
// runtime's tests use.
type fakeBatchClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeBatchTimer
}

type fakeBatchTimer struct {
	c  chan time.Time
	at time.Time
}

func newBatchClock(at time.Time) *fakeBatchClock {
	return &fakeBatchClock{now: at}
}

func (fc *fakeBatchClock) Now() time.Time {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	return fc.now
}

func (fc *fakeBatchClock) After(d time.Duration) <-chan time.Time {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	t := &fakeBatchTimer{c: make(chan time.Time, 1), at: fc.now.Add(d)}
	fc.timers = append(fc.timers, t)
	return t.c
}

// Advance moves the clock forward and fires every timer whose deadline has
// passed.
func (fc *fakeBatchClock) Advance(d time.Duration) {
	fc.mu.Lock()
	fc.now = fc.now.Add(d)
	var fired []*fakeBatchTimer
	remains := fc.timers[:0]
	for _, t := range fc.timers {
		if !t.at.After(fc.now) {
			fired = append(fired, t)
		} else {
			remains = append(remains, t)
		}
	}
	fc.timers = remains
	fc.mu.Unlock()
	for _, t := range fired {
		select {
		case t.c <- t.at:
		default:
		}
	}
}

// TestRapidFireFragmentsTakeOneTurn pins the batcher's core contract:
// fragments of one thought delivered back to back from one sender must
// dispatch exactly one agent turn for the joined text, not one turn per
// fragment. The fragments are registered one at a time -- the way a channel
// delivers them -- and the batch closes when its quiet window passes.
func TestRapidFireFragmentsTakeOneTurn(t *testing.T) {
	r := NewRouter(nil, nil, "telegram")
	clock := newBatchClock(time.Now())
	r.Batches = NewTextBatchAggregator(time.Hour, 0, 0, clock.Now, clock.After)

	var mu sync.Mutex
	turns := 0
	var dispatched []string
	r.LLMStream = func(_ context.Context, in Inbound, stream TurnStream) (string, error) {
		mu.Lock()
		turns++
		dispatched = append(dispatched, in.Message.Text)
		mu.Unlock()
		stream.Delta("…")
		return "the answer", nil
	}

	key := batchKey{Platform: "telegram", Conversation: "chat:1", SenderID: "sam"}
	texts := []string{"first think", "about it", "then act"}
	outcomes := make(chan routeOutcome, len(texts))
	for i, text := range texts {
		go func(i int, text string) {
			deltas := 0
			reply, err := r.RouteStream(context.Background(), fragment(i, "sam", text), DeltaFunc(func(string) {
				deltas++
			}))
			outcomes <- routeOutcome{reply: reply, err: err, deltas: deltas}
		}(i, text)
		waitForFragments(t, r.Batches, key, i+1)
	}
	clock.Advance(2 * time.Hour)

	answerers := 0
	for range texts {
		outcome := <-outcomes
		if outcome.err != nil {
			t.Errorf("RouteStream: %v", outcome.err)
			continue
		}
		if outcome.reply == "the answer" {
			answerers++
			if outcome.deltas == 0 {
				t.Error("the fragment whose turn ran must see the reply stream through it, but no delta arrived")
			}
		} else if outcome.reply != "" {
			t.Errorf("a covered fragment answered %q, want an empty reply", outcome.reply)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if turns != 1 {
		t.Errorf("rapid-fire fragments from one sender started %d agent turns, want 1 (dispatched %q)", turns, dispatched)
	}
	if answerers != 1 {
		t.Errorf("%d of %d fragments returned the turn's own reply, want exactly 1 dispatcher", answerers, len(texts))
	}
	if turns == 1 && dispatched[0] != strings.Join(texts, "\n") {
		t.Errorf("the turn dispatched %q, want the fragments joined in arrival order", dispatched[0])
	}
}

// ---- component tests ---------------------------------------------------------

// TestTextBatchAggregatorJoinsThenReleasesOnQuietWindow walks the batcher
// directly: fragments join in order, the batch stays open while fragments
// keep arriving inside the window (each join re-arms the quiet deadline),
// settling releases everyone, and the batch's dispatcher receives the
// joined payload under the first fragment's identity. A fragment that
// arrives after the batch settled starts a fresh batch.
func TestTextBatchAggregatorJoinsThenReleasesOnQuietWindow(t *testing.T) {
	clock := newBatchClock(time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC))
	ag := NewTextBatchAggregator(time.Hour, 0, 0, clock.Now, clock.After)

	key := batchKey{Platform: "telegram", Conversation: "chat:1", SenderID: "sam"}
	texts := []string{"first think", "about it", "then act"}
	results := make(chan collectResult, len(texts))
	for i, text := range texts {
		go func(i int, text string) {
			payload, mine, err := ag.Collect(context.Background(), fragment(i, "sam", text))
			results <- collectResult{payload: payload, mine: mine, err: err}
		}(i, text)
		waitForFragments(t, ag, key, i+1)
	}
	clock.Advance(2 * time.Hour)

	got := 0
	for range len(texts) {
		out := <-results
		if out.err != nil {
			t.Errorf("Collect: %v", out.err)
			continue
		}
		got++
		if out.mine {
			first := fragment(0, "sam", "")
			if out.payload.Message.Text != strings.Join(texts, "\n") {
				t.Errorf("payload text = %q, want the fragments joined in arrival order", out.payload.Message.Text)
			}
			if out.payload.Message.SenderID != first.Message.SenderID ||
				out.payload.Message.SourceID != first.Message.SourceID ||
				out.payload.Message.ConversationID != first.Message.ConversationID {
				t.Errorf("payload identity = %v/%v, want the first fragment's", out.payload.Message.SenderID, out.payload.Message.SourceID)
			}
		} else if out.payload.Message.Text != "" {
			t.Error("a covered fragment received a payload, want none")
		}
	}
	if got != len(texts) {
		t.Errorf("%d of %d fragments were released, want all", got, len(texts))
	}

	// A fragment that arrives after the batch settled starts a fresh batch
	// rather than joining a settled one, and its window is its own.
	next := fragment(10, "sam", "a later thought")
	fresh := make(chan collectResult, 1)
	go func() {
		payload, mine, err := ag.Collect(context.Background(), next)
		fresh <- collectResult{payload: payload, mine: mine, err: err}
	}()
	waitForFragments(t, ag, key, 1)
	clock.Advance(2 * time.Hour)
	out := <-fresh
	if out.err != nil || !out.mine || out.payload.Message.Text != "a later thought" {
		t.Errorf("a fresh batch's single fragment = %q, mine %v, err %v; want itself dispatched alone", out.payload.Message.Text, out.mine, out.err)
	}
}

// collectResult pairs what one Collect call learned, for tests.
type collectResult struct {
	payload Inbound
	mine    bool
	err     error
}

// TestTextBatchAggregatorPassesNonFragmentsThrough pins what a batch never
// holds: media, a command, a message no person sent, and one with no text
// dispatch at once whatever the clock says, and carry their own payload.
func TestTextBatchAggregatorPassesNonFragmentsThrough(t *testing.T) {
	clock := newBatchClock(time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC))
	ag := NewTextBatchAggregator(time.Hour, 0, 0, clock.Now, clock.After)
	key := batchKey{Platform: "telegram", Conversation: "chat:1", SenderID: "sam"}

	media := fragment(1, "sam", "the attached log")
	media.Message.Media = []messaging.MediaAttachment{{Type: "document"}}
	command := fragment(2, "sam", "/status")
	stranger := fragment(3, "", "a route wrote this")
	empty := fragment(4, "sam", "")

	for _, in := range []Inbound{media, command, stranger, empty} {
		payload, mine, err := ag.Collect(context.Background(), in)
		if err != nil {
			t.Fatalf("Collect(%q): %v", in.Message.Text, err)
		}
		if !mine || payload.Message.Text != in.Message.Text {
			t.Errorf("Collect(%q) = (payload %q, mine %v), want it dispatched as itself", in.Message.Text, payload.Message.Text, mine)
		}
	}
	if batchOf(ag, key) != nil {
		t.Error("non-fragment messages left a batch open behind them")
	}

	// A lone plain-text fragment is its own batch: it dispatches once the
	// quiet window passes, with its payload unchanged.
	lone := make(chan collectResult, 1)
	go func() {
		payload, mine, err := ag.Collect(context.Background(), fragment(5, "lone-person", "just one message"))
		lone <- collectResult{payload: payload, mine: mine, err: err}
	}()
	waitForFragments(t, ag, batchKey{Platform: "telegram", Conversation: "chat:1", SenderID: "lone-person"}, 1)
	clock.Advance(2 * time.Hour)
	waitForSettled(t, ag, batchKey{Platform: "telegram", Conversation: "chat:1", SenderID: "lone-person"})
	out := <-lone
	if out.err != nil || !out.mine || out.payload.Message.Text != "just one message" {
		t.Errorf("a singleton = %q, mine %v, err %v; want itself dispatched once the window passed", out.payload.Message.Text, out.mine, out.err)
	}
}

// TestTextBatchAggregatorClosesOnItsBounds pins the early close: a batch
// that reaches its fragment bound settles at once, without waiting out the
// window, and releases its fragments; a later fragment forms a fresh batch.
func TestTextBatchAggregatorClosesOnItsBounds(t *testing.T) {
	clock := newBatchClock(time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC))
	ag := NewTextBatchAggregator(time.Hour, 2, 0, clock.Now, clock.After)
	key := batchKey{Platform: "telegram", Conversation: "chat:1", SenderID: "sam"}

	results := make(chan collectResult, 2)
	for i := range 2 {
		go func(i int) {
			payload, mine, err := ag.Collect(context.Background(), fragment(i, "sam", fmt.Sprintf("fragment %d", i)))
			results <- collectResult{payload: payload, mine: mine, err: err}
		}(i)
		if i == 0 {
			waitForFragments(t, ag, key, 1)
		}
	}
	// Both settles happened without any clock movement: the bound, not the
	// window, closed the batch.
	waitForSettled(t, ag, key)

	var payloads []string
	for range 2 {
		out := <-results
		if out.err != nil {
			t.Errorf("Collect: %v", out.err)
			continue
		}
		if out.mine {
			payloads = append(payloads, out.payload.Message.Text)
		}
	}
	if len(payloads) != 1 || payloads[0] != "fragment 0\nfragment 1" {
		t.Errorf("the bounded batch dispatched %q from %d dispatchers, want the two fragments joined by its single dispatcher", strings.Join(payloads, "|"), len(payloads))
	}

	// A fragment arriving after the bound closed its batch waits on a fresh
	// batch window rather than being attached to nothing.
	after := make(chan collectResult, 1)
	go func() {
		payload, mine, err := ag.Collect(context.Background(), fragment(5, "sam", "after the bound"))
		after <- collectResult{payload: payload, mine: mine, err: err}
	}()
	waitForFragments(t, ag, key, 1)
	if bat := batchOf(ag, key); bat == nil || len(bat.fragments) != 1 {
		t.Error("the post-bound fragment did not start its own batch")
	}
	clock.Advance(2 * time.Hour)
	out := <-after
	if out.err != nil || !out.mine || out.payload.Message.Text != "after the bound" {
		t.Errorf("the post-bound fragment = %q, mine %v, err %v; want its own batch closed on the window", out.payload.Message.Text, out.mine, out.err)
	}
}

// TestTextBatchAggregatorSeparatesSenders pins the batch key: two senders
// in one conversation hold two open batches, and neither dispatches more
// text than its sender wrote.
func TestTextBatchAggregatorSeparatesSenders(t *testing.T) {
	clock := newBatchClock(time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC))
	ag := NewTextBatchAggregator(time.Hour, 0, 0, clock.Now, clock.After)
	keyOf := func(sender string) batchKey {
		return batchKey{Platform: "telegram", Conversation: "chat:1", SenderID: sender}
	}

	results := make(chan collectResult, 3)
	go func() {
		payload, mine, err := ag.Collect(context.Background(), fragment(1, "sam", "did the build pass"))
		if mine {
			results <- collectResult{payload: payload, err: err}
		}
	}()
	waitForFragments(t, ag, keyOf("sam"), 1)
	go func() {
		payload, mine, err := ag.Collect(context.Background(), fragment(2, "kim", "same here, checking"))
		if mine {
			results <- collectResult{payload: payload, err: err}
		}
	}()
	waitForFragments(t, ag, keyOf("kim"), 1)
	go func() {
		payload, mine, err := ag.Collect(context.Background(), fragment(3, "sam", "and the tests"))
		if mine {
			results <- collectResult{payload: payload, err: err}
		}
	}()
	waitForFragments(t, ag, keyOf("sam"), 2)
	clock.Advance(2 * time.Hour)

	var texts []string
	for range 2 {
		out := <-results
		if out.err != nil {
			t.Errorf("Collect: %v", out.err)
		}
		texts = append(texts, out.payload.Message.Text)
	}
	sam := strings.Join([]string{"did the build pass", "and the tests"}, "\n")
	for _, text := range texts {
		switch text {
		case sam, "same here, checking":
		default:
			t.Errorf("senders dispatched %q; want only %q and %q", text, sam, "same here, checking")
		}
	}
}

// TestTextBatchAggregatorHandsTheDispatchToAStillWaitingFragment pins the
// handover: the caller that opens a batch may leave before the batch
// settles, and the batch's turn must still run -- the first fragment still
// waiting inherits the dispatch.
func TestTextBatchAggregatorHandsTheDispatchToAStillWaitingFragment(t *testing.T) {
	clock := newBatchClock(time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC))
	ag := NewTextBatchAggregator(time.Hour, 0, 0, clock.Now, clock.After)
	key := batchKey{Platform: "telegram", Conversation: "chat:1", SenderID: "sam"}

	leaderCtx, cancelLeader := context.WithCancel(context.Background())
	results := make(chan collectResult, 2)
	go func() {
		payload, mine, err := ag.Collect(leaderCtx, fragment(0, "sam", "half a thought"))
		results <- collectResult{payload: payload, mine: mine, err: err}
	}()
	waitForFragments(t, ag, key, 1)
	go func() {
		payload, mine, err := ag.Collect(context.Background(), fragment(1, "sam", "the other half"))
		results <- collectResult{payload: payload, mine: mine, err: err}
	}()
	waitForFragments(t, ag, key, 2)

	cancelLeader()
	leader := <-results
	if leader.err == nil || leader.mine {
		t.Errorf("the cancelled dispatcher = mine %v, err %v; want its own error", leader.mine, leader.err)
	}

	clock.Advance(2 * time.Hour)
	heir := <-results
	if heir.err != nil || !heir.mine {
		t.Fatalf("the remaining fragment = mine %v, err %v; want it to inherit the dispatch", heir.mine, heir.err)
	}
	if heir.payload.Message.Text != "half a thought\nthe other half" {
		t.Errorf("the inherited turn dispatched %q, want the whole batch joined", heir.payload.Message.Text)
	}
}
