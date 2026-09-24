package taskactions

import (
	"context"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/identity"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/taskstate"
)

type fakeStore struct {
	task   *Task
	events []events.Event
}

func (f *fakeStore) TaskByID(context.Context, int64) (*Task, error) { return f.task, nil }
func (f *fakeStore) Transition(context.Context, int64, string, string, string) error {
	return nil
}

func (f *fakeStore) Requeue(context.Context, int64, string, string) error { return nil }
func (f *fakeStore) RetryTask(context.Context, int64, string, string) error {
	return nil
}

func (f *fakeStore) ArchiveTask(_ context.Context, _ int64, _ string, e events.Event) (int64, error) {
	f.events = append(f.events, e)
	return int64(len(f.events)), nil
}

func (f *fakeStore) InsertEvent(_ context.Context, e events.Event) (int64, error) {
	f.events = append(f.events, e)
	return int64(len(f.events)), nil
}

func (f *fakeStore) last() events.Event {
	if len(f.events) == 0 {
		return events.Event{}
	}
	return f.events[len(f.events)-1]
}

const (
	humanID = identity.IdentityID("20000000-0000-5000-8000-000000000001")
	agentID = identity.IdentityID("20000000-0000-5000-8000-000000000002")
)

func humanActor() Actor {
	return ActorFor(identity.Identity{ID: humanID, Kind: identity.KindUser, Lifecycle: identity.LifecycleActive})
}

func agentActor() Actor {
	return ActorFor(identity.Identity{ID: agentID, Kind: identity.KindBot, Lifecycle: identity.LifecycleActive})
}

// TestEventKindDescribesTheActor is the rule that the bug turned on: an agent's
// approval was written down as a human's. The kind names the actor, so an
// agent's action can never be read as a person's, and an action with no verified
// actor is recorded as a task-level event that credits nobody.
func TestEventKindDescribesTheActor(t *testing.T) {
	tests := []struct {
		name     string
		action   taskstate.Action
		actor    Actor
		wantKind string
	}{
		{"a person approves", taskstate.ActionApprove, humanActor(), events.KindHumanApproved},
		{"an agent approves", taskstate.ActionApprove, agentActor(), events.KindAgentApproved},
		{"a person rejects", taskstate.ActionReject, humanActor(), events.KindHumanRejected},
		{"an agent rejects", taskstate.ActionReject, agentActor(), events.KindAgentRejected},
		{"an unattributed approval", taskstate.ActionApprove, Actor{}, events.KindTaskApproved},
		{"an unattributed rejection", taskstate.ActionReject, Actor{}, events.KindTaskRejected},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeStore{task: &Task{ID: 7, Owner: "acme", Repo: "widgets", Status: "waiting_human"}}
			service := Service{Store: store}

			if err := service.Apply(context.Background(), nil, tc.actor, 7, tc.action); err != nil {
				t.Fatalf("Apply() error = %v", err)
			}
			got := store.last()
			if got.Kind != tc.wantKind {
				t.Fatalf("event kind = %q, want %q", got.Kind, tc.wantKind)
			}
			if tc.actor.Human() && got.Kind == events.KindAgentApproved {
				t.Fatal("a person's action was recorded as an agent's")
			}
			if tc.actor.Attributed() && !tc.actor.Human() && got.Kind == events.KindHumanApproved {
				t.Fatal("an agent's action was recorded as a human approval")
			}
		})
	}
}

// TestActorAndPrincipalAreRecordedSeparately is the two-attribution requirement:
// an agent acting under a person's standing authority records both, so "approved
// by came from me" is answerable from the record.
func TestActorAndPrincipalAreRecordedSeparately(t *testing.T) {
	store := &fakeStore{task: &Task{ID: 7, Owner: "acme", Repo: "widgets", Status: "waiting_human"}}
	service := Service{Store: store}

	actor := agentActor().AuthorisedBy(humanID)
	if err := service.Apply(context.Background(), nil, actor, 7, taskstate.ActionApprove); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	got := store.last()
	if got.ActorID != string(agentID) {
		t.Fatalf("actor = %q, want %q", got.ActorID, agentID)
	}
	if got.ActorKind != string(identity.KindBot) {
		t.Fatalf("actor kind = %q, want %q", got.ActorKind, identity.KindBot)
	}
	if got.PrincipalID != string(humanID) {
		t.Fatalf("principal = %q, want %q", got.PrincipalID, humanID)
	}
	if got.Kind != events.KindAgentApproved {
		t.Fatalf("event kind = %q, want %q", got.Kind, events.KindAgentApproved)
	}
}

