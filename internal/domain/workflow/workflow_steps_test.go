package workflow

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"testing"

	"github.com/samcharles93/archie-core/internal/agentexec"
	task "github.com/samcharles93/archie-core/internal/domain/workflow/task"
	"github.com/samcharles93/archie-core/internal/events"
)

// The stage-recording half of docs/prds/execution-tree-state-machine.md:
// workflow.Run records every stage it runs as a StepExecution through the
// store, and a failed step write parks the execution -- the step does not run
// unrecorded.

// TestRunRecordsEveryStageAsAStepExecution pins the recording shape: one
// StartStep before each stage's body and one FinishStep with that stage's
// outcome, both of kind stage, against the task's own execution and attempt.
func TestRunRecordsEveryStageAsAStepExecution(t *testing.T) {
	store := &recordingStore{}
	stage := func(name string) Stage {
		return Stage{Name: name, Run: func(context.Context, *TaskContext) error { return nil }}
	}
	wf := Workflow{Name: "recording", Stages: []Stage{stage("plan"), stage("implement")}}
	tc := &TaskContext{
		Task:  &Task{ID: 41, Attempt: 3, Owner: "acme", Repo: "widgets", Status: StatusRunning},
		Store: store, Log: slog.New(slog.DiscardHandler),
	}

	Run(context.Background(), wf, tc)

	if len(store.steps) != 2 || len(store.finishes) != 2 {
		t.Fatalf("steps = %+v finishes = %+v, want one recorded start and finish per stage", store.steps, store.finishes)
	}
	for i, step := range store.steps {
		if step.ExecutionID != 41 || step.Attempt != 3 {
			t.Errorf("stage %d: ExecutionID/Attempt = %d/%d, want the run's own 41/3", i, step.ExecutionID, step.Attempt)
		}
		if step.Kind != task.StepKindStage {
			t.Errorf("stage %d: kind = %q, want %q", i, step.Kind, task.StepKindStage)
		}
		if want := []string{"plan", "implement"}[i]; step.Name != want {
			t.Errorf("stage %d: name = %q, want %q", i, step.Name, want)
		}
	}
	if got := store.finishes[0].To; got != "succeeded" {
		t.Errorf("stage finish to = %q, want succeeded", got)
	}
}

// TestRunPublishesTheRecordedEvents pins the post-commit half: the events the
// store returned, already persisted, are what reach the bus -- not a second,
// unpersisted copy emitted beside the store write.
func TestRunPublishesThePersistedStepEvents(t *testing.T) {
	store := &recordingStore{}
	wf := Workflow{Name: "recording", Stages: []Stage{{
		Name: "implement", Run: func(context.Context, *TaskContext) error { return nil },
	}}}
	bus := events.NewBus()
	sub := bus.Subscribe(16)
	defer sub.Close()
	tc := &TaskContext{
		Task:  &Task{ID: 7, Attempt: 1, Owner: "acme", Repo: "widgets", Status: StatusRunning},
		Store: store, Bus: bus, Log: slog.New(slog.DiscardHandler),
	}

	Run(context.Background(), wf, tc)

	var seen []string
	ids := map[string]int64{}
	for len(sub.C) > 0 {
		e := <-sub.C
		seen = append(seen, e.Kind)
		ids[e.Kind] = e.ID
	}
	for _, want := range []string{events.KindStageStart, events.KindStageFinish} {
		if !slices.Contains(seen, want) {
			t.Errorf("bus saw %v, want %q among them: the persisted events are what reach the bus", seen, want)
		}
		if ids[want] == 0 {
			t.Errorf("bus event %q carries no ID: an event without the store's assigned ID would be inserted a second time by the daemon's event sink", want)
		}
	}
}

