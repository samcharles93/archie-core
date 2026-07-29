// Package provider defines the typed tool-provider capability family.
package provider

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/samcharles93/archie-core/internal/plugin"
	"github.com/samcharles93/archie-core/internal/tools"
)

var (
	// ErrDuplicateProvider is returned when a manifest ID is already registered.
	ErrDuplicateProvider = errors.New("duplicate tool provider")
	// ErrRegistryStarted is returned when registration occurs after lifecycle start.
	ErrRegistryStarted = errors.New("tool provider registry already started")
)

const toolCapability plugin.CapabilityKind = "tools"

const cleanupTimeout = 10 * time.Second

// Engine is one lifecycle-managed source of executable tools.
type Engine interface {
	plugin.Module
	Discover(context.Context) ([]tools.ToolEntry, error)
}

type registryState uint8

const (
	stateIdle registryState = iota
	stateStarting
	stateRunning
	stateStopping
	stateStopped
	stateFailed
)

type registration struct {
	engine   Engine
	manifest plugin.Manifest
}

type runningProvider struct {
	registration registration
	toolNames    []string
}

// Registry owns tool-provider registration, lifecycle, and central indexing.
// The capability host starts and stops the Registry as one typed family module;
// the Registry, in turn, owns provider ordering and atomic tool availability.
type Registry struct {
	opMu sync.Mutex
	mu   sync.RWMutex

	index     *tools.Registry
	providers map[string]registration
	running   []runningProvider
	state     registryState
}

// NewRegistry creates a provider registry backed by the central tool index.
func NewRegistry(index *tools.Registry) *Registry {
	if index == nil {
		index = tools.NewRegistry()
	}
	return &Registry{
		index:     index,
		providers: make(map[string]registration),
	}
}

// Register adds an engine to the family.
func (r *Registry) Register(engine Engine) error {
	if isNilEngine(engine) {
		return errors.New("tool provider is nil")
	}
	manifest, err := safeManifest(engine)
	if err != nil {
		return err
	}
	if err := manifest.Validate(); err != nil {
		return fmt.Errorf("tool provider manifest: %w", err)
	}
	if !slices.Contains(manifest.Capabilities, toolCapability) {
		return fmt.Errorf("tool provider %q does not declare %q capability", manifest.ID, toolCapability)
	}
	manifest = cloneManifest(manifest)

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state != stateIdle {
		return ErrRegistryStarted
	}
	if _, exists := r.providers[manifest.ID]; exists {
		return fmt.Errorf("%w: %q", ErrDuplicateProvider, manifest.ID)
	}
	r.providers[manifest.ID] = registration{engine: engine, manifest: manifest}
	return nil
}

// Get returns a registered engine by manifest ID.
func (r *Registry) Get(id string) (Engine, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	registration, ok := r.providers[id]
	return registration.engine, ok
}

// Manifest identifies the tool-provider family to the capability host.
func (r *Registry) Manifest() plugin.Manifest {
	return plugin.Manifest{
		ID:           "archie.tools",
		Name:         "Tool providers",
		Version:      "1.0.0",
		APIVersion:   plugin.HostAPIVersion,
		Capabilities: []plugin.CapabilityKind{toolCapability},
	}
}

// Start starts and discovers registered providers in dependency order. A
// provider's tools become visible as one atomic batch only after Start and
// Discover both succeed. Any failure rolls the complete family back.
func (r *Registry) Start(ctx context.Context) error {
	r.opMu.Lock()
	defer r.opMu.Unlock()

	r.mu.Lock()
	switch r.state {
	case stateRunning:
		r.mu.Unlock()
		return nil
	case stateIdle:
		r.state = stateStarting
	default:
		state := r.state
		r.mu.Unlock()
		return fmt.Errorf("tool provider registry cannot start from state %d", state)
	}
	providers := cloneRegistrations(r.providers)
	r.mu.Unlock()

	order, err := dependencyOrder(providers)
	if err != nil {
		r.setFailed(nil)
		return err
	}

	started := make([]runningProvider, 0, len(order))
	for _, id := range order {
		reg := providers[id]
		rp, err := r.startProvider(ctx, reg, started)
		if err != nil {
			return err
		}
		started = append(started, rp)
	}

	r.mu.Lock()
	r.running = started
	r.state = stateRunning
	r.mu.Unlock()
	return nil
}

