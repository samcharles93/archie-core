// Package taskactions owns operator task mutations shared by chat and dashboard.
package taskactions

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/samcharles93/archie-core/internal/domain/identity"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	workflowtask "github.com/samcharles93/archie-core/internal/domain/workflow/task"
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
	ID                                        int64
	Owner, Repo, Identity, Status, ParkReason string
	IssueNumber, RetryCount                   int
	// PRNumber is the open pull request a pr_open task carries; rejecting the
	// task closes it.
	PRNumber    int
	ForgeBacked bool
	// Branch is the branch this task already pushed, if any. A retry that
	// continues pushed work resumes it, so the service refuses that mode for a
	// task with no branch rather than queueing a resume that cannot land.
	Branch string
	// RetryMode is the worktree mode already persisted for this task. A retry
	// that carries no explicit mode (chat has no mode syntax) keeps this value
	// rather than resetting it, so a chat retry of a remediation still
	// continues the branch its queued run recorded.
	RetryMode string
	// ReviewGate is the operator-approval gate's document as the task carries
	// it (workflowtask.EncodeReviewGate): the offer the gate wrote, and the answer
	// the response path fills in. The response path reads the offer here and
	// writes the answered document back.
	ReviewGate string
	// RereviewRounds is how many re-reviews this task's gate has granted. The
	// service reads it to refuse a re-review at the cap before the guarded
	// write, and the write itself re-checks it in the row.
	RereviewRounds int
	// Stages are the pinned definition's stage names in order, the resume
	// points a retry may name.
	Stages []string
	// Attempt is the run the operator acted on. It is stamped onto the event
	// this action records so an intervention is attributable to the run it
	// changed course -- without it a retry or a stop is indistinguishable
	// between attempts on the timeline.
	Attempt int
}

// ActionPayload is the per-action data an operator action carries: the
// review-gate answer for approve and rereview, and the worktree mode for retry.
// Every field is empty for the actions that do not use it, and for chat's
// /approve and /retry, which carry no instruction, selection or mode syntax.
type ActionPayload struct {
	// Instructions are what a re-review must focus on. The response path
	// requires them for ActionRereview and ignores them otherwise.
	Instructions string
	// Findings are the keys (prreview.ScoredFinding.Key) of the offered
	// findings an approve posts. Empty means all of them.
	Findings []string
	// RetryMode is the worktree choice a retry carries (taskstate.RetryMode):
	// refresh onto the base branch, or continue the work already pushed on the
	// task's branch. It is empty -- the explicit default, refresh -- for every
	// other action.
	RetryMode taskstate.RetryMode
	// ResumeFrom is the stage a retry starts at, keeping the results of the
	// stages before it. Empty runs every stage.
	ResumeFrom string
}

// Actor is who performed an action and the principal whose authority allowed
// it. An empty Principal means unattributed.
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

