// Package lifecycle is the registration and start/health/stop machinery
// shared by engine families such as curators and memory engines.
package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"reflect"
	"slices"
	"sync"
)

// HealthStatus is an engine's point-in-time health.
type HealthStatus string

const (
	HealthHealthy   HealthStatus = "healthy"
	HealthDegraded  HealthStatus = "degraded"
	HealthUnhealthy HealthStatus = "unhealthy"
)

// Health is a point-in-time engine health report.
type Health struct {
	Status  HealthStatus
	Message string
}

// Registry errors.
var (
	// ErrDuplicate is returned when an engine name is already registered.
	ErrDuplicate = errors.New("duplicate engine")
	// ErrStarted is returned when registration or lifecycle is attempted
	// after the registry has started.
	ErrStarted = errors.New("registry already started")
)

// Engine is a named, host-bound component with a lifecycle.
type Engine[H any] interface {
	Name() string
	Bind(host H)
	Start(ctx context.Context) error
	Health(ctx context.Context) Health
	Stop(ctx context.Context) error
}

type state uint8

const (
	stateIdle state = iota
	stateRunning
	stateStopped
)

type status struct {
	started  bool
	startErr error
}

// Registry owns registration and lifecycle for one engine family. An
// engine's failure, including a panic, affects only that engine.
type Registry[E Engine[H], H any] struct {
	kind    string
	log     *slog.Logger
	mu      sync.Mutex
	engines map[string]E
	status  map[string]status
	order   []string
	state   state
}

// New builds an empty registry. kind prefixes errors and log lines; a nil
// log disables lifecycle logging.
func New[E Engine[H], H any](kind string, log *slog.Logger) *Registry[E, H] {
	return &Registry[E, H]{
		kind:    kind,
		log:     log,
		engines: make(map[string]E),
		status:  make(map[string]status),
	}
}

// IsNil reports whether v is nil or an interface holding a nil value.
func IsNil(v any) bool {
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Invalid:
		return true
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return rv.IsNil()
	}
	return false
}

// Add binds e to host and registers it. A panicking Bind refuses the engine.
func (r *Registry[E, H]) Add(e E, host H) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state != stateIdle {
		return fmt.Errorf("%s: %w", r.kind, ErrStarted)
	}
	name := e.Name()
	if _, dup := r.engines[name]; dup {
		return fmt.Errorf("%w: %s %s", ErrDuplicate, r.kind, name)
	}
	if err := guard(func() error { e.Bind(host); return nil }); err != nil {
		return fmt.Errorf("%s %s: bind %w", r.kind, name, err)
	}
	r.engines[name] = e
	r.order = append(r.order, name)
	return nil
}

// Get returns the registered engine by name.
func (r *Registry[E, H]) Get(name string) (E, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.engines[name]
	return e, ok
}

// Names returns the registered engine names in sorted order.
func (r *Registry[E, H]) Names() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Sorted(maps.Keys(r.engines))
}

// Start starts every engine in registration order. An engine whose Start
// fails is marked unhealthy and skipped; the rest start regardless, and all
// failures are joined into the returned error. A stopped registry may be
// started again.
func (r *Registry[E, H]) Start(ctx context.Context) error {
	r.mu.Lock()
	if r.state == stateRunning {
		r.mu.Unlock()
		return fmt.Errorf("%s: %w", r.kind, ErrStarted)
	}
	r.state = stateRunning
	order := slices.Clone(r.order)
	r.mu.Unlock()

	var errs []error
	for _, name := range order {
		e, _ := r.Get(name)
		err := guard(func() error { return e.Start(ctx) })
		r.mu.Lock()
		if err != nil {
			r.status[name] = status{startErr: err}
			errs = append(errs, r.fail(name, "start", err))
		} else {
			r.status[name] = status{started: true}
		}
		r.mu.Unlock()
	}
	return errors.Join(errs...)
}

// Health reports every engine's health. An engine whose Start failed, or
// whose Health panics, is reported unhealthy.
func (r *Registry[E, H]) Health(ctx context.Context) map[string]Health {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]Health, len(r.engines))
	for name, e := range r.engines {
		if st := r.status[name]; st.startErr != nil {
			out[name] = Health{Status: HealthUnhealthy, Message: st.startErr.Error()}
			continue
		}
		out[name] = healthSafely(ctx, e)
	}
	return out
}

// Stop stops started engines in reverse order, bounded by ctx, and joins
// their errors.
func (r *Registry[E, H]) Stop(ctx context.Context) error {
	r.mu.Lock()
	if r.state != stateRunning {
		r.mu.Unlock()
		return nil
	}
	r.state = stateStopped
	order := slices.Clone(r.order)
	r.mu.Unlock()

	var errs []error
	for _, name := range slices.Backward(order) {
		r.mu.Lock()
		e, st := r.engines[name], r.status[name]
		r.mu.Unlock()
		if !st.started {
			continue
		}
		if err := guard(func() error { return e.Stop(ctx) }); err != nil {
			errs = append(errs, r.fail(name, "stop", err))
		}
	}
	return errors.Join(errs...)
}

// Replace removes the engines drop selects and appends every engine from
// src, keeping src's order. Call it only while stopped.
func (r *Registry[E, H]) Replace(drop func(E) bool, src *Registry[E, H]) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.order = slices.DeleteFunc(r.order, func(name string) bool {
		if !drop(r.engines[name]) {
			return false
		}
		delete(r.engines, name)
		delete(r.status, name)
		return true
	})
	for _, name := range src.order {
		r.engines[name] = src.engines[name]
		r.order = append(r.order, name)
	}
}

func (r *Registry[E, H]) fail(name, phase string, err error) error {
	if r.log != nil {
		r.log.Error(r.kind+" lifecycle failed", "name", name, "phase", phase, "err", err)
	}
	return fmt.Errorf("%s %s: %w", r.kind, name, err)
}

// guard runs fn, converting a panic into an error.
func guard(fn func() error) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic: %v", p)
		}
	}()
	return fn()
}

func healthSafely[E Engine[H], H any](ctx context.Context, e E) (h Health) {
	defer func() {
		if p := recover(); p != nil {
			h = Health{Status: HealthUnhealthy, Message: fmt.Sprintf("health panic: %v", p)}
		}
	}()
	return e.Health(ctx)
}
