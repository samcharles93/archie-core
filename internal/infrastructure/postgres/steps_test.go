package postgres

import (
	"errors"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	task "github.com/samcharles93/archie-core/internal/domain/workflow/task"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/taskstate"
)

// The step-execution writes (docs/prds/execution-tree-state-machine.md): each
// of StartStep and FinishStep is one transaction -- guarded row write plus the
// domain event row -- under the step transition table, with the execution's
// own status gating whether a step may run at all.

// runningExecution enqueues and claims one task, the state a step runs in.
func runningExecution(t *testing.T, s *Store) *workflow.Task {
	t.Helper()
	seed, err := s.EnqueueChatTask(t.Context(), "acme", "widgets", "t", "b", "implement", "")
	if err != nil || seed == nil {
		t.Fatalf("EnqueueChatTask: %+v %v", seed, err)
	}
	claimed, err := s.ClaimNext(t.Context())
	if err != nil || claimed == nil {
		t.Fatalf("ClaimNext: %+v %v", claimed, err)
	}
	return claimed
}

func TestStartStepRecordsAStageEnteringRunning(t *testing.T) {
	s := storeFor(t)
	execution := runningExecution(t, s)

	stepID, startEvent, err := s.StartStep(t.Context(), task.StepStart{
		ExecutionID: execution.ID, Attempt: execution.Attempt, Kind: task.StepKindStage, Name: "implement",
	})
	if err != nil {
		t.Fatalf("StartStep: %v", err)
	}
	if startEvent.Kind != events.KindStageStart || startEvent.Stage != "implement" ||
		startEvent.TaskID != execution.ID || startEvent.Attempt != execution.Attempt ||
		startEvent.Repo != "acme/widgets" || startEvent.ID == 0 {
		t.Fatalf("stage_start event = %+v, want the transition's row", startEvent)
	}

	eventsForTask, err := s.TaskEvents(t.Context(), execution.ID)
	if err != nil {
		t.Fatalf("TaskEvents: %v", err)
	}
	if n := len(eventsForTask); n != 1 {
		t.Fatalf("events after StartStep = %d, want exactly the one the transition wrote", n)
	}

	step, err := s.FinishStep(t.Context(), task.StepFinish{
		StepID: stepID, ExecutionID: execution.ID,
		From: taskstate.StepRunning, To: taskstate.StepSucceeded, Detail: "implemented",
	})
	if err != nil {
		t.Fatalf("FinishStep: %v", err)
	}
	if step.Kind != events.KindStageFinish || step.Stage != "implement" || step.ID == 0 {
		t.Fatalf("stage_finish event = %+v, want the transition's row", step)
	}
	if _, ok := step.Data["duration_ms"]; !ok {
		t.Errorf("stage_finish data = %v, want duration_ms: the stage statistics read it", step.Data)
	}
	if _, has := step.Data["error"]; has {
		t.Errorf("stage_finish data = %v, want no error key for a succeeded step", step.Data)
	}
}

func TestFinishStepFailedCarriesTheErrorTheDashboardRenders(t *testing.T) {
	s := storeFor(t)
	execution := runningExecution(t, s)

	stepID, _, err := s.StartStep(t.Context(), task.StepStart{
		ExecutionID: execution.ID, Attempt: execution.Attempt, Kind: task.StepKindStage, Name: "review",
	})
	if err != nil {
		t.Fatalf("StartStep: %v", err)
	}
	step, err := s.FinishStep(t.Context(), task.StepFinish{
		StepID: stepID, ExecutionID: execution.ID,
		From: taskstate.StepRunning, To: taskstate.StepFailed, Detail: "review found 3 findings",
	})
	if err != nil {
		t.Fatalf("FinishStep: %v", err)
	}
	if step.Data["error"] != "review found 3 findings" {
		t.Errorf("stage_finish data = %v, want the error text stageOutcome renders", step.Data)
	}
}

