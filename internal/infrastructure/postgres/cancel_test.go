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

// RecoverStale is the crash-recovery half of
// docs/prds/execution-tree-state-machine.md: requeueing a running execution
// moves the steps its interrupted attempt left running to the table's
// interrupted outcome, through the step transition table (the pair pinned in
// SQL, like the execution's own edge), with one event row per step transition
// and the execution's audit row in the same transaction. Killing the daemon
// mid-stage and restarting leaves that stage interrupted and the execution
// queued with a fresh attempt for the next run.
func TestRecoverStaleInterruptsRunningSteps(t *testing.T) {
	s := storeFor(t)
	execution := runningExecution(t, s)

	stageID, _, err := s.StartStep(t.Context(), task.StepStart{
		ExecutionID: execution.ID, Attempt: execution.Attempt, Kind: task.StepKindStage, Name: "implement",
	})
	if err != nil {
		t.Fatalf("StartStep stage: %v", err)
	}
	childID, _, err := s.StartStep(t.Context(), task.StepStart{
		ExecutionID: execution.ID, Attempt: execution.Attempt,
		ParentID: stageID, Kind: task.StepKindAgent, Name: "implement",
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

	requeued, err := s.RecoverStale(t.Context())
	if err != nil {
		t.Fatalf("RecoverStale: %v", err)
	}
	if requeued != 1 {
		t.Fatalf("RecoverStale requeued %d, want the one running execution", requeued)
	}

	got, err := s.TaskByID(t.Context(), execution.ID)
	if err != nil || got == nil {
		t.Fatalf("TaskByID: %+v %v", got, err)
	}
	if got.Status != taskstate.Queued {
		t.Fatalf("execution after recovery = %q, want queued for the next attempt", got.Status)
	}

	var statuses map[int64]string
	if err := s.pool.QueryRow(t.Context(),
		`SELECT jsonb_object_agg(id, status) FROM step_executions WHERE execution_id = $1`,
		execution.ID).Scan(&statuses); err != nil {
		t.Fatalf("read steps: %v", err)
	}
	if statuses[stageID] != string(taskstate.StepSucceeded) {
		t.Errorf("the finished step was rewritten to %q; recovery only interrupts", statuses[stageID])
	}
	if statuses[childID] != string(taskstate.StepInterrupted) {
		t.Errorf("the step the crash left running = %q, want interrupted", statuses[childID])
	}

	evs, err := s.TaskEvents(t.Context(), execution.ID)
	if err != nil {
		t.Fatalf("TaskEvents: %v", err)
	}
	var interrupted []events.Event
	for _, e := range evs {
		if e.Data["interrupted"] == true {
			interrupted = append(interrupted, e)
		}
	}
	if len(interrupted) != 1 {
		t.Fatalf("interrupted step events = %d, want exactly one for the step transition", len(interrupted))
	}
	if interrupted[0].Kind != events.KindStageFinish || interrupted[0].Stage != "implement" ||
		interrupted[0].Attempt != got.Attempt {
		t.Errorf("interrupted event = %+v, want a stage_finish row attributed to the interrupted attempt", interrupted[0])
	}
}

// A wait:true call step is how the store knows the caller is still waiting on
// its callee: it closes only when the callee ends, so a non-terminal call
// step is a callee the caller waits on. Cancelling the caller sweeps each
// such callee through its own CancelExecution -- the callee's own lifecycle
// -- while a finished call step's callee (wait:false, or already ended) runs
// on. Trees never cross runs: a call step naming a task that is not the
// caller's callee cannot even be recorded (StartStep verifies the callee's
// call_parent_task_id).
func TestCancelExecutionCancelsTheCalleesTheCallerWaitsOn(t *testing.T) {
	s := storeFor(t)
	caller := runningExecution(t, s)

	// The callee: started through the call linkage the way a wait:true step
	// does (the store derives the callee from the caller's own row), running
	// under its own lifecycle, with a running step of its own.
	callee, err := s.StartCall(t.Context(), caller.ID, "implement", nil)
	if err != nil {
		t.Fatalf("EnqueueCallTask: %v", err)
	}
	if _, err := s.ClaimNext(t.Context()); err != nil {
		t.Fatalf("ClaimNext callee: %v", err)
	}
	// The claim increments the attempt; read the row back the way the
	// container's own request carries it.
	callee, err = s.TaskByID(t.Context(), callee.ID)
	if err != nil || callee == nil {
		t.Fatalf("TaskByID(callee): %+v %v", callee, err)
	}
	if _, _, err := s.StartStep(t.Context(), task.StepStart{
		ExecutionID: callee.ID, Attempt: callee.Attempt, Kind: task.StepKindStage, Name: "implement",
	}); err != nil {
		t.Fatalf("callee StartStep: %v", err)
	}

	// The caller's wait:true call step names the callee and is still running.
	if _, _, err := s.StartStep(t.Context(), task.StepStart{
		ExecutionID: caller.ID, Attempt: caller.Attempt, Kind: task.StepKindCall,
		Name: "workflow.call", CalledExecutionID: callee.ID,
	}); err != nil {
		t.Fatalf("call step: %v", err)
	}

	// A wait:false call step's callee runs on: its call step already ended.
	unwaited, err := s.StartCall(t.Context(), caller.ID, "implement", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimNext(t.Context()); err != nil {
		t.Fatalf("ClaimNext no-wait callee: %v", err)
	}
	noWaitStep, _, err := s.StartStep(t.Context(), task.StepStart{
		ExecutionID: caller.ID, Attempt: caller.Attempt, Kind: task.StepKindCall,
		Name: "workflow.call", CalledExecutionID: unwaited.ID,
	})
	if err != nil {
		t.Fatalf("no-wait call step: %v", err)
	}
	if _, err := s.FinishStep(t.Context(), task.StepFinish{
		StepID: noWaitStep, ExecutionID: caller.ID,
		From: taskstate.StepRunning, To: taskstate.StepSucceeded, Detail: "started; no wait",
	}); err != nil {
		t.Fatalf("close the no-wait call step: %v", err)
	}

	if _, err := s.CancelExecution(t.Context(), caller.ID, "stopped by operator", taskstate.Parked); err != nil {
		t.Fatalf("CancelExecution: %v", err)
	}

	waited, err := s.TaskByID(t.Context(), callee.ID)
	if err != nil || waited == nil {
		t.Fatalf("TaskByID(callee): %+v %v", waited, err)
	}
	if waited.Status != taskstate.Parked {
		t.Fatalf("the waited-on callee = %q, want parked under its own lifecycle", waited.Status)
	}
	waitedSteps, err := s.TaskEvents(t.Context(), callee.ID)
	if err != nil {
		t.Fatalf("callee TaskEvents: %v", err)
	}
	if len(waitedSteps) == 0 {
		t.Error("the callee's steps were not cancelled with their events")
	}

	unwaitedAfter, err := s.TaskByID(t.Context(), unwaited.ID)
	if err != nil || unwaitedAfter == nil {
		t.Fatalf("TaskByID no-wait callee: %+v %v", unwaitedAfter, err)
	}
	if unwaitedAfter.Status != taskstate.Running {
		t.Errorf("the wait:false callee = %q, want it running on", unwaitedAfter.Status)
	}
}
