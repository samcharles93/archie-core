// Shared fixtures for the crondelivery tests. The fakes here come in two
// flavours, and the distinction is load-bearing: the "naive" ones ignore ctx
// entirely, so a cancellation test built on them proves the RUNNER's own guard
// is what suppresses the side effect, rather than the fake's good manners.
package crondelivery

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/scheduling"
)

// --- fakes --------------------------------------------------------------------

// naiveCourier records every call unconditionally and ignores ctx entirely. It
// models the worst realistic transport — one that would send after shutdown —
// so a test using it proves the RUNNER's own cancellation guard is what
// prevents the side effect, not the fake's good manners.
type naiveCourier struct {
	mu    sync.Mutex
	calls int
	chats []string
	texts []string
}

func (n *naiveCourier) send(_ context.Context, chatID, text string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.calls++
	n.chats = append(n.chats, chatID)
	n.texts = append(n.texts, text)
	return nil
}

func (n *naiveCourier) snapshot() (chats, texts []string, calls int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string(nil), n.chats...), append([]string(nil), n.texts...), n.calls
}

// naiveSubmitter mirrors naiveCourier for the workflow path.
type naiveSubmitter struct {
	mu    sync.Mutex
	calls int
	ids   []string
}

func (n *naiveSubmitter) Submit(_ context.Context, identity, _, _ string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.calls++
	n.ids = append(n.ids, identity)
	return nil
}

func (n *naiveSubmitter) count() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.calls
}

// naiveRunner counts calls and ignores ctx, like naiveCourier. It is what the
// router's cancellation case forwards to, so the router's own guard is the only
// thing that can stop the dispatch.
type naiveRunner struct {
	mu    sync.Mutex
	calls int
}

func (n *naiveRunner) Run(_ context.Context, _ scheduling.Job) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.calls++
	return nil
}

func (n *naiveRunner) count() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.calls
}

// fakeLookup serves specs from a map and ignores ctx entirely, like
// naiveCourier.
//
// The cancellation tests need it because a production store is expected to be
// cancellation-aware: a source whose Get returns ctx.Err() before it reads
// masks the runner's own guard — hydrate fails and the run returns the context
// error whether or not the runner checked. A lookup that ignores ctx removes
// that mask, leaving the runner's guard as the only thing that can prevent the
// side effect.
type fakeLookup struct {
	specs map[string]scheduling.JobSpec
}

func (f *fakeLookup) Get(_ context.Context, id string) (scheduling.JobSpec, bool, error) {
	spec, ok := f.specs[id]
	return spec, ok, nil
}

// MarkRun satisfies RouterStore. It deliberately does nothing: these tests
// exercise the runner's and the router's own dispatch guards, and the record
// step is covered against a store that really advances in
// TestRouterAdvancesTheScheduleAfterASuccessfulRun.
func (f *fakeLookup) MarkRun(_ context.Context, _ string, _ time.Time) error { return nil }

// fakeCourier records what was sent and can be made to fail or to block until
// cancelled.
type fakeCourier struct {
	mu      sync.Mutex
	chats   []string
	texts   []string
	calls   int
	err     error
	entered chan struct{} // closed when send is entered; nil means no signal
	blockOn chan struct{} // when non-nil, send waits on it (or ctx) before returning
}

func (f *fakeCourier) send(ctx context.Context, chatID, text string) error {
	f.mu.Lock()
	f.calls++
	f.chats = append(f.chats, chatID)
	f.texts = append(f.texts, text)
	entered, block := f.entered, f.blockOn
	err := f.err
	f.mu.Unlock()
	if entered != nil {
		select {
		case <-entered:
		default:
			close(entered)
		}
	}
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return err
}

func (f *fakeCourier) snapshot() (chats, texts []string, calls int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.chats...), append([]string(nil), f.texts...), f.calls
}

// fakeSubmitter records submitted work.
type fakeSubmitter struct {
	mu         sync.Mutex
	identities []string
	titles     []string
	bodies     []string
	calls      int
	err        error
	entered    chan struct{}
	block      chan struct{}
}

func (f *fakeSubmitter) Submit(ctx context.Context, identity, title, body string) error {
	f.mu.Lock()
	f.calls++
	f.identities = append(f.identities, identity)
	f.titles = append(f.titles, title)
	f.bodies = append(f.bodies, body)
	entered, block := f.entered, f.block
	err := f.err
	f.mu.Unlock()
	if entered != nil {
		select {
		case <-entered:
		default:
			close(entered)
		}
	}
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return err
}

func (f *fakeSubmitter) snapshot() (ids, titles, bodies []string, calls int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.identities...),
		append([]string(nil), f.titles...),
		append([]string(nil), f.bodies...),
		f.calls
}

