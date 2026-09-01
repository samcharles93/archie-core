package curator

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	domainmemory "github.com/samcharles93/archie-core/internal/domain/memory"
	"github.com/samcharles93/archie-core/internal/tools"
)

// --- host-service fakes -----------------------------------------------------

type testLLM struct{ calls int }

func (l *testLLM) Chat(_ context.Context, _ ChatRequest) (ChatResult, error) {
	l.calls++
	return ChatResult{Text: "ok"}, nil
}

type testBuilder struct{ built [][]string }

func (b *testBuilder) Build(_ context.Context, declared []string) ([]tools.ToolEntry, error) {
	b.built = append(b.built, declared)
	return nil, nil
}

type testStore struct{}

func (testStore) List(context.Context) ([]SkillRef, error)    { return nil, nil }
func (testStore) Read(context.Context, string) (Skill, error) { return Skill{}, nil }
func (testStore) Write(context.Context, Skill) error          { return nil }
func (testStore) Delete(context.Context, string) error        { return nil }

// testMemoryEngines is a fake MemoryEngineSource with one engine, "builtin".
type testMemoryEngines struct{}

func (testMemoryEngines) Get(name string) (domainmemory.MemoryEngine, bool) {
	if name != "builtin" {
		return nil, false
	}
	return fakeMemoryEngine{}, true
}

// fakeMemoryEngine is the minimal domain/memory.MemoryEngine a resolved
// lookup can return; its own behavior is exercised by
// internal/domain/memory's and internal/infrastructure/memory's own test
// suites, not here.
type fakeMemoryEngine struct{}

func (fakeMemoryEngine) Name() string                    { return "builtin" }
func (fakeMemoryEngine) Version() string                 { return "test" }
func (fakeMemoryEngine) Manifest() domainmemory.Manifest { return domainmemory.Manifest{} }
func (fakeMemoryEngine) Bind(domainmemory.Registrar)     {}
func (fakeMemoryEngine) Start(context.Context) error     { return nil }
func (fakeMemoryEngine) Health(context.Context) domainmemory.Health {
	return domainmemory.Health{Status: domainmemory.HealthHealthy}
}
func (fakeMemoryEngine) Stop(context.Context) error { return nil }
func (fakeMemoryEngine) Write(context.Context, domainmemory.Observation) (domainmemory.Record, error) {
	return domainmemory.Record{}, nil
}

func (fakeMemoryEngine) Query(context.Context, domainmemory.Query) ([]domainmemory.Record, error) {
	return nil, nil
}

func (fakeMemoryEngine) List(context.Context, string) ([]domainmemory.Record, error) {
	return nil, nil
}
func (fakeMemoryEngine) Forget(context.Context, string) error { return nil }

type testSink struct {
	mu    sync.Mutex
	kinds []string
}

func (s *testSink) Emit(kind, _ string, _ map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.kinds = append(s.kinds, kind)
}

func (s *testSink) kindsOf() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.kinds)
}

func (s *testSink) count(kind string) int {
	n := 0
	for _, k := range s.kindsOf() {
		if k == kind {
			n++
		}
	}
	return n
}

type testClock struct{ now time.Time }

func (c testClock) Now() time.Time { return c.now }
func (c testClock) After(d time.Duration) <-chan time.Time {
	return time.After(d)
}

// --- fake curator ------------------------------------------------------------

type fakeCurator struct {
	name      string
	manifest  Manifest
	events    []string
	recorder  *[]string // shared lifecycle log; nil means record to events
	startErr  error
	stopErr   error
	stopBlock chan struct{}
	bindPanic bool
	view      Registrar

	// pass/check control (runtime tests).
	checkResult   bool
	checkErr      error
	checkPanic    bool
	checkCalls    atomic.Int32
	passResult    PassResult
	passErr       error
	passPanic     bool
	passBlock     chan struct{} // if set, Pass blocks until closed or ctx done
	passIgnoreCtx bool          // if true, Pass ignores ctx while blocked
	passCalls     atomic.Int32
	inputMu       sync.Mutex
	inputs        []PassInput
}

