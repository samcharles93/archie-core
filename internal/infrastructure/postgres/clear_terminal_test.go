package postgres

import (
	"testing"

	"github.com/samcharles93/archie-core/internal/taskstate"
)

// ClearTerminalTasks deletes exactly the statuses taskstate.Terminal names, so
// the SQL list cannot fall behind a new terminal status.
func TestClearTerminalTasksMatchesTaskstate(t *testing.T) {
	s := storeFor(t)
	statuses := taskstate.Statuses()
	for i, meta := range statuses {
		if _, err := s.EnqueueIssue(t.Context(), "acme", "widgets", i+1, "t", "b", "", ""); err != nil {
			t.Fatal(err)
		}
		if _, err := s.pool.Exec(t.Context(), "UPDATE tasks SET status = $1 WHERE issue_number = $2", meta.ID, i+1); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.ClearTerminalTasks(t.Context()); err != nil {
		t.Fatal(err)
	}
	for i, meta := range statuses {
		task, err := s.TaskByIssue(t.Context(), "acme", "widgets", i+1)
		if err != nil {
			t.Fatal(err)
		}
		if kept := task != nil; kept == taskstate.Terminal(meta.ID) {
			t.Errorf("status %q: kept = %v, want kept only when not terminal", meta.ID, kept)
		}
	}
}
