package webui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/store"
)

// recordingCloser captures the forge issue-closure calls an action makes.
type recordingCloser struct {
	calls []closeCall
	err   error
}

type closeCall struct {
	owner, repo string
	number      int
	comment     string
}

func (r *recordingCloser) CloseIssue(_ context.Context, owner, repo string, number int, comment string) error {
	r.calls = append(r.calls, closeCall{owner, repo, number, comment})
	return r.err
}

// actionServer builds a dashboard server with a task in the given state, plus
// the collaborators the action path needs.
func actionServer(t *testing.T, status, parkReason string) (*Server, *store.Task, *recordingCloser, *events.Bus, *events.Sub) {
	t.Helper()
	srv := newTestServer(t)
	ctx := t.Context()

	if _, err := srv.Store.EnqueueIssue(ctx, "acme", "widget", 11, "a task", "", "", ""); err != nil {
		t.Fatal(err)
	}
	task, err := srv.Store.ClaimNext(ctx)
	if err != nil || task == nil {
		t.Fatalf("ClaimNext = (%+v, %v)", task, err)
	}
	if err := srv.Store.Transition(ctx, task.ID, store.StatusRunning, status, parkReason); err != nil {
		t.Fatal(err)
	}

	closer := &recordingCloser{}
	bus := events.NewBus()
	t.Cleanup(bus.Close)
	sub := bus.Subscribe(16)

	srv.Issues = closer
	srv.Events = bus
	srv.Cfg = &config.Config{MaxRetries: 3}
	return srv, task, closer, bus, sub
}

func postAction(t *testing.T, srv *Server, id int64, action string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/tasks/"+strconv.FormatInt(id, 10)+"/action",
		bytes.NewBufferString(fmt.Sprintf(`{"action":%q}`, action)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	return w
}

// max_retries is configured, defaulted and shown in /api/config, but nothing
// enforced it: the Web UI retry incremented retry_count with no cap, so a
// task that fails every time could be retried forever. At the cap the task
// belongs in dead, which is what the setting exists to express.
func TestRetryEnforcesMaxRetries(t *testing.T) {
	tests := []struct {
		name       string
		priorCount int
		maxRetries int
		wantStatus int
		wantTask   string
	}{
		{name: "under the cap", priorCount: 0, maxRetries: 3, wantStatus: http.StatusOK, wantTask: store.StatusQueued},
		{name: "one below the cap", priorCount: 2, maxRetries: 3, wantStatus: http.StatusOK, wantTask: store.StatusQueued},
		{name: "at the cap", priorCount: 3, maxRetries: 3, wantStatus: http.StatusConflict, wantTask: store.StatusDead},
		{name: "past the cap", priorCount: 5, maxRetries: 3, wantStatus: http.StatusConflict, wantTask: store.StatusDead},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv, task, _, _, _ := actionServer(t, store.StatusParked, "it broke")
			srv.Cfg = &config.Config{MaxRetries: tc.maxRetries}
			ctx := t.Context()
			for range tc.priorCount {
				if err := srv.Store.IncrementRetryCount(ctx, task.ID); err != nil {
					t.Fatal(err)
				}
			}

			w := postAction(t, srv, task.ID, "retry")
			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", w.Code, tc.wantStatus, w.Body)
			}

			got, err := srv.Store.TaskByID(ctx, task.ID)
			if err != nil || got == nil {
				t.Fatalf("TaskByID = (%+v, %v)", got, err)
			}
			if got.Status != tc.wantTask {
				t.Errorf("status = %q, want %q", got.Status, tc.wantTask)
			}
		})
	}
}

// Rejecting a task must stop the forge issue coming back. It stays open,
// labelled and assigned otherwise, so the next poll re-enqueues it -- and if
// the operator has used the dashboard's Clear, which deletes the row, that is
// a second implementation and a second PR for work already declined.
func TestRejectClosesTheForgeIssue(t *testing.T) {
	srv, task, closer, _, _ := actionServer(t, store.StatusWaitingHuman, "review")

	if w := postAction(t, srv, task.ID, "reject"); w.Code != http.StatusOK {
		t.Fatalf("reject status = %d, body %s", w.Code, w.Body)
	}

	if len(closer.calls) != 1 {
		t.Fatalf("CloseIssue calls = %d, want 1: the issue stays open and is "+
			"re-polled, so the work is done twice", len(closer.calls))
	}
	call := closer.calls[0]
	if call.owner != "acme" || call.repo != "widget" || call.number != 11 {
		t.Errorf("closed %s/%s#%d, want acme/widget#11", call.owner, call.repo, call.number)
	}
	if call.comment == "" {
		t.Error("closed with no comment: an issue that closes itself with no " +
			"explanation reads as archie losing the work")
	}
}

// Approve and retry put the task back to work, so the issue must stay open.
func TestApproveAndRetryDoNotCloseTheIssue(t *testing.T) {
	for _, tc := range []struct{ action, status, reason string }{
		{"approve", store.StatusWaitingHuman, "review"},
		{"retry", store.StatusParked, "it broke"},
	} {
		t.Run(tc.action, func(t *testing.T) {
			srv, task, closer, _, _ := actionServer(t, tc.status, tc.reason)
			if w := postAction(t, srv, task.ID, tc.action); w.Code != http.StatusOK {
				t.Fatalf("status = %d, body %s", w.Code, w.Body)
			}
			if len(closer.calls) != 0 {
				t.Errorf("CloseIssue called %d times for %s; the task is going "+
					"back to work", len(closer.calls), tc.action)
			}
		})
	}
}

