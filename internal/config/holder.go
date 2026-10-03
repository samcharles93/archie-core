package config

import "sync"

// Holder publishes a Config to concurrent readers; Set swaps it atomically.
// A published Config must not be mutated: Get's copy shares maps and slices
// with it. Take one Get per operation. Construct with NewHolder.
type Holder struct {
	mu  sync.RWMutex
	cfg Config
}

// NewHolder returns a Holder preloaded with c.
func NewHolder(c Config) *Holder {
	return &Holder{cfg: c}
}

// Get returns the published Config. Its maps and slices must not be mutated.
// Panics on a nil Holder.
func (h *Holder) Get() Config {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.cfg
}

// Set replaces the published Config with c. The previous snapshot is
// dropped; c becomes the new published value and its reference-type
// fields become shared with all subsequent Get callers. Set panics on
// a nil receiver.
func (h *Holder) Set(c Config) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cfg = c
}
