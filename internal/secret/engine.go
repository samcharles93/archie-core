// Package secret provides a pluggable secrets-engine registry. Engines
// resolve SecretRef values (engine + key) into strings, keeping credentials
// out of config files and serialized worker contracts.
//
// Only the "env" engine is compiled in. Every other backend is an extension
// served over the secretengine.v1 gRPC surface and registered here while it
// is enabled.
package secret

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
)

// Engine resolves secret references into their plaintext values.
// Implementations must be safe for concurrent use.
type Engine interface {
	// Name returns a unique engine identifier (e.g. "env", "bws", "sops").
	Name() string
	// Resolve looks up a secret by key and returns its value. Returns an
	// error when the key is unknown or the backend is unreachable.
	Resolve(key string) (string, error)
}

// Registry holds all loaded secret engines. The zero value is usable.
type Registry struct {
	mu      sync.RWMutex
	engines map[string]Engine // name → engine
}

// Register adds an engine. If an engine with the same name already
// exists, it is replaced (last-write-wins for overrides).
func (r *Registry) Register(e Engine) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.engines == nil {
		r.engines = make(map[string]Engine)
	}
	r.engines[e.Name()] = e
}

// Get returns the engine with the given name, or false when not found.
func (r *Registry) Get(name string) (Engine, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.engines[name]
	return e, ok
}

// Getenv resolves an environment-shaped secret name. The real process
// environment wins; registered external engines are tried in stable name
// order. A resolved value is exported so SDKs and child processes that accept
// only environment-variable credentials observe the same secret.
func (r *Registry) Getenv(key string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	r.mu.RLock()
	names := make([]string, 0, len(r.engines))
	for name := range r.engines {
		if name != "env" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	engines := make([]Engine, 0, len(names))
	for _, name := range names {
		engines = append(engines, r.engines[name])
	}
	r.mu.RUnlock()
	for _, engine := range engines {
		value, err := engine.Resolve(key)
		value = strings.TrimSpace(value)
		if err != nil || value == "" {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return ""
		}
		return value
	}
	return ""
}

// Resolve resolves a SecretRef through the registered engine. The zero
// value (engine and key both empty) resolves to "" with no error -- the
// zero value means "not configured".
func (r *Registry) Resolve(ref SecretRef) (string, error) {
	if ref.Engine == "" && ref.Key == "" {
		return "", nil
	}
	e, ok := r.Get(ref.Engine)
	if !ok {
		return "", fmt.Errorf("secret engine %q not registered", ref.Engine)
	}
	return e.Resolve(ref.Key)
}

// Unregister removes the named engine. Removing one that is not registered is
// not an error.
func (r *Registry) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.engines, name)
}

// NewRegistry creates a Registry holding the built-in env engine.
func NewRegistry() *Registry {
	r := &Registry{engines: make(map[string]Engine)}
	r.Register(&envEngine{})
	return r
}
