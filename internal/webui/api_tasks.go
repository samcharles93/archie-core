package webui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/store"
	"github.com/samcharles93/archie-core/internal/taskstate"
)

func (s *Server) handleSummary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	statuses, err := s.Store.StatusCounts(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	workflows, err := s.Store.WorkflowStats(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	stages, err := s.Store.StageStats(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	days, err := s.Store.TokensByDay(ctx, 14)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{
		"statuses": statuses, "workflows": workflows,
		"stages": stages, "tokens_by_day": days,
	})
}

func (s *Server) handleTasks(w http.ResponseWriter, r *http.Request) {
	tasks, err := s.Store.Tasks(r.Context(), 100)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, tasks)
}

func (s *Server) handleClearTasks(w http.ResponseWriter, r *http.Request) {
	deleted, err := s.Store.ClearTerminalTasks(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"deleted": deleted})
}

func (s *Server) handleTask(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	evs, err := s.Store.TaskEvents(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, evs)
}

type taskActionRequest struct {
	Action string `json:"action"`
}

func (s *Server) handleTaskAction(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	var req taskActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid action", http.StatusBadRequest)
		return
	}
	task, err := s.Store.TaskByID(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if task == nil {
		http.Error(w, "task not found", http.StatusNotFound)
		return
	}

	// Each action reports the event to record, or writes its own error
	// response and returns nothing. Keeping the dispatch flat here and the
	// rules in one function each is what stops this growing back into a
	// single branch nobody can read.
	ctx := r.Context()
	var outcome *actionOutcome
	switch req.Action {
	case "approve":
		outcome = s.approveTask(ctx, w, task)
	case "retry":
		outcome = s.retryTask(ctx, w, task)
	case "reject":
		outcome = s.rejectTask(ctx, w, task)
	default:
		http.Error(w, "unknown action", http.StatusBadRequest)
		return
	}
	if outcome == nil {
		return
	}

	s.emit(events.Event{
		Kind: outcome.kind, TaskID: task.ID,
		Repo: task.Owner + "/" + task.Repo, Issue: task.IssueNumber,
		Detail: outcome.detail,
	})
	writeJSON(w, map[string]any{"ok": true, "action": req.Action, "task_id": id})
}

// actionOutcome is what a completed operator action should record.
type actionOutcome struct {
	kind   string
	detail string
}

// storeFailed writes the response for a failed store write and reports
// whether it did. A store write that fails is not the caller's fault;
// answering 409 made the UI say "conflict" for a broken database.
func storeFailed(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	http.Error(w, err.Error(), http.StatusInternalServerError)
	return true
}

func (s *Server) approveTask(ctx context.Context, w http.ResponseWriter, task *store.Task) *actionOutcome {
	if err := taskstate.CheckApprove(task.Status); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return nil
	}
	if storeFailed(w, s.Store.Requeue(ctx, task.ID, store.StatusWaitingHuman, "implement")) {
		return nil
	}
	return &actionOutcome{events.KindHumanApproved, "approved via the dashboard"}
}

// retryTask requeues a parked task, or retires it once max_retries is spent.
//
// max_retries was configured, defaulted and reported by /api/config while
// nothing enforced it: retry_count climbed forever, so a task that fails
// every time could be retried without limit. At the cap the task belongs in
// dead, which is what the setting exists to say.
func (s *Server) retryTask(ctx context.Context, w http.ResponseWriter, task *store.Task) *actionOutcome {
	if err := taskstate.CheckRetry(task.Status); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return nil
	}
	if limit := s.maxRetriesFor(task); limit > 0 && task.RetryCount >= limit {
		s.retireTask(ctx, task, fmt.Sprintf("max retries reached (%d/%d)", task.RetryCount, limit))
		http.Error(w, fmt.Sprintf("max retries reached (%d/%d)", task.RetryCount, limit), http.StatusConflict)
		return nil
	}
	if storeFailed(w, s.Store.Requeue(ctx, task.ID, store.StatusParked, "")) {
		return nil
	}
	if storeFailed(w, s.Store.IncrementRetryCount(ctx, task.ID)) {
		return nil
	}
	return &actionOutcome{events.KindTaskRetried, "retried via the dashboard"}
}

// retireTask moves a spent task to dead. Failure is logged, not returned:
// the caller is already answering with the reason it refused the retry.
func (s *Server) retireTask(ctx context.Context, task *store.Task, reason string) {
	if err := s.Store.Transition(ctx, task.ID, store.StatusParked, store.StatusDead, reason); err != nil {
		s.logf("transition to dead failed", "task", task.ID, "err", err)
		return
	}
	s.emit(events.Event{
		Kind: events.KindTaskDead, TaskID: task.ID,
		Repo: task.Owner + "/" + task.Repo, Issue: task.IssueNumber, Detail: reason,
	})
}

func (s *Server) rejectTask(ctx context.Context, w http.ResponseWriter, task *store.Task) *actionOutcome {
	// The dashboard only offers Reject on a waiting_human task, but the rule
	// is the shared one so the two surfaces cannot drift apart again.
	if err := taskstate.CheckApprove(task.Status); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return nil
	}
	err := s.Store.Transition(ctx, task.ID, store.StatusWaitingHuman, store.StatusClosedWontDo, "declined from the dashboard")
	if storeFailed(w, err) {
		return nil
	}
	s.closeRejectedIssue(ctx, task)
	return &actionOutcome{events.KindHumanRejected, "rejected via the dashboard"}
}

// maxRetriesFor resolves the retry cap for a task, honouring a per-repo
// override. Zero means unlimited.
func (s *Server) maxRetriesFor(task *store.Task) int {
	if s.Cfg == nil {
		return 0
	}
	for _, repo := range s.Cfg.Repos {
		if repo.Owner == task.Owner && repo.Name == task.Repo {
			return repo.EffectiveMaxRetries(s.Cfg.MaxRetries)
		}
	}
	return s.Cfg.MaxRetries
}

// closeRejectedIssue closes the forge issue behind a task the operator has
// declined.
//
// Without this the issue stays open, labelled and assigned, so the next poll
// re-enqueues it -- and once the operator uses Clear, which deletes the task
// row, that is a second implementation and a second PR for work already
// refused. Approve and retry deliberately leave the issue open: the task is
// going back to work.
//
// Failure is logged, not returned. The operator's decision is already
// recorded, and reporting an error would invite them to click again.
func (s *Server) closeRejectedIssue(ctx context.Context, task *store.Task) {
	if s.Issues == nil {
		s.logf("task rejected but no forge is wired; the issue stays open and will be re-polled",
			"task", task.ID, "issue", task.IssueNumber)
		return
	}
	// A chat task's issue number is synthetic and matches no forge issue.
	if !task.IsForgeBacked() {
		return
	}
	const comment = "Closing: this was rejected from the archie dashboard. " +
		"Reopen the issue to have archie pick it up again."
	if err := s.Issues.CloseIssue(ctx, task.Owner, task.Repo, task.IssueNumber, comment); err != nil {
		s.logf("closing rejected issue failed; it stays open and will be re-polled",
			"task", task.ID, "issue", task.IssueNumber, "err", err)
	}
}

// emit publishes an operator action so it reaches the task timeline and the
// live activity stream. A nil publisher is not an error: the action is still
// recorded in the store.
func (s *Server) emit(e events.Event) {
	if s.Events == nil || e.Kind == "" {
		return
	}
	s.Events.Publish(e)
}

func (s *Server) logf(msg string, args ...any) {
	if s.Log == nil {
		return
	}
	s.Log.Warn(msg, args...)
}
