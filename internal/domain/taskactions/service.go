// Package taskactions owns operator task mutations shared by chat and dashboard.
package taskactions

import (
	"context"
	"errors"
	"fmt"

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

// Apply scopes chat requests to an identity. A nil identity denotes an
// authenticated dashboard operator, who may act across identities.
func (s Service) Apply(ctx context.Context, identity *string, id int64, action taskstate.Action) error {
	task, err := s.Store.TaskByID(ctx, id)
	if err != nil {
		return err
	}
	if task == nil {
		return fmt.Errorf("task %d: %w", id, ErrNotFound)
	}
	if identity != nil && task.Identity != *identity {
		return fmt.Errorf("task %d belongs to %q, not %q", id, task.Identity, *identity)
	}
	if err := taskstate.CheckAction(task.Status, action); err != nil {
		return fmt.Errorf("%w: %w", ErrConflict, err)
	}
	mutation, err := s.apply(ctx, task, identity, action)
	if err != nil {
		return err
	}
	if mutation.verb != "" && task.ForgeBacked {
		s.closeRejectedIssue(ctx, task, identity, mutation.verb)
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
func (s Service) apply(ctx context.Context, task *Task, identity *string, action taskstate.Action) (outcome, error) {
	source := "the dashboard"
	if identity != nil {
		source = "chat"
	}
	o := outcome{event: events.Event{TaskID: task.ID, Repo: task.Owner + "/" + task.Repo, Issue: task.IssueNumber}}
	switch action {
	case taskstate.ActionApprove:
		err := s.Store.Requeue(ctx, task.ID, "waiting_human", "implement")
		o.event.Kind, o.event.Detail = events.KindHumanApproved, "approved via "+source
		return o, err
	case taskstate.ActionRetry:
		return s.applyRetry(ctx, task, source, o)
	case taskstate.ActionStop:
		return s.applyStop(ctx, task, source, o)
	case taskstate.ActionReject:
		o.event.Kind, o.event.Detail, o.verb = events.KindHumanRejected, "rejected via "+source, "rejected"
		if task.Status == "running" && s.CancelTask != nil {
			s.CancelTask(task.ID)
		}
		return o, s.Store.Transition(ctx, task.ID, task.Status, "closed_wont_do", "declined from "+source)
	case taskstate.ActionCancel, taskstate.ActionAbandon:
		return s.applyCancelOrAbandon(ctx, task, action, source, o)
	case taskstate.ActionArchive:
		return s.applyArchive(ctx, task, source, o)
	default:
		return o, fmt.Errorf("unsupported task action %q", action)
	}
}

func (s Service) applyRetry(ctx context.Context, task *Task, source string, o outcome) (outcome, error) {
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
	o.event.Kind, o.event.Detail = events.KindTaskRetried, "retried via "+source
	o.event.Data = map[string]any{"retry_count": task.RetryCount + 1, "previous_stage": task.Stage, "previous_reason": task.ParkReason}
	return o, err
}

func (s Service) applyStop(ctx context.Context, task *Task, source string, o outcome) (outcome, error) {
	if s.CancelTask == nil {
		return o, ErrUnavailable
	}
	o.event.Kind, o.event.Detail = events.KindTaskStopped, "stopped via "+source+"; recoverable work remains parked"
	if !s.CancelTask(task.ID) {
		o.event.Detail = "no active execution was found; recoverable work was parked via " + source
	}
	return o, s.Store.Transition(ctx, task.ID, "running", "parked", o.event.Detail)
}

func (s Service) applyCancelOrAbandon(ctx context.Context, task *Task, action taskstate.Action, source string, o outcome) (outcome, error) {
	from, kind, verb := "queued", events.KindTaskCancelled, "cancelled"
	if action == taskstate.ActionAbandon {
		from, kind, verb = "parked", events.KindTaskAbandoned, "abandoned"
	}
	o.event.Kind, o.event.Detail, o.verb = kind, verb+" via "+source, verb
	return o, s.Store.Transition(ctx, task.ID, from, "closed_wont_do", o.event.Detail)
}

func (s Service) applyArchive(ctx context.Context, task *Task, source string, o outcome) (outcome, error) {
	o.event.Kind, o.event.Detail = events.KindTaskArchiveRequested, "archive requested via "+source
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

func (s Service) closeRejectedIssue(ctx context.Context, task *Task, identity *string, verb string) {
	if s.CloseIssue == nil {
		s.warn("task rejected but no forge is wired; the issue stays open and will be re-polled", "task", task.ID)
		return
	}
	comment := fmt.Sprintf("Closing: this task was %s from the archie %s. Reopen the issue to have archie pick it up again.", verb, "dashboard")
	if identity != nil {
		comment = fmt.Sprintf("Closing: this task was %s from archie chat. Reopen the issue to have archie pick it up again.", verb)
	}
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