func newFake(name string, manifest Manifest) *fakeCurator {
	return &fakeCurator{name: name, manifest: manifest}
}

func (f *fakeCurator) record(e string) {
	if f.recorder != nil {
		*f.recorder = append(*f.recorder, e)
	} else {
		f.events = append(f.events, e)
	}
}

func (f *fakeCurator) Name() string       { return f.name }
func (f *fakeCurator) Version() string    { return "test" }
func (f *fakeCurator) Manifest() Manifest { return f.manifest }
func (f *fakeCurator) Bind(host Registrar) {
	f.view = host
	f.record("bind:" + f.name)
	if f.bindPanic {
		panic("bind boom")
	}
}

func (f *fakeCurator) Check(context.Context) (bool, error) {
	f.checkCalls.Add(1)
	if f.checkPanic {
		panic("check boom")
	}
	if f.checkErr != nil {
		return false, f.checkErr
	}
	return f.checkResult, nil
}

func (f *fakeCurator) Pass(ctx context.Context, in PassInput) (PassResult, error) {
	f.passCalls.Add(1)
	f.inputMu.Lock()
	f.inputs = append(f.inputs, in)
	f.inputMu.Unlock()
	if f.passPanic {
		panic("pass boom")
	}
	if f.passBlock != nil {
		if f.passIgnoreCtx {
			<-f.passBlock
		} else {
			select {
			case <-f.passBlock:
			case <-ctx.Done():
				return PassResult{}, ctx.Err()
			}
		}
	}
	if f.passErr != nil {
		return PassResult{}, f.passErr
	}
	return f.passResult, nil
}

func (f *fakeCurator) Start(context.Context) error {
	if f.startErr != nil {
		return f.startErr
	}
	f.record("start:" + f.name)
	return nil
}

func (f *fakeCurator) Health(context.Context) Health { return Health{Status: HealthHealthy} }

func (f *fakeCurator) Stop(ctx context.Context) error {
	if f.stopBlock != nil {
		select {
		case <-f.stopBlock:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if f.stopErr != nil {
		return f.stopErr
	}
	f.record("stop:" + f.name)
	return nil
}

// --- registry behavior -------------------------------------------------------

func TestRegistryRegisterAndDiscover(t *testing.T) {
	t.Parallel()

	r := NewRegistry(Registrar{})
	a := newFake("a", Manifest{Interval: time.Hour})
	b := newFake("b", Manifest{Interval: time.Hour})
	if err := r.Register(a); err != nil {
		t.Fatalf("Register(a) = %v, want nil", err)
	}
	if err := r.Register(b); err != nil {
		t.Fatalf("Register(b) = %v, want nil", err)
	}

	got, ok := r.Get("a")
	if !ok || got != a {
		t.Fatalf("Get(a) = (%v, %v), want (a, true)", got, ok)
	}
	if _, ok := r.Get("missing"); ok {
		t.Fatal("Get(missing) = true, want false")
	}

	if got := r.Names(); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("Names() = %v, want [a b]", got)
	}
}

func TestRegistryRegisterDuplicateRejected(t *testing.T) {
	t.Parallel()

	r := NewRegistry(Registrar{})
	a := newFake("a", Manifest{Interval: time.Hour})
	if err := r.Register(a); err != nil {
		t.Fatalf("Register(a) = %v, want nil", err)
	}
	err := r.Register(newFake("a", Manifest{Interval: time.Hour}))
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("Register(dup) = %v, want ErrDuplicate", err)
	}
	if got := r.Names(); len(got) != 1 {
		t.Fatalf("Names() = %v, want only the first registration", got)
	}
}

func TestRegistryRegisterNilEngineRejected(t *testing.T) {
	t.Parallel()

	r := NewRegistry(Registrar{})
	if err := r.Register(nil); err == nil {
		t.Fatal("Register(nil) = nil, want error")
	}
}