func TestStartStepRefusesWhatTheStateMachineForbids(t *testing.T) {
	tests := []struct {
		name  string
		seed  func(s *Store, taskID int64) error
		start task.StepStart
		want  error
	}{
		{
			name:  "an execution that is not running",
			start: task.StepStart{Kind: task.StepKindStage, Name: "plan", Attempt: 1},
			want:  storecontract.ErrIllegalTransition,
		},
		{
			name:  "an execution that does not exist",
			start: task.StepStart{Kind: task.StepKindStage, Name: "plan", Attempt: 1},
			want:  storecontract.ErrStaleTransition,
		},
		{
			name:  "an attempt the execution no longer runs",
			start: task.StepStart{Kind: task.StepKindStage, Name: "plan", Attempt: 99},
			want:  storecontract.ErrStaleTransition,
		},
		{
			name:  "an unknown step kind",
			start: task.StepStart{Kind: "quantum", Name: "plan", Attempt: 1},
			want:  storecontract.ErrInvalidStep,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := storeFor(t)
			execution := runningExecution(t, s)
			if tt.name == "an execution that is not running" {
				// Back on the queue: nothing may run under it. The seed rides
				// the legal running->queued edge, not a hand-written pair.
				if err := s.Transition(t.Context(), execution.ID, taskstate.Running, taskstate.Queued, "requeue"); err != nil {
					t.Fatalf("seed queued: %v", err)
				}
			}
			start := tt.start
			start.ExecutionID = execution.ID
			if tt.name == "an execution that does not exist" {
				start.ExecutionID = execution.ID + 999999
			}
			if _, _, err := s.StartStep(t.Context(), start); !errors.Is(err, tt.want) {
				t.Fatalf("StartStep = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestStartStepUnderATerminalParentIsIllegal(t *testing.T) {
	s := storeFor(t)
	execution := runningExecution(t, s)

	parentID, _, err := s.StartStep(t.Context(), task.StepStart{
		ExecutionID: execution.ID, Attempt: execution.Attempt, Kind: task.StepKindStage, Name: "review",
	})
	if err != nil {
		t.Fatalf("StartStep parent: %v", err)
	}
	if _, err := s.FinishStep(t.Context(), task.StepFinish{
		StepID: parentID, ExecutionID: execution.ID,
		From: taskstate.StepRunning, To: taskstate.StepSucceeded, Detail: "done",
	}); err != nil {
		t.Fatalf("FinishStep parent: %v", err)
	}

	_, _, err = s.StartStep(t.Context(), task.StepStart{
		ExecutionID: execution.ID, Attempt: execution.Attempt,
		ParentID: parentID, Kind: task.StepKindAgent, Name: "review:2",
	})
	if !errors.Is(err, storecontract.ErrIllegalTransition) {
		t.Fatalf("StartStep under a terminal parent = %v, want ErrIllegalTransition", err)
	}
}

func TestStepDepthIsDerivedFromTheParent(t *testing.T) {
	s := storeFor(t)
	execution := runningExecution(t, s)

	parentID, _, err := s.StartStep(t.Context(), task.StepStart{
		ExecutionID: execution.ID, Attempt: execution.Attempt, Kind: task.StepKindStage, Name: "implement",
	})
	if err != nil {
		t.Fatalf("StartStep stage: %v", err)
	}
	childID, _, err := s.StartStep(t.Context(), task.StepStart{
		ExecutionID: execution.ID, Attempt: execution.Attempt,
		ParentID: parentID, Kind: task.StepKindAgent, Name: "implement",
	})
	if err != nil {
		t.Fatalf("StartStep agent child: %v", err)
	}
	var depths map[int64]int
	if err := s.pool.QueryRow(t.Context(),
		`SELECT jsonb_object_agg(id, depth) FROM step_executions WHERE execution_id = $1`,
		execution.ID).Scan(&depths); err != nil {
		t.Fatalf("read depths: %v", err)
	}
	if depths[parentID] != 0 || depths[childID] != 1 {
		t.Fatalf("depths = %v, want the stage at 0 and its child at 1", depths)
	}
}

func TestFinishStepRefusesStaleAndForeign(t *testing.T) {
	s := storeFor(t)
	execution := runningExecution(t, s)
	other := runningExecution(t, s)

	stepID, _, err := s.StartStep(t.Context(), task.StepStart{
		ExecutionID: execution.ID, Attempt: execution.Attempt, Kind: task.StepKindStage, Name: "plan",
	})
	if err != nil {
		t.Fatalf("StartStep: %v", err)
	}

	if _, err := s.FinishStep(t.Context(), task.StepFinish{
		StepID: stepID, ExecutionID: other.ID,
		From: taskstate.StepRunning, To: taskstate.StepSucceeded,
	}); !errors.Is(err, storecontract.ErrStaleTransition) {
		t.Fatalf("FinishStep naming another execution = %v, want ErrStaleTransition", err)
	}
	if _, err := s.FinishStep(t.Context(), task.StepFinish{
		StepID: stepID, ExecutionID: execution.ID,
		From: taskstate.StepSucceeded, To: taskstate.StepFailed,
	}); !errors.Is(err, storecontract.ErrStaleTransition) {
		t.Fatalf("FinishStep with an untruthful from = %v, want ErrStaleTransition", err)
	}
	if _, err := s.FinishStep(t.Context(), task.StepFinish{
		StepID: stepID, ExecutionID: execution.ID,
		From: taskstate.StepRunning, To: taskstate.StepPending,
	}); !errors.Is(err, storecontract.ErrIllegalTransition) {
		t.Fatalf("FinishStep off-table pair = %v, want ErrIllegalTransition", err)
	}
	if _, err := s.FinishStep(t.Context(), task.StepFinish{
		StepID: stepID + 999999, ExecutionID: execution.ID,
		From: taskstate.StepRunning, To: taskstate.StepSucceeded,
	}); !errors.Is(err, storecontract.ErrStaleTransition) {
		t.Fatalf("FinishStep on a missing step = %v, want ErrStaleTransition", err)
	}
}

// A late FinishStep for a step another path already finished is stale, the
// PRD's named wire behaviour: errors.Is matches on the client too.
func TestFinishStepOffTablePairIsIllegalOverTheTable(t *testing.T) {
	if !taskstate.CanStepTransition(taskstate.StepPending, taskstate.StepRunning) {
		t.Fatal("pending -> running must be in the step transition table")
	}
	if taskstate.CanStepTransition(taskstate.StepSucceeded, taskstate.StepRunning) {
		t.Fatal("succeeded -> running must not be in the step transition table: terminal is terminal")
	}
}