// Health reports aggregate provider health.
func (r *Registry) Health(ctx context.Context) plugin.Health {
	r.mu.RLock()
	state := r.state
	running := append([]runningProvider(nil), r.running...)
	r.mu.RUnlock()

	switch state {
	case stateFailed:
		return plugin.Health{Status: plugin.HealthUnhealthy, Message: "tool-provider registry failed"}
	case stateRunning:
	default:
		return plugin.Health{Status: plugin.HealthUnknown, Message: "tool-provider registry is not running"}
	}

	aggregate := plugin.Health{Status: plugin.HealthHealthy}
	var messages []string
	for _, provider := range running {
		health := safeHealth(ctx, provider.registration.engine)
		switch health.Status {
		case plugin.HealthHealthy:
		case plugin.HealthDegraded:
			if aggregate.Status == plugin.HealthHealthy {
				aggregate.Status = plugin.HealthDegraded
			}
		case plugin.HealthUnhealthy:
			aggregate.Status = plugin.HealthUnhealthy
		default:
			aggregate.Status = plugin.HealthUnhealthy
			health.Message = fmt.Sprintf("invalid health status %q", health.Status)
		}
		if health.Message != "" {
			messages = append(messages, provider.registration.manifest.ID+": "+health.Message)
		}
	}
	aggregate.Message = strings.Join(messages, "; ")
	return aggregate
}

// Stop removes all provider-owned tools before stopping providers in reverse
// startup order. It continues after provider failures and joins their errors.
func (r *Registry) Stop(ctx context.Context) error {
	r.opMu.Lock()
	defer r.opMu.Unlock()

	r.mu.Lock()
	switch r.state {
	case stateStopped:
		r.mu.Unlock()
		return nil
	case stateRunning:
		r.state = stateStopping
	case stateFailed:
		if len(r.running) == 0 {
			r.state = stateStopped
			r.mu.Unlock()
			return nil
		}
		r.state = stateStopping
	case stateIdle:
		r.running = nil
		r.state = stateStopped
		r.mu.Unlock()
		return nil
	default:
		state := r.state
		r.mu.Unlock()
		return fmt.Errorf("tool provider registry cannot stop from state %d", state)
	}
	running := append([]runningProvider(nil), r.running...)
	r.mu.Unlock()

	var errs []error
	var failed []runningProvider
	for i := len(running) - 1; i >= 0; i-- {
		current := running[i]
		r.index.Unregister(current.toolNames...)
		if err := safeStop(ctx, current.registration.engine); err != nil {
			failed = append(failed, current)
			errs = append(errs, fmt.Errorf(
				"stop tool provider %q: %w",
				current.registration.manifest.ID,
				err,
			))
		}
	}

	r.mu.Lock()
	r.running = reverseRunning(failed)
	if len(failed) == 0 {
		r.state = stateStopped
	} else {
		r.state = stateFailed
	}
	r.mu.Unlock()
	return errors.Join(errs...)
}

func (r *Registry) setFailed(running []runningProvider) {
	r.mu.Lock()
	r.running = append([]runningProvider(nil), running...)
	r.state = stateFailed
	r.mu.Unlock()
}

// startProvider starts, discovers, and indexes one provider. On failure
// it rolls back previously-started providers and returns a joined error.
func (r *Registry) startProvider(ctx context.Context, reg registration, started []runningProvider) (runningProvider, error) {
	id := reg.manifest.ID
	if err := safeStart(ctx, reg.engine); err != nil {
		return runningProvider{}, r.failProvider(ctx, id, reg, started,
			fmt.Errorf("start tool provider %q: %w", id, err))
	}
	entries, err := safeDiscover(ctx, reg.engine)
	if err != nil {
		return runningProvider{}, r.failProvider(ctx, id, reg, started,
			fmt.Errorf("discover tools from provider %q: %w", id, err))
	}
	if err := r.index.RegisterBatch(entries); err != nil {
		return runningProvider{}, r.failProvider(ctx, id, reg, started,
			fmt.Errorf("index tools from provider %q: %w", id, err))
	}
	return runningProvider{
		registration: reg,
		toolNames:    namesOf(entries),
	}, nil
}