func TestRegistryRegisterTypedNilEngineRejected(t *testing.T) {
	t.Parallel()

	r := NewRegistry(Registrar{})
	var c *fakeCurator
	if err := r.Register(c); err == nil {
		t.Fatal("Register(typed nil) = nil, want error")
	}
}

func TestRegistryRegisterInvalidManifestRejected(t *testing.T) {
	t.Parallel()

	r := NewRegistry(Registrar{})
	if err := r.Register(newFake("bad", Manifest{})); err == nil {
		t.Fatal("Register(zero interval) = nil, want error")
	}
	if got := r.Names(); len(got) != 0 {
		t.Fatalf("Names() = %v, want empty after rejected registration", got)
	}
}

func TestRegistryRegisterAfterStartRejected(t *testing.T) {
	t.Parallel()

	r := NewRegistry(Registrar{})
	if err := r.Register(newFake("a", Manifest{Interval: time.Hour})); err != nil {
		t.Fatalf("Register(a) = %v, want nil", err)
	}
	if err := r.Start(context.Background()); err != nil {
		t.Fatalf("Start() = %v, want nil", err)
	}
	err := r.Register(newFake("b", Manifest{Interval: time.Hour}))
	if !errors.Is(err, ErrStarted) {
		t.Fatalf("Register after Start = %v, want ErrStarted", err)
	}
}

