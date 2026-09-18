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
	logs := logging.NewTaskRegistry(baseDir, logging.NewFeed(10), logging.TaskSinkOptions{})
	srv.TaskLogs = logs

	if _, err := srv.Store.EnqueueIssue(ctx, "acme", "widget", 1, "task", "", "", ""); err != nil {
		t.Fatal(err)
	}
	task, err := srv.Store.TaskByIssue(ctx, "acme", "widget", 1)
	if err != nil {
		t.Fatal(err)
	}

	if err := logs.Open(task.ID, task.Attempt); err != nil {
		t.Fatal(err)
	}
	logs.Write(ctx, task.ID, logging.Entry{
		Level: "ERROR", Message: "gate failed", Fields: map[string]any{"component": "gate"},
	})
	if err := logs.Close(task.ID); err != nil {
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
	logs := logging.NewTaskRegistry(baseDir, logging.NewFeed(10), logging.TaskSinkOptions{})
	srv.TaskLogs = logs

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

// TestHandleTaskLogsFiltersByStageAndKeepsTheAttempt pins R5's read half: the
// stage parameter reaches the reader's query, narrows the attempt's entries to
// the lines that record that stage, and leaves both the attempt it resolved and
// the file's existence (found) untouched -- a filter that matches nothing is
// "nothing matching this filter", never "this deployment cannot read logs".
func TestHandleTaskLogsFiltersByStageAndKeepsTheAttempt(t *testing.T) {
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

	// Written to attempt 1's file: an earlier attempt the task's current
	// Attempt field does not point at, so the attempt must be requested.
	sink, err := logging.NewTaskSink(baseDir, task.ID, 1, logging.TaskSinkOptions{})
	if err != nil {
		t.Fatal(err)
	}
	sink.Logger().Warn("committing the worktree", "stage", "commit")
	sink.Logger().Warn("agent produced a diff", "component", "agent")
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		query     map[string]string
		wantCount int
		wantFound bool
	}{
		{
			name:      "no stage filter",
			query:     map[string]string{"attempt": "1"},
			wantCount: 2,
			wantFound: true,
		},
		{
			name:      "the stage this attempt recorded",
			query:     map[string]string{"attempt": "1", "stage": "commit"},
			wantCount: 1,
			wantFound: true,
		},
		{
			name:      "a stage no entry records",
			query:     map[string]string{"attempt": "1", "stage": "implement"},
			wantCount: 0,
			wantFound: true,
		},
		{
			name:      "a stage filter on an attempt with no log at all",
			query:     map[string]string{"attempt": "2", "stage": "commit"},
			wantCount: 0,
			wantFound: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := getTaskLogs(t, srv, task.ID, tt.query)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, body %s", w.Code, w.Body)
			}
			var body struct {
				Entries  []logging.Entry `json:"entries"`
				Attempt  int             `json:"attempt"`
				Found    bool            `json:"found"`
				Disabled bool            `json:"disabled"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode response: %v (%s)", err, w.Body)
			}
			wantAttempt := 1
			if tt.query["attempt"] == "2" {
				wantAttempt = 2
			}
			if body.Attempt != wantAttempt {
				t.Errorf("attempt = %d, want the requested attempt %d", body.Attempt, wantAttempt)
			}
			if len(body.Entries) != tt.wantCount {
				t.Errorf("entries = %d, want %d (%s)", len(body.Entries), tt.wantCount, w.Body)
			}
			if body.Found != tt.wantFound {
				t.Errorf("found = %v, want %v: a filter that matches nothing is not a missing log", body.Found, tt.wantFound)
			}
			if body.Disabled {
				t.Error("disabled = true for a process that has a task-log reader")
			}
		})
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
