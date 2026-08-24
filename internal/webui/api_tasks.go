package webui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"

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
	views := make([]taskView, len(tasks))
	for i := range tasks {
		views[i] = taskView{Task: tasks[i], Actions: taskstate.Actions(tasks[i].Status)}
	}
	writeJSON(w, views)
}

type taskView struct {
	store.Task
	Actions []taskstate.Action `json:"actions"`
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
	if !s.authorizeTaskMutation(w, r) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	action, ok := decodeTaskAction(w, r)
	if !ok {
		return
	}
	task, err := s.Store.TaskByID(r.Context(), id)
	if err != nil {
		s.logf("task action lookup failed", "task", id, "err", err)
		http.Error(w, "task action failed", http.StatusInternalServerError)
		return
	}
	if task == nil {
		http.Error(w, "task not found", http.StatusNotFound)
		return
	}
	if err := taskstate.CheckAction(task.Status, action); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}

	// Each action reports the event to record, or writes its own error
	// response and returns nothing. Keeping the dispatch flat here and the
	// rules in one function each is what stops this growing back into a
	// single branch nobody can read.
	outcome := s.applyTaskAction(r.Context(), w, task, action)
	if outcome == nil {
		return
	}

	s.emit(r.Context(), events.Event{
		ID:   outcome.eventID,
		Kind: outcome.kind, TaskID: task.ID,
		Repo: task.Owner + "/" + task.Repo, Issue: task.IssueNumber,
		Detail: outcome.detail,
	})
	writeJSON(w, map[string]any{"ok": true, "action": action, "task_id": id})
}

func decodeTaskAction(w http.ResponseWriter, r *http.Request) (taskstate.Action, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var req taskActionRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(w, "action body too large", http.StatusRequestEntityTooLarge)
			return "", false
		}
		http.Error(w, "invalid action", http.StatusBadRequest)
		return "", false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		http.Error(w, "invalid action", http.StatusBadRequest)
		return "", false
	}
	action := taskstate.Action(req.Action)
	if !taskMutation(action) {
		http.Error(w, "unknown action", http.StatusBadRequest)
		return "", false
	}
	return action, true
}

func (s *Server) applyTaskAction(
	ctx context.Context,
	w http.ResponseWriter,
	task *store.Task,
	action taskstate.Action,
) *actionOutcome {
	switch action {
	case taskstate.ActionCancel:
		return s.declineTask(ctx, w, task, store.StatusQueued, events.KindTaskCancelled, "cancelled")
	case taskstate.ActionStop:
		return s.stopTask(ctx, w, task)
	case taskstate.ActionApprove:
		return s.approveTask(ctx, w, task)
	case taskstate.ActionRetry:
		return s.retryTask(ctx, w, task)
	case taskstate.ActionReject:
		return s.rejectTask(ctx, w, task)
	case taskstate.ActionAbandon:
		return s.declineTask(ctx, w, task, store.StatusParked, events.KindTaskAbandoned, "abandoned")
	case taskstate.ActionArchive:
		return s.archiveTask(ctx, w, task)
	default:
		return nil
	}
}

func taskMutation(action taskstate.Action) bool {
	switch action {
	case taskstate.ActionCancel, taskstate.ActionStop, taskstate.ActionApprove,
		taskstate.ActionReject, taskstate.ActionRetry, taskstate.ActionAbandon,
		taskstate.ActionArchive:
		return true
	default:
		return false
	}
}

