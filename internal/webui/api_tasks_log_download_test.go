package webui

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/logging"
)

// TestHandleTaskLogsDistinguishesNoLogForTheAttempt covers the dashboard's
// misleading report. A process that HAS a task-log reader and an attempt that
// genuinely has no file on disk is a different answer from a process that
// cannot read logs at all, and only the second one is "logging is not enabled
// here". The response has to carry that difference or the page cannot state it.
func TestHandleTaskLogsDistinguishesNoLogForTheAttempt(t *testing.T) {
	srv := newTestServer(t)
	ctx := t.Context()
	srv.TaskLogs = logging.NewTaskRegistry(t.TempDir(), logging.NewFeed(10), logging.TaskSinkOptions{})

	if _, err := srv.Store.EnqueueIssue(ctx, "acme", "widget", 1, "task", "", "", ""); err != nil {
		t.Fatal(err)
	}
	task, err := srv.Store.TaskByIssue(ctx, "acme", "widget", 1)
	if err != nil {
		t.Fatal(err)
	}
	// No log is ever opened for this task: the reader works, the attempt has
	// no file.

	w := getTaskLogs(t, srv, task.ID, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", w.Code, w.Body)
	}
	var body struct {
		Disabled bool `json:"disabled"`
		Found    bool `json:"found"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v (%s)", err, w.Body)
	}
	if body.Disabled {
		t.Error("disabled = true for a process that has a task-log reader; that is the report the page turns into \"logging was not enabled for this run\"")
	}
	if body.Found {
		t.Error("found = true for an attempt with no log file on disk")
	}
	if !strings.Contains(w.Body.String(), `"found"`) {
		t.Errorf("body = %s, want an explicit found marker so the page can say the attempt has no log rather than that logging is off", w.Body)
	}
}

// TestHandleTaskLogsDownloadServesTheAttemptLog pins the deliverable: the
// endpoint hands back the attempt's log as a file download. The route falling
// through to the SPA asset handler would answer 200 with an HTML page, so this
// asserts on the attachment headers and the body, not merely on the status.
func TestHandleTaskLogsDownloadServesTheAttemptLog(t *testing.T) {
	srv := newTestServer(t)
	ctx := t.Context()
	logs := logging.NewTaskRegistry(t.TempDir(), logging.NewFeed(10), logging.TaskSinkOptions{})
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
	logs.Write(ctx, task.ID, logging.Entry{Level: "ERROR", Message: "gate failed"})
	if err := logs.Close(task.ID); err != nil {
		t.Fatal(err)
	}

	url := "/api/tasks/" + strconv.FormatInt(task.ID, 10) + "/logs/download"
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, body %s", url, w.Code, w.Body)
	}
	wantName := "task-" + strconv.FormatInt(task.ID, 10) + "-attempt-" + strconv.Itoa(task.Attempt) + ".log"
	if got := w.Header().Get("Content-Disposition"); got != `attachment; filename="`+wantName+`"` {
		t.Errorf("Content-Disposition = %q, want an attachment named %q", got, wantName)
	}
	if !strings.Contains(w.Body.String(), "gate failed") {
		t.Errorf("download body = %s, want the attempt's own log content", w.Body)
	}
}

// TestHandleTaskLogsDownloadWithoutALogAnswersNotFound keeps the download
// honest: there is nothing to attach, so it says so rather than handing back a
// zero-byte file that looks like a successful download of nothing.
func TestHandleTaskLogsDownloadWithoutALogAnswersNotFound(t *testing.T) {
	srv := newTestServer(t)
	ctx := t.Context()
	srv.TaskLogs = logging.NewTaskRegistry(t.TempDir(), logging.NewFeed(10), logging.TaskSinkOptions{})

	if _, err := srv.Store.EnqueueIssue(ctx, "acme", "widget", 1, "task", "", "", ""); err != nil {
		t.Fatal(err)
	}
	task, err := srv.Store.TaskByIssue(ctx, "acme", "widget", 1)
	if err != nil {
		t.Fatal(err)
	}

	url := "/api/tasks/" + strconv.FormatInt(task.ID, 10) + "/logs/download"
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("GET %s with no log = %d, want 404", url, w.Code)
	}
	// The response is an error, not a file. Attachment headers are written
	// before the read (the body streams, so the size is unknown up front), and
	// leaving them on a 404 hands the caller a .log attachment containing only
	// the error text.
	if got := w.Header().Get("Content-Disposition"); got != "" {
		t.Errorf("Content-Disposition = %q on a 404, want it cleared so the error is not presented as a download", got)
	}
	if got := w.Header().Get("Content-Type"); strings.Contains(got, "ndjson") {
		t.Errorf("Content-Type = %q on a 404, want the error content type rather than the log's", got)
	}
}

// TestHandleTaskLogsDownloadWithoutAReaderAnswersServiceUnavailable is the
// degradation the boundary permits: a process with no task-log contract answers
// 503 rather than 404, because "I cannot read logs" and "there is no log" are
// not the same claim.
func TestHandleTaskLogsDownloadWithoutAReaderAnswersServiceUnavailable(t *testing.T) {
	srv := newTestServer(t)
	ctx := t.Context()
	if _, err := srv.Store.EnqueueIssue(ctx, "acme", "widget", 1, "task", "", "", ""); err != nil {
		t.Fatal(err)
	}
	task, err := srv.Store.TaskByIssue(ctx, "acme", "widget", 1)
	if err != nil {
		t.Fatal(err)
	}

	url := "/api/tasks/" + strconv.FormatInt(task.ID, 10) + "/logs/download"
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		body, _ := io.ReadAll(w.Result().Body)
		t.Fatalf("GET %s without a reader = %d (%s), want 503", url, w.Code, body)
	}
	// Same rule as the 404: an unavailable reader is an error, not a file.
	if got := w.Header().Get("Content-Disposition"); got != "" {
		t.Errorf("Content-Disposition = %q on a 503, want it cleared so the error is not presented as a download", got)
	}
}
