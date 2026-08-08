package webui

// Tests for the task-timeline route. The dashboard's "Click a row for its
// timeline" interaction depends on GET /api/tasks/{id} returning exactly that
// task's events, ordered, and on the literal /api/tasks/clear route beating
// the {id} wildcard.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/store"
)

// Approve requeues from the task's own recorded state rather than anything
// the caller supplies, so a stale dashboard cannot approve a task twice.
func TestHandleTaskActionApproveUsesRecordedState(t *testing.T) {
	srv := newTestServer(t)
	ctx := t.Context()
	if _, err := srv.Store.EnqueueIssue(ctx, "acme", "widget", 1, "approve me", "", "", ""); err != nil {
		t.Fatal(err)
	}
	task, err := srv.Store.ClaimNext(ctx)
	if err != nil || task == nil {
		t.Fatalf("claim = (%+v, %v)", task, err)
	}
	if err := srv.Store.Transition(ctx, task.ID, store.StatusRunning, store.StatusWaitingHuman, "review"); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/tasks/"+strconv.FormatInt(task.ID, 10)+"/action", bytes.NewBufferString(`{"action":"approve"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Archie-CSRF", "1")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("approve status = %d, body = %s", w.Code, w.Body)
	}
	got, err := srv.Store.TaskByID(ctx, task.ID)
	if err != nil || got == nil {
		t.Fatalf("TaskByID = (%+v, %v)", got, err)
	}
	if got.Status != store.StatusQueued || got.Workflow != "implement" {
		t.Fatalf("approved task = %+v, want queued/implement", got)
	}
}

func TestHandleTaskActionRetriesParkedTask(t *testing.T) {
	srv := newTestServer(t)
	ctx := t.Context()
	if _, err := srv.Store.EnqueueIssue(ctx, "acme", "widget", 1, "retry me", "", "", ""); err != nil {
		t.Fatal(err)
	}
	task, err := srv.Store.ClaimNext(ctx)
	if err != nil || task == nil {
		t.Fatalf("claim = (%+v, %v)", task, err)
	}
	if err := srv.Store.Transition(ctx, task.ID, store.StatusRunning, store.StatusParked, "failed"); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/tasks/"+strconv.FormatInt(task.ID, 10)+"/action", bytes.NewBufferString(`{"action":"retry"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Archie-CSRF", "1")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("retry status = %d, body = %s", w.Code, w.Body)
	}
	got, err := srv.Store.TaskByID(ctx, task.ID)
	if err != nil || got == nil {
		t.Fatalf("TaskByID = (%+v, %v)", got, err)
	}
	if got.Status != store.StatusQueued || got.RetryCount != 1 {
		t.Fatalf("retried task = %+v, want queued/retry_count=1", got)
	}
}

// TestHandleTaskIsolatesTasks returns the timeline for one task, and only
// that task: another task's events must not leak into the response.
func TestHandleTaskIsolatesTasks(t *testing.T) {
	srv := newTestServer(t)
	ctx := t.Context()
	for _, ev := range []events.Event{
		{TaskID: 7, Kind: "stage_start", Stage: "plan", Detail: "bootstrap"},
		{TaskID: 7, Kind: "stage_end", Stage: "plan"},
		{TaskID: 8, Kind: "stage_start", Stage: "implement"},
	} {
		if _, err := srv.Store.InsertEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}

	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/tasks/7", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body)
	}
	var got []events.Event
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("timeline has %d events, want 2 (task 8 must not leak in)", len(got))
	}
	if got[0].Kind != "stage_start" || got[0].Stage != "plan" {
		t.Errorf("first event = %+v, want stage_start/plan", got[0])
	}
}

// TestHandleTaskUnknownID returns an empty list, not an error, for a task id
// that has no events yet -- the UI renders "No events yet" from that.
func TestHandleTaskUnknownID(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/tasks/999", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if body := strings.TrimSpace(w.Body.String()); body != "null" && body != "[]" {
		t.Fatalf("body = %s, want an empty timeline", body)
	}
}

// TestHandleTaskBadID is covered in webui_test.go (400 on a non-numeric id).

// TestHandleTaskStoreError proves a store failure surfaces as 500 -- the UI
// shows its "Could not load timeline" state and offers a retry.
func TestHandleTaskStoreError(t *testing.T) {
	base := newTestServer(t)
	srv := &Server{
		Store: &stubStore{TaskStore: base.Store, taskEventsErr: fmt.Errorf("db locked")},
		Log:   base.Log,
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/tasks/7", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
}

func TestBulkClearRouteCannotMutateTasks(t *testing.T) {
	srv := newTestServer(t)
	if _, err := srv.Store.EnqueueIssue(t.Context(), "acme", "widget", 1, "task", "", "", ""); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/tasks/clear", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code == http.StatusOK {
		t.Fatal("GET /api/tasks/clear still exposes a destructive mutation")
	}
	tasks, err := srv.Store.Tasks(t.Context(), 10)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("tasks after legacy clear request = (%d, %v), want one preserved", len(tasks), err)
	}
}
