package webui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/samcharles93/archie-core/internal/logging"
)

func TestHandleTaskLogsReturnsEntriesForLatestAttemptByDefault(t *testing.T) {
	srv := newTestServer(t)
	ctx := t.Context()
	baseDir := t.TempDir()
	srv.TaskLogs = logging.NewTaskRegistry(baseDir, logging.NewFeed(10), logging.TaskSinkOptions{})

	if _, err := srv.Store.EnqueueIssue(ctx, "acme", "widget", 1, "task", "", "", ""); err != nil {
		t.Fatal(err)
	}
	task, err := srv.Store.TaskByIssue(ctx, "acme", "widget", 1)
	if err != nil {
		t.Fatal(err)
	}

	if err := srv.TaskLogs.Open(task.ID, task.Attempt); err != nil {
		t.Fatal(err)
	}
	srv.TaskLogs.Write(task.ID, logging.Entry{
		Level: "ERROR", Message: "gate failed", Fields: map[string]any{"component": "gate"},
	})
	if err := srv.TaskLogs.Close(task.ID); err != nil {
		t.Fatal(err)
	}

	w := getTaskLogs(t, srv, task.ID, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", w.Code, w.Body)
	}
	var body struct {
		Entries []logging.Entry `json:"entries"`
		Attempt int             `json:"attempt"`
		File    string          `json:"file"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Attempt != task.Attempt {
		t.Errorf("attempt = %d, want %d (task's current attempt, resolved without a query param)", body.Attempt, task.Attempt)
	}
	if len(body.Entries) != 1 || body.Entries[0].Message != "gate failed" {
		t.Fatalf("entries = %+v, want one entry with message %q", body.Entries, "gate failed")
	}
}

func TestHandleTaskLogsHonoursExplicitAttempt(t *testing.T) {
	srv := newTestServer(t)
	ctx := t.Context()
	baseDir := t.TempDir()
	srv.TaskLogs = logging.NewTaskRegistry(baseDir, logging.NewFeed(10), logging.TaskSinkOptions{})

	if _, err := srv.Store.EnqueueIssue(ctx, "acme", "widget", 1, "task", "", "", ""); err != nil {
		t.Fatal(err)
	}
	task, err := srv.Store.TaskByIssue(ctx, "acme", "widget", 1)
	if err != nil {
		t.Fatal(err)
	}

	// Write directly to attempt 1's file on disk -- a prior, already-closed
	// attempt that the task's current Attempt field no longer points at.
	sink, err := logging.NewTaskSink(baseDir, task.ID, 1, logging.TaskSinkOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sink.Logger().Warn("first attempt failed")
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}

	w := getTaskLogs(t, srv, task.ID, map[string]string{"attempt": "1"})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", w.Code, w.Body)
	}
	var body struct {
		Entries []logging.Entry `json:"entries"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Entries) != 1 || body.Entries[0].Message != "first attempt failed" {
		t.Fatalf("entries = %+v, want one entry with message %q", body.Entries, "first attempt failed")
	}
}

func TestHandleTaskLogsBadID(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/tasks/not-a-number/logs", nil)
	req.SetPathValue("id", "not-a-number")
	w := httptest.NewRecorder()
	srv.handleTaskLogs(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleTaskLogsUnknownTask(t *testing.T) {
	srv := newTestServer(t)
	srv.TaskLogs = logging.NewTaskRegistry(t.TempDir(), logging.NewFeed(10), logging.TaskSinkOptions{})
	w := getTaskLogs(t, srv, 999, nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

// TestHandleTaskLogsTaskLoggingDisabled guards the same "optional feature,
// not an error" behaviour handleLogs already has for the daemon-wide log:
// a deployment that never configured task logging must not turn "why did
// this task park?" into a 500 for every task.
func TestHandleTaskLogsTaskLoggingDisabled(t *testing.T) {
	srv := newTestServer(t)
	ctx := t.Context()
	if _, err := srv.Store.EnqueueIssue(ctx, "acme", "widget", 1, "task", "", "", ""); err != nil {
		t.Fatal(err)
	}
	task, err := srv.Store.TaskByIssue(ctx, "acme", "widget", 1)
	if err != nil {
		t.Fatal(err)
	}

	w := getTaskLogs(t, srv, task.ID, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", w.Code, w.Body)
	}
	var body struct {
		Disabled bool `json:"disabled"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !body.Disabled {
		t.Error("disabled = false, want true when TaskLogs is not configured")
	}
}

func getTaskLogs(t *testing.T, srv *Server, taskID int64, query map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	url := "/api/tasks/" + strconv.FormatInt(taskID, 10) + "/logs"
	if len(query) > 0 {
		q := make([]byte, 0)
		sep := "?"
		for k, v := range query {
			q = append(q, []byte(sep+k+"="+v)...)
			sep = "&"
		}
		url += string(q)
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	req.SetPathValue("id", strconv.FormatInt(taskID, 10))
	w := httptest.NewRecorder()
	srv.handleTaskLogs(w, req)
	return w
}
