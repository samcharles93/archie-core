// Package taskactions owns operator task mutations shared by chat and dashboard.
package taskactions

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/samcharles93/archie-core/internal/domain/identity"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/taskstate"
)

var (
	ErrNotFound    = errors.New("task not found")
	ErrConflict    = errors.New("task action conflict")
	ErrUnavailable = errors.New("runtime task control is unavailable")
)

// Task is the state needed to authorize and apply an operator action.
type Task struct {
	ID                                               int64
	Owner, Repo, Identity, Status, Stage, ParkReason string
	IssueNumber, RetryCount                          int
	ForgeBacked                                      bool
	// Attempt is the run the operator acted on. It is stamped onto the event
	// this action records so an intervention is attributable to the run it
	// changed course -- without it a retry or a stop is indistinguishable
	// between attempts on the timeline.
	Attempt int
}

// Actor is the identity that performed an action, and the principal whose
// authority permitted it.
//
// The two are separate fields because an agent acting under a person's standing
// approval is not the same fact as a person acting: a record that collapsed
// them would answer "who approved this" with the wrong identity. An empty
// Principal means UNATTRIBUTED -- no authority was recorded, which is a
// different fact from the actor authorising itself.
type Actor struct {
	Identity  identity.IdentityID
	Kind      identity.Kind
	Principal identity.IdentityID
}

// ActorFor builds an actor from the identity a credential resolved to. The kind
// travels with the actor so an event can record that an agent acted without a
// consumer having to resolve the identity to find out.
func ActorFor(value identity.Identity) Actor {
	return Actor{Identity: value.ID, Kind: value.Kind}
}

// AuthorisedBy records where an action's authority came from.
func (a Actor) AuthorisedBy(principal identity.IdentityID) Actor {
	a.Principal = principal
	return a
}

// Human reports whether a person performed the action. Only a human's action is
// recorded as a human's: the event kind describes the actor.
func (a Actor) Human() bool { return a.Kind == identity.KindUser }

// Attributed reports whether the action is attributed to an identity. An action
// with no identity is still recorded -- refusing it would hide the action from
// the timeline -- but its record claims nothing about who performed it, and its
// kind says so.
func (a Actor) Attributed() bool { return strings.TrimSpace(string(a.Identity)) != "" }

// describe renders the attribution for a human reading a task timeline.
func (a Actor) describe(verb string) string {
	if !a.Attributed() {
		return verb + " with no recorded actor"
	}
	text := verb + " by " + string(a.Identity)
	if a.Kind != "" {
		text += " (" + string(a.Kind) + ")"
	}
	if a.Principal != "" {
		return text + ", authorised by " + string(a.Principal)
	}
	return text + ", no authorising principal"
}

// approvedKind and rejectedKind name what happened and who did it. There is no
// single kind for an approval: an agent's approval recorded as a human's is the
// falsehood this trio exists to prevent, and an action with no verified actor
// is recorded as a task-level event rather than credited to anyone.
func approvedKind(actor Actor) string {
	switch {
	case actor.Human():
		return events.KindHumanApproved
	case actor.Attributed():
		return events.KindAgentApproved
	default:
		return events.KindTaskApproved
	}
}

func rejectedKind(actor Actor) string {
	switch {
	case actor.Human():
		return events.KindHumanRejected
	case actor.Attributed():
		return events.KindAgentRejected
	default:
		return events.KindTaskRejected
	}
}

type Store interface {
	TaskByID(context.Context, int64) (*Task, error)
	Transition(context.Context, int64, string, string, string) error
	Requeue(context.Context, int64, string, string) error
	RetryTask(context.Context, int64, string, string) error
	ArchiveTask(context.Context, int64, string, events.Event) (int64, error)
	InsertEvent(context.Context, events.Event) (int64, error)
}

// Service runs in the daemon, which owns execution cancellation and events.
type Service struct {
	Store      Store
	MaxRetries func(*Task) int
	CancelTask func(int64) bool
	CloseIssue func(context.Context, string, string, int, string) error
	RemoveLogs func(int64) error
	Publish    func(events.Event)
	Warn       func(string, ...any)
}

// Apply scopes chat requests to an identity. A nil scope denotes a caller
// authenticated across identities, which is the dashboard's own credential;
// scope limits which task a chat identity may act on, and never says who acted.
//
// The actor is not derived from scope: scope is which tasks a caller may touch,
// actor is who touched one, and the two are only equal by coincidence.
func (s Service) Apply(ctx context.Context, scope *string, actor Actor, id int64, action taskstate.Action) error {
	task, err := s.Store.TaskByID(ctx, id)
	if err != nil {
		return err
	}
	if task == nil {
		return fmt.Errorf("task %d: %w", id, ErrNotFound)
	}
	if scope != nil && task.Identity != *scope {
		return fmt.Errorf("task %d belongs to %q, not %q", id, task.Identity, *scope)
	}
	if err := taskstate.CheckAction(task.Status, action); err != nil {
		return fmt.Errorf("%w: %w", ErrConflict, err)
	}
	mutation, err := s.apply(ctx, task, actor, action)
	if err != nil {
		return err
	}
	if mutation.verb != "" && task.ForgeBacked {
		s.closeRejectedIssue(ctx, task, actor, mutation.verb)
	}
	s.emit(ctx, mutation.event)
	return nil
}

type outcome struct {
	event events.Event
	verb  string
}