func TestRegistryDeclaredCapabilitiesRequireHostServices(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		host     Registrar
		manifest Manifest
		wantErr  bool
	}{
		{
			name:     "tools declared, no builder",
			host:     Registrar{},
			manifest: Manifest{Interval: time.Hour, Tools: []string{"skills.list"}},
			wantErr:  true,
		},
		{
			name:     "tools declared, builder present",
			host:     Registrar{Tools: &testBuilder{}},
			manifest: Manifest{Interval: time.Hour, Tools: []string{"skills.list"}},
		},
		{
			name:     "skills declared, no store",
			host:     Registrar{},
			manifest: Manifest{Interval: time.Hour, Skills: true},
			wantErr:  true,
		},
		{
			name:     "skills declared, store present",
			host:     Registrar{Skills: testStore{}},
			manifest: Manifest{Interval: time.Hour, Skills: true},
		},
		{
			name:     "memory engine declared, no source",
			host:     Registrar{},
			manifest: Manifest{Interval: time.Hour, MemoryEngine: "builtin"},
			wantErr:  true,
		},
		{
			name:     "memory engine declared, source present",
			host:     Registrar{MemoryEngines: testMemoryEngines{}},
			manifest: Manifest{Interval: time.Hour, MemoryEngine: "builtin"},
		},
		{
			name:     "nothing declared, empty host",
			host:     Registrar{},
			manifest: Manifest{Interval: time.Hour},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := NewRegistry(tt.host)
			err := r.Register(newFake("c", tt.manifest))
			if (err != nil) != tt.wantErr {
				t.Fatalf("Register() = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestRegistryBindFiltersToDeclaredCapabilities(t *testing.T) {
	t.Parallel()

	host := Registrar{
		Model:         "provider/default",
		LLM:           &testLLM{},
		Tools:         &testBuilder{},
		Skills:        testStore{},
		MemoryEngines: testMemoryEngines{},
		Events:        &testSink{},
		Clock:         testClock{now: time.Unix(0, 0)},
	}

	// A curator declaring nothing agentic receives no model access and no
	// capability services, but always receives events and time.
	r := NewRegistry(host)
	plain := newFake("plain", Manifest{Interval: time.Hour})
	if err := r.Register(plain); err != nil {
		t.Fatalf("Register(plain) = %v, want nil", err)
	}
	if plain.view.LLM != nil {
		t.Error("view.LLM = non-nil for a curator declaring no agentic capability")
	}
	if plain.view.Tools != nil {
		t.Error("view.Tools = non-nil for a curator declaring no tools")
	}
	if plain.view.Skills != nil {
		t.Error("view.Skills = non-nil for a curator not declaring skills")
	}
	if plain.view.MemoryEngines != nil {
		t.Error("view.MemoryEngines = non-nil for a curator declaring no memory engine")
	}
	if plain.view.Events == nil {
		t.Error("view.Events = nil; activity must always be attributable")
	}
	if plain.view.Clock == nil {
		t.Error("view.Clock = nil; time is always available")
	}

	// A curator declaring tools and skills receives exactly those, plus the
	// model access an agentic pass needs.
	toolsy := newFake("toolsy", Manifest{Interval: time.Hour, Tools: []string{"skills.list"}, Skills: true})
	if err := r.Register(toolsy); err != nil {
		t.Fatalf("Register(toolsy) = %v, want nil", err)
	}
	if toolsy.view.LLM == nil {
		t.Error("view.LLM = nil for a curator declaring agentic capabilities")
	}
	if toolsy.view.Tools == nil {
		t.Error("view.Tools = nil for a curator declaring tools")
	}
	if toolsy.view.Skills == nil {
		t.Error("view.Skills = nil for a curator declaring skills")
	}

	// A curator declaring only a memory engine still needs model access to
	// reason over memory.
	mem := newFake("mem", Manifest{Interval: time.Hour, MemoryEngine: "builtin"})
	if err := r.Register(mem); err != nil {
		t.Fatalf("Register(mem) = %v, want nil", err)
	}
	if mem.view.LLM == nil {
		t.Error("view.LLM = nil for a curator declaring a memory engine")
	}
	if mem.view.Tools != nil {
		t.Error("view.Tools = non-nil for a curator declaring no tools")
	}
	if mem.view.MemoryEngines == nil {
		t.Error("view.MemoryEngines = nil for a curator declaring a memory engine")
	}
	if _, ok := mem.view.MemoryEngines.Get("builtin"); !ok {
		t.Error("view.MemoryEngines.Get(builtin) = not found, want the fake engine registered under that name")
	}
}

func TestRegistryBindPanicIsolated(t *testing.T) {
	t.Parallel()

	r := NewRegistry(Registrar{})
	panicky := newFake("panicky", Manifest{Interval: time.Hour})
	panicky.bindPanic = true
	if err := r.Register(panicky); err == nil {
		t.Fatal("Register(panicking Bind) = nil, want error")
	}
	if got := r.Names(); len(got) != 0 {
		t.Fatalf("Names() = %v, want empty after a panicking Bind", got)
	}

	// The registry survives and still accepts healthy curators.
	if err := r.Register(newFake("ok", Manifest{Interval: time.Hour})); err != nil {
		t.Fatalf("Register(ok) after panicking Bind = %v, want nil", err)
	}
}

func TestRegistryStartStopOrdering(t *testing.T) {
	t.Parallel()

	r := NewRegistry(Registrar{})
	var log []string
	a := newFake("a", Manifest{Interval: time.Hour})
	a.recorder = &log
	b := newFake("b", Manifest{Interval: time.Hour})
	b.recorder = &log
	c := newFake("c", Manifest{Interval: time.Hour})
	c.recorder = &log
	for _, cur := range []*fakeCurator{a, b, c} {
		if err := r.Register(cur); err != nil {
			t.Fatalf("Register(%s) = %v, want nil", cur.name, err)
		}
	}

	if err := r.Start(context.Background()); err != nil {
		t.Fatalf("Start() = %v, want nil", err)
	}
	if err := r.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() = %v, want nil", err)
	}

	want := []string{
		"bind:a", "bind:b", "bind:c",
		"start:a", "start:b", "start:c",
		"stop:c", "stop:b", "stop:a",
	}
	if got := log; !equalStrings(got, want) {
		t.Fatalf("lifecycle events = %v, want %v (start in registration order, stop reversed)", got, want)
	}
}

func TestRegistryStartIsolatesFailure(t *testing.T) {
	t.Parallel()

	r := NewRegistry(Registrar{})
	var log []string
	a := newFake("a", Manifest{Interval: time.Hour})
	a.recorder = &log
	bad := newFake("bad", Manifest{Interval: time.Hour})
	bad.recorder = &log
	bad.startErr = errBoom
	c := newFake("c", Manifest{Interval: time.Hour})
	c.recorder = &log
	for _, cur := range []*fakeCurator{a, bad, c} {
		if err := r.Register(cur); err != nil {
			t.Fatalf("Register(%s) = %v, want nil", cur.name, err)
			return
		}
	}

	err := r.Start(context.Background())
	if !errors.Is(err, errBoom) {
		t.Fatalf("Start() error = %v, want the failing curator's error", err)
	}

	// The healthy curators started; the failing one never became started.
	if !logHas(log, "start:a") || !logHas(log, "start:c") {
		t.Fatalf("healthy curators did not start: %v", log)
	}
	if logHas(log, "start:bad") {
		t.Fatalf("failing curator started despite Start error: %v", log)
	}

	health := r.Health(context.Background())
	if health["bad"].Status != HealthUnhealthy {
		t.Fatalf("Health(bad).Status = %v, want unhealthy", health["bad"].Status)
	}
	if health["a"].Status != HealthHealthy {
		t.Fatalf("Health(a).Status = %v, want healthy", health["a"].Status)
	}

	// Stop skips the curator that never started and stops the rest in
	// reverse registration order.
	if err := r.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() = %v, want nil", err)
	}
	want := []string{"bind:a", "bind:bad", "bind:c", "start:a", "start:c", "stop:c", "stop:a"}
	if got := log; !equalStrings(got, want) {
		t.Fatalf("lifecycle events = %v, want %v", got, want)
	}
}

func TestRegistryStopBoundedByContext(t *testing.T) {
	t.Parallel()

	r := NewRegistry(Registrar{})
	blocked := newFake("blocked", Manifest{Interval: time.Hour})
	blocked.stopBlock = make(chan struct{})
	if err := r.Register(blocked); err != nil {
		t.Fatalf("Register(blocked) = %v, want nil", err)
	}
	if err := r.Start(context.Background()); err != nil {
		t.Fatalf("Start() = %v, want nil", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := r.Stop(ctx)
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Stop() error = %v, want context.DeadlineExceeded", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("Stop() took %v; a blocking curator must not hang shutdown", elapsed)
	}
}

func TestRegistryStopBeforeStartIsNoop(t *testing.T) {
	t.Parallel()

	r := NewRegistry(Registrar{})
	if err := r.Register(newFake("a", Manifest{Interval: time.Hour})); err != nil {
		t.Fatalf("Register(a) = %v, want nil", err)
	}
	if err := r.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() before Start = %v, want nil no-op", err)
	}
}

func TestRegistryStartTwiceRejected(t *testing.T) {
	t.Parallel()

	r := NewRegistry(Registrar{})
	if err := r.Start(context.Background()); err != nil {
		t.Fatalf("Start() = %v, want nil", err)
	}
	if err := r.Start(context.Background()); !errors.Is(err, ErrStarted) {
		t.Fatalf("second Start() = %v, want ErrStarted", err)
	}
}

func TestRegistryHealthReportsEveryCurator(t *testing.T) {
	t.Parallel()

	r := NewRegistry(Registrar{})
	if err := r.Register(newFake("a", Manifest{Interval: time.Hour})); err != nil {
		t.Fatalf("Register(a) = %v, want nil", err)
	}
	health := r.Health(context.Background())
	if len(health) != 1 || health["a"].Status != HealthHealthy {
		t.Fatalf("Health() = %v, want one healthy entry for a", health)
	}
}

// --- helpers -----------------------------------------------------------------

var errBoom = errors.New("boom")

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func logHas(log []string, want string) bool {
	return slices.Contains(log, want)
}
