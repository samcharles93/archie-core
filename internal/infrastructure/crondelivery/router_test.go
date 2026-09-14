package crondelivery

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/scheduling"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/infrastructure/cronstore"
)

// --- Router -------------------------------------------------------------------

// routerFixture builds a router over a real store with counting stand-ins for
// the per-kind runners, so a test can assert the router reached one and not
// the other.
func routerFixture(t *testing.T, kind string) (*Router, *countingRunner, *countingRunner, *recordingSink) {
	t.Helper()
	s := newStore(t)
	createJob(t, s, cronstore.JobSpec{
		ID:       "routed",
		Detail:   "routed job",
		Kind:     kind,
		Schedule: cronstore.Schedule{Kind: cronstore.ScheduleInterval, Interval: time.Hour},
	})
	chat, workflow := &countingRunner{}, &countingRunner{}
	sink := &recordingSink{}
	r, err := NewRouter(s, map[string]scheduling.Runner{
		cronstore.KindChat:     chat,
		cronstore.KindWorkflow: workflow,
	}, sink)
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	return r, chat, workflow, sink
}

func TestRouterDispatchesByKind(t *testing.T) {
	tests := []struct {
		name         string
		kind         string
		wantChat     int
		wantWorkflow int
	}{
		{"chat kind reaches the chat runner", cronstore.KindChat, 1, 0},
		{"workflow kind reaches the workflow runner", cronstore.KindWorkflow, 0, 1},
		{"empty kind defaults to the chat runner", "", 1, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r, chat, workflow, _ := routerFixture(t, tc.kind)
			job := scheduling.Job{ID: "routed"}

			if err := r.Run(t.Context(), job); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if got := chat.count(); got != tc.wantChat {
				t.Errorf("chat runner ran %d times, want %d", got, tc.wantChat)
			}
			if got := workflow.count(); got != tc.wantWorkflow {
				t.Errorf("workflow runner ran %d times, want %d", got, tc.wantWorkflow)
			}
		})
	}
}

// TestRouterUnknownKindIsRefusedAtTheGate is the acceptance criterion: an
// unrecognised kind is reported as KindJobError with phase "dispatch", reaches
// no runner, and returns nil — a no-op refused at the gate, not a failed run
// (which would suppress every later tick of the job).
func TestRouterUnknownKindIsRefusedAtTheGate(t *testing.T) {
	r, chat, workflow, sink := routerFixture(t, "teleport")
	job := scheduling.Job{ID: "routed", Pool: scheduling.PoolParallel, Detail: "routed job"}

	if err := r.Run(t.Context(), job); err != nil {
		t.Fatalf("Run on an unknown kind = %v, want nil (a refusal is not a failure)", err)
	}
	if got := chat.count(); got != 0 {
		t.Errorf("chat runner ran %d times for an unknown kind, want 0", got)
	}
	if got := workflow.count(); got != 0 {
		t.Errorf("workflow runner ran %d times for an unknown kind, want 0", got)
	}

	emitted := sink.all()
	if len(emitted) != 1 {
		t.Fatalf("emitted %d events, want 1: %+v", len(emitted), emitted)
	}
	got := emitted[0]
	if got.kind != events.KindJobError {
		t.Errorf("event kind = %q, want %q", got.kind, events.KindJobError)
	}
	// The data map matches the engine's own error shape, so one consumer
	// can render both without a special case for the router.
	for _, key := range []string{"job", "pool", "phase", "err"} {
		if _, ok := got.data[key]; !ok {
			t.Errorf("event data is missing key %q: %+v", key, got.data)
		}
	}
	if got.data["phase"] != "dispatch" {
		t.Errorf("phase = %v, want %q", got.data["phase"], "dispatch")
	}
	if got.data["job"] != "routed" {
		t.Errorf("job = %v, want %q", got.data["job"], "routed")
	}
	if got.data["pool"] != "parallel" {
		t.Errorf("pool = %v, want %q", got.data["pool"], "parallel")
	}
	if got.data["err"] == "" || got.data["err"] == nil {
		t.Errorf("err = %v, want a message naming the unknown kind", got.data["err"])
	}
}

