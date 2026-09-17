package webui

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/samcharles93/archie-core/internal/logging"
)

// handleTaskLogs serves one task attempt's persisted log history, the same
// way handleLogs serves the daemon-wide log -- transport only, parsing
// belongs to the logging package.
//
// A request with no "attempt" query param defaults to the task's current
// Attempt from the store, since that is what a human or Archie's own chat
// tool means by "why did task N park?" almost every time: the most recent
// run, not an arbitrary earlier retry.
//
// Two outcomes are kept apart on purpose, because only one of them is about
// the operator's configuration. A process with no task-log reader cannot say
// anything about an attempt, so it reports disabled and the page explains that
// this process cannot read logs. A process WITH a reader and an attempt with
// no log file reports found=false, which the page states as no log recorded
// for the attempt. Collapsing the second into the first is what made the
// dashboard claim "task logging is optional and was not enabled for this run"
// for every attempt of every task in a deployment where logging is
// unconditional.
func (s *Server) handleTaskLogs(w http.ResponseWriter, r *http.Request) {
	id, attempt, ok := s.taskLogTarget(w, r)
	if !ok {
		return
	}
	reader := s.taskLogReader()
	if reader == nil {
		writeDisabledTaskLogs(w, attempt)
		return
	}

	q := r.URL.Query()
	limit, ok := taskLogLimit(w, q.Get("limit"))
	if !ok {
		return
	}
	page, err := reader.TaskLog(r.Context(), id, attempt, logging.Query{
		Levels:    splitCSV(q.Get("level")),
		Component: strings.TrimSpace(q.Get("component")),
		Contains:  strings.TrimSpace(q.Get("q")),
		Limit:     limit,
	})
	if err != nil {
		// A reader that exists but cannot read here is still a statement about
		// this process rather than about the attempt, so it keeps the disabled
		// answer instead of claiming the attempt has no log.
		if errors.Is(err, logging.ErrTaskLogsUnavailable) {
			writeDisabledTaskLogs(w, attempt)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]any{
		"entries":    page.Entries,
		"truncated":  page.Truncated,
		"file":       page.File,
		"components": page.Components,
		"attempt":    attempt,
		// found is the distinction the page needs: a reader answering with no
		// entries because the file is absent is not a reader that is absent.
		"found": page.Found,
	})
}

// handleTaskLogDownload serves one task attempt's log verbatim as a file
// download -- the log itself rather than a decoded view of it, because an
// operator who asks for a download wants to read it in their own tooling or
// hand it to someone else.
//
// The conditions stay apart here too. A process with no reader answers 503,
// not 404: "there is no log for this attempt" is a claim only a process that
// can look is in a position to make.
func (s *Server) handleTaskLogDownload(w http.ResponseWriter, r *http.Request) {
	id, attempt, ok := s.taskLogTarget(w, r)
	if !ok {
		return
	}
	reader := s.taskLogReader()
	if reader == nil {
		http.Error(w, logging.ErrTaskLogsUnavailable.Error(), http.StatusServiceUnavailable)
		return
	}

	// Headers are set before the read because the body streams straight from
	// the reader into the response: a log file is unbounded input, so
	// buffering it to size the response would defeat the reason the reader
	// streams at all. The attempt's own name is known up front.
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Content-Disposition", `attachment; filename="`+taskLogFilename(id, attempt)+`"`)

	found, err := reader.TaskLogContent(r.Context(), id, attempt, w)
	if err != nil {
		if errors.Is(err, logging.ErrTaskLogsUnavailable) {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		// Once a 200 and a body are on the wire there is no status left to
		// send, so a failure partway through is logged where an operator can
		// find it rather than silently truncating the file.
		s.logf("task log download failed", "task", id, "attempt", attempt, "err", err)
		return
	}
	if !found {
		http.Error(w, "no log recorded for this attempt", http.StatusNotFound)
	}
}

// taskLogTarget resolves the {id} path value and the task it names, returning
// the attempt to read: the "attempt" query parameter when the request names
// one, otherwise the task's current Attempt. It answers the request itself and
// reports false when either step fails.
func (s *Server) taskLogTarget(w http.ResponseWriter, r *http.Request) (id int64, attempt int, ok bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return 0, 0, false
	}
	task, err := s.Store.TaskByID(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return 0, 0, false
	}
	if task == nil {
		http.Error(w, "task not found", http.StatusNotFound)
		return 0, 0, false
	}

	attempt = task.Attempt
	if raw := strings.TrimSpace(r.URL.Query().Get("attempt")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			http.Error(w, "bad attempt", http.StatusBadRequest)
			return 0, 0, false
		}
		attempt = parsed
	}
	return id, attempt, true
}

// taskLogLimit bounds a page the way handleLogs bounds the daemon-wide read:
// the limit is caller-supplied and a log file is unbounded, so a request must
// not be able to pull an arbitrary amount of it into memory.
func taskLogLimit(w http.ResponseWriter, raw string) (int, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return logging.DefaultTailLines, true
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil {
		http.Error(w, "bad limit", http.StatusBadRequest)
		return 0, false
	}
	switch {
	case parsed <= 0:
		return logging.DefaultTailLines, true
	case parsed > logging.MaxTailLines:
		return logging.MaxTailLines, true
	default:
		return parsed, true
	}
}

// writeDisabledTaskLogs is the "this process cannot read task logs" answer,
// the same convention handleLogs uses for the daemon-wide log: an optional
// capability that is absent here must not turn "why did this task park?" into
// a 500 for every task.
func writeDisabledTaskLogs(w http.ResponseWriter, attempt int) {
	writeJSON(w, map[string]any{
		"entries":  []logging.Entry{},
		"file":     "",
		"disabled": true,
		"attempt":  attempt,
	})
}

// taskLogFilename names a downloaded attempt's log. It carries the task and
// the attempt because an operator downloading two attempts of the same task
// must not end up with one file overwriting the other, and the attempt is the
// one identifier the log's own path would have given them.
func taskLogFilename(taskID int64, attempt int) string {
	return "task-" + strconv.FormatInt(taskID, 10) + "-attempt-" + strconv.Itoa(attempt) + ".log"
}

// taskLogReader is the task-log read capability this process has, or nil when
// it has none. The seam is a narrow interface rather than a concrete registry
// so the same handlers serve both processes: the daemon holds a registry over
// its own state directory, the dashboard holds the State Store client. A
// consumer-owned seam is the difference between the dashboard degrading
// honestly and the dashboard opening another process's files.
func (s *Server) taskLogReader() TaskLogSource {
	if s == nil {
		return nil
	}
	return s.TaskLogs
}

// TaskLogSource is the read seam the task-log handlers consume. It is the
// logging package's own reader contract, so a registry and a remote client
// satisfy it without either one restating the log format -- the package that
// owns the format also owns the shape of a read of it.
type TaskLogSource interface {
	// TaskLog returns a page of one attempt's decoded log.
	TaskLog(ctx context.Context, taskID int64, attempt int, q logging.Query) (logging.TaskLogPage, error)
	// TaskLogContent writes one attempt's log verbatim to w, for a download.
	TaskLogContent(ctx context.Context, taskID int64, attempt int, w io.Writer) (bool, error)
}
