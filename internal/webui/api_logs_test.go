package webui

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/logging"
)

func TestLogEndpointsValidateAndBoundLimit(t *testing.T) {
	srv := newTestServer(t)
	const entryCount = logging.MaxTailLines + 1

	daemonLogPath := filepath.Join(t.TempDir(), "archied.log")
	daemonLog, closer, err := logging.New(logging.Options{File: daemonLogPath})
	if err != nil {
		t.Fatal(err)
	}
	writeLogEntries(daemonLog, entryCount)
	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}
	srv.Cfg = config.NewHolder(config.Config{Log: config.Log{File: daemonLogPath}})

	taskLogDir := t.TempDir()
	srv.TaskLogs = logging.NewTaskRegistry(taskLogDir, nil, logging.TaskSinkOptions{})
	if _, err := srv.Store.EnqueueIssue(t.Context(), "acme", "widget", 1, "task", "", "", ""); err != nil {
		t.Fatal(err)
	}
	task, err := srv.Store.TaskByIssue(t.Context(), "acme", "widget", 1)
	if err != nil {
		t.Fatal(err)
	}
	taskSink, err := logging.NewTaskSink(taskLogDir, task.ID, task.Attempt, logging.TaskSinkOptions{})
	if err != nil {
		t.Fatal(err)
	}
	writeLogEntries(taskSink.Logger(), entryCount)
	if err := taskSink.Close(); err != nil {
		t.Fatal(err)
	}

	endpoints := []struct {
		name string
		path string
	}{
		{name: "daemon logs", path: "/api/logs"},
		{name: "task logs", path: "/api/tasks/" + strconv.FormatInt(task.ID, 10) + "/logs"},
	}
	tests := []struct {
		name        string
		query       string
		wantStatus  int
		wantEntries int
	}{
		{name: "missing uses default", wantStatus: http.StatusOK, wantEntries: logging.DefaultTailLines},
		{name: "zero uses default", query: "?limit=0", wantStatus: http.StatusOK, wantEntries: logging.DefaultTailLines},
		{name: "negative uses default", query: "?limit=-1", wantStatus: http.StatusOK, wantEntries: logging.DefaultTailLines},
		{name: "valid is honored", query: "?limit=3", wantStatus: http.StatusOK, wantEntries: 3},
		{name: "whitespace is ignored", query: "?limit=%203%20", wantStatus: http.StatusOK, wantEntries: 3},
		{
			name:        "oversized is clamped",
			query:       "?limit=" + strconv.Itoa(logging.MaxTailLines+1),
			wantStatus:  http.StatusOK,
			wantEntries: logging.MaxTailLines,
		},
		{name: "malformed is rejected", query: "?limit=not-a-number", wantStatus: http.StatusBadRequest},
		{name: "integer overflow is rejected", query: "?limit=999999999999999999999999", wantStatus: http.StatusBadRequest},
	}

	for _, endpoint := range endpoints {
		t.Run(endpoint.name, func(t *testing.T) {
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, endpoint.path+tt.query, nil)
					w := httptest.NewRecorder()

					srv.Handler().ServeHTTP(w, req)

					if w.Code != tt.wantStatus {
						t.Fatalf("status = %d, want %d", w.Code, tt.wantStatus)
					}
					if tt.wantStatus != http.StatusOK {
						return
					}
					var body struct {
						Entries []logging.Entry `json:"entries"`
					}
					if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
						t.Fatalf("decode response: %v", err)
					}
					if len(body.Entries) != tt.wantEntries {
						t.Errorf("entries = %d, want %d", len(body.Entries), tt.wantEntries)
					}
				})
			}
		})
	}
}

func writeLogEntries(log *slog.Logger, count int) {
	for i := range count {
		log.Info("entry", "index", i)
	}
}