// TestRouterNilSinkDoesNotPanicOnUnknownKind mirrors the engine's own
// tolerance: a nil sink disables emission without disabling the refusal.
func TestRouterNilSinkDoesNotPanicOnUnknownKind(t *testing.T) {
	s := newStore(t)
	createJob(t, s, cronstore.JobSpec{
		ID:       "routed",
		Kind:     "teleport",
		Schedule: cronstore.Schedule{Kind: cronstore.ScheduleInterval, Interval: time.Hour},
	})
	chat, workflow := &countingRunner{}, &countingRunner{}
	r, err := NewRouter(s, map[string]scheduling.Runner{
		cronstore.KindChat:     chat,
		cronstore.KindWorkflow: workflow,
	}, nil)
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	if err := r.Run(t.Context(), scheduling.Job{ID: "routed"}); err != nil {
		t.Fatalf("Run with a nil sink = %v, want nil", err)
	}
	if chat.count() != 0 || workflow.count() != 0 {
		t.Errorf("a runner was reached for an unknown kind: chat=%d workflow=%d",
			chat.count(), workflow.count())
	}
}

func TestRouterMissingSpecReturnsError(t *testing.T) {
	s := newStore(t)
	r, err := NewRouter(s, map[string]scheduling.Runner{
		cronstore.KindChat: &countingRunner{},
	}, &recordingSink{})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	err = r.Run(t.Context(), scheduling.Job{ID: "never-created"})
	if err == nil {
		t.Fatal("Run with a missing spec returned nil, want an error")
	}
	if !errors.Is(err, errSpecMissing) {
		t.Errorf("Run = %v, want it to wrap %v", err, errSpecMissing)
	}
}

// TestRouterPassesTheEngineJobThrough pins that the router hands the runner
// the job the engine built, not a re-derived one: identity and pool are the
// engine's, and the runner's event attribution depends on them surviving.
func TestRouterPassesTheEngineJobThrough(t *testing.T) {
	r, chat, _, _ := routerFixture(t, cronstore.KindChat)
	job := scheduling.Job{ID: "routed", Pool: scheduling.PoolSequential, Detail: "detail from engine"}

	if err := r.Run(t.Context(), job); err != nil {
		t.Fatalf("Run: %v", err)
	}
	chat.mu.Lock()
	defer chat.mu.Unlock()
	if len(chat.ids) != 1 || chat.ids[0] != "routed" {
		t.Errorf("runner saw job ids %v, want [routed]", chat.ids)
	}
}

// --- AC3: every runner honours cancellation ----------------------------------

// TestEveryRunnerHonoursCancellation covers the acceptance criterion as one
// property over all three runners rather than three separate tests: the engine
// hands every run a bounded context and cancels it on shutdown, so a runner
// that ignores cancellation holds the engine past its own deadline.
//
// The downstream effects used here deliberately IGNORE ctx. A fake that
// honours cancellation would make this pass whether or not the runner checks,
// which is the failure mode the criterion exists to prevent.
func TestEveryRunnerHonoursCancellation(t *testing.T) {
	lookup := &fakeLookup{specs: map[string]cronstore.JobSpec{
		"chat-job": chatSpec("chat-job", "ops-room", "hello"),
		"wf-job":   workflowSpec("wf-job", "workflow job", "body"),
	}}

	courier := &naiveCourier{}
	submitter := &naiveSubmitter{}
	chat, err := NewChatCourier(lookup, courier.send)
	if err != nil {
		t.Fatalf("NewChatCourier: %v", err)
	}
	workflow, err := NewWorkflowTask(lookup, submitter)
	if err != nil {
		t.Fatalf("NewWorkflowTask: %v", err)
	}
	// The router forwards to a ctx-ignoring runner, so this case pins the
	// router's own guard: a cancelled run must not reach the dispatch.
	forwarded := &naiveRunner{}
	router, err := NewRouter(lookup, map[string]scheduling.Runner{
		cronstore.KindChat:     forwarded,
		cronstore.KindWorkflow: workflow,
	}, &recordingSink{})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	tests := []struct {
		name        string
		runner      scheduling.Runner
		job         scheduling.Job
		sideEffects func() int
	}{
		{"ChatCourier", chat, scheduling.Job{ID: "chat-job"}, func() int {
			_, _, calls := courier.snapshot()
			return calls
		}},
		{"WorkflowTask", workflow, scheduling.Job{ID: "wf-job"}, submitter.count},
		{"Router", router, scheduling.Job{ID: "chat-job"}, forwarded.count},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cancelled, cancel := context.WithCancel(t.Context())
			cancel()
			if err := tc.runner.Run(cancelled, tc.job); !errors.Is(err, context.Canceled) {
				t.Errorf("Run on a cancelled ctx = %v, want context.Canceled", err)
			}
			if got := tc.sideEffects(); got != 0 {
				t.Errorf("a cancelled run produced %d side effect(s), want 0", got)
			}
		})
	}
}

