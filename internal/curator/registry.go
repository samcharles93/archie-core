package curator

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
	// ErrDuplicate is returned when a curator name is already registered.
	ErrDuplicate = errors.New("duplicate curator")
	// ErrStarted is returned when registration or lifecycle is attempted
	// after the registry has started.
	ErrStarted = errors.New("curator registry already started")
)

type registryState uint8

const (
	stateIdle registryState = iota
	stateRunning
	stateStopped
)

type curatorStatus struct {
	started  bool
	startErr error
}

// Registry is the curator family owner: it controls registration and
// discovery, lifecycle (start/health/stop), shutdown ordering, and failure
// isolation. A curator failure — a rejected registration, a failing Start, a
// blocking Stop, even a panicking Bind — affects only that curator and is
// reported, never fatal to the daemon or to other curators.
type Registry struct {
	mu       sync.Mutex
	host     Registrar
	curators map[string]CuratorEngine
	status   map[string]curatorStatus
	order    []string
	state    registryState
}

// NewRegistry builds the family registry. A nil Clock is replaced with the
// system clock; every other nil service is valid until a curator declares
// the capability that requires it.
func NewRegistry(host Registrar) *Registry {
	if host.Clock == nil {
		host.Clock = systemClock{}
	}
	return &Registry{
		host:     host,
		curators: make(map[string]CuratorEngine),
		status:   make(map[string]curatorStatus),
	}
}

// Register validates the curator's declared shape against the registrar and
// adds it, binding the curator to a host view filtered to its declared
// capabilities. Registration fails on nil engines, invalid manifests,
// duplicate names, a declared capability with no host service behind it, or
// a panicking Bind — and a failure affects only that curator.
func (r *Registry) Register(c CuratorEngine) error {
	if isNilEngine(c) {
		return errors.New("curator: nil engine")
	}
	m := c.Manifest()
	if err := m.Validate(); err != nil {
		return err
	}
	if err := r.validateDeclared(m); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state != stateIdle {
		return ErrStarted
	}
	if _, dup := r.curators[c.Name()]; dup {
		return fmt.Errorf("%w: %s", ErrDuplicate, c.Name())
	}
	if err := bindSafely(c, r.filter(m)); err != nil {
		return err
	}
	r.curators[c.Name()] = c
	r.order = append(r.order, c.Name())
	return nil
}

// validateDeclared enforces "only the host services a curator's declared
// shape requires": a declared capability must have a host service behind it.
// A curator can never discover the daemon or an untyped hook map here — the
// registrar is the only channel.
func (r *Registry) validateDeclared(m Manifest) error {
	if len(m.Tools) > 0 && r.host.Tools == nil {
		return errors.New("curator manifest: declares tools but the registrar has no ToolBuilder")
	}
	if m.Skills && r.host.Skills == nil {
		return errors.New("curator manifest: declares the skills capability but the registrar has no SkillStore")
	}
	return nil
}

// filter returns the registrar view narrowed to the curator's declared
// capabilities. Events and Clock are always present (activity must stay
// attributable and time is universal); model access is present only for
// agentic curators; every capability service is present only when declared.
func (r *Registry) filter(m Manifest) Registrar {
	v := r.host
	if !agentic(m) {
		v.LLM = nil
	}
	if len(m.Tools) == 0 {
		v.Tools = nil
	}
	if !m.Skills {
		v.Skills = nil
	}
	return v
}

func agentic(m Manifest) bool {
	return len(m.Tools) > 0 || m.Skills || m.MemoryEngine != ""
}

// bindSafely isolates a panicking Bind: a curator that panics while
// receiving its host access is refused without taking the registry down.
func bindSafely(c CuratorEngine, host Registrar) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("curator %s: bind panic: %v", c.Name(), p)
		}
	}()
	c.Bind(host)
	return nil
}

// Host returns the raw registrar bundle the registry was built with — the
// shared services (events, clock, model) the runtime and future surfaces
// use. It is narrow typed; the daemon itself is never part of it.
func (r *Registry) Host() Registrar {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.host
}

// Get returns the registered curator by name.
func (r *Registry) Get(name string) (CuratorEngine, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.curators[name]
	return c, ok
}

// Names returns the registered curator names in sorted order.
func (r *Registry) Names() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	names := make([]string, 0, len(r.curators))
	for name := range r.curators {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// Start starts every registered curator in registration order. A curator
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
		c := r.curators[name]
		err := startSafely(ctx, c)
		r.mu.Lock()
		if err != nil {
			r.status[name] = curatorStatus{startErr: err}
			errs = append(errs, fmt.Errorf("curator %s: %w", name, err))
		} else {
			r.status[name] = curatorStatus{started: true}
		}
		r.mu.Unlock()
	}
	return errors.Join(errs...)
}

func startSafely(ctx context.Context, c CuratorEngine) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("start panic: %v", p)
		}
	}()
	return c.Start(ctx)
}

// Health reports every registered curator's health. A curator whose Start
// failed is reported unhealthy with the failure; otherwise the curator's own
// Health is used. A panicking Health is reported unhealthy, not propagated.
func (r *Registry) Health(ctx context.Context) map[string]Health {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]Health, len(r.curators))
	for name, c := range r.curators {
		if st, ok := r.status[name]; ok && st.startErr != nil {
			out[name] = Health{Status: HealthUnhealthy, Message: st.startErr.Error()}
			continue
		}
		out[name] = healthSafely(ctx, c)
	}
	return out
}

func healthSafely(ctx context.Context, c CuratorEngine) (h Health) {
	defer func() {
		if p := recover(); p != nil {
			h = Health{Status: HealthUnhealthy, Message: fmt.Sprintf("health panic: %v", p)}
		}
	}()
	return c.Health(ctx)
}

// Stop stops every started curator in reverse registration order. Curators
// whose Start failed are skipped; the call is bounded by ctx — a curator
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
	for i := len(order) - 1; i >= 0; i-- {
		name := order[i]
		r.mu.Lock()
		st, ok := r.status[name]
		r.mu.Unlock()
		if !ok || !st.started {
			continue
		}
		c := r.curators[name]
		if err := stopSafely(ctx, c); err != nil {
			errs = append(errs, fmt.Errorf("curator %s: %w", name, err))
		}
	}
	return errors.Join(errs...)
}

func stopSafely(ctx context.Context, c CuratorEngine) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("stop panic: %v", p)
		}
	}()
	return c.Stop(ctx)
}

func isNilEngine(c CuratorEngine) bool {
	v := reflect.ValueOf(c)
	switch v.Kind() {
	case reflect.Invalid:
		return true
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	}
	return false
}
