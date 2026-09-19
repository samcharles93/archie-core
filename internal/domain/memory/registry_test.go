package memory

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- fake engine -------------------------------------------------------------

type fakeEngine struct {
	name      string
	manifest  Manifest
	recorder  *[]string // shared lifecycle log
	startErr  error
	stopErr   error
	stopBlock chan struct{}
	bindPanic bool
	view      Registrar

	records   map[RecordID]Record
	revisions map[RecordID][]Revision
	nextID    int
}

func newFake(name string, manifest Manifest) *fakeEngine {
	return &fakeEngine{
		name:      name,
		manifest:  manifest,
		records:   map[RecordID]Record{},
		revisions: map[RecordID][]Revision{},
	}
}

func (f *fakeEngine) record(e string) {
	if f.recorder != nil {
		*f.recorder = append(*f.recorder, e)
	}
}

func (f *fakeEngine) Name() string       { return f.name }
func (f *fakeEngine) Version() string    { return "test" }
func (f *fakeEngine) Manifest() Manifest { return f.manifest }
func (f *fakeEngine) Bind(host Registrar) {
	f.view = host
	f.record("bind:" + f.name)
	if f.bindPanic {
		panic("bind boom")
	}
}

// The fake implements the full Store so it satisfies MemoryEngine, but these
// tests exercise registration and lifecycle, not storage semantics -- the
// builtin engine's own suite is the store's real coverage. The in-memory
// behaviour here is deliberately minimal and not a reference implementation.
func (f *fakeEngine) Create(_ context.Context, in NewRecord) (Record, error) {
	f.nextID++
	rec := Record{
		ID:         RecordID(f.name + "-" + strconv.Itoa(f.nextID)),
		Scope:      in.Scope,
		Kind:       in.Kind,
		Content:    in.Content,
		Revision:   1,
		Author:     in.Author,
		OriginUser: in.OriginUser,
		Source:     in.Source,
	}
	f.records[rec.ID] = rec
	return rec, nil
}

func (f *fakeEngine) Get(_ context.Context, scope Scope, id RecordID) (Record, error) {
	rec, ok := f.records[id]
	if !ok || rec.Scope != scope {
		return Record{}, ErrNotFound
	}
	return rec, nil
}

func (f *fakeEngine) Query(_ context.Context, q Query) ([]Record, error) {
	wanted := make(map[Scope]bool, len(q.Scopes))
	for _, scope := range q.Scopes {
		wanted[scope] = true
	}
	var out []Record
	for _, r := range f.records {
		if q.Text != "" && !strings.Contains(r.Content, q.Text) {
			continue
		}
		if wanted[r.Scope] {
			out = append(out, r)
		}
	}
	slices.SortFunc(out, func(a, b Record) int { return strings.Compare(string(a.ID), string(b.ID)) })
	return limit(out, q.Limit), nil
}

func (f *fakeEngine) List(ctx context.Context, scope Scope) ([]Record, error) {
	return f.Query(ctx, Query{Scopes: []Scope{scope}})
}

func (f *fakeEngine) Update(_ context.Context, in RecordUpdate) (Record, error) {
	rec, ok := f.records[in.ID]
	if !ok || rec.Scope != in.Scope {
		return Record{}, ErrNotFound
	}
	if in.Expected != 0 && in.Expected != rec.Revision {
		return Record{}, ErrStaleRevision
	}
	f.revisions[rec.ID] = append(f.revisions[rec.ID], Revision{Record: rec, Deleted: false})
	rec.Content = in.Content
	rec.Revision++
	rec.Author = in.Author
	rec.Source = in.Source
	f.records[rec.ID] = rec
	return rec, nil
}

func (f *fakeEngine) Forget(_ context.Context, scope Scope, id RecordID) error {
	rec, ok := f.records[id]
	if !ok || rec.Scope != scope {
		return nil
	}
	f.revisions[id] = append(f.revisions[id], Revision{Record: rec, Deleted: true})
	delete(f.records, id)
	return nil
}

func (f *fakeEngine) Revisions(_ context.Context, _ Scope, id RecordID) ([]Revision, error) {
	return f.revisions[id], nil
}

// limit trims out to n when n > 0, matching the Store contract's Limit.
func limit(out []Record, n int) []Record {
	if n > 0 && len(out) > n {
		return out[:n]
	}
	return out
}

func (f *fakeEngine) Start(context.Context) error {
	if f.startErr != nil {
		return f.startErr
	}
	f.record("start:" + f.name)
	return nil
}

func (f *fakeEngine) Health(context.Context) Health { return Health{Status: HealthHealthy} }