// recordingSink captures emitted events.
type recordingSink struct {
	mu     sync.Mutex
	events []recordedEvent
}

type recordedEvent struct {
	kind   string
	detail string
	data   map[string]any
}

func (s *recordingSink) Emit(kind, detail string, data map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, recordedEvent{kind: kind, detail: detail, data: data})
}

func (s *recordingSink) all() []recordedEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]recordedEvent(nil), s.events...)
}

// countingRunner is a stand-in runner used to prove the router did or did not
// reach a kind's runner.
type countingRunner struct {
	mu    sync.Mutex
	calls int
	ids   []string
}

func (c *countingRunner) Run(_ context.Context, job scheduling.Job) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	c.ids = append(c.ids, job.ID)
	return nil
}

func (c *countingRunner) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// --- helpers ------------------------------------------------------------------

// memStore is the test's schedules document: a map with the three faces the
// production store shows the package — SpecLookup (Get), RunRecorder (MarkRun
// with real schedule arithmetic), and JobSource (Due). It replaces the file-
// backed store this package's fixtures used to open, which died with the
// legacy jobs.json store; the document now lives behind the control plane in
// production, and what these tests need from persistence is semantics, not a
// file.
type memStore struct {
	mu   sync.Mutex
	jobs map[string]scheduling.JobSpec
}

func newStore(t *testing.T) *memStore {
	t.Helper()
	return &memStore{jobs: make(map[string]scheduling.JobSpec)}
}

// Create persists a spec verbatim. Fixtures that want a due job set NextRun
// themselves (createJob does it when unset).
func (m *memStore) Create(_ context.Context, spec scheduling.JobSpec) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.jobs[spec.ID]; exists {
		return fmt.Errorf("memStore: id %q already exists", spec.ID)
	}
	m.jobs[spec.ID] = spec
	return nil
}

func (m *memStore) Get(_ context.Context, id string) (scheduling.JobSpec, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	spec, ok := m.jobs[id]
	return spec, ok, nil
}

// Due returns the jobs whose NextRun is at or before now, the same contract
// the production source keeps.
func (m *memStore) Due(_ context.Context, now time.Time) ([]scheduling.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	due := make([]scheduling.Job, 0, len(m.jobs))
	for _, spec := range m.jobs {
		if !spec.NextRun.After(now) {
			due = append(due, scheduling.Job{ID: spec.ID, Pool: scheduling.Pool(spec.Pool), Detail: spec.Detail})
		}
	}
	return due, nil
}

// MarkRun advances an interval job's NextRun the way the production
// recording step does, and reports a once schedule as unsupported — the
// sentinel the router swallows.
func (m *memStore) MarkRun(_ context.Context, id string, runAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	spec, ok := m.jobs[id]
	if !ok {
		return fmt.Errorf("%w: id %q", scheduling.ErrJobNotFound, id)
	}
	next, err := spec.Schedule.NextRun(runAt)
	if err != nil {
		return err
	}
	at := runAt.UTC()
	spec.LastRun = &at
	spec.NextRun = next
	m.jobs[id] = spec
	return nil
}

// createJob persists a spec and returns the scheduling job the engine would
// hand a runner for it.
func createJob(t *testing.T, s *memStore, spec scheduling.JobSpec) scheduling.Job {
	t.Helper()
	if spec.NextRun.IsZero() {
		// Due now: the engine only ever hands a runner a job its source
		// reported due, so the fixture must be in that state.
		spec.NextRun = time.Now().UTC().Add(-time.Minute)
	}
	if err := s.Create(t.Context(), spec); err != nil {
		t.Fatalf("Create(%q): %v", spec.ID, err)
	}
	return scheduling.Job{ID: spec.ID, Pool: scheduling.Pool(spec.Pool), Detail: spec.Detail}
}

// chatSpec is a minimal chat job.
func chatSpec(id, chatID, text string) scheduling.JobSpec {
	return scheduling.JobSpec{
		ID:       id,
		Detail:   "test job " + id,
		Kind:     scheduling.KindChat,
		Schedule: scheduling.Schedule{Kind: scheduling.ScheduleInterval, Interval: scheduling.Duration(time.Hour)},
		Target:   scheduling.Target{ChatID: chatID},
		Payload:  scheduling.Payload{Text: text},
	}
}

// workflowSpec is a minimal workflow job.
func workflowSpec(id, detail, body string) scheduling.JobSpec {
	return scheduling.JobSpec{
		ID:       id,
		Detail:   detail,
		Kind:     scheduling.KindWorkflow,
		Schedule: scheduling.Schedule{Kind: scheduling.ScheduleInterval, Interval: scheduling.Duration(time.Hour)},
		Payload:  scheduling.Payload{Text: body},
	}
}
