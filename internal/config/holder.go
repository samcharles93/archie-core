package config

import "sync"

// Holder publishes a Config snapshot to concurrent readers and lets a
// single writer swap the snapshot atomically.
//
// # Immutability invariant
//
// A published Config is immutable. The only way to update the published
// value is to call Set with a freshly constructed Config; in-place
// mutation of a published Config is a bug. The reason this matters: Get
// returns a value copy, but the copy shares its reference-type fields
// (Models, Providers, Repos, Identities, MCPServers, Extra, etc.) with
// the published snapshot. Mutating a map or slice through that copy
// mutates the published snapshot too, and any other reader sees the
// mutation outside the protection of the read lock.
//
// Reload paths (Loader.Reload, the file watcher, the UI PATCH handler)
// construct a fresh Config and call Set; they never mutate an existing
// one. Decode paths must finish constructing the new Config before Set
// is called; the existing decode.go:80 cfg.Extra[name] = value is fine
// because it runs pre-publish.
//
// # Convention
//
// Call Get once per logical operation and bind the result to a local:
//
//	cfg := d.Cfg.Get()
//	interval := cfg.PollInterval.Std()
//
// Inline accessors like d.Cfg.Get().Foo are a code smell: they may
// torn-read across a Set if a reload lands between two consecutive
// calls. With a single Get, every value in the local cfg came from
// the same published snapshot.
//
// # Why a pointer
//
// The zero value of Holder is not usable (the mutex is uninitialised);
// construct one with [NewHolder]. Holder is always passed by pointer
// because the embedded sync.RWMutex is itself a non-copyable type, and
// copying a Daemon that embeds a value Holder triggers go vet copylocks.
type Holder struct {
	mu  sync.RWMutex
	cfg Config
}

// NewHolder returns a Holder preloaded with c.
func NewHolder(c Config) *Holder {
	return &Holder{cfg: c}
}

// Get returns the currently published Config. The returned value is a
// shallow copy; the reference-type fields inside it are shared with the
// published snapshot and must not be mutated. See the type doc comment.
func (h *Holder) Get() Config {
	if h == nil {
		return Config{}
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.cfg
}

// Set replaces the published Config with c. The previous snapshot is
// dropped; c becomes the new published value and its reference-type
// fields become shared with all subsequent Get callers.
func (h *Holder) Set(c Config) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cfg = c
}
