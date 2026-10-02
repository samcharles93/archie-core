package taskactions

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/workflow/prreview"
	workflowtask "github.com/samcharles93/archie-core/internal/domain/workflow/task"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/taskstate"
)

// gateStore is a fakeStore that records the review gate response write and
// the plain requeue separately, so a test can tell which path an approval
// took.
type gateStore struct {
	fakeStore

	requeues   []requeueCall
	gateWrites []gateWrite
	gateErr    error
}

type requeueCall struct {
	taskID         int64
	from, workflow string
}

type gateWrite struct {
	taskID   int64
	from     string
	gate     string
	rereview bool
	max      int
}

func (g *gateStore) Requeue(_ context.Context, taskID int64, from, workflow string) error {
	g.requeues = append(g.requeues, requeueCall{taskID: taskID, from: from, workflow: workflow})
	return nil
}

func (g *gateStore) RespondReviewGate(_ context.Context, taskID int64, from, gate string, rereview bool, maxRounds int) error {
	if g.gateErr != nil {
		return g.gateErr
	}
	g.gateWrites = append(g.gateWrites, gateWrite{taskID: taskID, from: from, gate: gate, rereview: rereview, max: maxRounds})
	return nil
}

// offeredGate is a task waiting with the review gate offer recorded, which is
// what an operator's approve or re-review answers.
func offeredGate(findings ...prreview.ScoredFinding) *Task {
	return &Task{
		ID: 7, Owner: "acme", Repo: "widgets", Status: taskstate.WaitingHuman,
		ReviewGate: workflowtask.EncodeReviewGate(workflowtask.ReviewGate{
			Findings: gateDocuments(findings...), HeadSHA: "abc", Owner: "acme", Repo: "widgets",
			PRNumber: 42, Workflow: "pr-review",
		}),
	}
}

// gateDocuments renders scored findings the way the pipeline's gate stage
// does: the stable key an operator's selection names, beside the finding's
// own encoding.
func gateDocuments(findings ...prreview.ScoredFinding) []workflowtask.ReviewGateFinding {
	out := make([]workflowtask.ReviewGateFinding, len(findings))
	for i, f := range findings {
		out[i] = prreview.GateFinding(f)
	}
	return out
}

func TestApproveOfAGateRecordsTheSelectionOnTheOffer(t *testing.T) {
	findings := []prreview.ScoredFinding{
		{Title: "keep", File: "main.go", LineStart: 1, Body: "keep me"},
		{Title: "drop", File: "main.go", LineStart: 9, Body: "drop me"},
	}
	store := &gateStore{task: offeredGate(findings...)}
	service := Service{Store: store}

	if err := service.Apply(context.Background(), nil, humanActor(), 7, taskstate.ActionApprove,
		ActionPayload{Findings: []string{findings[0].Key()}}); err != nil {
		t.Fatalf("Apply(approve): %v", err)
	}
	if len(store.requeues) != 0 {
		t.Fatalf("approve of a gate requeued plainly: %+v", store.requeues)
	}
	if len(store.gateWrites) != 1 {
		t.Fatalf("gate writes = %+v, want exactly the one response write", store.gateWrites)
	}
	write := store.gateWrites[0]
	if write.rereview || write.max != workflowtask.MaxRereviewRounds || write.from != taskstate.WaitingHuman {
		t.Fatalf("write = %+v, want an approve, from waiting_human, at the domain cap", write)
	}
	gate, ok := workflowtask.DecodeReviewGate(write.gate)
	if !ok || !gate.Approved() {
		t.Fatalf("recorded gate = %q, want an approve", write.gate)
	}
	if len(gate.Selection) != 1 || gate.Selection[0] != findings[0].Key() {
		t.Fatalf("recorded selection = %v, want exactly the selected key", gate.Selection)
	}
	if len(gate.Findings) != len(findings) {
		t.Fatalf("recorded findings = %d, want the whole offer kept for the resume to post", len(gate.Findings))
	}
	if got := store.last(); got.Kind != events.KindHumanApproved {
		t.Fatalf("event kind = %q, want %q", got.Kind, events.KindHumanApproved)
	}
}

func TestApproveWithNoSelectionRecordsEveryOfferedFinding(t *testing.T) {
	findings := []prreview.ScoredFinding{{Title: "only", File: "main.go", LineStart: 1, Body: "b"}}
	store := &gateStore{task: offeredGate(findings...)}
	service := Service{Store: store}

	if err := service.Apply(context.Background(), nil, humanActor(), 7, taskstate.ActionApprove, ActionPayload{}); err != nil {
		t.Fatalf("Apply(approve): %v", err)
	}
	gate, _ := workflowtask.DecodeReviewGate(store.gateWrites[0].gate)
	if len(gate.Selection) != 1 || gate.Selection[0] != findings[0].Key() {
		t.Fatalf("selection = %v, want every offered finding's key", gate.Selection)
	}
}

