package postgres_test

import (
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgstore"
	"github.com/samcharles93/archie-core/internal/taskstate"
)

// A review that arrives while a remediation owns the task is queued behind it
// and remediated when that run finishes, in arrival order. A re-delivered
// review is absorbed rather than queued to run a second time.
func TestSecondReviewDuringRemediationIsQueued(t *testing.T) {
	ctx := t.Context()
	db := pgstore.Open(t)
	id := openTaskAtPROpen(t, db)

	first, second := reviewUnit(t, 11), reviewUnit(t, 22)

	if err := db.BeginRemediation(ctx, id, first); err != nil {
		t.Fatalf("begin first remediation: %v", err)
	}
	if err := db.BeginRemediation(ctx, id, second); err != nil {
		t.Fatalf("review arriving during remediation was refused, not queued: %v", err)
	}
	if err := db.BeginRemediation(ctx, id, first); err != nil {
		t.Fatalf("re-delivered review was refused: %v", err)
	}
	assertActiveReview(t, db, id, first)

	// An inline comment on the waiting review is collected into its queued
	// unit rather than dropped, and travels with it when it is promoted.
	comment := workflow.ReviewUnitComment{CommentID: 5, Path: "main.go", Line: 3, Body: "rename this"}
	if err := db.UpdateReviewPayload(ctx, id, reviewUnitWith(t, 22, comment)); err != nil {
		t.Fatalf("comment on the queued review was refused: %v", err)
	}
	if err := db.UpdateReviewPayload(ctx, id, reviewUnitWith(t, 22, comment)); err != nil {
		t.Fatalf("re-delivered comment was refused: %v", err)
	}

	// The first round runs and finishes; the second, still waiting, is
	// promoted behind it with its comment.
	runRemediationRound(t, db, id)
	assertActiveReview(t, db, id, withComment(t, second, comment))

	// The second round runs and finds nothing left waiting.
	runRemediationRound(t, db, id)
	task, err := db.TaskByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != taskstate.PROpen {
		t.Fatalf("status after both rounds = %q, want %q (a review was queued twice)", task.Status, taskstate.PROpen)
	}
}

// openTaskAtPROpen enqueues a task, claims it and opens its pull request.
func openTaskAtPROpen(t *testing.T, db *pgstore.TaskDB) int64 {
	t.Helper()
	ctx := t.Context()
	created, err := db.EnqueueChatTask(ctx, "acme", "widget", "a task", "", "tdd", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ClaimNext(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.Transition(ctx, created.ID, taskstate.Running, taskstate.PROpen, "opened the pull request"); err != nil {
		t.Fatal(err)
	}
	return created.ID
}

// runRemediationRound claims the queued task and finishes one round of it.
func runRemediationRound(t *testing.T, db *pgstore.TaskDB, id int64) {
	t.Helper()
	ctx := t.Context()
	run, err := db.ClaimNext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if run == nil || run.ID != id {
		t.Fatalf("claim: got %v, want task %d queued", run, id)
	}
	if err := db.Transition(ctx, id, taskstate.Running, taskstate.PROpen, "remediated a review round"); err != nil {
		t.Fatal(err)
	}
}

// assertActiveReview asserts the task is queued on payload, which is the
// review a remediation would run next.
func assertActiveReview(t *testing.T, db *pgstore.TaskDB, id int64, payload string) {
	t.Helper()
	task, err := db.TaskByID(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != taskstate.Queued || task.Workflow != "remediate" {
		t.Fatalf("task is %q on workflow %q, want a queued remediation", task.Status, task.Workflow)
	}
	if task.ReviewPayload != payload {
		t.Fatalf("active review = %s, want %s", task.ReviewPayload, payload)
	}
}

func reviewUnit(t *testing.T, reviewID int64) string {
	t.Helper()
	payload, err := workflow.EncodeReviewUnit(workflow.ReviewUnit{
		ReviewID: reviewID, State: "requested_changes", Body: "please change this",
	})
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func reviewUnitWith(t *testing.T, reviewID int64, comment workflow.ReviewUnitComment) string {
	t.Helper()
	payload, err := workflow.EncodeReviewUnit(workflow.ReviewUnit{ReviewID: reviewID, Comments: []workflow.ReviewUnitComment{comment}})
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func withComment(t *testing.T, payload string, comment workflow.ReviewUnitComment) string {
	t.Helper()
	unit, err := workflow.DecodeReviewUnit(payload)
	if err != nil {
		t.Fatal(err)
	}
	unit.Comments = append(unit.Comments, comment)
	encoded, err := workflow.EncodeReviewUnit(unit)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
