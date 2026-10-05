package webui

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/samcharles93/archie-core/internal/logging"
)

// handleTaskLogs serves one attempt's log history, defaulting to the current
// attempt. No reader reports disabled; no log file reports found=false.
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
	beforeID, ok := taskLogBeforeID(w, q.Get("before"))
	if !ok {
		return
	}
	since, ok := taskLogTime(w, "since", q.Get("since"))
	if !ok {
		return
	}
	until, ok := taskLogTime(w, "until", q.Get("until"))
	if !ok {
		return
	}
	page, err := reader.TaskLog(r.Context(), id, attempt, logging.Query{
		Levels:    splitCSV(q.Get("level")),
		Component: strings.TrimSpace(q.Get("component")),
		// Stage is a narrowing filter, not a scoping one: only entries a stage
		// tagged carry the field, so the UI states that a stage filter matches
		// the lines that record a stage and nothing else.
		Stage:    strings.TrimSpace(q.Get("stage")),
		Contains: strings.TrimSpace(q.Get("q")),
		Limit:    limit,
		BeforeID: beforeID,
		Since:    since,
		Until:    until,
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
		"entries":        page.Entries,
		"truncated":      page.Truncated,
		"more_available": page.MoreAvailable,
		"cursor":         page.Cursor,
		"file":           page.File,
		"components":     page.Components,
		"attempt":        attempt,
		// found is the distinction the page needs: a reader answering with no
		// entries because the file is absent is not a reader that is absent.
		"found": page.Found,
	})
}

// handleTaskLogDownload serves one attempt's raw log as a download. No reader
// answers 503.
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

	// The body streams, so headers are set first and must be cleared before
	// writing an error.
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Content-Disposition", `attachment; filename="`+taskLogFilename(id, attempt)+`"`)

	clearAttachment := func() {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Del("Content-Disposition")
	}

	found, err := reader.TaskLogContent(r.Context(), id, attempt, w)
	if err != nil {
		if errors.Is(err, logging.ErrTaskLogsUnavailable) {
			clearAttachment()
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
		clearAttachment()
		http.Error(w, "no log recorded for this attempt", http.StatusNotFound)
	}
}

// taskLogTarget resolves the task and attempt for a log read, answering the
// request itself on failure.
func (s *Server) taskLogTarget(w http.ResponseWriter, r *http.Request) (id int64, attempt int, ok bool) {
	t, attempt, ok := s.taskAttemptTarget(w, r)
	if !ok {
		return 0, 0, false
	}
	return t.ID, attempt, true
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

// taskLogBeforeID parses the byte-offset cursor a previous page returned: the
// read returns the entries older than it.
func taskLogBeforeID(w http.ResponseWriter, raw string) (int64, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, true
	}
	parsed, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || parsed < 0 {
		http.Error(w, "bad before", http.StatusBadRequest)
		return 0, false
	}
	return parsed, true
}

// taskLogTime parses an RFC 3339 time bound; empty disables it.
func taskLogTime(w http.ResponseWriter, name, raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, true
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		http.Error(w, "bad "+name, http.StatusBadRequest)
		return time.Time{}, false
	}
	return parsed, true
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

// taskLogReader returns this process's task-log reader, or nil.
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
