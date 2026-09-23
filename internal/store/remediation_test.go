package store

import (
	"errors"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/workflow"
)

// prOpenTask inserts a task and walks it to the state a reaction resolves
// against: the task opened its PR and is idle waiting for review.
func prOpenTask(t *testing.T, s *Store, owner, repo string, prNumber int) int64 {
	t.Helper()
	ok, err := s.EnqueueIssue(t.Context(), owner, repo, 1, "title", "body", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("enqueue: task already tracked")
	}
	task, err := s.TaskByIssue(t.Context(), owner, repo, 1)
	if err != nil || task == nil {
		t.Fatalf("TaskByIssue = (%v, %v)", task, err)
	}
	task.PRNumber = prNumber
	task.Status = workflow.StatusPROpen
	if err := s.Transition(t.Context(), task.ID, workflow.StatusQueued, workflow.StatusPROpen, "PR opened"); err != nil {
		t.Fatal(err)
	}
	if err := s.Update(t.Context(), task); err != nil {
		t.Fatal(err)
	}
	return task.ID
}

func TestBeginRemediationMovesAnOpenPRTaskToQueuedRemediate(t *testing.T) {
	s := openTest(t)
	id := prOpenTask(t, s, "acme", "widgets", 42)

	if err := s.BeginRemediation(t.Context(), id, `{"review_id":7}`); err != nil {
		t.Fatalf("BeginRemediation: %v", err)
	}
	got, err := s.TaskByID(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != workflow.StatusQueued {
		t.Errorf("status = %q, want queued", got.Status)
	}
	if got.Workflow != "remediate" {
		t.Errorf("workflow = %q, want remediate", got.Workflow)
	}
	if got.ReviewPayload != `{"review_id":7}` {
		t.Errorf("review_payload = %q, want the carried unit", got.ReviewPayload)
	}
	if got.ParkReason != "" || got.Stage != "" {
		t.Errorf("park_reason/stage = %q/%q, want both empty", got.ParkReason, got.Stage)
	}
}

func TestBeginRemediationRefusesAnAlreadyClaimedTask(t *testing.T) {
	s := openTest(t)
	id := prOpenTask(t, s, "acme", "widgets", 42)
	if err := s.BeginRemediation(t.Context(), id, `{"review_id":7}`); err != nil {
		t.Fatal(err)
	}
	if err := s.BeginRemediation(t.Context(), id, `{"review_id":7}`); !errors.Is(err, ErrStaleTransition) {
		t.Errorf("second BeginRemediation err = %v, want ErrStaleTransition", err)
	}
}

func TestBeginRemediationRefusesNonOpenPRStatuses(t *testing.T) {
	s := openTest(t)
	id := prOpenTask(t, s, "acme", "widgets", 42)
	if err := s.Transition(t.Context(), id, workflow.StatusPROpen, workflow.StatusParked, "gate failed"); err != nil {
		t.Fatal(err)
	}
	if err := s.BeginRemediation(t.Context(), id, `{"review_id":7}`); !errors.Is(err, ErrStaleTransition) {
		t.Errorf("BeginRemediation on parked err = %v, want ErrStaleTransition", err)
	}
}

func TestUpdateReviewPayloadAppendsOnlyWhileARemediationIsQueued(t *testing.T) {
	s := openTest(t)
	id := prOpenTask(t, s, "acme", "widgets", 42)
	if err := s.BeginRemediation(t.Context(), id, `{"review_id":7}`); err != nil {
		t.Fatal(err)
	}

	if err := s.UpdateReviewPayload(t.Context(), id, `{"review_id":7,"comments":[{"comment_id":9}]}`); err != nil {
		t.Fatalf("UpdateReviewPayload while queued: %v", err)
	}
	got, err := s.TaskByID(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if got.ReviewPayload != `{"review_id":7,"comments":[{"comment_id":9}]}` {
		t.Errorf("review_payload = %q, want the appended unit", got.ReviewPayload)
	}

	// Claim the task: the remediation is running, and a payload written under
	// it would race the builder's input.
	if _, err := s.ClaimNext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateReviewPayload(t.Context(), id, `{"review_id":7,"comments":[{"comment_id":10}]}`); !errors.Is(err, ErrStaleTransition) {
		t.Errorf("UpdateReviewPayload while running err = %v, want ErrStaleTransition", err)
	}
}

// TestParkTaskWritesItsClassification pins the classified park write: the
// class is recorded at the park site in the same guarded write, and an
// unclassified park defaults to needs_human.
func TestParkTaskWritesItsClassification(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	if _, err := s.EnqueueIssue(ctx, "acme", "widgets", 1, "t", "b", "", ""); err != nil {
		t.Fatal(err)
	}
	task, _ := s.TaskByIssue(ctx, "acme", "widgets", 1)
	if _, err := s.ClaimNext(ctx); err != nil {
		t.Fatal(err)
	}

	if err := s.ParkTask(ctx, task.ID, workflow.StatusRunning, "container acquire failed", "transient"); err != nil {
		t.Fatalf("ParkTask: %v", err)
	}
	got, err := s.TaskByID(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != workflow.StatusParked || got.ParkReason != "container acquire failed" {
		t.Errorf("park row = status:%q reason:%q", got.Status, got.ParkReason)
	}
	if got.ParkClass != "transient" {
		t.Errorf("park_class = %q, want transient", got.ParkClass)
	}

	// Requeue clears the class with the reason: a stale class on a queued
	// task would group a live task with parked ones.
	if err := s.Requeue(ctx, task.ID, workflow.StatusParked, ""); err != nil {
		t.Fatal(err)
	}
	got, _ = s.TaskByID(ctx, task.ID)
	if got.ParkClass != "needs_human" || got.ParkReason != "" {
		t.Errorf("after requeue: class=%q reason=%q, want cleared", got.ParkClass, got.ParkReason)
	}
}

func TestParkTaskRejectsAnUnknownClass(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	if _, err := s.EnqueueIssue(ctx, "acme", "widgets", 1, "t", "b", "", ""); err != nil {
		t.Fatal(err)
	}
	task, _ := s.TaskByIssue(ctx, "acme", "widgets", 1)
	if _, err := s.ClaimNext(ctx); err != nil {
		t.Fatal(err)
	}
	// An unknown class normalizes to needs_human rather than persisting a
	// value no consumer knows how to group.
	if err := s.ParkTask(ctx, task.ID, workflow.StatusRunning, "x", "urgent"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.TaskByID(ctx, task.ID)
	if got.ParkClass != "needs_human" {
		t.Errorf("park_class = %q, want the needs_human fallback", got.ParkClass)
	}
}

func TestParkTaskStaleGuard(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	if _, err := s.EnqueueIssue(ctx, "acme", "widgets", 1, "t", "b", "", ""); err != nil {
		t.Fatal(err)
	}
	task, _ := s.TaskByIssue(ctx, "acme", "widgets", 1)
	// Still queued, not running: the from guard must refuse.
	if err := s.ParkTask(ctx, task.ID, workflow.StatusRunning, "x", "transient"); !errors.Is(err, ErrStaleTransition) {
		t.Errorf("ParkTask stale err = %v, want ErrStaleTransition", err)
	}
}

func TestTransitionToParkedDefaultsTheClass(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	if _, err := s.EnqueueIssue(ctx, "acme", "widgets", 1, "t", "b", "", ""); err != nil {
		t.Fatal(err)
	}
	task, _ := s.TaskByIssue(ctx, "acme", "widgets", 1)
	if _, err := s.ClaimNext(ctx); err != nil {
		t.Fatal(err)
	}
	// The generic transition is the workflow engine's park path: every
	// unclassified park reads as operator-actionable.
	if err := s.Transition(ctx, task.ID, workflow.StatusRunning, workflow.StatusParked, "gate failed"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.TaskByID(ctx, task.ID)
	if got.ParkClass != "needs_human" {
		t.Errorf("park_class = %q, want the needs_human default", got.ParkClass)
	}
}

func TestUpdateCarriesRemediationRounds(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	if _, err := s.EnqueueIssue(ctx, "acme", "widgets", 1, "t", "b", "", ""); err != nil {
		t.Fatal(err)
	}
	task, _ := s.TaskByIssue(ctx, "acme", "widgets", 1)
	task.RemediationRounds = 2
	if err := s.Update(ctx, task); err != nil {
		t.Fatal(err)
	}
	got, _ := s.TaskByID(ctx, task.ID)
	if got.RemediationRounds != 2 {
		t.Errorf("remediation_rounds = %d, want 2", got.RemediationRounds)
	}
}
