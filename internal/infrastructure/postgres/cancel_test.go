package postgres

import (
	"errors"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	task "github.com/samcharles93/archie-core/internal/domain/workflow/task"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/taskstate"
)

// CancelExecution is the one cancel path (docs/prds/execution-tree-state-machine.md,
// "Cancellation"): one transaction cancels every non-terminal StepExecution of
// the execution's current attempt and moves the execution to the status the
// operator action names, with one event row per step transition and the
// execution's own audit row in the same write.

func TestCancelExecutionCancelsTheAttemptsStepsAndTheExecution(t *testing.T) {
	s := storeFor(t)
	execution := runningExecution(t, s)

	stageID, _, err := s.StartStep(t.Context(), task.StepStart{
		ExecutionID: execution.ID, Attempt: execution.Attempt, Kind: task.StepKindStage, Name: "review",
	})
	if err != nil {
		t.Fatalf("StartStep stage: %v", err)
	}
	childID, _, err := s.StartStep(t.Context(), task.StepStart{
		ExecutionID: execution.ID, Attempt: execution.Attempt,
		ParentID: stageID, Kind: task.StepKindAgent, Name: "review:1",
	})
	if err != nil {
		t.Fatalf("StartStep child: %v", err)
	}
	if _, err := s.FinishStep(t.Context(), task.StepFinish{
		StepID: stageID, ExecutionID: execution.ID,
		From: taskstate.StepRunning, To: taskstate.StepSucceeded, Detail: "done",
	}); err != nil {
		t.Fatalf("FinishStep: %v", err)
	}

	steps, err := s.CancelExecution(t.Context(), execution.ID, "stopped by operator", taskstate.Parked)
	if err != nil {
		t.Fatalf("CancelExecution: %v", err)
	}
	if len(steps) != 1 || steps[0] != childID {
		t.Fatalf("cancelled steps = %v, want only the non-terminal %d", steps, childID)
	}

	// Every non-terminal step of the current attempt is cancelled; the one
	// that already finished is never rewritten.
	var statuses map[int64]string
	if err := s.pool.QueryRow(t.Context(),
		`SELECT jsonb_object_agg(id, status) FROM step_executions WHERE execution_id = $1`,
		execution.ID).Scan(&statuses); err != nil {
		t.Fatalf("read steps: %v", err)
	}
	if statuses[stageID] != string(taskstate.StepSucceeded) || statuses[childID] != string(taskstate.StepCancelled) {
		t.Fatalf("statuses = %v, want the finished step kept and the child cancelled", statuses)
	}

	got, err := s.TaskByID(t.Context(), execution.ID)
	if err != nil || got == nil {
		t.Fatalf("TaskByID: %+v %v", got, err)
	}
	if got.Status != taskstate.Parked || got.ParkReason != "stopped by operator" {
		t.Fatalf("execution after cancel = %s (%q), want parked with the reason", got.Status, got.ParkReason)
	}
}

func TestCancelExecutionWritesOneEventPerCancelledStep(t *testing.T) {
	s := storeFor(t)
	execution := runningExecution(t, s)

	stageID, _, err := s.StartStep(t.Context(), task.StepStart{
		ExecutionID: execution.ID, Attempt: execution.Attempt, Kind: task.StepKindStage, Name: "implement",
	})
	if err != nil {
		t.Fatalf("StartStep: %v", err)
	}
	if _, _, err := s.StartStep(t.Context(), task.StepStart{
		ExecutionID: execution.ID, Attempt: execution.Attempt,
		ParentID: stageID, Kind: task.StepKindAgent, Name: "implement",
	}); err != nil {
		t.Fatalf("StartStep child: %v", err)
	}

	if _, err := s.CancelExecution(t.Context(), execution.ID, "stopped by operator", taskstate.Parked); err != nil {
		t.Fatalf("CancelExecution: %v", err)
	}
	evs, err := s.TaskEvents(t.Context(), execution.ID)
	if err != nil {
		t.Fatalf("TaskEvents: %v", err)
	}
	var cancelled []events.Event
	for _, e := range evs {
		if e.Data["cancelled"] == true {
			cancelled = append(cancelled, e)
		}
	}
	if len(cancelled) != 2 {
		t.Fatalf("cancel events = %d, want one per step the transaction cancelled", len(cancelled))
	}
	for _, e := range cancelled {
		if e.Kind != events.KindStageFinish || e.Attempt != execution.Attempt {
			t.Errorf("cancel event = %+v, want a stage_finish row attributed to the attempt", e)
		}
	}
}

