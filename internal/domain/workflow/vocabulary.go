package workflow

import (
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"sync"

	"github.com/samcharles93/archie-core/internal/domain/stableid"
)

// StepType is one named workflow step type: the vocabulary word a YAML
// workflow names, plus the factory that builds its stage.
type StepType struct {
	Name    string
	Factory StepFactory
}

// StepTypeProvider is the typed extension point through which a plugin
// contributes workflow step types. StepTypes is the capability-specific
// operation — a provider exposing identity metadata alone would be a metadata
// entry, not a step-type provider — and it is the only method the family calls.
type StepTypeProvider interface {
	// Name identifies the provider in registration diagnostics.
	Name() string
	// StepTypes returns every step type this provider contributes. The
	// manager validates the whole contribution before applying any of it.
	StepTypes() []StepType
}

// Manager is the workflow step-type family owner: it controls registration,
// validation, and resolution of the vocabulary that workflow definitions are
// parsed and compiled against.
//
// It is a constructed, injected value. There is deliberately no package-level
// registry and no init()-time registration: each process builds a Manager at
// its composition root from the same provider set
// (internal/infrastructure/workflowsteps) and injects it, so within one build
// a definition that validates on the validating side is compilable on the
// executing side. That agreement is per build: the State Store and archie-agent
// are separately deployed binaries, so one built from newer source than the
// other can still disagree, and only a matching deploy fixes that.
//
// Registration is expected to finish before the first resolution, and that
// order is a documented constraint rather than an enforced one: Registry hands
// back a copy, so a Register that arrives after a consumer resolved is not
// observed by that consumer. Register every provider before injecting the
// manager.
type Manager struct {
	mu       sync.Mutex
	registry StepRegistry
	// owners names the provider that claimed each step type, so a collision is
	// reported against the provider that is already there rather than as an
	// anonymous duplicate. The stages of the shipped workflows are claimed by
	// the provider that contributes them, which is how a contribution that
	// shadows a shipped stage is refused.
	owners    map[string]string
	providers []string
}

// NewManager builds an empty family manager. It holds no vocabulary of its own:
// the shipped stages are a provider like any other, registered by the process's
// composition root, so the vocabulary every side resolves is the one that was
// actually registered.
func NewManager() *Manager {
	return &Manager{registry: StepRegistry{}, owners: map[string]string{}}
}

// Register validates one provider's contribution and applies it. Every way a
// contribution can be invalid is refused here, at the producer: a nil or
// unnamed provider, a malformed provider or step-type identifier, a step type
// with no factory, a step type another provider already claimed (including one
// that shadows a shipped stage), and a provider declaring one type twice.
//
// Validation is atomic: the whole contribution is checked before any of it is
// applied, so a refused provider contributes nothing.
func (m *Manager) Register(provider StepTypeProvider) error {
	if isNilProvider(provider) {
		return errors.New("workflow step type provider is nil")
	}
	name := provider.Name()
	if !stableid.Valid(name) {
		return fmt.Errorf("workflow step type provider name %q is not a stable identifier", name)
	}

	contributed := provider.StepTypes()
	pending := make(StepRegistry, len(contributed))
	order := make([]string, 0, len(contributed))
	for _, stepType := range contributed {
		switch {
		case !stableid.Valid(stepType.Name):
			return fmt.Errorf("workflow step type provider %q: step type %q is not a stable identifier", name, stepType.Name)
		case stepType.Factory == nil:
			return fmt.Errorf("workflow step type provider %q: step type %q has no factory", name, stepType.Name)
		}
		if _, declared := pending[stepType.Name]; declared {
			return fmt.Errorf("workflow step type provider %q declares step type %q twice", name, stepType.Name)
		}
		pending[stepType.Name] = stepType.Factory
		order = append(order, stepType.Name)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	for _, stepType := range order {
		if owner, claimed := m.owners[stepType]; claimed {
			return fmt.Errorf("workflow step type %q is already claimed by provider %q", stepType, owner)
		}
	}
	for _, stepType := range order {
		m.registry[stepType] = pending[stepType]
		m.owners[stepType] = name
	}
	m.providers = append(m.providers, name)
	return nil
}

// Registry returns the assembled vocabulary for the parser and compiler entry
// points. It is a copy: the closed set is a property of the manager, not
// something a consumer can widen.
func (m *Manager) Registry() StepRegistry {
	m.mu.Lock()
	defer m.mu.Unlock()
	registry := make(StepRegistry, len(m.registry))
	maps.Copy(registry, m.registry)
	return registry
}

// StepTypes returns every registered step type name, sorted.
func (m *Manager) StepTypes() []string {
	registry := m.Registry()
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// Providers returns the registered provider names, sorted.
func (m *Manager) Providers() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Sorted(slices.Values(m.providers))
}

// isNilProvider catches a nil interface and a typed nil pointer alike: a
// provider cannot answer Name() in either case, so it is refused rather than
// dereferenced. Mirrors internal/domain/memory's engine check.
func isNilProvider(provider StepTypeProvider) bool {
	value := reflect.ValueOf(provider)
	switch value.Kind() {
	case reflect.Invalid:
		return true
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	}
	return false
}