func TestApproveRefusesANonOfferedFindingKey(t *testing.T) {
	store := &gateStore{task: offeredGate(prreview.ScoredFinding{Title: "offered", File: "main.go", LineStart: 1})}
	service := Service{Store: store}

	err := service.Apply(context.Background(), nil, humanActor(), 7, taskstate.ActionApprove,
		ActionPayload{Findings: []string{"main.go:99:stale"}})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("Apply(approve with a stale key) = %v, want ErrConflict", err)
	}
	if len(store.gateWrites) != 0 {
		t.Fatalf("refused approve wrote %+v", store.gateWrites)
	}
}

// TestApproveWithoutAGateKeepsTheTasksWorkflow is the feasibility handoff:
// no gate offer means there is no review to answer, and the approval releases
// waiting work under the workflow the wait recorded. It must not hardcode
// implement -- the PRD's Decision 1 -- which is why feasibility names
// implement itself before it waits.
func TestApproveWithoutAGateKeepsTheTasksWorkflow(t *testing.T) {
	store := &gateStore{task: &Task{ID: 7, Owner: "acme", Repo: "widgets", Status: taskstate.WaitingHuman}}
	service := Service{Store: store}

	if err := service.Apply(context.Background(), nil, humanActor(), 7, taskstate.ActionApprove, ActionPayload{}); err != nil {
		t.Fatalf("Apply(approve): %v", err)
	}
	if len(store.gateWrites) != 0 {
		t.Fatalf("a task with no gate wrote %+v", store.gateWrites)
	}
	if len(store.requeues) != 1 || store.requeues[0].workflow != "" || store.requeues[0].from != taskstate.WaitingHuman {
		t.Fatalf("requeues = %+v, want one waiting_human requeue that keeps the task's workflow", store.requeues)
	}
}

func TestRereviewRecordsInstructionsAndClearsTheOffer(t *testing.T) {
	findings := []prreview.ScoredFinding{{Title: "old", File: "main.go", LineStart: 1}}
	store := &gateStore{task: offeredGate(findings...)}
	service := Service{Store: store}

	if err := service.Apply(context.Background(), nil, humanActor(), 7, taskstate.ActionRereview,
		ActionPayload{Instructions: "look at the migration ordering"}); err != nil {
		t.Fatalf("Apply(rereview): %v", err)
	}
	write := store.gateWrites[0]
	if !write.rereview {
		t.Fatalf("write = %+v, want the rereview flag that increments the round count", write)
	}
	gate, _ := workflowtask.DecodeReviewGate(write.gate)
	if gate.Outcome != workflowtask.GateRereview || gate.Instructions != "look at the migration ordering" {
		t.Fatalf("recorded gate = %+v, want the re-review outcome and instructions", gate)
	}
	if len(gate.Findings) != 0 || len(gate.Selection) != 0 {
		t.Fatalf("re-review kept the offer: %+v; the resumed run recomputes it", gate)
	}
	if got := store.last(); got.Kind != events.KindHumanRereviewed {
		t.Fatalf("event kind = %q, want %q", got.Kind, events.KindHumanRereviewed)
	}
}

func TestRereviewRefusedWithoutInstructionsAndAtTheCap(t *testing.T) {
	tests := []struct {
		name string
		task *Task
		res  ActionPayload
		want string
	}{
		{
			name: "no instructions",
			task: offeredGate(prreview.ScoredFinding{Title: "f", File: "main.go", LineStart: 1}),
			want: "instructions",
		},
		{
			name: "no gate offer",
			task: &Task{ID: 7, Owner: "acme", Repo: "widgets", Status: taskstate.WaitingHuman},
			res:  ActionPayload{Instructions: "look again"},
			want: "no review gate",
		},
		{
			name: "at the cap",
			task: func() *Task {
				task := offeredGate(prreview.ScoredFinding{Title: "f", File: "main.go", LineStart: 1})
				task.RereviewRounds = workflowtask.MaxRereviewRounds
				return task
			}(),
			res:  ActionPayload{Instructions: "look again"},
			want: "re-reviews",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := &gateStore{task: tc.task}
			service := Service{Store: store}
			err := service.Apply(context.Background(), nil, humanActor(), 7, taskstate.ActionRereview, tc.res)
			if !errors.Is(err, ErrConflict) {
				t.Fatalf("Apply(rereview) = %v, want ErrConflict", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want it to name %q", err, tc.want)
			}
			if len(store.gateWrites) != 0 || len(store.requeues) != 0 || len(store.events) != 0 {
				t.Fatalf("a refused re-review wrote store state: gate=%+v requeues=%+v events=%+v", store.gateWrites, store.requeues, store.events)
			}
		})
	}
}