func (f *fakeEngine) Stop(ctx context.Context) error {
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

// --- registry behavior --------------------------------------------------------

func TestRegistryRegisterAndDiscover(t *testing.T) {
	t.Parallel()

	r := NewRegistry(Registrar{})
	a := newFake("a", Manifest{})
	b := newFake("b", Manifest{})
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
	if err := r.Register(newFake("a", Manifest{})); err != nil {
		t.Fatalf("Register(a) = %v, want nil", err)
	}
	err := r.Register(newFake("a", Manifest{}))
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
	var e *fakeEngine
	if err := r.Register(e); err == nil {
		t.Fatal("Register(typed nil) = nil, want error")
	}
}

func TestRegistryRegisterAfterStartRejected(t *testing.T) {
	t.Parallel()

	r := NewRegistry(Registrar{})
	if err := r.Register(newFake("a", Manifest{})); err != nil {
		t.Fatalf("Register(a) = %v, want nil", err)
	}
	if err := r.Start(context.Background()); err != nil {
		t.Fatalf("Start() = %v, want nil", err)
	}
	err := r.Register(newFake("b", Manifest{}))
	if !errors.Is(err, ErrStarted) {
		t.Fatalf("Register after Start = %v, want ErrStarted", err)
	}
}

func TestRegistryBindAlwaysReceivesEventsAndClock(t *testing.T) {
	t.Parallel()

	sink := &testSink{}
	clock := testClock{now: time.Unix(0, 0)}
	r := NewRegistry(Registrar{Events: sink, Clock: clock})
	e := newFake("a", Manifest{})
	if err := r.Register(e); err != nil {
		t.Fatalf("Register(a) = %v, want nil", err)
	}
	if e.view.Events == nil {
		t.Error("view.Events = nil; activity must always be attributable")
	}
	if e.view.Clock == nil {
		t.Error("view.Clock = nil; time is always available")
	}
}

func TestRegistryBindPanicIsolated(t *testing.T) {
	t.Parallel()

	r := NewRegistry(Registrar{})
	panicky := newFake("panicky", Manifest{})
	panicky.bindPanic = true
	if err := r.Register(panicky); err == nil {
		t.Fatal("Register(panicking Bind) = nil, want error")
	}
	if got := r.Names(); len(got) != 0 {
		t.Fatalf("Names() = %v, want empty after a panicking Bind", got)
	}

	// The registry survives and still accepts healthy engines.
	if err := r.Register(newFake("ok", Manifest{})); err != nil {
		t.Fatalf("Register(ok) after panicking Bind = %v, want nil", err)
	}
}

func TestRegistryStartStopOrdering(t *testing.T) {
	t.Parallel()

	r := NewRegistry(Registrar{})
	var log []string
	a := newFake("a", Manifest{})
	a.recorder = &log
	b := newFake("b", Manifest{})
	b.recorder = &log
	c := newFake("c", Manifest{})
	c.recorder = &log
	for _, e := range []*fakeEngine{a, b, c} {
		if err := r.Register(e); err != nil {
			t.Fatalf("Register(%s) = %v, want nil", e.name, err)
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
	a := newFake("a", Manifest{})
	a.recorder = &log
	bad := newFake("bad", Manifest{})
	bad.recorder = &log
	bad.startErr = errBoom
	c := newFake("c", Manifest{})
	c.recorder = &log
	for _, e := range []*fakeEngine{a, bad, c} {
		if err := r.Register(e); err != nil {
			t.Fatalf("Register(%s) = %v, want nil", e.name, err)
		}
	}

	err := r.Start(context.Background())
	if !errors.Is(err, errBoom) {
		t.Fatalf("Start() error = %v, want the failing engine's error", err)
	}

	if !logHas(log, "start:a") || !logHas(log, "start:c") {
		t.Fatalf("healthy engines did not start: %v", log)
	}
	if logHas(log, "start:bad") {
		t.Fatalf("failing engine started despite Start error: %v", log)
	}

	health := r.Health(context.Background())
	if health["bad"].Status != HealthUnhealthy {
		t.Fatalf("Health(bad).Status = %v, want unhealthy", health["bad"].Status)
	}
	if health["a"].Status != HealthHealthy {
		t.Fatalf("Health(a).Status = %v, want healthy", health["a"].Status)
	}

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
	blocked := newFake("blocked", Manifest{})
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
		t.Fatalf("Stop() took %v; a blocking engine must not hang shutdown", elapsed)
	}
}

func TestRegistryStopBeforeStartIsNoop(t *testing.T) {
	t.Parallel()

	r := NewRegistry(Registrar{})
	if err := r.Register(newFake("a", Manifest{})); err != nil {
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

func TestRegistryHealthReportsEveryEngine(t *testing.T) {
	t.Parallel()

	r := NewRegistry(Registrar{})
	if err := r.Register(newFake("a", Manifest{})); err != nil {
		t.Fatalf("Register(a) = %v, want nil", err)
	}
	health := r.Health(context.Background())
	if len(health) != 1 || health["a"].Status != HealthHealthy {
		t.Fatalf("Health() = %v, want one healthy entry for a", health)
	}
}

// --- helpers -------------------------------------------------------------

type testSink struct {
	mu    sync.Mutex
	kinds []string
}

func (s *testSink) Emit(kind, _ string, _ map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.kinds = append(s.kinds, kind)
}

type testClock struct{ now time.Time }

func (c testClock) Now() time.Time { return c.now }

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
