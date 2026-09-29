package gateway

import (
	"slices"
	"sync"
	"time"
)

// The deduplicator's delivery window and memory bound. A redelivery
// (webhook retry, Telegram long-poll re-fetch after a slow ack, forge
// redelivery) reaches the gateway within seconds to a few minutes of the
// first delivery, so five minutes clears the fast-path gate without ever
// colliding with the durable prior-reply replay in turn.go, which remains
// the backstop for a duplicate answered long before or long after the
// window. Each entry is one key and a timestamp, so 1024 entries stay
// comfortably under a megabyte.
const (
	defaultDedupTTL      = 5 * time.Minute
	defaultDedupCapacity = 1024
)

// dedupKey identifies one platform message delivery: the channel that
// carried it, the conversation it addressed, and its channel-native
// message ID -- the same tuple a repeat delivery is keyed on. Two
// components are not enough: Telegram message IDs are only unique per
// chat, and different platforms reuse the same numeric ID ranges.
type dedupKey struct {
	Platform     string
	Conversation string
	SourceID     string
}

// MessageDeduplicator rejects a second delivery of the same platform
// message within a TTL window, so at-least-once hand-off (webhook
// retries, Telegram long-poll re-fetch, forge redelivery) does not
// process one message twice -- a duplicate still inside the window never
// reaches the turn pipeline at all.
//
// Memory is bounded by design, not by chance: at most capacity entries
// are held. An insert brings the cache back under the bound by evicting
// expired entries first (oldest recorded delivery first, exactly the
// entries a TTL sweep would drop), then the oldest still-live delivery
// once expired entries alone no longer make room. A rejected repeat
// never extends the window: expiry is judged from the first delivery.
//
// One Router may be reached from several channel goroutines at once, so
// every method holds a mutex; Admit's test-and-record is one atomic
// step, which is what makes a concurrent duplicate see the race and
// lose it.
//
// Admit runs before the turn, so a message whose first processing fails
// stays inside the window until it expires; redeliveries of failed work
// wait out the TTL, and turn.go's prior-reply path covers anything
// that was actually completed in the meantime.
type MessageDeduplicator struct {
	mu sync.Mutex
	// ttl is the delivery window; cap is the live-entry bound.
	ttl time.Duration
	cap int
	// now injects the clock, so expiry is testable without sleeping.
	now func() time.Time
	// entries maps each live key to when it was delivered; order is the
	// FIFO eviction queue, oldest delivery first. A live entry always has
	// at least one queue slot; a queue slot may outlive its entry after
	// an expiry re-arm and is dropped on the next sweep.
	entries map[dedupKey]time.Time
	order   []dedupKey
}

// NewMessageDeduplicator returns a gate with the given TTL window and
// entry bound. Zero values select the package defaults; now injects the
// clock, so tests judge expiry without sleeping. A nil now means
// time.Now, which is the production shape.
func NewMessageDeduplicator(ttl time.Duration, capacity int, now func() time.Time) *MessageDeduplicator {
	if ttl <= 0 {
		ttl = defaultDedupTTL
	}
	if capacity <= 0 {
		capacity = defaultDedupCapacity
	}
	if now == nil {
		now = time.Now
	}
	return &MessageDeduplicator{
		ttl:     ttl,
		cap:     capacity,
		now:     now,
		entries: make(map[dedupKey]time.Time),
	}
}

// Admit records this delivery and reports whether the message may be
// processed: true for the first delivery inside the window (or the first
// after an earlier one expired), false for a repeat that is already
// inside the window. A message with no channel-native ID (its SourceID
// is empty -- gateway-local commands, most dashboard turns) has no key to
// deduplicate against and is always admitted.
func (d *MessageDeduplicator) Admit(in Inbound) bool {
	if in.Message.SourceID == "" {
		return true
	}
	key := dedupKey{
		Platform:     in.Platform,
		Conversation: in.Message.ConversationID.String(),
		SourceID:     in.Message.SourceID,
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	at := d.now()
	if seen, live := d.entries[key]; live {
		if at.Sub(seen) < d.ttl {
			return false
		}
		// Expired: re-arm from the fresh delivery time by dropping the
		// stale queue slots for the key, then recording anew below.
		d.order = slices.DeleteFunc(d.order, func(k dedupKey) bool { return k == key })
		delete(d.entries, key)
	}
	d.evictLocked(at)
	d.entries[key] = at
	d.order = append(d.order, key)
	return true
}

// evictLocked brings the cache under its bound ahead of one insert: it
// sweeps every expired entry oldest-first, then evicts the oldest
// still-live delivery. len(order) always covers every live entry, so the
// live-entry loop below has a queue slot to evict from.
func (d *MessageDeduplicator) evictLocked(at time.Time) {
	d.order = slices.DeleteFunc(d.order, func(k dedupKey) bool {
		seen, live := d.entries[k]
		if !live {
			return true
		}
		if at.Sub(seen) >= d.ttl {
			delete(d.entries, k)
			return true
		}
		return false
	})
	for len(d.entries) >= d.cap {
		key := d.order[0]
		d.order = d.order[1:]
		delete(d.entries, key)
	}
}

// dedupReply is returned to a sender whose message arrived again inside
// the dedup window. Like the rate-limit reply it is prose addressed to a
// human reader of the chat; a webhook caller should treat any successful,
// non-original reply the same way it treats rate-limit prose (see
// archie-core-1173).
const dedupReply = "I already received this message, so I'm ignoring this repeat delivery."