// TestRunParksWhenAStepWriteFails is the PRD's persistence rule: a failed
// step write parks the execution, and the step does not run unrecorded.
func TestRunParksWhenAStepWriteFails(t *testing.T) {
	store := &recordingStore{stepEr: errStepWrite}
	stageRan := false
	wf := Workflow{Name: "recording", Stages: []Stage{{
		Name: "implement",
		Run:  func(context.Context, *TaskContext) error { stageRan = true; return nil },
	}}}
	tc := &TaskContext{
		Task:  &Task{ID: 9, Attempt: 1, Owner: "acme", Repo: "widgets", Status: StatusRunning},
		Store: store, Log: slog.New(slog.DiscardHandler),
	}

	Run(context.Background(), wf, tc)

	if stageRan {
		t.Fatal("the stage ran despite its recording write failing; the step must not run unrecorded")
	}
	if len(store.transitions) == 0 || store.transitions[0].to != StatusParked {
		t.Fatalf("transitions = %+v, want the execution parked", store.transitions)
	}
	if len(store.finishes) != 0 {
		t.Errorf("finishes = %+v, want none: the stage never ran", store.finishes)
	}
}

var errStepWrite = errors.New("recording write failed")

// The agent-call children (docs/prds/execution-tree-state-machine.md, "Model"):
// every agent call a stage makes is its own StepExecution of kind agent,
// parented to the stage's recorded step, with the call's own outcome and the
// tokens it accounted. Stage "implement" carries agent "implement" one level
// down, the way the model draws it.
func TestRunRecordsAgentChildrenUnderTheStageStep(t *testing.T) {
	store := &recordingStore{}
	wf := Workflow{Name: "recording", Stages: []Stage{{
		Name: "implement",
		Run: func(ctx context.Context, tc *TaskContext) error {
			_, err := tc.RunAgentChild(ctx, "implement", func() (agentexec.Result, error) {
				return agentexec.Result{TokensUsed: 120, Summary: "built"}, nil
			})
			return err
		},
	}}}
	tc := &TaskContext{
		Task:  &Task{ID: 41, Attempt: 3, Owner: "acme", Repo: "widgets", Status: StatusRunning},
		Store: store, Log: slog.New(slog.DiscardHandler),
	}

	Run(context.Background(), wf, tc)

	if len(store.steps) != 2 {
		t.Fatalf("steps = %+v, want the stage and its agent child", store.steps)
	}
	stageStep, child := store.steps[0], store.steps[1]
	if stageStep.Kind != StepKindStage || stageStep.Name != "implement" {
		t.Errorf("stage step = %+v, want the recorded stage", stageStep)
	}
	if child.Kind != StepKindAgent || child.Name != "implement" {
		t.Errorf("child step = %+v, want the agent call", child)
	}
	// The parent is the stage's own step: the store derives depth from it.
	if child.ParentID == 0 || child.ParentID == stageStep.ExecutionID {
		t.Errorf("child parent = %d, want the stage step's id, not the execution's", child.ParentID)
	}
	if len(store.finishes) != 2 {
		t.Fatalf("finishes = %+v, want the stage's and the child's", store.finishes)
	}
	var childFinish StepFinish
	for _, f := range store.finishes {
		if f.StepID == child.ParentID+1 {
			childFinish = f
		}
	}
	if childFinish.To != "succeeded" || childFinish.TokensUsed != 120 {
		t.Errorf("child finish = %+v, want succeeded with the call's own usage", childFinish)
	}
}

// TestRunParksWhenAChildStepWriteFails: a child's recording write parks the
// execution the same way a stage's does -- an agent call that ran unrecorded
// is exactly the invisibility the step table exists to end.
func TestRunParksWhenAChildStepWriteFails(t *testing.T) {
	store := &recordingStore{finishEr: errStepWrite}
	wf := Workflow{Name: "recording", Stages: []Stage{{
		Name: "implement",
		Run: func(ctx context.Context, tc *TaskContext) error {
			_, err := tc.RunAgentChild(ctx, "implement", func() (agentexec.Result, error) {
				return agentexec.Result{}, nil
			})
			return err
		},
	}}}
	tc := &TaskContext{
		Task:  &Task{ID: 9, Attempt: 1, Owner: "acme", Repo: "widgets", Status: StatusRunning},
		Store: store, Log: slog.New(slog.DiscardHandler),
	}

	Run(context.Background(), wf, tc)

	if len(store.transitions) == 0 || store.transitions[0].to != StatusParked {
		t.Fatalf("transitions = %+v, want the execution parked after the child's recording failed", store.transitions)
	}
}