// TestUnattributedActionClaimsNoApprover: with no verified actor the record must
// not credit the actor's own authority, and must not name a human.
func TestUnattributedActionClaimsNoApprover(t *testing.T) {
	store := &fakeStore{task: &Task{ID: 7, Owner: "acme", Repo: "widgets", Status: "waiting_human"}}
	service := Service{Store: store}

	if err := service.Apply(context.Background(), nil, Actor{}, 7, taskstate.ActionApprove); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	got := store.last()
	if got.ActorID != "" || got.ActorKind != "" || got.PrincipalID != "" {
		t.Fatalf("unattributed action recorded an actor: %+v", got)
	}
	if got.Kind == events.KindHumanApproved || got.Kind == events.KindAgentApproved {
		t.Fatalf("unattributed action claimed an actor through its kind: %q", got.Kind)
	}
}

// TestEveryActionCarriesItsAttribution: no action path may record an event that
// skipped the attribution, including the ones that also close a forge issue.
func TestEveryActionCarriesItsAttribution(t *testing.T) {
	actions := []struct {
		action taskstate.Action
		status string
	}{
		{taskstate.ActionApprove, "waiting_human"},
		{taskstate.ActionReject, "waiting_human"},
		{taskstate.ActionRetry, "parked"},
		{taskstate.ActionStop, "running"},
		{taskstate.ActionCancel, "queued"},
		{taskstate.ActionAbandon, "parked"},
	}

	for _, tc := range actions {
		t.Run(string(tc.action), func(t *testing.T) {
			store := &fakeStore{task: &Task{ID: 7, Owner: "acme", Repo: "widgets", Status: tc.status}}
			service := Service{
				Store:      store,
				CancelTask: func(int64) bool { return true },
			}
			if err := service.Apply(context.Background(), nil, agentActor(), 7, tc.action); err != nil {
				t.Fatalf("Apply(%s) error = %v", tc.action, err)
			}
			got := store.last()
			if got.ActorID != string(agentID) || got.ActorKind != string(identity.KindBot) {
				t.Fatalf("%s recorded without its actor: %+v", tc.action, got)
			}
		})
	}
}

// TestScopeStillLimitsATaskButIsNotTheActor: scope answers which task a caller
// may touch. It is not an actor, and passing one must not attribute the action
// to it.
func TestScopeStillLimitsATaskButIsNotTheActor(t *testing.T) {
	other := "sam"
	store := &fakeStore{task: &Task{ID: 7, Owner: "acme", Repo: "widgets", Status: "waiting_human", Identity: "archie"}}
	service := Service{Store: store}

	if err := service.Apply(context.Background(), &other, agentActor(), 7, taskstate.ActionApprove); err == nil {
		t.Fatal("Apply() allowed an identity to act on another identity's task")
	}
	if len(store.events) != 0 {
		t.Fatalf("a refused action recorded %d events", len(store.events))
	}

	if err := service.Apply(context.Background(), &other, agentActor(), 7, taskstate.ActionApprove); err == nil {
		t.Fatal("Apply() allowed a scoped caller to act on a foreign task")
	}
}

func TestActorForCarriesTheKindAndLeavesTheActionUnattributedWithoutAPrincipal(t *testing.T) {
	actor := agentActor()
	if actor.Principal != "" {
		t.Fatalf("ActorFor() set a principal: %q", actor.Principal)
	}
	if !actor.Attributed() {
		t.Fatal("ActorFor() produced an unattributed actor for a real identity")
	}
	if (Actor{}).Attributed() {
		t.Fatal("the zero Actor reported itself as attributed")
	}
	if (Actor{}).Human() {
		t.Fatal("the zero Actor reported itself as a human")
	}
}

// TestOnlyRejectAndCancelCloseTheIssue: abandoning is archie giving up on its
// own run, not a verdict on the issue, so the issue stays open for a human.
func TestOnlyRejectAndCancelCloseTheIssue(t *testing.T) {
	for _, tc := range []struct {
		action taskstate.Action
		status string
		closes bool
	}{
		{taskstate.ActionReject, "waiting_human", true},
		{taskstate.ActionCancel, "queued", true},
		{taskstate.ActionAbandon, "parked", false},
	} {
		t.Run(string(tc.action), func(t *testing.T) {
			store := &fakeStore{task: &Task{ID: 7, Owner: "acme", Repo: "widgets", Status: tc.status, ForgeBacked: true}}
			closed := false
			service := Service{Store: store, CloseIssue: func(context.Context, string, string, int, string) error {
				closed = true
				return nil
			}}
			if err := service.Apply(context.Background(), nil, agentActor(), 7, tc.action); err != nil {
				t.Fatalf("Apply(%s) error = %v", tc.action, err)
			}
			if closed != tc.closes {
				t.Fatalf("Apply(%s) closed the issue = %v, want %v", tc.action, closed, tc.closes)
			}
		})
	}
}
