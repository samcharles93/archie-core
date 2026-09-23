package daemon

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/eda/playbook"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/store"
)

// runRecorder is a playbook set that matches no workflow playbook and records
// each action-playbook run, failing it when err is set.
type runRecorder struct {
	runs []playbook.DispatchInput
	err  error
}

func (r *runRecorder) Dispatch(playbook.DispatchInput) (playbook.Decision, bool) {
	return playbook.Decision{}, false
}

func (r *runRecorder) Run(_ context.Context, _ storecontract.PlaybookDispatcher, _ *slog.Logger, input playbook.DispatchInput) error {
	r.runs = append(r.runs, input)
	return r.err
}

type nopPlaybookLedger struct{}

func (nopPlaybookLedger) RecordPlaybookDispatch(context.Context, string, string, string, string) error {
	return nil
}

func (nopPlaybookLedger) DeletePlaybookDispatches(context.Context, string) error { return nil }

func TestPinWorkflowDefinitionRunsActionPlaybooks(t *testing.T) {
	tests := []struct {
		name     string
		ledger   storecontract.PlaybookDispatcher
		workflow string
		runErr   error
		wantRuns int
	}{
		{name: "runs once with a ledger", ledger: nopPlaybookLedger{}, wantRuns: 1},
		{name: "a failed run leaves routing alone", ledger: nopPlaybookLedger{}, runErr: errors.New("boom"), wantRuns: 1},
		{name: "never runs without a ledger", wantRuns: 0},
		{name: "skips a task that already names a workflow", ledger: nopPlaybookLedger{}, workflow: "tdd", wantRuns: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resources := store.OpenTest(t)
			defer resources.Close()
			task := forgeTask(t, resources, "bug")
			task.Workflow = tt.workflow
			recorder := &runRecorder{err: tt.runErr}
			d := &Daemon{
				Store:               resources,
				WorkflowDefinitions: routableDefinitions(),
				Log:                 slog.New(slog.DiscardHandler),
				Playbooks:           recorder,
				PlaybookLedger:      tt.ledger,
			}
			if err := d.pinWorkflowDefinition(t.Context(), task); err != nil {
				t.Fatal(err)
			}
			// A second pin reuses the stored definition and must not run again.
			if err := d.pinWorkflowDefinition(t.Context(), task); err != nil {
				t.Fatal(err)
			}
			if len(recorder.runs) != tt.wantRuns {
				t.Fatalf("runs = %d, want %d", len(recorder.runs), tt.wantRuns)
			}
			if task.Workflow != "tdd" {
				t.Fatalf("pinned workflow = %q, want tdd (the kind binding, untouched by the run)", task.Workflow)
			}
		})
	}
}