// authorizeTaskMutation applies the browser mutation contract at the handler
// boundary: JSON only, a non-simple custom header, and a matching Origin when
// the browser supplies one. The custom header forces cross-origin callers
// through a CORS preflight, which this server never permits.
//
// Origin comparison previously validated against the daemon's own direct network
// connection (r.TLS). It now validates against the effective external scheme
// and host, deriving them from X-Forwarded-Proto and X-Forwarded-Host only when
// explicit proxy header trust is enabled (Web.TrustForwardedHeaders). When trust
// is disabled (the default), forwarded headers are ignored to prevent untrusted
// clients from forging Origin scheme checks on exposed daemons.
func (s *Server) authorizeTaskMutation(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("X-Archie-CSRF") != "1" {
		http.Error(w, "missing CSRF header", http.StatusForbidden)
		return false
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		http.Error(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		http.Error(w, "cross-origin mutation refused", http.StatusForbidden)
		return false
	}
	wantScheme, wantHost := s.expectedOrigin(r)
	if !validOrigin(u, wantScheme, wantHost) {
		http.Error(w, "cross-origin mutation refused", http.StatusForbidden)
		return false
	}
	return true
}

func (s *Server) expectedOrigin(r *http.Request) (scheme, host string) {
	scheme = "http"
	if r.TLS != nil {
		scheme = "https"
	}
	host = r.Host
	if s == nil || !s.trustForwardedHeaders() {
		return scheme, host
	}
	if proto := firstForwardedValue(r.Header.Get("X-Forwarded-Proto")); proto != "" {
		scheme = strings.ToLower(proto)
	}
	if fHost := firstForwardedValue(r.Header.Get("X-Forwarded-Host")); fHost != "" {
		host = fHost
	}
	return scheme, host
}

func firstForwardedValue(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if first, _, ok := strings.Cut(raw, ","); ok {
		raw = first
	}
	return strings.TrimSpace(raw)
}

func validOrigin(u *url.URL, wantScheme, wantHost string) bool {
	if u == nil || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	return strings.EqualFold(u.Scheme, wantScheme) && strings.EqualFold(u.Host, wantHost)
}

// actionOutcome is what a completed operator action should record.
type actionOutcome struct {
	kind    string
	detail  string
	eventID int64
}

// storeFailed writes the response for a failed store write and reports
// whether it did. A store write that fails is not the caller's fault;
// answering 409 made the UI say "conflict" for a broken database.
func (s *Server) storeFailed(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, store.ErrStaleTransition) {
		http.Error(w, "task changed state; refresh and try again", http.StatusConflict)
		return true
	}
	s.logf("task action store write failed", "err", err)
	http.Error(w, "task action failed", http.StatusInternalServerError)
	return true
}

func (s *Server) declineTask(
	ctx context.Context,
	w http.ResponseWriter,
	task *store.Task,
	from, kind, verb string,
) *actionOutcome {
	detail := verb + " via the dashboard"
	if s.storeFailed(w, s.Store.Transition(ctx, task.ID, from, store.StatusClosedWontDo, detail)) {
		return nil
	}
	s.closeDeclinedIssue(ctx, task, verb)
	return &actionOutcome{kind: kind, detail: detail}
}

func (s *Server) stopTask(ctx context.Context, w http.ResponseWriter, task *store.Task) *actionOutcome {
	if s.TaskStopper == nil {
		http.Error(w, "runtime task control is unavailable", http.StatusServiceUnavailable)
		return nil
	}
	cancelled := s.TaskStopper.CancelTask(task.ID)
	detail := "stopped via the dashboard; recoverable work remains parked"
	if !cancelled {
		// A running row without a runtime entry is stale work from a crashed or
		// migrated process. Nothing is executing, but parking the guarded row
		// still gives the operator the promised recoverable state.
		detail = "no active execution was found; recoverable work was parked via the dashboard"
	}
	if s.storeFailed(w, s.Store.Transition(ctx, task.ID, store.StatusRunning, store.StatusParked, detail)) {
		return nil
	}
	return &actionOutcome{kind: events.KindTaskStopped, detail: detail}
}

func (s *Server) archiveTask(ctx context.Context, w http.ResponseWriter, task *store.Task) *actionOutcome {
	detail := "archive requested via the dashboard"
	eventID, err := s.Store.ArchiveTask(ctx, task.ID, task.Status, events.Event{
		Kind: events.KindTaskArchiveRequested, TaskID: task.ID,
		Repo: task.Owner + "/" + task.Repo, Issue: task.IssueNumber,
		Detail: detail,
	})
	if s.storeFailed(w, err) {
		return nil
	}
	// Best-effort: an archived task's log files not getting cleaned up is
	// far less costly than the archive action itself failing over it, so
	// this never blocks the response the way s.storeFailed above does.
	//
	// Known, accepted gap: a task reaches a terminal status (satisfying
	// ArchiveTask's precondition) partway through Daemon.process, but its
	// TaskLogs sink isn't closed until process() itself returns -- which can
	// be later, if teardown after the terminal transition takes any time at
	// all. A system-log message landing in that window races this Remove
	// with an open Write to the same directory. Not corrupting (the write
	// lands on a soon-to-be-unlinked inode and is discarded) and not worth
	// closing here: doing so would mean tying TaskLogs.Close to the store
	// transition instead of to process()'s return, a larger change than
	// this call site should make unilaterally.
	if err := s.TaskLogs.Remove(task.ID); err != nil {
		s.Log.Warn("task log cleanup failed", "task", task.ID, "err", err)
	}
	return &actionOutcome{kind: events.KindTaskArchiveRequested, detail: detail, eventID: eventID}
}

