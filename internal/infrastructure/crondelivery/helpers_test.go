// Shared fixtures for the crondelivery tests. The fakes here come in two
// flavours, and the distinction is load-bearing: the "naive" ones ignore ctx
// entirely, so a cancellation test built on them proves the RUNNER's own guard
// is what suppresses the side effect, rather than the fake's good manners.
package crondelivery

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/scheduling"
	"github.com/samcharles93/archie-core/internal/infrastructure/cronstore"
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
// The cancellation tests need it because the real store is itself
// cancellation-aware: *cronstore.Store.Get returns ctx.Err() before it reads,
// so with a real store the runner's own guard is masked — hydrate fails and the
// run returns the context error whether or not the runner checked. A lookup
// that ignores ctx removes that mask, leaving the runner's guard as the only
// thing that can prevent the side effect.
type fakeLookup struct {
	specs map[string]cronstore.JobSpec
}

func (f *fakeLookup) Get(_ context.Context, id string) (cronstore.JobSpec, bool, error) {
	spec, ok := f.specs[id]
	return spec, ok, nil
}

// MarkRun satisfies RouterStore. It deliberately does nothing: these tests
// exercise the runner's and the router's own dispatch guards, and the record
// step is covered against the real store in
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

// newStore opens a real cronstore on a temp path and closes it on cleanup.
func newStore(t *testing.T) *cronstore.Store {
	t.Helper()
	s, err := cronstore.Open(filepath.Join(t.TempDir(), "jobs.json"))
	if err != nil {
		t.Fatalf("cronstore.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// createJob persists a spec through the real store and returns the scheduling
// job the engine would hand a runner for it.
func createJob(t *testing.T, s *cronstore.Store, spec cronstore.JobSpec) scheduling.Job {
	t.Helper()
	if spec.NextRun.IsZero() {
		// Due now: the engine only ever hands a runner a job its source
		// reported due, so the fixture must be in that state.
		spec.NextRun = time.Now().UTC().Add(-time.Minute)
	}
	if err := s.Create(t.Context(), spec); err != nil {
		t.Fatalf("cronstore.Create(%q): %v", spec.ID, err)
	}
	return scheduling.Job{ID: spec.ID, Pool: scheduling.Pool(spec.Pool), Detail: spec.Detail}
}

// chatSpec is a minimal chat job.
func chatSpec(id, chatID, text string) cronstore.JobSpec {
	return cronstore.JobSpec{
		ID:       id,
		Detail:   "test job " + id,
		Kind:     cronstore.KindChat,
		Schedule: cronstore.Schedule{Kind: cronstore.ScheduleInterval, Interval: time.Hour},
		Target:   cronstore.Target{ChatID: chatID},
		Payload:  cronstore.Payload{Text: text},
	}
}

// workflowSpec is a minimal workflow job.
func workflowSpec(id, detail, body string) cronstore.JobSpec {
	return cronstore.JobSpec{
		ID:       id,
		Detail:   detail,
		Kind:     cronstore.KindWorkflow,
		Schedule: cronstore.Schedule{Kind: cronstore.ScheduleInterval, Interval: time.Hour},
		Payload:  cronstore.Payload{Text: body},
	}
}
