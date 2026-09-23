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
