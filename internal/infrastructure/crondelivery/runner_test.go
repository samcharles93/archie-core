package crondelivery

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/scheduling"
	"github.com/samcharles93/archie-core/internal/infrastructure/cronstore"
)

// --- ChatCourier --------------------------------------------------------------

func TestChatCourierSendsPayloadToTargetChat(t *testing.T) {
	s := newStore(t)
	courier := &fakeCourier{}
	runner, err := NewChatCourier(s, courier.send)
	if err != nil {
		t.Fatalf("NewChatCourier: %v", err)
	}
	job := createJob(t, s, chatSpec("standup", "ops-room", "morning"))

	if err := runner.Run(t.Context(), job); err != nil {
		t.Fatalf("Run: %v", err)
	}
	chats, texts, calls := courier.snapshot()
	if calls != 1 {
		t.Fatalf("courier called %d times, want 1", calls)
	}
	if chats[0] != "ops-room" {
		t.Errorf("chat id = %q, want %q", chats[0], "ops-room")
	}
	if texts[0] != "morning" {
		t.Errorf("text = %q, want %q", texts[0], "morning")
	}
}

func TestChatCourierPropagatesCourierError(t *testing.T) {
	s := newStore(t)
	wantErr := errors.New("channel refused")
	courier := &fakeCourier{err: wantErr}
	runner, err := NewChatCourier(s, courier.send)
	if err != nil {
		t.Fatalf("NewChatCourier: %v", err)
	}
	job := createJob(t, s, chatSpec("boom", "ops-room", "morning"))

	err = runner.Run(t.Context(), job)
	if !errors.Is(err, wantErr) {
		t.Errorf("Run = %v, want it to wrap %v", err, wantErr)
	}
}

func TestChatCourierMissingSpecReturnsError(t *testing.T) {
	s := newStore(t)
	courier := &fakeCourier{}
	runner, err := NewChatCourier(s, courier.send)
	if err != nil {
		t.Fatalf("NewChatCourier: %v", err)
	}

	// The engine reported this id due; the store does not have it. That is
	// a real inconsistency, so it must surface rather than be swallowed.
	err = runner.Run(t.Context(), scheduling.Job{ID: "never-created"})
	if err == nil {
		t.Fatal("Run with a missing spec returned nil, want an error")
	}
	if _, _, calls := courier.snapshot(); calls != 0 {
		t.Errorf("courier was called %d times for a missing spec, want 0", calls)
	}
}

// TestChatCourierHonoursCancellation is the bounded-shutdown property the
// engine relies on: it cancels a run on Stop, and a runner that blocks through
// cancellation keeps the run in flight past the engine's own deadline.
//
// Two cases, because they exercise different code paths: a run started with an
// already-cancelled context must produce no side effect at all, and a run
// cancelled while the courier is mid-send must return the context error so the
// engine's shutdown completes rather than waiting for a full timeout.
func TestChatCourierHonoursCancellation(t *testing.T) {
	t.Run("cancelled before the run sends nothing", func(t *testing.T) {
		// Both the lookup and the courier ignore ctx, so only the
		// runner's own guard can stop the send.
		lookup := &fakeLookup{specs: map[string]cronstore.JobSpec{
			"cancel-me": chatSpec("cancel-me", "ops-room", "morning"),
		}}
		courier := &naiveCourier{}
		runner, err := NewChatCourier(lookup, courier.send)
		if err != nil {
			t.Fatalf("NewChatCourier: %v", err)
		}

		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		err = runner.Run(cancelled, scheduling.Job{ID: "cancel-me"})
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Run on a cancelled ctx = %v, want context.Canceled", err)
		}
		if _, _, calls := courier.snapshot(); calls != 0 {
			t.Errorf("courier was called %d times on a cancelled ctx, want 0", calls)
		}
	})

	t.Run("cancelled mid-send returns the context error", func(t *testing.T) {
		s := newStore(t)
		entered := make(chan struct{})
		never := make(chan struct{})
		defer close(never) // never released; only cancellation can unblock
		courier := &fakeCourier{entered: entered, blockOn: never}
		runner, err := NewChatCourier(s, courier.send)
		if err != nil {
			t.Fatalf("NewChatCourier: %v", err)
		}
		job := createJob(t, s, chatSpec("cancel-mid", "ops-room", "morning"))

		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- runner.Run(ctx, job) }()

		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			cancel()
			t.Fatal("courier was never entered; Run did not reach the send")
		}
		cancel()

		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Errorf("Run cancelled mid-send = %v, want context.Canceled", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Run did not return after cancellation; it ignored ctx.Done()")
		}
	})
}

// --- WorkflowTask -------------------------------------------------------------