// ActorFromScope builds an actor for a caller whose identity archie knows but did
// not derive from a credential: a chat channel's bound identity. The kind is not
// known here, so the action is recorded as an agent's rather than a person's, and
// the authority it acted under is recorded separately when it is known.
func ActorFromScope(id string) Actor {
	return Actor{Identity: identity.IdentityID(id)}
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

// rereviewedKind names a re-review's actor the way the approved and rejected
// trios do: the kind describes the ACTOR, so an agent asking for a re-review is
// never recorded as a person's, and an action with no verified actor is
// recorded as a task-level event that credits nobody.
func rereviewedKind(actor Actor) string {
	switch {
	case actor.Human():
		return events.KindHumanRereviewed
	case actor.Attributed():
		return events.KindAgentRereviewed
	default:
		return events.KindTaskRereviewed
	}
}

type Store interface {
	TaskByID(context.Context, int64) (*Task, error)
	Transition(context.Context, int64, string, string, string) error
	Requeue(context.Context, int64, string, string) error
	// RetryTask requeues a parked task, records the operator's worktree mode
	// for the next dispatch and increments retry_count in one guarded write.
	RetryTask(context.Context, int64, string, string, string, workflowtask.Resume) error
	// RespondReviewGate is the guarded review gate response write
	// (internal/domain/storecontract.ReviewGateResponder): it records the
	// answered document, and for a re-review increments rereview_rounds and
	// requeues, refusing a re-review past the cap with ErrRereviewCapReached.
	RespondReviewGate(context.Context, int64, string, string, bool, int) error
	ArchiveTask(context.Context, int64, string, events.Event) (int64, error)
	InsertEvent(context.Context, events.Event) (int64, error)
	// CancelExecution cancels the execution and its open steps in one
	// transaction.
	CancelExecution(context.Context, int64, string, string) ([]int64, error)
}

// Service runs in the daemon, which owns execution cancellation and events.
type Service struct {
	Store      Store
	MaxRetries func(*Task) int
	CancelTask func(int64) bool
	CloseIssue func(context.Context, string, string, int, string) error
	ClosePR    func(context.Context, string, string, int, string) error
	MergePR    func(context.Context, string, string, int) error
	RemoveLogs func(int64) error
	// RemoveWorktree discards the task's clone on archive, the reap path for a
	// worktree terminal cleanup kept because it may hold uncaptured work.
	RemoveWorktree func(*Task) error
	Publish        func(events.Event)
	Warn           func(string, ...any)
}

// Apply runs action on task id. A nil scope is a dashboard caller acting
// across identities; otherwise scope limits which tasks may be acted on. res
// is the action's payload.
func (s Service) Apply(ctx context.Context, scope *string, actor Actor, id int64, action taskstate.Action, res ActionPayload) error {
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
	mutation, err := s.apply(ctx, task, actor, action, res)
	if err != nil {
		return err
	}
	if mutation.verb != "" && task.Status == taskstate.PROpen && task.PRNumber > 0 {
		s.closeRejectedPR(ctx, task, actor, mutation.verb)
	}
	if mutation.verb != "" && task.ForgeBacked {
		s.closeRejectedIssue(ctx, task, actor, mutation.verb)
	}
	s.emit(ctx, mutation.event)
	return nil
}

// Withdraw declines a queued or running task whose issue was withdrawn on
// the forge -- closed, unlabelled or unassigned -- cancelling its run. The
// issue is left as the person who withdrew it left it. A task in any other
// state, or one with a pull request open, is not withdrawn: its work is
// already in review or finished.
func (s Service) Withdraw(ctx context.Context, id int64, reason string) error {
	task, err := s.Store.TaskByID(ctx, id)
	if err != nil || task == nil {
		return err
	}
	if (task.Status != taskstate.Queued && task.Status != taskstate.Running) || task.PRNumber > 0 {
		return nil
	}
	event := s.attributed(task, Actor{})
	event.Kind, event.Detail = events.KindTaskWithdrawn, "issue withdrawn: "+reason
	event.Data = map[string]any{"reason": reason}
	if _, err := s.Store.CancelExecution(ctx, task.ID, event.Detail, declined); err != nil {
		return err
	}
	s.deliver(task.ID)
	s.emit(ctx, event)
	return nil
}

// The statuses the cancel path records, taken from the lifecycle's action table
// so the service cannot drift from the transitions it offers.
var (
	declined, _ = taskstate.ActionTarget(taskstate.ActionReject)
	parked, _   = taskstate.ActionTarget(taskstate.ActionStop)
)

type outcome struct {
	event events.Event
	verb  string
}

// apply performs the store mutation for one action and returns the event to
// record alongside it. Errors from the store are returned unwrapped: the
// caller maps them to a response, and the rules-level errors (ErrConflict)
// were already checked above.
func (s Service) apply(ctx context.Context, task *Task, actor Actor, action taskstate.Action, res ActionPayload) (outcome, error) {
	o := outcome{event: s.attributed(task, actor)}
	switch action {
	case taskstate.ActionApprove:
		return s.applyApprove(ctx, task, actor, o, res)
	case taskstate.ActionRereview:
		return s.applyRereview(ctx, task, actor, o, res)
	case taskstate.ActionRetry:
		return s.applyRetry(ctx, task, actor, o, res)
	case taskstate.ActionStop:
		return s.applyStop(ctx, task, actor, o)
	case taskstate.ActionReject:
		// A rejection ends the run's work wherever it sits, so it rides the
		// one cancel path like every other operator stop.
		o.event.Kind, o.event.Detail, o.verb = rejectedKind(actor), actor.describe("rejected"), "declined"
		if _, err := s.Store.CancelExecution(ctx, task.ID, actor.describe("rejected"), declined); err != nil {
			return o, err
		}
		s.deliver(task.ID)
		return o, nil
	case taskstate.ActionCancel, taskstate.ActionAbandon:
		return s.applyCancelOrAbandon(ctx, task, action, actor, o)
	case taskstate.ActionArchive:
		return s.applyArchive(ctx, task, actor, o)
	case taskstate.ActionMerge:
		return s.applyMerge(ctx, task, actor, o)
	default:
		return o, fmt.Errorf("unsupported task action %q", action)
	}
}

// applyApprove approves a waiting task. With a gate offer it records the
// answer on the offer; otherwise it requeues under the workflow the wait
// names.
func (s Service) applyApprove(ctx context.Context, task *Task, actor Actor, o outcome, res ActionPayload) (outcome, error) {
	o.event.Kind, o.event.Detail = approvedKind(actor), actor.describe("approved")
	gate, ok := workflowtask.DecodeReviewGate(task.ReviewGate)
	if !ok || !gate.Offered() {
		return o, s.Store.Requeue(ctx, task.ID, task.Status, "")
	}
	selection, err := selectedFindingKeys(gate, res.Findings)
	if err != nil {
		return o, err
	}
	gate.Outcome = workflowtask.GateApprove
	gate.Selection = selection
	gate.Instructions = ""
	if err := s.Store.RespondReviewGate(ctx, task.ID, task.Status, workflowtask.EncodeReviewGate(gate), false, workflowtask.MaxRereviewRounds); err != nil {
		return o, err
	}
	o.event.Detail = fmt.Sprintf("%s (%d of %d finding(s) selected)", actor.describe("approved"), len(selection), len(gate.Findings))
	return o, nil
}

// applyRereview records the operator's instructions and requeues the review.
// It conflicts when there is no gate offer or the cap is reached.
func (s Service) applyRereview(ctx context.Context, task *Task, actor Actor, o outcome, res ActionPayload) (outcome, error) {
	gate, ok := workflowtask.DecodeReviewGate(task.ReviewGate)
	if !ok || !gate.Offered() {
		return o, fmt.Errorf("task %d has no review gate to re-review: %w", task.ID, ErrConflict)
	}
	if strings.TrimSpace(res.Instructions) == "" {
		return o, fmt.Errorf("a re-review needs instructions: %w", ErrConflict)
	}
	if task.RereviewRounds >= workflowtask.MaxRereviewRounds {
		return o, fmt.Errorf("task %d has used all %d re-reviews: %w", task.ID, workflowtask.MaxRereviewRounds, ErrConflict)
	}
	gate.Outcome = workflowtask.GateRereview
	gate.Instructions = strings.TrimSpace(res.Instructions)
	gate.Findings = nil
	gate.Selection = nil
	o.event.Kind, o.event.Detail = rereviewedKind(actor), actor.describe("requested a re-review")
	if err := s.Store.RespondReviewGate(ctx, task.ID, task.Status, workflowtask.EncodeReviewGate(gate), true, workflowtask.MaxRereviewRounds); err != nil {
		return o, err
	}
	return o, nil
}

// selectedFindingKeys resolves an approve's selection against the offered
// findings. An absent selection means all of them; a selection naming a
// finding this offer does not hold is refused, because silently posting
// nothing is how an operator's answer turns into a review nobody sees.
func selectedFindingKeys(gate workflowtask.ReviewGate, requested []string) ([]string, error) {
	offered := make(map[string]struct{}, len(gate.Findings))
	keys := make([]string, 0, len(gate.Findings))
	for _, f := range gate.Findings {
		offered[f.Key] = struct{}{}
		keys = append(keys, f.Key)
	}
	if len(requested) == 0 {
		return keys, nil
	}
	selection := make([]string, 0, len(requested))
	for _, key := range requested {
		if _, ok := offered[key]; !ok {
			return nil, fmt.Errorf("finding %q is not part of this review offer: %w", key, ErrConflict)
		}
		selection = append(selection, key)
	}
	return selection, nil
}

// deliver cancels the in-memory context of a run the store has already recorded
// as cancelled.
func (s Service) deliver(taskID int64) {
	if s.CancelTask != nil {
		s.CancelTask(taskID)
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

func (s Service) applyRetry(ctx context.Context, task *Task, actor Actor, o outcome, payload ActionPayload) (outcome, error) {
	// An explicit mode always wins. When the action carries none -- chat has no
	// mode syntax -- the task's persisted mode is kept rather than reset, so a
	// chat retry of a remediation still continues its branch. An empty stored
	// value reads as the explicit refresh default.
	requested := payload.RetryMode
	if requested == "" {
		requested = taskstate.RetryMode(task.RetryMode)
	}
	mode, ok := taskstate.ResolveRetryMode(string(requested))
	if !ok {
		return o, fmt.Errorf("unknown retry mode %q: %w", requested, ErrConflict)
	}
	resume, err := resumePoint(task.Stages, payload.ResumeFrom)
	if err != nil {
		return o, fmt.Errorf("%w: %w", err, ErrConflict)
	}
	// The skipped steps' work must be on the branch the attempt continues.
	if resume.From != "" && task.Owner != "" {
		if strings.TrimSpace(task.Branch) == "" {
			return o, fmt.Errorf("resuming from %q needs the earlier steps' work pushed, and task %d has no pushed branch: %w", resume.From, task.ID, ErrConflict)
		}
		mode = taskstate.RetryContinuePushedWork
	}
	if mode == taskstate.RetryContinuePushedWork && strings.TrimSpace(task.Branch) == "" {
		return o, fmt.Errorf("task %d has no pushed branch to continue: %w", task.ID, ErrConflict)
	}
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
	err = s.Store.RetryTask(ctx, task.ID, "parked", "", string(mode), resume)
	if errors.Is(err, storecontract.ErrResumeIncomplete) {
		err = fmt.Errorf("%q did not complete in its last run, so there is nothing to resume from: %w", resume.After, ErrConflict)
	}
	o.event.Kind, o.event.Detail = events.KindTaskRetried, actor.describe("retried")
	o.event.Data = map[string]any{"retry_count": task.RetryCount + 1, "previous_reason": task.ParkReason, "retry_mode": string(mode)}
	if resume.From != "" {
		o.event.Data["resume_from"] = resume.From
	}
	return o, err
}

// resumePoint resolves a resume stage against the pinned definition's stages.
// Stage names are how runs are recorded, so a name two stages share cannot
// say which one to resume.
func resumePoint(stages []string, from string) (workflowtask.Resume, error) {
	if from == "" {
		return workflowtask.Resume{}, nil
	}
	at := slices.Index(stages, from)
	if at < 0 {
		return workflowtask.Resume{}, fmt.Errorf("the pinned workflow has no step %q", from)
	}
	resume := workflowtask.Resume{From: from}
	if at > 0 {
		resume.After = stages[at-1]
	}
	for _, name := range []string{resume.From, resume.After} {
		if name != "" && countOf(stages, name) > 1 {
			return workflowtask.Resume{}, fmt.Errorf("several steps run as %q; give each an id to resume there", name)
		}
	}
	return resume, nil
}

func countOf(stages []string, name string) int {
	n := 0
	for _, stage := range stages {
		if stage == name {
			n++
		}
	}
	return n
}

func (s Service) applyStop(ctx context.Context, task *Task, actor Actor, o outcome) (outcome, error) {
	if s.CancelTask == nil {
		return o, ErrUnavailable
	}
	o.event.Kind, o.event.Detail = events.KindTaskStopped, actor.describe("stopped")+"; recoverable work remains parked"
	// The store records the stop first, then the context delivers it. A
	// context cancel that found nothing executing says so; the parked
	// execution is recorded either way, and the task's own unwind cannot win
	// the race any more -- its next step write is already stale.
	if _, err := s.Store.CancelExecution(ctx, task.ID, o.event.Detail, parked); err != nil {
		return o, err
	}
	if !s.CancelTask(task.ID) {
		o.event.Detail = "no active execution was found; recoverable work was parked, " + actor.describe("stopped")
	}
	return o, nil
}

func (s Service) applyCancelOrAbandon(ctx context.Context, task *Task, action taskstate.Action, actor Actor, o outcome) (outcome, error) {
	if action == taskstate.ActionAbandon {
		// Abandoning ends archie's run, not the issue: no verb, so the issue
		// stays open for a human.
		o.event.Kind, o.event.Detail = events.KindTaskAbandoned, actor.describe("abandoned")
		if _, err := s.Store.CancelExecution(ctx, task.ID, o.event.Detail, declined); err != nil {
			return o, err
		}
		return o, nil
	}
	o.event.Kind, o.event.Detail, o.verb = events.KindTaskCancelled, actor.describe("cancelled"), "cancelled"
	if _, err := s.Store.CancelExecution(ctx, task.ID, o.event.Detail, declined); err != nil {
		return o, err
	}
	s.deliver(task.ID)
	return o, nil
}

// applyMerge merges the task's pull request on the forge, then records the
// task merged. A forge refusal (conflicts, failing checks, protection) leaves
// the task in review.
func (s Service) applyMerge(ctx context.Context, task *Task, actor Actor, o outcome) (outcome, error) {
	if s.MergePR == nil {
		return o, ErrUnavailable
	}
	if task.PRNumber <= 0 {
		return o, fmt.Errorf("%w: task has no pull request", ErrConflict)
	}
	if err := s.MergePR(ctx, task.Owner, task.Repo, task.PRNumber); err != nil {
		return o, fmt.Errorf("%w: merge pull request #%d: %w", ErrConflict, task.PRNumber, err)
	}
	o.event.Kind, o.event.Detail = events.KindPRMergeRequested, actor.describe(fmt.Sprintf("merged pull request #%d", task.PRNumber))
	o.event.Data = map[string]any{"pr_number": task.PRNumber}
	merged, _ := taskstate.ActionTarget(taskstate.ActionMerge)
	return o, s.Store.Transition(ctx, task.ID, task.Status, merged, o.event.Detail)
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
	if s.RemoveWorktree != nil {
		if cleanupErr := s.RemoveWorktree(task); cleanupErr != nil {
			s.warn("task worktree cleanup failed", "task", task.ID, "err", cleanupErr)
		}
	}
	return o, nil
}

// closeRejectedPR closes a rejected task's pull request and records it on the
// task's timeline. A failure is logged and leaves the PR open.
func (s Service) closeRejectedPR(ctx context.Context, task *Task, actor Actor, verb string) {
	if s.ClosePR == nil {
		s.warn("task rejected but no forge is wired; its pull request stays open", "task", task.ID)
		return
	}
	comment := fmt.Sprintf("Closing: this task was %s in archie (%s).", verb, actor.describe("actioned"))
	if err := s.ClosePR(ctx, task.Owner, task.Repo, task.PRNumber, comment); err != nil {
		s.warn("closing rejected task's pull request failed; it stays open", "task", task.ID, "pr", task.PRNumber, "err", err)
		return
	}
	s.emit(ctx, events.Event{
		TaskID: task.ID, Kind: events.KindPRClosed,
		Detail: fmt.Sprintf("closed pull request #%d", task.PRNumber),
		Data:   map[string]any{"pr_number": task.PRNumber},
	})
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
