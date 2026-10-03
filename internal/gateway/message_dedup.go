package gateway

import (
	"slices"
	"sync"
	"time"
)

// Dedup window and capacity.
const (
	defaultDedupTTL      = 5 * time.Minute
	defaultDedupCapacity = 1024
)

// dedupKey identifies one delivery: platform, conversation and
// channel-native message ID.
type dedupKey struct {
	Platform     string
	Conversation string
	SourceID     string
}

// MessageDeduplicator rejects a repeat delivery of the same platform message
// within a TTL of the first. It holds at most capacity entries, evicting
// expired entries first and then the oldest. Safe for concurrent use.
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

// Admit records the delivery and reports whether it may be processed. A
// message with no SourceID is always admitted.
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
const dedupReply = "I already received this message, so I'm ignoring this repeat delivery."
