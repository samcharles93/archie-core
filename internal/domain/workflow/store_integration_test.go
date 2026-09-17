package workflow_test

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/store"
)

// storeIntegrationRunner is a minimal agentexec.Runner stub, duplicated
// here (rather than reused from the in-package tests) because this file
// lives in the external workflow_test package: it needs the real
// *store.Store as a workflow.Store, and the package under test now owns
// workflow.Task/Status/Source, which would otherwise cycle back through
// internal/store's dependency on internal/domain/workflow.
type storeIntegrationRunner struct {
	result agentexec.Result
}

func (r *storeIntegrationRunner) Run(
	_ context.Context, _ string, req agentexec.Request, _ agentexec.ToolCallReporter,
) (agentexec.Result, error) {
	res := r.result
	if res.Version == 0 {
		res.Version = agentexec.ProtocolVersion
	}
	res.TaskID = req.TaskID
	res.Attempt = req.Attempt
	res.Stage = req.Stage
	return res, nil
}

// TestRunRecordsAParkReasonInTheRunsOwnLog pins the agent-side half of the
// same contract: a run's own log is what an operator downloads to answer "why
// did this park?", and a parked OUTCOME (a review that could not run, a stage
// that ended parked rather than failing) used to reach the timeline as an event
// and the store as park_reason, but never the log. A workflow that ends without
// an outcome is the smallest way to reach that path.
func TestRunRecordsAParkReasonInTheRunsOwnLog(t *testing.T) {
	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "archie.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	ctx := context.Background()
	if _, err := st.EnqueueIssue(ctx, "owner", "repo", 3, "title", "body", "", ""); err != nil {
		t.Fatal(err)
	}
	task, err := st.ClaimNext(ctx)
	if err != nil {
		t.Fatal(err)
	}

	var logs bytes.Buffer
	workflow.Run(ctx, workflow.Workflow{Name: "empty"}, &workflow.TaskContext{
		Task: task, Store: st, Repo: config.Repo{Owner: "owner", Name: "repo"},
		Log: slog.New(slog.NewJSONHandler(&logs, nil)),
	})

	const reason = "workflow ended without an outcome (definition bug)"
	if !strings.Contains(logs.String(), reason) {
		t.Errorf("run log = %s, want the park reason %q recorded in it", logs.String(), reason)
	}
	got, err := st.TaskByIssue(ctx, "owner", "repo", 3)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != workflow.StatusParked || got.ParkReason != reason {
		t.Fatalf("task status/park_reason = (%q, %q), want parked with the same reason the log carries", got.Status, got.ParkReason)
	}
}

func TestAgentStagePersistsReturnedNotes(t *testing.T) {
	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "archie.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	ctx := context.Background()
	if _, err := st.EnqueueIssue(ctx, "owner", "repo", 1, "title", "body", "", ""); err != nil {
		t.Fatal(err)
	}
	task, err := st.ClaimNext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	runner := &storeIntegrationRunner{result: agentexec.Result{
		Version: agentexec.ProtocolVersion, Status: agentexec.StatusPassed,
		AppendedNotes: []string{"checked with go test"},
	}}
	stage := workflow.AgentStage{
		Name: "build", Role: "builder", Mission: func(*workflow.TaskContext) string { return "build" },
	}.Stage()
	tc := &workflow.TaskContext{
		Task: task, Store: st, Agent: runner, Log: slog.New(slog.DiscardHandler),
		Cfg: config.Config{Models: map[string]string{"builder": "provider/model"}},
	}
	if err := stage.Run(ctx, tc); err != nil {
		t.Fatal(err)
	}
	if task.Notes != "- checked with go test\n" {
		t.Fatalf("task notes = %q", task.Notes)
	}
}

func TestRunLeavesInterruptedTaskForCrashRecovery(t *testing.T) {
	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "archie.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	if _, err := st.EnqueueIssue(context.Background(), "owner", "repo", 2, "title", "body", "", ""); err != nil {
		t.Fatal(err)
	}
	task, err := st.ClaimNext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	workflow.Run(ctx, workflow.Workflow{Name: "test", Stages: []workflow.Stage{{
		Name: "agent", Run: func(ctx context.Context, _ *workflow.TaskContext) error { return ctx.Err() },
	}}}, &workflow.TaskContext{
		Task: task, Store: st, Repo: config.Repo{Owner: "owner", Name: "repo"},
		Log: slog.New(slog.DiscardHandler),
	})
	got, err := st.TaskByIssue(context.Background(), "owner", "repo", 2)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != workflow.StatusRunning || got.ParkReason != "" {
		t.Fatalf("interrupted task status=%q park_reason=%q, want running with no park reason", got.Status, got.ParkReason)
	}
}