func (s *Server) approveTask(ctx context.Context, w http.ResponseWriter, task *store.Task) *actionOutcome {
	if err := taskstate.CheckApprove(task.Status); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return nil
	}
	if s.storeFailed(w, s.Store.Requeue(ctx, task.ID, store.StatusWaitingHuman, "implement")) {
		return nil
	}
	return &actionOutcome{kind: events.KindHumanApproved, detail: "approved via the dashboard"}
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
	if s.storeFailed(w, s.Store.RetryTask(ctx, task.ID, store.StatusParked, "")) {
		return nil
	}
	return &actionOutcome{kind: events.KindTaskRetried, detail: "retried via the dashboard"}
}

// retireTask moves a spent task to dead. Failure is logged, not returned:
// the caller is already answering with the reason it refused the retry.
func (s *Server) retireTask(ctx context.Context, task *store.Task, reason string) {
	if err := s.Store.Transition(ctx, task.ID, store.StatusParked, store.StatusDead, reason); err != nil {
		s.logf("transition to dead failed", "task", task.ID, "err", err)
		return
	}
	s.emit(ctx, events.Event{
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
	if s.storeFailed(w, err) {
		return nil
	}
	s.closeDeclinedIssue(ctx, task, "rejected")
	return &actionOutcome{kind: events.KindHumanRejected, detail: "rejected via the dashboard"}
}

// maxRetriesFor resolves the retry cap for a task, honouring a per-repo
// override. Zero means unlimited.
func (s *Server) maxRetriesFor(task *store.Task) int {
	if s.Cfg == nil {
		return 0
	}
	cfg := s.Cfg.Get()
	for _, repo := range cfg.Repos {
		if repo.Owner == task.Owner && repo.Name == task.Repo {
			return repo.EffectiveMaxRetries(cfg.MaxRetries)
		}
	}
	return cfg.MaxRetries
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
func (s *Server) closeDeclinedIssue(ctx context.Context, task *store.Task, verb string) {
	if s.Issues == nil {
		s.logf("task rejected but no forge is wired; the issue stays open and will be re-polled",
			"task", task.ID, "issue", task.IssueNumber)
		return
	}
	// A chat task's issue number is synthetic and matches no forge issue.
	if !task.IsForgeBacked() {
		return
	}
	comment := fmt.Sprintf("Closing: this task was %s from the archie dashboard. "+
		"Reopen the issue to have archie pick it up again.", verb)
	if err := s.Issues.CloseIssue(ctx, task.Owner, task.Repo, task.IssueNumber, comment); err != nil {
		s.logf("closing rejected issue failed; it stays open and will be re-polled",
			"task", task.ID, "issue", task.IssueNumber, "err", err)
	}
}

// emit publishes an operator action so it reaches the task timeline and the
// live activity stream. A nil publisher is not an error: the action is still
// recorded in the store.
func (s *Server) emit(ctx context.Context, e events.Event) {
	if e.Kind == "" {
		return
	}
	if e.ID == 0 {
		id, err := s.Store.InsertEvent(ctx, e)
		if err != nil {
			s.logf("operator activity persistence failed", "kind", e.Kind, "task", e.TaskID, "err", err)
		} else {
			e.ID = id
		}
	}
	if s.Events != nil {
		s.Events.Publish(e)
	}
}

func (s *Server) logf(msg string, args ...any) {
	if s.Log == nil {
		return
	}
	s.Log.Warn(msg, args...)
}
