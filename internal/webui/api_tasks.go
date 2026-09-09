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

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/taskactions"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
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
		repoURL, issueURL, prURL := s.taskURLs(tasks[i])
		views[i] = taskView{Task: tasks[i], Actions: taskstate.Actions(tasks[i].Status), RepoURL: repoURL, IssueURL: issueURL, PRURL: prURL}
	}
	writeJSON(w, views)
}

type taskView struct {
	workflow.Task
	Actions  []taskstate.Action `json:"actions"`
	RepoURL  string             `json:"repo_url,omitempty"`
	IssueURL string             `json:"issue_url,omitempty"`
	PRURL    string             `json:"pr_url,omitempty"`
}

func (s *Server) taskURLs(task workflow.Task) (repoURL, issueURL, prURL string) {
	if s.Cfg == nil {
		return "", "", ""
	}
	forgeCfg := forgeConfigForTask(s.Cfg.Get(), task)
	if forgeCfg.Host == "" || task.Owner == "" || task.Repo == "" {
		return "", "", ""
	}
	repoURL = strings.TrimRight(forgeCfg.Host, "/") + "/" + url.PathEscape(task.Owner) + "/" + url.PathEscape(task.Repo)
	if task.IsForgeBacked() && task.IssueNumber > 0 {
		issueURL = repoURL + "/issues/" + strconv.Itoa(task.IssueNumber)
	}
	if task.PRNumber > 0 {
		segment := "pull"
		if forgeCfg.Type == "gitea" {
			segment = "pulls"
		}
		prURL = repoURL + "/" + segment + "/" + strconv.Itoa(task.PRNumber)
	}
	return repoURL, issueURL, prURL
}

func forgeConfigForTask(cfg config.Config, task workflow.Task) config.Forge {
	for _, identity := range cfg.Identities {
		if task.Identity != "" && identity.Name == task.Identity {
			return identity.Forge
		}
	}
	var matched *config.Forge
	for i := range cfg.Identities {
		identity := &cfg.Identities[i]
		for _, repo := range identity.Repos {
			if repo.Owner == task.Owner && repo.Name == task.Repo {
				if matched != nil {
					return cfg.Forge
				}
				forgeCopy := identity.Forge
				matched = &forgeCopy
			}
		}
	}
	if matched != nil {
		return *matched
	}
	return cfg.Forge
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
	if err := s.applyOperatorTaskAction(r.Context(), id, action); err != nil {
		switch {
		case errors.Is(err, taskactions.ErrNotFound):
			http.Error(w, "task not found", http.StatusNotFound)
		case errors.Is(err, taskactions.ErrConflict), errors.Is(err, store.ErrStaleTransition):
			http.Error(w, err.Error(), http.StatusConflict)
		case errors.Is(err, taskactions.ErrUnavailable):
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
		default:
			s.logf("task action failed", "task", id, "err", err)
			http.Error(w, "task action failed", http.StatusInternalServerError)
		}
		return
	}
	writeJSON(w, map[string]any{"ok": true, "action": action, "task_id": id})
}

func decodeTaskAction(w http.ResponseWriter, r *http.Request) (taskstate.Action, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var req taskActionRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
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

// applyOperatorTaskAction sends the action to whoever owns task execution.
// The dashboard operator is authenticated and acts across identities, which
// the Gateway contract carries as its own method (archie-core-8cda.5.4).
//
// The UI process cannot run this itself: retry limits come from the daemon's
// configuration, closing the forge issue needs its forge client, the
// timeline needs its event bus, and stopping running work needs the
// goroutine or container that is executing it. Composing a local service
// over the task store alone would silently drop all four.
func (s *Server) applyOperatorTaskAction(ctx context.Context, id int64, action taskstate.Action) error {
	if s.Chat == nil || s.Chat.Contract == nil {
		return fmt.Errorf("%w: no gateway contract is wired", taskactions.ErrUnavailable)
	}
	_, err := s.Chat.Contract.ApplyOperatorTaskAction(ctx, id, action)
	return err
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
