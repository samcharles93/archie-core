// Package secret provides a pluggable secrets-engine registry. Engines
// resolve SecretRef values (engine + key) into strings at daemon startup,
// keeping credentials out of config files and environment variables.
//
// Built-in engines (env, bws, sops) are compiled in; custom engines are
// loaded from a directory via Yaegi, mirroring internal/plugin conventions.
package secret

import (
	"fmt"
	"os"
	"sync"

	"github.com/samcharles93/archie-core/internal/yaegiutil"
)

// Engine resolves secret references into their plaintext values.
// Implementations must be safe for concurrent use.
type Engine interface {
	// Name returns a unique engine identifier (e.g. "env", "bws", "sops").
	Name() string
	// Version returns a semver string for dependency tracking.
	Version() string
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

// Resolve resolves a SecretRef through the registered engine.
func (r *Registry) Resolve(ref SecretRef) (string, error) {
	e, ok := r.Get(ref.Engine)
	if !ok {
		return "", fmt.Errorf("secret engine %q not registered", ref.Engine)
	}
	return e.Resolve(ref.Key)
}

// LoadDir loads yaegi-interpreted secret engine plugins from a directory
// of .go files. Each file must export a "main.Engine" variable satisfying
// the Engine interface. Failed plugins are skipped; successful ones are
// registered. Returns the count of loaded engines.
func (r *Registry) LoadDir(dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("secret: read dir %s: %w", dir, err)
	}

	loaded := 0
	for _, entry := range entries {
		if entry.IsDir() || !isGoFile(entry.Name()) {
			continue
		}
		path := dir + "/" + entry.Name()
		src, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		e, err := yaegiutil.Resolve[Engine](nil, string(src), "main.Engine")
		if err != nil {
			continue
		}
		r.Register(e)
		loaded++
	}
	return loaded, nil
}

// DefaultRegistry returns a singleton pre-loaded with the built-in env engine.
// Use this when you need a one-off secret resolution without threading a
// Registry through the entire call stack.
func DefaultRegistry() *Registry {
	return defaultReg
}

var defaultReg = &Registry{engines: map[string]Engine{"env": &envEngine{}}}

// NewRegistry creates a Registry pre-loaded with the built-in env engine.
func NewRegistry() *Registry {
	r := &Registry{engines: make(map[string]Engine)}
	r.Register(&envEngine{})
	return r
}

func isGoFile(name string) bool {
	return len(name) > 3 && name[len(name)-3:] == ".go"
}
