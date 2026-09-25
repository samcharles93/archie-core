package postgres

import (
	"testing"

	"github.com/samcharles93/archie-core/internal/taskstate"
)

// TestWorkflowStatsCountsCompletedRuns pins the per-workflow count the
// transition table made reachable: a run that finishes with no pull request
// (a no-change build, a triage that needed no code) ends in taskstate.Completed,
// so the workflows page has to be able to see those runs at all. The count is a
// status filter in SQL, which fails silently when it is wrong -- a typo'd or
// missing filter reads as zero, not as an error -- so it is asserted against the
// store rather than left to the dashboard to notice.
func TestWorkflowStatsCountsCompletedRuns(t *testing.T) {
	pool, q := migrated(t)
	store := New(pool)

	finished := insertChatTask(t, q)
	merged := insertChatTask(t, q)
	finish := func(id int64, to string) {
		t.Helper()
		if err := store.Transition(t.Context(), id, taskstate.Queued, taskstate.Running, ""); err != nil {
			t.Fatalf("queued -> running: %v", err)
		}
		if err := store.Transition(t.Context(), id, taskstate.Running, to, ""); err != nil {
			t.Fatalf("running -> %s: %v", to, err)
		}
	}
	finish(finished.ID, taskstate.Completed)
	finish(merged.ID, taskstate.Merged)

	stats, err := store.WorkflowStats(t.Context())
	if err != nil {
		t.Fatalf("WorkflowStats: %v", err)
	}
	if len(stats) != 1 {
		t.Fatalf("WorkflowStats = %+v, want one row for %q", stats, finished.Workflow)
	}
	got := stats[0]
	if got.Workflow != merged.Workflow {
		t.Fatalf("WorkflowStats row is %q, want %q", got.Workflow, merged.Workflow)
	}
	if got.Runs != 2 || got.Completed != 1 || got.Merged != 1 {
		t.Errorf("WorkflowStats = %+v, want runs 2, completed 1, merged 1: the completed count has to be its own status, not a share of merged", got)
	}
}