// apply performs the store mutation for one action and returns the event to
// record alongside it. Errors from the store are returned unwrapped: the
// caller maps them to a response, and the rules-level errors (ErrConflict)
// were already checked above.
func (s Service) apply(ctx context.Context, task *Task, actor Actor, action taskstate.Action) (outcome, error) {
	o := outcome{event: s.attributed(task, actor)}
	switch action {
	case taskstate.ActionApprove:
		err := s.Store.Requeue(ctx, task.ID, "waiting_human", "implement")
		o.event.Kind, o.event.Detail = approvedKind(actor), actor.describe("approved")
		return o, err
	case taskstate.ActionRetry:
		return s.applyRetry(ctx, task, actor, o)
	case taskstate.ActionStop:
		return s.applyStop(ctx, task, actor, o)
	case taskstate.ActionReject:
		o.event.Kind, o.event.Detail, o.verb = rejectedKind(actor), actor.describe("rejected"), "rejected"
		if task.Status == "running" && s.CancelTask != nil {
			s.CancelTask(task.ID)
		}
		return o, s.Store.Transition(ctx, task.ID, task.Status, "closed_wont_do", actor.describe("declined"))
	case taskstate.ActionCancel, taskstate.ActionAbandon:
		return s.applyCancelOrAbandon(ctx, task, action, actor, o)
	case taskstate.ActionArchive:
		return s.applyArchive(ctx, task, actor, o)
	default:
		return o, fmt.Errorf("unsupported task action %q", action)
	}
}

// attributed builds the event skeleton every action carries: the task, the run
// and both identities. Every event this service records passes through here, so
// no action can be recorded without its attribution.
func (s Service) attributed(task *Task, actor Actor) events.Event {
	return events.Event{
		TaskID:      task.ID,
		Repo:        task.Owner + "/" + task.Repo,
		Issue:       task.IssueNumber,
		Attempt:     task.Attempt,
		ActorID:     string(actor.Identity),
		ActorKind:   string(actor.Kind),
		PrincipalID: string(actor.Principal),
	}
}

func (s Service) applyRetry(ctx context.Context, task *Task, actor Actor, o outcome) (outcome, error) {
	limit := 0
	if s.MaxRetries != nil {
		limit = s.MaxRetries(task)
	}
	if limit > 0 && task.RetryCount >= limit {
		reason := fmt.Sprintf("max retries reached (%d/%d)", task.RetryCount, limit)
		if err := s.Store.Transition(ctx, task.ID, "parked", "dead", reason); err != nil {
			s.warn("transition to dead failed", "task", task.ID, "err", err)
		} else {
			o.event.Kind, o.event.Detail = events.KindTaskDead, reason
			s.emit(ctx, o.event)
		}
		return o, fmt.Errorf("%w: %s", ErrConflict, reason)
	}
	err := s.Store.RetryTask(ctx, task.ID, "parked", "")
	o.event.Kind, o.event.Detail = events.KindTaskRetried, actor.describe("retried")
	o.event.Data = map[string]any{"retry_count": task.RetryCount + 1, "previous_stage": task.Stage, "previous_reason": task.ParkReason}
	return o, err
}

func (s Service) applyStop(ctx context.Context, task *Task, actor Actor, o outcome) (outcome, error) {
	if s.CancelTask == nil {
		return o, ErrUnavailable
	}
	o.event.Kind, o.event.Detail = events.KindTaskStopped, actor.describe("stopped")+"; recoverable work remains parked"
	if !s.CancelTask(task.ID) {
		o.event.Detail = "no active execution was found; recoverable work was parked, " + actor.describe("stopped")
	}
	return o, s.Store.Transition(ctx, task.ID, "running", "parked", o.event.Detail)
}

func (s Service) applyCancelOrAbandon(ctx context.Context, task *Task, action taskstate.Action, actor Actor, o outcome) (outcome, error) {
	from, kind, verb := "queued", events.KindTaskCancelled, "cancelled"
	if action == taskstate.ActionAbandon {
		from, kind, verb = "parked", events.KindTaskAbandoned, "abandoned"
	}
	o.event.Kind, o.event.Detail, o.verb = kind, actor.describe(verb), verb
	return o, s.Store.Transition(ctx, task.ID, from, "closed_wont_do", o.event.Detail)
}

func (s Service) applyArchive(ctx context.Context, task *Task, actor Actor, o outcome) (outcome, error) {
	o.event.Kind, o.event.Detail = events.KindTaskArchiveRequested, actor.describe("archive requested")
	id, err := s.Store.ArchiveTask(ctx, task.ID, task.Status, o.event)
	if err != nil {
		return o, err
	}
	o.event.ID = id
	if s.RemoveLogs != nil {
		if cleanupErr := s.RemoveLogs(task.ID); cleanupErr != nil {
			s.warn("task log cleanup failed", "task", task.ID, "err", cleanupErr)
		}
	}
	return o, nil
}

func (s Service) closeRejectedIssue(ctx context.Context, task *Task, actor Actor, verb string) {
	if s.CloseIssue == nil {
		s.warn("task rejected but no forge is wired; the issue stays open and will be re-polled", "task", task.ID)
		return
	}
	comment := fmt.Sprintf("Closing: this task was %s in archie (%s). Reopen the issue to have archie pick it up again.", verb, actor.describe("actioned"))
	if err := s.CloseIssue(ctx, task.Owner, task.Repo, task.IssueNumber, comment); err != nil {
		s.warn("closing rejected issue failed; it stays open and will be re-polled", "task", task.ID, "err", err)
	}
}

func (s Service) emit(ctx context.Context, e events.Event) {
	if e.ID == 0 {
		id, err := s.Store.InsertEvent(ctx, e)
		if err != nil {
			s.warn("operator activity persistence failed", "kind", e.Kind, "task", e.TaskID, "err", err)
		} else {
			e.ID = id
		}
	}
	if s.Publish != nil {
		s.Publish(e)
	}
}

func (s Service) warn(msg string, args ...any) {
	if s.Warn != nil {
		s.Warn(msg, args...)
	}
}
