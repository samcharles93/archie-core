package store

import (
	"context"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/events"
)

func TestTaskByIssue(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	if got, err := s.TaskByIssue(ctx, "acme", "widget", 1); err != nil || got != nil {
		t.Fatalf("TaskByIssue on empty store = (%+v, %v)", got, err)
	}

	if _, err := s.EnqueueIssue(ctx, "acme", "widget", 1, "title", "body", "bug", ""); err != nil {
		t.Fatal(err)
	}
	got, err := s.TaskByIssue(ctx, "acme", "widget", 1)
	if err != nil || got == nil {
		t.Fatalf("TaskByIssue = (%+v, %v)", got, err)
	}
	if got.Title != "title" || got.IssueNumber != 1 {
		t.Fatalf("TaskByIssue = %+v", got)
	}

	if got, err := s.TaskByIssue(ctx, "acme", "widget", 2); err != nil || got != nil {
		t.Fatalf("TaskByIssue for missing issue = (%+v, %v)", got, err)
	}
}

func TestWaitingTasksAndRequeue(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	if _, err := s.EnqueueIssue(ctx, "acme", "widget", 1, "t", "b", "", ""); err != nil {
		t.Fatal(err)
	}
	task, err := s.ClaimNext(ctx)
	if err != nil || task == nil {
		t.Fatalf("claim = (%v, %v)", task, err)
	}

	if waiting, err := s.WaitingTasks(ctx); err != nil || len(waiting) != 0 {
		t.Fatalf("WaitingTasks before transition = (%+v, %v)", waiting, err)
	}

	if err := s.Transition(ctx, task.ID, StatusRunning, StatusWaitingHuman, "needs input"); err != nil {
		t.Fatal(err)
	}
	waiting, err := s.WaitingTasks(ctx)
	if err != nil || len(waiting) != 1 || waiting[0].ID != task.ID {
		t.Fatalf("WaitingTasks = (%+v, %v)", waiting, err)
	}

	// Requeue with a workflow forces it (waiting_human -> approved -> implement).
	if err := s.Requeue(ctx, task.ID, StatusWaitingHuman, "implement"); err != nil {
		t.Fatal(err)
	}
	requeued, err := s.TaskByIssue(ctx, "acme", "widget", 1)
	if err != nil || requeued == nil || requeued.Status != StatusQueued || requeued.Workflow != "implement" {
		t.Fatalf("after forced requeue = %+v, %v", requeued, err)
	}

	// Requeue with an empty workflow keeps the task's current workflow
	// (retrying a parked task).
	task2, err := s.ClaimNext(ctx)
	if err != nil || task2 == nil {
		t.Fatalf("second claim = (%v, %v)", task2, err)
	}
	task2.Workflow = "feasibility"
	if err := s.Update(ctx, task2); err != nil {
		t.Fatal(err)
	}
	if err := s.Transition(ctx, task2.ID, StatusRunning, StatusParked, "parked"); err != nil {
		t.Fatal(err)
	}
	if err := s.Requeue(ctx, task2.ID, StatusParked, ""); err != nil {
		t.Fatal(err)
	}
	retried, err := s.TaskByIssue(ctx, "acme", "widget", 1)
	if err != nil || retried == nil || retried.Status != StatusQueued || retried.Workflow != "feasibility" {
		t.Fatalf("after empty-workflow requeue = %+v, %v", retried, err)
	}
	if retried.ParkReason != "" {
		t.Fatalf("Requeue must clear park_reason, got %q", retried.ParkReason)
	}

	// Verify retry_count is NOT reset by Requeue (it persists).
	if err := s.IncrementRetryCount(ctx, task2.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.IncrementRetryCount(ctx, task2.ID); err != nil {
		t.Fatal(err)
	}
	// Park again and requeue.
	if err := s.Transition(ctx, task2.ID, StatusQueued, StatusParked, "parked again"); err != nil {
		t.Fatal(err)
	}
	if err := s.Requeue(ctx, task2.ID, StatusParked, ""); err != nil {
		t.Fatal(err)
	}
	afterRequeue, err := s.TaskByIssue(ctx, "acme", "widget", 1)
	if err != nil || afterRequeue == nil {
		t.Fatalf("TaskByIssue after second requeue = (%+v, %v)", afterRequeue, err)
	}
	if afterRequeue.RetryCount != 2 {
		t.Fatalf("Requeue must not reset retry_count, got %d, want 2", afterRequeue.RetryCount)
	}
}

func TestTasksListingAndStatusCounts(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	for i, status := range []string{StatusQueued, StatusRunning, StatusPROpen} {
		if _, err := s.EnqueueIssue(ctx, "acme", "widget", i+1, "t", "b", "", ""); err != nil {
			t.Fatal(err)
		}
		task, err := s.TaskByIssue(ctx, "acme", "widget", i+1)
		if err != nil || task == nil {
			t.Fatalf("TaskByIssue(%d) = (%+v, %v)", i+1, task, err)
		}
		if status != StatusQueued {
			if err := s.Transition(ctx, task.ID, StatusQueued, status, ""); err != nil {
				t.Fatal(err)
			}
		}
	}

	tasks, err := s.Tasks(ctx, 10)
	if err != nil || len(tasks) != 3 {
		t.Fatalf("Tasks = (%d, %v)", len(tasks), err)
	}

	limited, err := s.Tasks(ctx, 2)
	if err != nil || len(limited) != 2 {
		t.Fatalf("Tasks with limit = (%d, %v)", len(limited), err)
	}

	counts, err := s.StatusCounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{StatusQueued: 1, StatusRunning: 1, StatusPROpen: 1}
	for status, n := range want {
		if counts[status] != n {
			t.Fatalf("StatusCounts[%s] = %d, want %d (all: %+v)", status, counts[status], n, counts)
		}
	}
}

func TestWorkflowStats(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	if _, err := s.EnqueueIssue(ctx, "acme", "widget", 1, "t", "b", "", ""); err != nil {
		t.Fatal(err)
	}
	task, err := s.ClaimNext(ctx)
	if err != nil || task == nil {
		t.Fatalf("claim = (%v, %v)", task, err)
	}
	task.Workflow = "implement"
	task.TokensUsed = 1000
	task.Iterations = 4
	if err := s.Update(ctx, task); err != nil {
		t.Fatal(err)
	}
	if err := s.Transition(ctx, task.ID, StatusRunning, StatusMerged, "merged"); err != nil {
		t.Fatal(err)
	}

	stats, err := s.WorkflowStats(ctx)
	if err != nil || len(stats) != 1 {
		t.Fatalf("WorkflowStats = (%+v, %v)", stats, err)
	}
	got := stats[0]
	if got.Workflow != "implement" || got.Runs != 1 || got.Merged != 1 || got.AvgTokens != 1000 || got.TotalToken != 1000 {
		t.Fatalf("WorkflowStats[0] = %+v", got)
	}
}

func TestRecentEvents(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	for range 3 {
		if _, err := s.InsertEvent(ctx, events.Event{Kind: "log", Detail: "line"}); err != nil {
			t.Fatal(err)
		}
	}

	recent, err := s.RecentEvents(ctx, 2)
	if err != nil || len(recent) != 2 {
		t.Fatalf("RecentEvents = (%d, %v)", len(recent), err)
	}
	// Newest first.
	if recent[0].ID <= recent[1].ID {
		t.Fatalf("RecentEvents not newest-first: %+v", recent)
	}
}

func TestTokensByDay(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	today := time.Now().UTC()
	yesterday := today.AddDate(0, 0, -1)

	if _, err := s.InsertEvent(ctx, events.Event{
		Kind: "agent_finish", At: today, Data: map[string]any{"tokens": 100},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertEvent(ctx, events.Event{
		Kind: "agent_finish", At: today, Data: map[string]any{"tokens": 50},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertEvent(ctx, events.Event{
		Kind: "agent_finish", At: yesterday, Data: map[string]any{"tokens": 25},
	}); err != nil {
		t.Fatal(err)
	}

	byDay, err := s.TokensByDay(ctx, 14)
	if err != nil || len(byDay) != 2 {
		t.Fatalf("TokensByDay = (%+v, %v)", byDay, err)
	}
	// Newest day first.
	if byDay[0].Day != today.Format("2006-01-02") || byDay[0].Tokens != 150 {
		t.Fatalf("TokensByDay[0] = %+v", byDay[0])
	}
	if byDay[1].Day != yesterday.Format("2006-01-02") || byDay[1].Tokens != 25 {
		t.Fatalf("TokensByDay[1] = %+v", byDay[1])
	}

	limited, err := s.TokensByDay(ctx, 1)
	if err != nil || len(limited) != 1 {
		t.Fatalf("TokensByDay with limit = (%+v, %v)", limited, err)
	}
}

func TestStageStats(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	// Insert 6 stage_finish events across 2 workflows × 3 stage groups.
	// plan/draft: 3 runs, avg 400 ms, 1 error
	// plan/review: 1 run, avg 300 ms, 0 errors
	// implement/build: 2 runs, avg 1500 ms, 1 error

	type stageEvent struct {
		workflow   string
		stage      string
		durationMs int
		errorMsg   string // empty means no error key
	}

	inputs := []stageEvent{
		{workflow: "plan", stage: "draft", durationMs: 200},
		{workflow: "plan", stage: "draft", durationMs: 400},
		{workflow: "plan", stage: "draft", durationMs: 600, errorMsg: "something broke"},

		{workflow: "plan", stage: "review", durationMs: 300},

		{workflow: "implement", stage: "build", durationMs: 1000},
		{workflow: "implement", stage: "build", durationMs: 2000, errorMsg: "build failed"},
	}

	for _, in := range inputs {
		data := map[string]any{"duration_ms": in.durationMs}
		if in.errorMsg != "" {
			data["error"] = in.errorMsg
		}
		if _, err := s.InsertEvent(ctx, events.Event{
			Kind:     "stage_finish",
			Workflow: in.workflow,
			Stage:    in.stage,
			Data:     data,
		}); err != nil {
			t.Fatal(err)
		}
	}

	stats, err := s.StageStats(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if len(stats) != 3 {
		t.Fatalf("StageStats: got %d groups, want 3: %+v", len(stats), stats)
	}

	// Verify ordering: workflow then stage.
	want := []struct {
		workflow string
		stage    string
		runs     int
		avgMs    int
		errors   int
	}{
		{workflow: "implement", stage: "build", runs: 2, avgMs: 1500, errors: 1},
		{workflow: "plan", stage: "draft", runs: 3, avgMs: 400, errors: 1},
		{workflow: "plan", stage: "review", runs: 1, avgMs: 300, errors: 0},
	}

	for i, w := range want {
		got := stats[i]
		if got.Workflow != w.workflow {
			t.Fatalf("stats[%d].Workflow = %q, want %q", i, got.Workflow, w.workflow)
		}
		if got.Stage != w.stage {
			t.Fatalf("stats[%d].Stage = %q, want %q", i, got.Stage, w.stage)
		}
		if got.Runs != w.runs {
			t.Fatalf("stats[%d].Runs = %d, want %d", i, got.Runs, w.runs)
		}
		if got.AvgMs != w.avgMs {
			t.Fatalf("stats[%d].AvgMs = %d, want %d", i, got.AvgMs, w.avgMs)
		}
		if got.Errors != w.errors {
			t.Fatalf("stats[%d].Errors = %d, want %d", i, got.Errors, w.errors)
		}
	}
}