// A chat-sourced task has a synthetic issue number that matches no forge
// issue, so closing it would either error or close an unrelated issue.
func TestRejectDoesNotCloseAChatTaskIssue(t *testing.T) {
	srv := newTestServer(t)
	ctx := t.Context()
	closer := &recordingCloser{}
	srv.Issues = closer
	srv.Cfg = &config.Config{MaxRetries: 3}

	task, err := srv.Store.EnqueueChatTask(ctx, "acme", "widget", "chat task", "", "", "", 900001)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.Store.ClaimNext(ctx); err != nil {
		t.Fatal(err)
	}
	if err := srv.Store.Transition(ctx, task.ID, store.StatusRunning, store.StatusWaitingHuman, "review"); err != nil {
		t.Fatal(err)
	}

	if w := postAction(t, srv, task.ID, "reject"); w.Code != http.StatusOK {
		t.Fatalf("reject status = %d, body %s", w.Code, w.Body)
	}
	if len(closer.calls) != 0 {
		t.Errorf("CloseIssue called for a chat task: issue number %d is synthetic",
			task.IssueNumber)
	}
}

// A forge that cannot close the issue must not fail the action: the operator's
// decision is already recorded in the store, and reporting failure would
// invite them to click again.
func TestRejectSurvivesAForgeFailure(t *testing.T) {
	srv, task, closer, _, _ := actionServer(t, store.StatusWaitingHuman, "review")
	closer.err = errors.New("forge unreachable")

	if w := postAction(t, srv, task.ID, "reject"); w.Code != http.StatusOK {
		t.Fatalf("reject status = %d, want 200: the store transition succeeded", w.Code)
	}
	got, err := srv.Store.TaskByID(t.Context(), task.ID)
	if err != nil || got == nil {
		t.Fatalf("TaskByID = (%+v, %v)", got, err)
	}
	if got.Status != store.StatusClosedWontDo {
		t.Errorf("status = %q, want %q", got.Status, store.StatusClosedWontDo)
	}
}

// Actions wrote store rows and nothing else, so the activity stream and the
// task timeline never showed that a human had intervened -- the one kind of
// event most worth seeing.
func TestActionsEmitEvents(t *testing.T) {
	tests := []struct {
		name    string
		action  string
		status  string
		reason  string
		wantMsg string
	}{
		{name: "approve", action: "approve", status: store.StatusWaitingHuman, reason: "review", wantMsg: "approve"},
		{name: "retry", action: "retry", status: store.StatusParked, reason: "it broke", wantMsg: "retry"},
		{name: "reject", action: "reject", status: store.StatusWaitingHuman, reason: "review", wantMsg: "reject"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv, task, _, _, sub := actionServer(t, tc.status, tc.reason)

			if w := postAction(t, srv, task.ID, tc.action); w.Code != http.StatusOK {
				t.Fatalf("status = %d, body %s", w.Code, w.Body)
			}

			select {
			case ev := <-sub.C:
				if ev.TaskID != task.ID {
					t.Errorf("event TaskID = %d, want %d", ev.TaskID, task.ID)
				}
				if ev.Kind == "" {
					t.Error("event Kind is empty")
				}
			default:
				t.Fatalf("%s emitted no event: the activity stream and timeline "+
					"never show that a human intervened", tc.action)
			}
		})
	}
}

// Error mapping. A store failure is not the client's fault, and returning 409
// tells the UI to say "conflict" for what is actually a broken database.
func TestTaskActionErrorMapping(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		setup      func(t *testing.T, srv *Server, id int64)
		id         func(id int64) string
		wantStatus int
	}{
		{
			name:       "unknown action",
			body:       `{"action":"detonate"}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "malformed body",
			body:       `not json`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "non-numeric id",
			body:       `{"action":"approve"}`,
			id:         func(int64) string { return "banana" },
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "unknown task",
			body:       `{"action":"approve"}`,
			id:         func(int64) string { return "424242" },
			wantStatus: http.StatusNotFound,
		},
		{
			name: "wrong state",
			body: `{"action":"retry"}`,
			// The task is waiting_human, not parked.
			wantStatus: http.StatusConflict,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv, task, _, _, _ := actionServer(t, store.StatusWaitingHuman, "review")
			if tc.setup != nil {
				tc.setup(t, srv, task.ID)
			}
			path := strconv.FormatInt(task.ID, 10)
			if tc.id != nil {
				path = tc.id(task.ID)
			}

			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
				"/api/tasks/"+path+"/action", bytes.NewBufferString(tc.body))
			w := httptest.NewRecorder()
			srv.Handler().ServeHTTP(w, req)

			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", w.Code, tc.wantStatus, w.Body)
			}
		})
	}
}

// A store write failure is a 500, not a 409.
func TestTaskActionStoreFailureIs500(t *testing.T) {
	base := newTestServer(t)
	ctx := t.Context()
	if _, err := base.Store.EnqueueIssue(ctx, "acme", "widget", 11, "a task", "", "", ""); err != nil {
		t.Fatal(err)
	}
	task, err := base.Store.ClaimNext(ctx)
	if err != nil || task == nil {
		t.Fatalf("ClaimNext = (%+v, %v)", task, err)
	}
	if err := base.Store.Transition(ctx, task.ID, store.StatusRunning, store.StatusWaitingHuman, "review"); err != nil {
		t.Fatal(err)
	}

	srv := &Server{
		Store: &stubStore{TaskStore: base.Store, requeueErr: errors.New("db locked")},
		Log:   base.Log,
		Cfg:   &config.Config{MaxRetries: 3},
	}
	w := postAction(t, srv, task.ID, "approve")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: a store failure is not a conflict", w.Code)
	}
}