func TestCancelExecutionLeavesEarlierAttemptsAlone(t *testing.T) {
	s := storeFor(t)
	execution := runningExecution(t, s)

	firstAttempt := execution.Attempt
	stepID, _, err := s.StartStep(t.Context(), task.StepStart{
		ExecutionID: execution.ID, Attempt: firstAttempt, Kind: task.StepKindStage, Name: "implement",
	})
	if err != nil {
		t.Fatalf("StartStep: %v", err)
	}
	if _, err := s.FinishStep(t.Context(), task.StepFinish{
		StepID: stepID, ExecutionID: execution.ID,
		From: taskstate.StepRunning, To: taskstate.StepSucceeded,
	}); err != nil {
		t.Fatalf("FinishStep: %v", err)
	}
	// A retry requeues and reclaims: attempt 2.
	if err := s.Requeue(t.Context(), execution.ID, taskstate.Running, "implement"); err != nil {
		t.Fatalf("Requeue: %v", err)
	}
	retried, err := s.ClaimNext(t.Context())
	if err != nil || retried == nil {
		t.Fatalf("ClaimNext: %+v %v", retried, err)
	}

	if _, err := s.CancelExecution(t.Context(), execution.ID, "stopped by operator", taskstate.Parked); err != nil {
		t.Fatalf("CancelExecution: %v", err)
	}
	var firstAttemptStatuses []string
	if err := s.pool.QueryRow(t.Context(),
		`SELECT jsonb_agg(status) FROM step_executions WHERE execution_id = $1 AND attempt = $2`,
		execution.ID, firstAttempt).Scan(&firstAttemptStatuses); err != nil {
		t.Fatalf("read first attempt's steps: %v", err)
	}
	for _, status := range firstAttemptStatuses {
		if status != string(taskstate.StepSucceeded) {
			t.Errorf("an earlier attempt's step was rewritten to %q; history is never rewritten", status)
		}
	}
}

func TestCancelExecutionRefusesWhatTheTableForbids(t *testing.T) {
	tests := []struct {
		name   string
		target string
		want   error
	}{
		{name: "a terminal execution", target: taskstate.Queued, want: storecontract.ErrIllegalTransition},
		{name: "an unroutable destination", target: "quantum", want: storecontract.ErrIllegalTransition},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := storeFor(t)
			execution := runningExecution(t, s)
			if tt.name == "a terminal execution" {
				// completed is terminal: the table routes nothing out of it.
				if err := s.Transition(t.Context(), execution.ID, taskstate.Running, taskstate.Completed, "done"); err != nil {
					t.Fatalf("seed completed: %v", err)
				}
			}
			if _, err := s.CancelExecution(t.Context(), execution.ID, "why", tt.target); !errors.Is(err, tt.want) {
				t.Fatalf("CancelExecution(to=%q) = %v, want %v", tt.target, err, tt.want)
			}
		})
	}
	// A queued execution cancels fine: the table routes queued -> closed_wont_do.
	s := storeFor(t)
	execution := runningExecution(t, s)
	if err := s.Transition(t.Context(), execution.ID, taskstate.Running, taskstate.Queued, "requeue"); err != nil {
		t.Fatalf("seed queued: %v", err)
	}
	if _, err := s.CancelExecution(t.Context(), execution.ID, "cancelled by operator", taskstate.Declined); err != nil {
		t.Fatalf("CancelExecution(queued -> closed_wont_do) = %v, want nil", err)
	}
}

func TestCancelExecutionOnAMissingExecutionIsStale(t *testing.T) {
	s := storeFor(t)
	if _, err := s.CancelExecution(t.Context(), 999999, "why", taskstate.Parked); !errors.Is(err, storecontract.ErrStaleTransition) {
		t.Fatalf("CancelExecution on a missing execution = %v, want ErrStaleTransition", err)
	}
}
