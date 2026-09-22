package archieui

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"google.golang.org/grpc"

	"github.com/samcharles93/archie-core/internal/infrastructure/edastore"
	"github.com/samcharles93/archie-core/internal/infrastructure/staterpc"
	"github.com/samcharles93/archie-core/internal/logging"
	"github.com/samcharles93/archie-core/internal/store"
	"github.com/samcharles93/archie-core/internal/webui"
)

// TestUIProcessReadsTaskLogsOverTheStateStoreContract is the dashboard bug,
// pinned end to end: the extracted UI process serves GET /api/tasks/{id}/logs
// and has no way to read a task's persisted log at all, because the only reader
// it ever had was a daemon-local file handle. The page therefore answered "Task
// logging is optional and was not enabled for this run" for every attempt of
// every task, on a deployment where task logging is unconditional and the files
// exist.
//
// The fix is a State Store read contract, so this drives the real thing: a log
// file written on the state-directory side, a gRPC server over it, a dashboard
// composed exactly as the UI process composes one, and an HTTP request through
// the composed handler.
func TestUIProcessReadsTaskLogsOverTheStateStoreContract(t *testing.T) {
	srv, taskID, wantAttempt := composeUIProcessWithLog(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	path := "/api/tasks/" + strconv.FormatInt(taskID, 10) + "/logs"
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.URL+path, nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var body struct {
		Entries []logging.Entry `json:"entries"`
		Attempt int             `json:"attempt"`
		Found   bool            `json:"found"`
		File    string          `json:"file"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode response: %v (%s)", err, raw)
	}
	if !body.Found {
		t.Errorf("found = false for an attempt whose log exists on the state-directory side (%s)", raw)
	}
	if body.Attempt != wantAttempt {
		t.Errorf("attempt = %d, want %d (the task's current attempt, resolved without a query param)", body.Attempt, wantAttempt)
	}
	if len(body.Entries) != 1 || body.Entries[0].Message != "gate failed" {
		t.Fatalf("entries = %+v, want the one entry the log holds", body.Entries)
	}
	if got, ok := body.Entries[0].Fields["component"]; !ok || got != "gate" {
		t.Errorf("entry fields = %+v, want the structured component field to survive the hop", body.Entries[0].Fields)
	}
	// The path is reported, not used: the UI process must not be able to open
	// it, and the page only names where the log came from.
	if !strings.HasSuffix(body.File, "attempt-"+strconv.Itoa(wantAttempt)+".jsonl") {
		t.Errorf("file = %q, want the attempt's own log path", body.File)
	}
}

// TestUIProcessServesTaskLogDownload drives the download endpoint through the
// real composition: the bytes arrive over the contract, and the response is an
// attachment named for the attempt rather than an SPA page.
//
// A GET that falls through to the asset handler answers 200 with HTML, which
// is why this asserts the attachment headers and the content, not the status.
func TestUIProcessServesTaskLogDownload(t *testing.T) {
	srv, taskID, wantAttempt := composeUIProcessWithLog(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	path := "/api/tasks/" + strconv.FormatInt(taskID, 10) + "/logs/download"
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.URL+path, nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	wantName := "task-" + strconv.FormatInt(taskID, 10) + "-attempt-" + strconv.Itoa(wantAttempt) + ".log"
	if got := resp.Header.Get("Content-Disposition"); got != `attachment; filename="`+wantName+`"` {
		t.Errorf("Content-Disposition = %q, want the attempt's own filename %q (status %d, body %s)", got, wantName, resp.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "gate failed") {
		t.Errorf("download body = %s, want the attempt's own log content", raw)
	}
}

// TestUIProcessReportsMissingTaskLogsHonestly pins the second half of the fix:
// with the reader wired and no log on disk, the page must be able to say that
// the ATTEMPT has no log. It is a different claim from "this process cannot
// read logs", and only the second one is about the operator's configuration.
func TestUIProcessReportsMissingTaskLogsHonestly(t *testing.T) {
	srv, taskID := composeUIProcess(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	path := "/api/tasks/" + strconv.FormatInt(taskID, 10) + "/logs"
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.URL+path, nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	var body struct {
		Disabled bool `json:"disabled"`
		Found    bool `json:"found"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode response: %v (%s)", err, raw)
	}
	if body.Disabled {
		t.Fatalf("disabled = true for a process that can read task logs (%s); the page turns that into \"logging was not enabled for this run\"", raw)
	}
	if body.Found {
		t.Errorf("found = true for an attempt with no log file (%s)", raw)
	}
}

// composeUIProcessWithLog is composeUIProcess plus one real log on the
// state-directory side: a gRPC server whose task-log reader holds an attempt's
// file, so the dashboard's read has to cross the wire to see anything.
func composeUIProcessWithLog(t *testing.T) (*webui.Server, int64, int) {
	t.Helper()
	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "tasks.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	task, err := st.EnqueueChatTask(t.Context(), "acme", "widget", "a task", "body", "implement", "")
	if err != nil {
		t.Fatalf("seed task: %v", err)
	}

	// The state directory the State Store process owns. The dashboard never
	// learns this path: it asks the contract for the log.
	logs := logging.NewTaskRegistry(filepath.Join(t.TempDir(), "logs", "tasks"), logging.NewFeed(10), logging.TaskSinkOptions{})
	if err := logs.Open(task.ID, task.Attempt); err != nil {
		t.Fatalf("open task log: %v", err)
	}
	logs.Write(t.Context(), task.ID, logging.Entry{
		Level: "ERROR", Message: "gate failed", Fields: map[string]any{"component": "gate"},
	})
	if err := logs.Close(task.ID); err != nil {
		t.Fatalf("close task log: %v", err)
	}

	target, stop := serveGRPC(t, func(r grpc.ServiceRegistrar) {
		eda := edastore.OpenTest(t)
		staterpc.RegisterServer(r, staterpc.Deps{
			Tasks: st, Captures: eda, BindingDispatcher: eda, ConfigSnapshots: st,
			TaskLogs: logs, Log: slog.New(slog.DiscardHandler),
		})
	})
	t.Cleanup(stop)
	client, closeClient, err := staterpc.Dial(target, "")
	if err != nil {
		t.Fatalf("dial state store: %v", err)
	}
	t.Cleanup(closeClient)

	options := Options{Listen: "127.0.0.1:0", DependencyTimeout: defaultDependencyTimeout}
	srv := compose(deps{
		Options: options,
		Log:     slog.New(slog.DiscardHandler),
		Store:   client,
		Health:  newReadinessRegistry(options, client, nil),
	})
	return srv, task.ID, task.Attempt
}
