package gateway

import (
	"context"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/samcharles93/archie-core/internal/domain/messaging"
)

// The batcher's quiet window, its per-batch bounds, and its open-batch cap.
// Chat clients -- Telegram in particular -- split one thought across several
// messages sent within a second or two of each other, so a quiet window of
// 1.5s since the last fragment catches that habit; a lone message is delayed
// by exactly the window, which is the price a coalescer can never avoid
// (every inbound message is bounded away from dispatch by at most the
// window). A batch is also bounded up front: at most defaultBatchFragments
// fragments or defaultBatchMaxRunes of combined text close it before the
// window runs out, and at most defaultBatchOpenCap batches may be open at
// once -- the oldest is settled early, never dropped, when a later one
// opens. The cap bounds memory the way the deduplicator's cache does: one
// open batch is a handful of Inbound values, and settling is bounded by the
// same window.
const (
	defaultBatchWindow    = 1500 * time.Millisecond
	defaultBatchFragments = 8
	defaultBatchMaxRunes  = 4000
	defaultBatchOpenCap   = 256
)

// TextBatchAggregator coalesces rapid-fire text fragments from one sender
// into a single turn instead of running the agent loop once per fragment.
//
// Collect is the whole contract. A message that is not a fragment (media, a
// command, anything without a per-person SenderID, anything without text)
// passes through at once: those are not parts of one thought, and a /stop
// must never wait on a window. A fragment is registered under the batch key
// (platform, conversation, sender) and the call blocks until the batch
// closes -- on a quiet window since the last fragment (reset by every join),
// or on one of the size bounds. The first still-waiting fragment is the
// dispatcher: it receives the combined payload and runs the one turn, and
// every other fragment is released with mine=false, because the batch's
// single dispatch covers it. Fragments arrive in order and are joined in
// that order; the payload carries the first fragment's identity, so the
// session records one message with one channel-native source ID.
//
// The release -- not the dispatch -- happens inside Collect: a covered
// fragment's caller is free the moment its batch settles and renders
// nothing on its own behalf, while the dispatcher's caller carries the turn
// through the ordinary session machinery, which is where per-session
// ordering stays enforced. The gate order upstream is unchanged: dedup and
// the rate limit run per fragment before Collect, so a redelivered fragment
// is a declined delivery as always and never joins the batch whose original
// it repeats, and each fragment is charged its own budget.
//
// Cancellation is per call: a fragment whose context ends before the batch
// settles leaves with its own error and takes no dispatch duty; if it was
// the dispatcher, the first still-waiting fragment inherits the slot, so
// the batch never loses its one turn to a caller that gave up. A batch
// whose every caller left settles with no dispatcher at all.
type TextBatchAggregator struct {
	window       time.Duration
	maxFragments int
	maxRunes     int
	openCap      int
	// now and after inject the clock, so close times are tested without
	// sleeping; nil selects the system clock, which is the production shape.
	now   func() time.Time
	after func(time.Duration) <-chan time.Time

	mu      sync.Mutex
	batches map[batchKey]*textBatch
	// opened lists the open batches oldest-first, so eviction can settle the
	// oldest one when the open-batch cap is reached.
	opened []*textBatch
}

// batchKey identifies one conversational stream a batch coalesces: the
// channel that carried the fragments, the conversation, and the sender.
// A struct, not a concatenated string, for the same reason the
// deduplicator's key is one: no separator can ever collide.
type batchKey struct {
	Platform     string
	Conversation string
	SenderID     string
}

// coalescableText reports whether in is a fragment a batch may hold. A
// fragment is plain text from a sender with a stable per-person ID: no
// media (a photo is a turn of its own), no command (an operator's /stop is
// never delayed into a window), and text to join. An empty sender means no
// per-person stream exists to fragment -- a webhook route is an automated
// source, not someone splitting a thought.
func coalescableText(in Inbound) bool {
	text := strings.TrimSpace(in.Message.Text)
	if text == "" || strings.HasPrefix(text, "/") {
		return false
	}
	if len(in.Message.Media) != 0 || in.Message.SenderID == "" {
		return false
	}
	return in.Message.ToolCall == nil && in.Message.ToolResult == nil
}

// textBatch is one open batch: the fragments in arrival order, the quiet
// deadline the latest join set, and the release every waiting call reads
// once the batch settles. The payload and the chosen dispatcher are written
// under the aggregator's mutex before release closes, so a released caller
// reads them safely.
type textBatch struct {
	key       batchKey
	fragments []Inbound
	left      []bool
	runes     int
	deadline  time.Time

	release    chan struct{}
	wake       chan struct{}
	settled    bool
	payload    Inbound
	dispatcher int
}