// --- AC4: the headline scenario, end to end -----------------------------------

// dailyStatusSummary is the hard-coded template AC4 calls for. The blueprints
// slice replaces it with parameterisation; until then the composed message is
// this constant, and the test asserts the delivered text against it.
const dailyStatusSummary = "Daily status summary: all workers healthy, no tasks parked."

// TestDailyStatusSummaryEndToEnd is the epic's headline scenario exercised
// through the whole chain a deployment uses: a real job persisted in a real
// cronstore is reported due by the store's own JobSource, handed to the router
// the engine is given, dispatched to the chat runner, and delivered as the
// composed message.
func TestDailyStatusSummaryEndToEnd(t *testing.T) {
	s := newStore(t)
	const chatID = "ops-room"

	spec := cronstore.JobSpec{
		ID:       "daily-status",
		Detail:   "daily status summary",
		Kind:     cronstore.KindChat,
		Schedule: cronstore.Schedule{Kind: cronstore.ScheduleInterval, Interval: 24 * time.Hour},
		Target:   cronstore.Target{ChatID: chatID},
		Payload:  cronstore.Payload{Text: dailyStatusSummary},
		// Due now: create it with a next_run in the past, the state a
		// long-idle daemon wakes up to.
		NextRun: time.Now().UTC().Add(-time.Minute),
	}
	if err := s.Create(t.Context(), spec); err != nil {
		t.Fatalf("cronstore.Create: %v", err)
	}

	// The engine's source: the store itself, answering what is due.
	due, err := s.Due(t.Context(), time.Now().UTC())
	if err != nil {
		t.Fatalf("Due: %v", err)
	}
	if len(due) != 1 {
		t.Fatalf("Due returned %d jobs, want 1: %+v", len(due), due)
	}

	courier := &fakeCourier{}
	courierRunner, err := NewChatCourier(s, courier.send)
	if err != nil {
		t.Fatalf("NewChatCourier: %v", err)
	}
	router, err := NewRouter(s, map[string]scheduling.Runner{
		cronstore.KindChat: courierRunner,
	}, &recordingSink{})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	// The engine hands the router the job its source reported.
	if err := router.Run(t.Context(), due[0]); err != nil {
		t.Fatalf("router.Run: %v", err)
	}

	chats, texts, calls := courier.snapshot()
	if calls != 1 {
		t.Fatalf("courier called %d times, want 1", calls)
	}
	if chats[0] != chatID {
		t.Errorf("delivered to %q, want %q", chats[0], chatID)
	}
	if texts[0] != dailyStatusSummary {
		t.Errorf("delivered %q, want the composed summary %q", texts[0], dailyStatusSummary)
	}
}

// --- constructors -------------------------------------------------------------

func TestConstructorsRejectNilDependencies(t *testing.T) {
	s := newStore(t)
	courier := &fakeCourier{}
	tests := []struct {
		name string
		run  func() error
	}{
		{"ChatCourier without a spec lookup", func() error {
			_, err := NewChatCourier(nil, courier.send)
			return err
		}},
		{"ChatCourier without a courier", func() error {
			_, err := NewChatCourier(s, nil)
			return err
		}},
		{"WorkflowTask without a spec lookup", func() error {
			_, err := NewWorkflowTask(nil, &fakeSubmitter{})
			return err
		}},
		{"WorkflowTask without a submitter", func() error {
			_, err := NewWorkflowTask(s, nil)
			return err
		}},
		{"Router without a spec lookup", func() error {
			_, err := NewRouter(nil, map[string]scheduling.Runner{
				cronstore.KindChat: &countingRunner{},
			}, nil)
			return err
		}},
		{"Router without any runner", func() error {
			_, err := NewRouter(s, nil, nil)
			return err
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); err == nil {
				t.Error("constructor returned nil error, want a refusal")
			}
		})
	}
}

// TestImplementsSchedulingRunner is the compile-time contract check the engine
// depends on.
func TestImplementsSchedulingRunner(t *testing.T) {
	var (
		_ scheduling.Runner = (*ChatCourier)(nil)
		_ scheduling.Runner = (*WorkflowTask)(nil)
		_ scheduling.Runner = (*Router)(nil)
	)
}
