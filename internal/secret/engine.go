// Package secret provides a pluggable secrets-engine registry. Engines
// resolve SecretRef values (engine + key) into strings at daemon startup,
// keeping credentials out of config files and environment variables.
//
// Only the "env" engine is compiled in (see env.go). Other backends
// (sops, Bitwarden Secrets Manager, Vault/OpenBao) ship as Yaegi-loaded
// .go plugins under a configurable directory via Registry.LoadDir,
// mirroring internal/plugin conventions  --  they are not hard-coded
// into the binary.
package secret

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"

	"github.com/traefik/yaegi/interp"

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
	//
	// Caution for callers: a Yaegi-loaded engine whose exported value
	// omits the Resolve function resolves every key to ("", nil) rather
	// than failing (see secretextract's generated wrapper nil-guards) --
	// check for an empty string in addition to a non-nil error before
	// trusting a resolved secret.
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

// LoadDir discovers and evaluates .go files in dir. Each file must export
// a variable named "Engine" that implements the Engine interface. Failed
// engines are logged and skipped  --  the daemon starts with the remaining
// set, matching internal/plugin.LoadDir's degrade-gracefully behavior.
// Returns the count of newly registered engines.
//
// extraSymbols are additional Yaegi symbol tables made available to
// interpreted code. Callers should pass secretextract.Symbols so that
// interpreted types can satisfy the secret.Engine interface across the
// Yaegi/Go boundary.
func (r *Registry) LoadDir(dir string, extraSymbols ...map[string]map[string]reflect.Value) (int, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("secret: read dir %s: %w", dir, err)
	}

	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	loaded := 0
	for _, name := range names {
		path := filepath.Join(dir, name)
		src, err := os.ReadFile(path)
		if err != nil {
			slog.Default().Warn("skipping unreadable secret engine", "file", name, "err", err)
			continue
		}
		// Each file is package main  --  must use a fresh interpreter to
		// avoid symbol collisions between files.
		i, err := yaegiutil.New(interp.Options{}, extraSymbols...)
		if err != nil {
			slog.Default().Warn("skipping secret engine  --  interpreter setup failed", "file", name, "err", err)
			continue
		}
		e, err := yaegiutil.Resolve[Engine](i, string(src), "main.Engine")
		if err != nil {
			slog.Default().Warn("skipping secret engine", "file", name, "err", err)
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