// failProvider stops the failing provider, rolls back previously-started
// providers, and returns a joined error.
func (r *Registry) failProvider(ctx context.Context, id string, reg registration, started []runningProvider, cause error) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()
	cleanupErr := safeStop(cleanupCtx, reg.engine)
	rollbackFailed, rollbackErr := rollback(cleanupCtx, r.index, started)
	if cleanupErr != nil {
		rollbackFailed = append(rollbackFailed, runningProvider{registration: reg})
	}
	r.setFailed(rollbackFailed)
	return errors.Join(cause, wrapCleanupError(id, cleanupErr), rollbackErr)
}

func cloneRegistrations(source map[string]registration) map[string]registration {
	clone := make(map[string]registration, len(source))
	for id, value := range source {
		value.manifest = cloneManifest(value.manifest)
		clone[id] = value
	}
	return clone
}

func dependencyOrder(providers map[string]registration) ([]string, error) {
	ids := make([]string, 0, len(providers))
	for id := range providers {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	indegree := make(map[string]int, len(providers))
	dependents := make(map[string][]string, len(providers))
	for _, id := range ids {
		for _, dependency := range providers[id].manifest.Dependencies {
			if _, exists := providers[dependency]; !exists {
				return nil, fmt.Errorf("tool provider %q depends on missing provider %q", id, dependency)
			}
			indegree[id]++
			dependents[dependency] = append(dependents[dependency], id)
		}
	}

	var ready []string
	for _, id := range ids {
		if indegree[id] == 0 {
			ready = append(ready, id)
		}
	}
	var order []string
	for len(ready) > 0 {
		id := ready[0]
		ready = ready[1:]
		order = append(order, id)
		slices.Sort(dependents[id])
		for _, dependent := range dependents[id] {
			indegree[dependent]--
			if indegree[dependent] == 0 {
				ready = append(ready, dependent)
				slices.Sort(ready)
			}
		}
	}
	if len(order) != len(providers) {
		return nil, errors.New("tool provider dependency cycle")
	}
	return order, nil
}

func rollback(
	ctx context.Context,
	index *tools.Registry,
	started []runningProvider,
) ([]runningProvider, error) {
	var errs []error
	var failed []runningProvider
	for i := len(started) - 1; i >= 0; i-- {
		current := started[i]
		index.Unregister(current.toolNames...)
		if err := safeStop(ctx, current.registration.engine); err != nil {
			failed = append(failed, current)
			errs = append(errs, fmt.Errorf(
				"rollback tool provider %q: %w",
				current.registration.manifest.ID,
				err,
			))
		}
	}
	return reverseRunning(failed), errors.Join(errs...)
}

func reverseRunning(values []runningProvider) []runningProvider {
	out := append([]runningProvider(nil), values...)
	slices.Reverse(out)
	return out
}

func wrapCleanupError(id string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("clean up failed tool provider %q: %w", id, err)
}

func namesOf(entries []tools.ToolEntry) []string {
	names := make([]string, len(entries))
	for i, entry := range entries {
		names[i] = entry.Name
	}
	return names
}

func cloneManifest(manifest plugin.Manifest) plugin.Manifest {
	clone := manifest
	clone.Capabilities = append([]plugin.CapabilityKind(nil), manifest.Capabilities...)
	clone.Dependencies = append([]string(nil), manifest.Dependencies...)
	clone.Permissions = append([]plugin.Permission(nil), manifest.Permissions...)
	clone.ConfigSchema = append([]byte(nil), manifest.ConfigSchema...)
	return clone
}

func isNilEngine(engine Engine) bool {
	if engine == nil {
		return true
	}
	value := reflect.ValueOf(engine)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func safeManifest(engine Engine) (manifest plugin.Manifest, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("tool provider manifest panic: %v", recovered)
		}
	}()
	return engine.Manifest(), nil
}

func safeStart(ctx context.Context, engine Engine) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("panic: %v", recovered)
		}
	}()
	return engine.Start(ctx)
}

func safeDiscover(ctx context.Context, engine Engine) (entries []tools.ToolEntry, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("panic: %v", recovered)
		}
	}()
	return engine.Discover(ctx)
}

func safeHealth(ctx context.Context, engine Engine) (health plugin.Health) {
	defer func() {
		if recovered := recover(); recovered != nil {
			health = plugin.Health{
				Status:  plugin.HealthUnhealthy,
				Message: fmt.Sprintf("health panic: %v", recovered),
			}
		}
	}()
	return engine.Health(ctx)
}

func safeStop(ctx context.Context, engine Engine) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("panic: %v", recovered)
		}
	}()
	return engine.Stop(ctx)
}
