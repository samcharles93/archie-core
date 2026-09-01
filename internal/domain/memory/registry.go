package memory

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sync"
)

// Registry errors.
var (
	// ErrDuplicate is returned when an engine name is already registered.
	ErrDuplicate = errors.New("duplicate memory engine")
	// ErrStarted is returned when registration or lifecycle is attempted
	// after the registry has started.
	ErrStarted = errors.New("memory registry already started")
)

type registryState uint8

const (
	stateIdle registryState = iota
	stateRunning
	stateStopped
)

type engineStatus struct {
	started  bool
	startErr error
}

// Registry is the memory family owner: it controls registration and
// discovery, lifecycle (start/health/stop), shutdown ordering, and failure
// isolation. An engine failure — a rejected registration, a failing Start, a
// blocking Stop, even a panicking Bind — affects only that engine and is
// reported, never fatal to the daemon or to other engines.
type Registry struct {
	mu      sync.Mutex
	host    Registrar
	engines map[string]MemoryEngine
	status  map[string]engineStatus
	order   []string
	state   registryState
}

// NewRegistry builds the family registry. A nil Clock is replaced with the
// system clock.
func NewRegistry(host Registrar) *Registry {
	if host.Clock == nil {
		host.Clock = systemClock{}
	}
	return &Registry{
		host:    host,
		engines: make(map[string]MemoryEngine),
		status:  make(map[string]engineStatus),
	}
}

// Register validates the engine's declared shape and adds it, binding the
// engine to the registrar. Registration fails on nil engines, invalid
// manifests, duplicate names, or a panicking Bind — and a failure affects
// only that engine.
func (r *Registry) Register(e MemoryEngine) error {
	if isNilEngine(e) {
		return errors.New("memory: nil engine")
	}
	if err := e.Manifest().Validate(); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state != stateIdle {
		return ErrStarted
	}
	if _, dup := r.engines[e.Name()]; dup {
		return fmt.Errorf("%w: %s", ErrDuplicate, e.Name())
	}
	if err := bindSafely(e, r.host); err != nil {
		return err
	}
	r.engines[e.Name()] = e
	r.order = append(r.order, e.Name())
	return nil
}

// bindSafely isolates a panicking Bind: an engine that panics while
// receiving its host access is refused without taking the registry down.
func bindSafely(e MemoryEngine, host Registrar) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("memory engine %s: bind panic: %v", e.Name(), p)
		}
	}()
	e.Bind(host)
	return nil
}

// Get returns the registered engine by name.
func (r *Registry) Get(name string) (MemoryEngine, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.engines[name]
	return e, ok
}

// Names returns the registered engine names in sorted order.
func (r *Registry) Names() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	names := make([]string, 0, len(r.engines))
	for name := range r.engines {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// Start starts every registered engine in registration order. An engine
// whose Start fails (error or panic) is marked unhealthy and skipped; the
// rest start regardless, and all failures are joined into the returned
// error. This is the family's failure-isolation boundary for lifecycle.
func (r *Registry) Start(ctx context.Context) error {
	r.mu.Lock()
	if r.state != stateIdle {
		r.mu.Unlock()
		return ErrStarted
	}
	r.state = stateRunning
	order := slices.Clone(r.order)
	r.mu.Unlock()

	var errs []error
	for _, name := range order {
		e := r.engines[name]
		err := startSafely(ctx, e)
		r.mu.Lock()
		if err != nil {
			r.status[name] = engineStatus{startErr: err}
			errs = append(errs, fmt.Errorf("memory engine %s: %w", name, err))
		} else {
			r.status[name] = engineStatus{started: true}
		}
		r.mu.Unlock()
	}
	return errors.Join(errs...)
}

func startSafely(ctx context.Context, e MemoryEngine) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("start panic: %v", p)
		}
	}()
	return e.Start(ctx)
}

// Health reports every registered engine's health. An engine whose Start
// failed is reported unhealthy with the failure; otherwise the engine's own
// Health is used. A panicking Health is reported unhealthy, not propagated.
func (r *Registry) Health(ctx context.Context) map[string]Health {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]Health, len(r.engines))
	for name, e := range r.engines {
		if st, ok := r.status[name]; ok && st.startErr != nil {
			out[name] = Health{Status: HealthUnhealthy, Message: st.startErr.Error()}
			continue
		}
		out[name] = healthSafely(ctx, e)
	}
	return out
}

func healthSafely(ctx context.Context, e MemoryEngine) (h Health) {
	defer func() {
		if p := recover(); p != nil {
			h = Health{Status: HealthUnhealthy, Message: fmt.Sprintf("health panic: %v", p)}
		}
	}()
	return e.Health(ctx)
}

// Stop stops every started engine in reverse registration order. Engines
// whose Start failed are skipped; the call is bounded by ctx — an engine
// that ignores cancellation cannot hang the daemon's shutdown beyond the
// caller's deadline, and its error is joined into the result. Stopping a
// registry that never started is a no-op.
func (r *Registry) Stop(ctx context.Context) error {
	r.mu.Lock()
	if r.state == stateIdle || r.state == stateStopped {
		r.mu.Unlock()
		return nil
	}
	r.state = stateStopped
	order := slices.Clone(r.order)
	r.mu.Unlock()

	var errs []error
	for _, name := range slices.Backward(order) {
		r.mu.Lock()
		st, ok := r.status[name]
		r.mu.Unlock()
		if !ok || !st.started {
			continue
		}
		e := r.engines[name]
		if err := stopSafely(ctx, e); err != nil {
			errs = append(errs, fmt.Errorf("memory engine %s: %w", name, err))
		}
	}
	return errors.Join(errs...)
}

func stopSafely(ctx context.Context, e MemoryEngine) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("stop panic: %v", p)
		}
	}()
	return e.Stop(ctx)
}

func isNilEngine(e MemoryEngine) bool {
	v := reflect.ValueOf(e)
	switch v.Kind() {
	case reflect.Invalid:
		return true
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	}
	return false
}
