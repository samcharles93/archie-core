package postgres

import (
	"errors"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
)

// Tests for the review gate response write
// (docs/prds/pr-review-operator-response.md, Decision 2): one guarded
// requeue beside RetryTask/BeginRemediation. The rereview cap is enforced in
// the row, so a race cannot spend a round the cap forbids.

// waitingTaskWithGate claims a task, runs it to waiting_human and writes a
// gate document on it -- the state an operator's response acts on.
func waitingTaskWithGate(t *testing.T, s *Store) *workflow.Task {
	t.Helper()
	execution := runningTask(t, s)
	if err := s.Transition(t.Context(), execution.ID, workflow.StatusRunning, workflow.StatusWaitingHuman, "awaiting operator review"); err != nil {
		t.Fatalf("Transition to waiting_human: %v", err)
	}
	if err := s.Update(t.Context(), &workflow.Task{
		ID: execution.ID, Workflow: execution.Workflow, ReviewGate: `{"findings":[{"title":"f0"}]}`,
	}); err != nil {
		t.Fatalf("write gate offer: %v", err)
	}
	refreshed, err := s.TaskByID(t.Context(), execution.ID)
	if err != nil || refreshed == nil {
		t.Fatalf("TaskByID = (%v, %v)", refreshed, err)
	}
	return refreshed
}

func TestRespondReviewGateIncrementsOnlyForARereview(t *testing.T) {
	s := storeFor(t)
	task := waitingTaskWithGate(t, s)

	if err := s.RespondReviewGate(t.Context(), task.ID, workflow.StatusWaitingHuman, `{"outcome":"approve"}`, false, 2); err != nil {
		t.Fatalf("RespondReviewGate(approve): %v", err)
	}
	got, err := s.TaskByID(t.Context(), task.ID)
	if err != nil || got == nil {
		t.Fatalf("TaskByID = (%v, %v)", got, err)
	}
	if got.Status != workflow.StatusQueued {
		t.Fatalf("status after approve = %q, want %q: the response requeues", got.Status, workflow.StatusQueued)
	}
	if got.RereviewRounds != 0 {
		t.Fatalf("rereview_rounds after approve = %d, want 0: an approve counts no round", got.RereviewRounds)
	}
	if got.ReviewGate != `{"outcome":"approve"}` {
		t.Fatalf("review_gate after approve = %q, want the caller's document", got.ReviewGate)
	}
	if got.Workflow != task.Workflow {
		t.Fatalf("workflow after approve = %q, want %q (the wait resumes the workflow the wait names)", got.Workflow, task.Workflow)
	}

	// Requeue again to waiting, then answer with a re-review: the counter moves.
	if err := s.Transition(t.Context(), task.ID, workflow.StatusQueued, workflow.StatusRunning, ""); err != nil {
		t.Fatalf("claim again: %v", err)
	}
	if err := s.Transition(t.Context(), task.ID, workflow.StatusRunning, workflow.StatusWaitingHuman, ""); err != nil {
		t.Fatalf("wait again: %v", err)
	}
	if err := s.RespondReviewGate(t.Context(), task.ID, workflow.StatusWaitingHuman, `{"outcome":"rereview","instructions":"look again"}`, true, 2); err != nil {
		t.Fatalf("RespondReviewGate(rereview): %v", err)
	}
	got, err = s.TaskByID(t.Context(), task.ID)
	if err != nil || got == nil {
		t.Fatalf("TaskByID = (%v, %v)", got, err)
	}
	if got.RereviewRounds != 1 {
		t.Fatalf("rereview_rounds after one re-review = %d, want 1 (and it must survive the store round trip)", got.RereviewRounds)
	}
	if got.Status != workflow.StatusQueued {
		t.Fatalf("status after re-review = %q, want %q", got.Status, workflow.StatusQueued)
	}
	if got.ReviewGate != `{"outcome":"rereview","instructions":"look again"}` {
		t.Fatalf("review_gate after re-review = %q, want the caller's document", got.ReviewGate)
	}
}

func TestRespondReviewGateAtCapRefusedAndStaysWaiting(t *testing.T) {
	s := storeFor(t)
	task := waitingTaskWithGate(t, s)

	// Spend both rounds: two waiting_human gates answered with re-reviews.
	for range 2 {
		if err := s.RespondReviewGate(t.Context(), task.ID, workflow.StatusWaitingHuman, `{"outcome":"rereview"}`, true, 2); err != nil {
			t.Fatalf("RespondReviewGate(reviewer round %d): %v", task.RereviewRounds, err)
		}
		if err := s.Transition(t.Context(), task.ID, workflow.StatusQueued, workflow.StatusRunning, ""); err != nil {
			t.Fatalf("claim again: %v", err)
		}
		if err := s.Transition(t.Context(), task.ID, workflow.StatusRunning, workflow.StatusWaitingHuman, ""); err != nil {
			t.Fatalf("wait again: %v", err)
		}
	}

	before, err := s.TaskByID(t.Context(), task.ID)
	if err != nil || before == nil {
		t.Fatalf("TaskByID = (%v, %v)", before, err)
	}
	err = s.RespondReviewGate(t.Context(), task.ID, workflow.StatusWaitingHuman, `{"outcome":"rereview"}`, true, 2)
	if !errors.Is(err, storecontract.ErrRereviewCapReached) {
		t.Fatalf("RespondReviewGate at cap = %v, want ErrRereviewCapReached", err)
	}
	after, err := s.TaskByID(t.Context(), task.ID)
	if err != nil || after == nil {
		t.Fatalf("TaskByID = (%v, %v)", after, err)
	}
	if after.Status != workflow.StatusWaitingHuman {
		t.Fatalf("status after refused re-review = %q, want %q: the task stays waiting", after.Status, workflow.StatusWaitingHuman)
	}
	if after.RereviewRounds != before.RereviewRounds || after.ReviewGate != before.ReviewGate {
		t.Fatalf("refused re-review changed the row: rounds %d->%d, gate %q->%q",
			before.RereviewRounds, after.RereviewRounds, before.ReviewGate, after.ReviewGate)
	}
}

func TestRespondReviewGateStaleFromStatusWritesNothing(t *testing.T) {
	s := storeFor(t)
	task := waitingTaskWithGate(t, s)

	err := s.RespondReviewGate(t.Context(), task.ID, workflow.StatusParked, `{"outcome":"approve"}`, false, 2)
	if !errors.Is(err, storecontract.ErrStaleTransition) {
		t.Fatalf("RespondReviewGate from parked = %v, want ErrStaleTransition", err)
	}
	got, err := s.TaskByID(t.Context(), task.ID)
	if err != nil || got == nil {
		t.Fatalf("TaskByID = (%v, %v)", got, err)
	}
	if got.Status != workflow.StatusWaitingHuman || got.ReviewGate != `{"findings":[{"title":"f0"}]}` {
		t.Fatalf("refused write changed the row: status=%q gate=%q", got.Status, got.ReviewGate)
	}
}
