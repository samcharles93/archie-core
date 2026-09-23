package store

import (
	"errors"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/workflow"
)

// TestOpenPRsCarriesReviewCursors pins the poll backstop's read side: the
// per-PR scan reads each task's persisted review and comment cursors, so a
// daemon restart resumes where the last scan stopped instead of re-listing
// every review the forge has ever returned.
func TestOpenPRsCarriesReviewCursors(t *testing.T) {
	s := openTest(t)
	id := prOpenTask(t, s, "acme", "widgets", 42)
	if err := s.SetReviewCursors(t.Context(), id, 100, 200); err != nil {
		t.Fatalf("SetReviewCursors: %v", err)
	}

	tasks, err := s.OpenPRs(t.Context())
	if err != nil || len(tasks) != 1 {
		t.Fatalf("OpenPRs = (%d tasks, %v), want 1", len(tasks), err)
	}
	if tasks[0].ReviewCursor != 100 {
		t.Errorf("ReviewCursor = %d, want 100", tasks[0].ReviewCursor)
	}
	if tasks[0].WatchCommentID != 200 {
		t.Errorf("WatchCommentID = %d, want 200 (the review-comment cursor)", tasks[0].WatchCommentID)
	}
}

// TestSetReviewCursorsGuardsOnStatus pins the write side: cursors only move
// while the task is idle in pr_open. A cursor written under a running or
// parked task would desync from what its remediation run has consumed.
func TestSetReviewCursorsRefusesNonOpenPRStatuses(t *testing.T) {
	s := openTest(t)
	id := prOpenTask(t, s, "acme", "widgets", 42)
	if err := s.Transition(t.Context(), id, workflow.StatusPROpen, workflow.StatusParked, "gate failed"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetReviewCursors(t.Context(), id, 5, 6); !errors.Is(err, ErrStaleTransition) {
		t.Errorf("SetReviewCursors on parked err = %v, want ErrStaleTransition", err)
	}
}
