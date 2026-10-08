package workflow

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/workflow/task"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/taskstate"
)

// fakeStepStore records the step writes a branch makes.
type fakeStepStore struct {
	started  []task.StepStart
	finished []task.StepFinish
	events   []events.Event
	nextID   int64
}

func (f *fakeStepStore) Update(context.Context, *task.Task) error { return nil }

func (f *fakeStepStore) Transition(context.Context, int64, string, string, string) error {
	return nil
}

func (f *fakeStepStore) InsertEvent(_ context.Context, e events.Event) (int64, error) {
	f.events = append(f.events, e)
	return 0, nil
}

func (f *fakeStepStore) StartStep(_ context.Context, s task.StepStart) (int64, events.Event, error) {
	f.nextID++
	f.started = append(f.started, s)
	return f.nextID, events.Event{ID: f.nextID}, nil
}

func (f *fakeStepStore) FinishStep(_ context.Context, s task.StepFinish) (events.Event, error) {
	f.finished = append(f.finished, s)
	return events.Event{ID: s.StepID}, nil
}

// TestRunBranchRecordsChildSteps pins the step contract a branch owes the
// canvas: every branch step is a stage row parented to the parallel step and
// named for its branch, and its outcome is recorded.
func TestRunBranchRecordsChildSteps(t *testing.T) {
	boom := errors.New("boom")
	tests := []struct {
		name       string
		stage      Stage
		wantErr    error
		wantStatus taskstate.StepStatus
		wantDetail string
	}{
		{
			name:       "a passing branch step",
			stage:      Stage{Name: "agent.run", Run: func(context.Context, *TaskContext) error { return nil }},
			wantStatus: taskstate.StepSucceeded,
		},
		{
			name:       "a failing branch step",
			stage:      Stage{Name: "command.run", Run: func(context.Context, *TaskContext) error { return boom }},
			wantErr:    boom,
			wantStatus: taskstate.StepFailed,
			wantDetail: "boom",
		},
		{
			name:       "a branch step whose when was false",
			stage:      wrapStep(StepRecord{ID: "agent.run", When: "task.title"}, func(context.Context, *TaskContext) error { return nil }),
			wantStatus: taskstate.StepSkipped,
			wantDetail: "task.title",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeStepStore{}
			tc := &TaskContext{Task: &task.Task{ID: 1, Attempt: 1}, Store: store, StepID: 7, workflowBranch: "docs", Log: slog.Default()}
			err := runBranch(context.Background(), tc, []Stage{tt.stage})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if len(store.started) != 1 {
				t.Fatalf("started %d steps, want 1", len(store.started))
			}
			start := store.started[0]
			if start.Kind != task.StepKindStage || start.ParentID != 7 || start.Name != "docs/"+tt.stage.Name {
				t.Fatalf("start = %#v, want a stage named %q under step 7", start, "docs/"+tt.stage.Name)
			}
			if len(store.finished) != 1 {
				t.Fatalf("finished %d steps, want 1", len(store.finished))
			}
			finish := store.finished[0]
			if finish.To != tt.wantStatus || finish.Detail != tt.wantDetail {
				t.Fatalf("finish = %#v, want %s %q", finish, tt.wantStatus, tt.wantDetail)
			}
			if tc.StepID != 7 {
				t.Fatalf("branch step leaked its step id: %d", tc.StepID)
			}
		})
	}
}