// NewTextBatchAggregator returns a batcher with the given quiet window and
// bounds. Zero values select the package defaults; nil now and after select
// the real clock, which is the production shape.
func NewTextBatchAggregator(window time.Duration, maxFragments, maxRunes int, now func() time.Time, after func(time.Duration) <-chan time.Time) *TextBatchAggregator {
	if window <= 0 {
		window = defaultBatchWindow
	}
	if maxFragments <= 0 {
		maxFragments = defaultBatchFragments
	}
	if maxRunes <= 0 {
		maxRunes = defaultBatchMaxRunes
	}
	if now == nil {
		now = time.Now
	}
	if after == nil {
		after = time.After
	}
	return &TextBatchAggregator{
		window:       window,
		maxFragments: maxFragments,
		maxRunes:     maxRunes,
		openCap:      defaultBatchOpenCap,
		now:          now,
		after:        after,
		batches:      make(map[batchKey]*textBatch),
	}
}

// Collect registers a plain-text fragment in the batch for its key and
// blocks until that batch settles. It reports the payload this call is
// responsible for and whether this call must dispatch it: true for the
// batch's dispatcher (whose payload equals its own message when no fragment
// ever joined, and carries the joined text when one did), false for a
// covered fragment that must render nothing of its own. A message that is
// no fragment returns at once with its own payload. A call's own context
// cancellation takes only itself out of the batch.
func (b *TextBatchAggregator) Collect(ctx context.Context, in Inbound) (Inbound, bool, error) {
	if !coalescableText(in) {
		return in, true, nil
	}

	key := batchKey{
		Platform:     in.Platform,
		Conversation: in.Message.ConversationID.String(),
		SenderID:     in.Message.SenderID,
	}

	b.mu.Lock()
	bat := b.batches[key]
	if bat == nil {
		bat = &textBatch{
			key:        key,
			deadline:   b.now().Add(b.window),
			release:    make(chan struct{}),
			wake:       make(chan struct{}, 1),
			dispatcher: 0,
		}
		b.batches[key] = bat
		b.opened = append(b.opened, bat)
		for len(b.opened) > b.openCap {
			// Forced eviction settles the oldest batch early -- with the
			// fragments it holds, not empty-handed -- by moving its quiet
			// deadline to now; its caretaker wakes and settles it. Its key
			// stays in the map until then, so a join in flight still lands.
			oldest := b.opened[0]
			b.opened = b.opened[1:]
			oldest.deadline = b.now()
			select {
			case oldest.wake <- struct{}{}:
			default:
			}
		}
		go b.watch(bat)
	}
	index := len(bat.fragments)
	bat.fragments = append(bat.fragments, in)
	bat.left = append(bat.left, false)
	bat.runes += utf8.RuneCountInString(strings.TrimSpace(in.Message.Text))
	if len(bat.fragments) >= b.maxFragments || bat.runes >= b.maxRunes {
		bat.deadline = b.now()
	}
	b.mu.Unlock()

	select {
	case bat.wake <- struct{}{}:
	default:
	}

	select {
	case <-bat.release:
	case <-ctx.Done():
		// This fragment's caller left before the batch settled: it takes no
		// dispatch duty, and if it held the dispatcher slot, the first still-
		// waiting fragment inherits it at settle.
		b.mu.Lock()
		bat.left[index] = true
		b.mu.Unlock()
		select {
		case bat.wake <- struct{}{}:
		default:
		}
		return Inbound{}, false, ctx.Err()
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if index == bat.dispatcher {
		return bat.payload, true, nil
	}
	return messaging.Inbound{}, false, nil
}

// watch drives one batch's quiet close: it re-arms on every fresh fragment
// (each join resets the quiet deadline to now+window) and on eviction, and
// settles the moment the deadline passes.
func (b *TextBatchAggregator) watch(bat *textBatch) {
	for {
		b.mu.Lock()
		deadline := bat.deadline
		b.mu.Unlock()
		remaining := deadline.Sub(b.now())
		if remaining <= 0 {
			break
		}
		select {
		case <-b.after(remaining):
		case <-bat.wake:
		}
	}
	b.settle(bat)
}

// settle closes one batch and releases its waiters. The payload joins every
// fragment's text, in arrival order, under the first fragment's identity,
// and the dispatch duty goes to the first fragment whose call still waits;
// -1 when none does.
func (b *TextBatchAggregator) settle(bat *textBatch) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if bat.settled {
		return
	}
	bat.settled = true
	delete(b.batches, bat.key)
	for i, listed := range b.opened {
		if listed == bat {
			b.opened = append(b.opened[:i], b.opened[i+1:]...)
			break
		}
	}
	// The dispatch duty goes to the first fragment whose call still waits;
	// a batch whose every caller left settles with no dispatcher (-1).
	bat.dispatcher = -1
	for i, gone := range bat.left {
		if !gone {
			bat.dispatcher = i
			break
		}
	}
	bat.payload = combined(bat.fragments)
	close(bat.release)
}

// combined joins fragments into one turn payload: the first fragment's
// identity fields, the texts joined in arrival order.
func combined(fragments []Inbound) Inbound {
	ready := fragments[0]
	var body strings.Builder
	for i, f := range fragments {
		if i > 0 {
			body.WriteString("\n")
		}
		body.WriteString(strings.TrimSpace(f.Message.Text))
	}
	ready.Message.Text = body.String()
	return ready
}
