package store

import (
	"context"
	"testing"
)

// Tasks() is what the dashboard's task list is built from, and the UI reads
// two fields the query never selected: Plan, which gates the "Decision
// required" panel on a waiting_human task, and Source, which decides whether
// to render a forge issue link at all.
//
// Both came back as the zero value, so the panel could never appear and a
// chat-sourced task -- whose issue_number is synthetic -- rendered a link to
// an issue that does not exist. Neither failed loudly; the features were
// simply dead.
func TestTasksSelectsFieldsTheUIReads(t *testing.T) {
	ctx := context.Background()
	s := OpenTest(t)
	t.Cleanup(func() { _ = s.Close() })

	if _, err := s.EnqueueIssue(ctx, "o", "r", 7, "forge task", "body", "", ""); err != nil {
		t.Fatalf("EnqueueIssue: %v", err)
	}
	forgeTaskRow, err := s.TaskByIssue(ctx, "o", "r", 7)
	if err != nil || forgeTaskRow == nil {
		t.Fatalf("TaskByIssue: %v", err)
	}
	forgeID := forgeTaskRow.ID

	chatTaskRow, err := s.EnqueueChatTask(ctx, "o", "r", "chat task", "body", "", "")
	if err != nil {
		t.Fatalf("EnqueueChatTask: %v", err)
	}
	chatID := chatTaskRow.ID

	// A plan is what an operator approves or rejects, so it has to survive
	// the read the approval UI is built on.
	const plan = "1. add the thing\n2. test the thing"
	forgeTaskRow.Plan = plan
	if err := s.Update(ctx, forgeTaskRow); err != nil {
		t.Fatalf("Update: %v", err)
	}

	tasks, err := s.Tasks(ctx, 50)
	if err != nil {
		t.Fatalf("Tasks: %v", err)
	}

	byID := make(map[int64]Task, len(tasks))
	for _, task := range tasks {
		byID[task.ID] = task
	}

	forgeTask, ok := byID[forgeID]
	if !ok {
		t.Fatalf("forge task %d missing from Tasks()", forgeID)
	}
	if forgeTask.Plan != plan {
		t.Errorf("Plan = %q, want %q: the approval panel is gated on this and "+
			"can never render while it is empty", forgeTask.Plan, plan)
	}
	if forgeTask.Source != SourceForge {
		t.Errorf("Source = %q, want %q", forgeTask.Source, SourceForge)
	}

	chatTask, ok := byID[chatID]
	if !ok {
		t.Fatalf("chat task %d missing from Tasks()", chatID)
	}
	if chatTask.Source != SourceChat {
		t.Errorf("Source = %q, want %q: without it the UI links a synthetic "+
			"issue number to a forge issue that does not exist",
			chatTask.Source, SourceChat)
	}
	if chatTask.IsForgeBacked() {
		t.Error("IsForgeBacked() is true for a chat task read through Tasks()")
	}
}
