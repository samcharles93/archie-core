package image

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"

	"github.com/samcharles93/archie-core/internal/domain/lifecycle"
)

// ErrDuplicate is returned when a provider name is already registered.
var ErrDuplicate = errors.New("duplicate image provider")

// Registry is the image family owner: registration and discovery only. A
// Provider is call-scoped (see package doc), so unlike curator's and
// memory's registries there is no Start/Health/Stop to orchestrate here.
type Registry struct {
	mu        sync.RWMutex
	providers map[string]Provider
}

// NewRegistry builds an empty provider registry.
func NewRegistry() *Registry {
	return &Registry{providers: make(map[string]Provider)}
}

// Register validates the provider's declared capability and adds it.
// Registration fails on a nil provider, an invalid Capability, or a
// duplicate name.
func (r *Registry) Register(p Provider) error {
	if lifecycle.IsNil(p) {
		return errors.New("image: nil provider")
	}
	if err := p.Capability().Validate(); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.providers[p.Name()]; dup {
		return fmt.Errorf("%w: %s", ErrDuplicate, p.Name())
	}
	r.providers[p.Name()] = p
	return nil
}

// Get returns the registered provider by name.
func (r *Registry) Get(name string) (Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[name]
	return p, ok
}

// Names returns every registered provider name, sorted.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return slices.Sorted(maps.Keys(r.providers))
}

// ByClass returns the sorted names of registered providers matching class.
func (r *Registry) ByClass(class ProviderClass) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var names []string
	for name, p := range r.providers {
		if p.Class() == class {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}