func TestWorkflowTaskSubmitsIdentityTitleAndBody(t *testing.T) {
	s := newStore(t)
	sub := &fakeSubmitter{}
	runner, err := NewWorkflowTask(s, sub)
	if err != nil {
		t.Fatalf("NewWorkflowTask: %v", err)
	}
	job := createJob(t, s, workflowSpec("nightly", "nightly maintenance", "run the sweep"))

	if err := runner.Run(t.Context(), job); err != nil {
		t.Fatalf("Run: %v", err)
	}
	ids, titles, bodies, calls := sub.snapshot()
	if calls != 1 {
		t.Fatalf("submitter called %d times, want 1", calls)
	}
	if ids[0] != "nightly" {
		t.Errorf("identity = %q, want the job id %q", ids[0], "nightly")
	}
	if titles[0] != "nightly maintenance" {
		t.Errorf("title = %q, want %q", titles[0], "nightly maintenance")
	}
	if bodies[0] != "run the sweep" {
		t.Errorf("body = %q, want %q", bodies[0], "run the sweep")
	}
}

// TestWorkflowTaskFallsBackToJobIDForTitle pins the documented fallback: a job
// with no Detail still submits under a nameable title rather than an empty one.
func TestWorkflowTaskFallsBackToJobIDForTitle(t *testing.T) {
	s := newStore(t)
	sub := &fakeSubmitter{}
	runner, err := NewWorkflowTask(s, sub)
	if err != nil {
		t.Fatalf("NewWorkflowTask: %v", err)
	}
	spec := workflowSpec("untitled", "", "body")
	job := createJob(t, s, spec)

	if err := runner.Run(t.Context(), job); err != nil {
		t.Fatalf("Run: %v", err)
	}
	_, titles, _, _ := sub.snapshot()
	if titles[0] != "untitled" {
		t.Errorf("title = %q, want the job id %q when Detail is empty", titles[0], "untitled")
	}
}

func TestWorkflowTaskMissingSpecReturnsError(t *testing.T) {
	s := newStore(t)
	sub := &fakeSubmitter{}
	runner, err := NewWorkflowTask(s, sub)
	if err != nil {
		t.Fatalf("NewWorkflowTask: %v", err)
	}
	err = runner.Run(t.Context(), scheduling.Job{ID: "never-created"})
	if err == nil {
		t.Fatal("Run with a missing spec returned nil, want an error")
	}
	if _, _, _, calls := sub.snapshot(); calls != 0 {
		t.Errorf("submitter was called %d times for a missing spec, want 0", calls)
	}
}

func TestWorkflowTaskPropagatesSubmitError(t *testing.T) {
	s := newStore(t)
	wantErr := errors.New("intake unavailable")
	sub := &fakeSubmitter{err: wantErr}
	runner, err := NewWorkflowTask(s, sub)
	if err != nil {
		t.Fatalf("NewWorkflowTask: %v", err)
	}
	job := createJob(t, s, workflowSpec("nightly", "nightly maintenance", "run"))

	err = runner.Run(t.Context(), job)
	if !errors.Is(err, wantErr) {
		t.Errorf("Run = %v, want it to wrap %v", err, wantErr)
	}
}

func TestWorkflowTaskHonoursCancellation(t *testing.T) {
	t.Run("cancelled before the run submits nothing", func(t *testing.T) {
		// Both the lookup and the submitter ignore ctx, so only the
		// runner's own guard can stop the submit.
		lookup := &fakeLookup{specs: map[string]cronstore.JobSpec{
			"cancel-me": workflowSpec("cancel-me", "cancel me", "body"),
		}}
		sub := &naiveSubmitter{}
		runner, err := NewWorkflowTask(lookup, sub)
		if err != nil {
			t.Fatalf("NewWorkflowTask: %v", err)
		}

		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		err = runner.Run(cancelled, scheduling.Job{ID: "cancel-me"})
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Run on a cancelled ctx = %v, want context.Canceled", err)
		}
		if calls := sub.count(); calls != 0 {
			t.Errorf("submitter was called %d times on a cancelled ctx, want 0", calls)
		}
	})

	t.Run("cancelled mid-submit returns the context error", func(t *testing.T) {
		s := newStore(t)
		entered := make(chan struct{})
		never := make(chan struct{})
		defer close(never)
		sub := &fakeSubmitter{entered: entered, block: never}
		runner, err := NewWorkflowTask(s, sub)
		if err != nil {
			t.Fatalf("NewWorkflowTask: %v", err)
		}
		job := createJob(t, s, workflowSpec("cancel-mid", "cancel me", "body"))

		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- runner.Run(ctx, job) }()

		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			cancel()
			t.Fatal("submitter was never entered; Run did not reach the submit")
		}
		cancel()

		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Errorf("Run cancelled mid-submit = %v, want context.Canceled", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Run did not return after cancellation; it ignored ctx.Done()")
		}
	})
}
