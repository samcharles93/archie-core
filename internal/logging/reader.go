package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"
)

// Entry is one decoded log line.
type Entry struct {
	ID      int64     `json:"id,omitempty"`
	Time    time.Time `json:"time"`
	Level   string    `json:"level"`
	Message string    `json:"msg"`
	// Fields carries every remaining key so structured context survives the
	// round trip -- component, task id, error and anything else a call site
	// attached.
	Fields map[string]any `json:"fields,omitempty"`
}

// Query filters a read. The zero value returns the most recent DefaultTailLines
// entries unfiltered.
type Query struct {
	// Levels restricts results to these levels (case-insensitive). Empty
	// means all levels.
	Levels []string
	// Component matches the "component" field exactly. Empty means any.
	Component string
	// Stage matches the entry's "stage" field, case-insensitively. Empty
	// matches any. Entries without a stage never match.
	Stage string
	// Contains matches the message or any field value, case-insensitively.
	Contains string
	// Limit caps returned entries. Zero selects DefaultTailLines; values
	// above MaxTailLines are clamped.
	Limit int
	// Since, when set, excludes entries whose Time is strictly before it.
	// Until, when set, excludes entries whose Time is strictly after it.
	// Both bounds are inclusive. The zero time disables its bound.
	Since time.Time
	Until time.Time
}

// Read bounds. A log file is unbounded input, so a request must not be able to
// pull an arbitrary amount of it into memory.
const (
	DefaultTailLines = 200
	MaxTailLines     = 2000
	// maxScanBytes caps how much of the tail is examined. Beyond this the
	// result is reported as truncated rather than silently partial.
	maxScanBytes = 8 << 20
)

// Result is a page of log entries, newest last.
type Result struct {
	Entries []Entry `json:"entries"`
	// Truncated reports that the scan hit maxScanBytes before satisfying the
	// query, so older matching entries exist that were not examined.
	Truncated bool `json:"truncated"`
	// File is the path read, for the UI to show where this came from.
	File string `json:"file"`
	// Found reports whether the log file exists.
	Found bool `json:"found"`
}

// TaskLogPage is one task attempt's log as a task-log reader returns it: the
// page Tail selected, whether the attempt has a file at all, and the distinct
// components present in the file's tail so a filter is built from what is
// actually there rather than a hardcoded list that drifts.
type TaskLogPage struct {
	Result
	Components []string `json:"components"`
}

// ErrTaskLogsUnavailable reports that this process cannot read task logs.
var ErrTaskLogsUnavailable = errors.New("logging: task log reader unavailable")

// taskLogChunkBytes bounds one write a TaskLogContent makes, so a reader that
// pushes into a bounded transport emits chunks that transport can carry.
// io.Copy would otherwise hand it reads of its own choosing.
const taskLogChunkBytes = 256 << 10

// TaskLog reads one task attempt's log. A nil registry returns
// ErrTaskLogsUnavailable.
func (r *TaskRegistry) TaskLog(_ context.Context, taskID int64, attempt int, q Query) (TaskLogPage, error) {
	if r == nil {
		return TaskLogPage{}, ErrTaskLogsUnavailable
	}
	res, err := Tail(TaskLogPath(r.baseDir, taskID, attempt), q)
	if err != nil {
		return TaskLogPage{}, err
	}
	page := TaskLogPage{Result: res, Components: []string{}}
	// The filter list is a convenience: failing to build it must not cost the
	// caller their entries. Components only reads the tail this page already
	// read, so its error is the same "treat as empty" case the daemon-wide
	// handler has always applied -- it is dropped deliberately, not missed.
	if res.Found {
		if components, err := Components(res.File); err == nil {
			page.Components = components
		}
	}
	return page, nil
}

// TaskLogContent streams one attempt's raw log to w. found is false when
// there is no file. A nil registry returns ErrTaskLogsUnavailable.
func (r *TaskRegistry) TaskLogContent(_ context.Context, taskID int64, attempt int, w io.Writer) (bool, error) {
	if r == nil {
		return false, ErrTaskLogsUnavailable
	}
	f, err := os.Open(TaskLogPath(r.baseDir, taskID, attempt))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("logging: open task log: %w", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := io.CopyBuffer(w, f, make([]byte, taskLogChunkBytes)); err != nil {
		return false, fmt.Errorf("logging: read task log: %w", err)
	}
	return true, nil
}

// PageResult is one forward page. Pass Cursor back to continue.
// MoreAvailable reports matches beyond Limit.
type PageResult struct {
	Entries       []Entry `json:"entries"`
	Truncated     bool    `json:"truncated"`
	MoreAvailable bool    `json:"more_available"`
	Cursor        int64   `json:"cursor"`
	File          string  `json:"file"`
}

// logWindow is the region of a log file one read examines: the
// tail-most maxScanBytes, held in memory so offset arithmetic is
// against a contiguous buffer rather than a live file position.
type logWindow struct {
	// data is the window's bytes; data[0] is at file offset start.
	data []byte
	// start is the file offset data begins at (0 when the whole file fits).
	start int64
	// fileSize is the file's size when the window was taken.
	fileSize int64
	// truncated reports that data is a strict suffix of the file, so
	// older bytes exist that this window does not contain.
	truncated bool
}

// readWindow opens path and reads its scan window into memory.
// found is false (with a nil error) for an absent file or empty path:
// file logging is optional, so neither is an error condition.
func readWindow(path string) (w logWindow, found bool, err error) {
	if strings.TrimSpace(path) == "" {
		return logWindow{}, false, nil
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return logWindow{}, false, nil
		}
		return logWindow{}, false, fmt.Errorf("logging: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return logWindow{}, false, fmt.Errorf("logging: stat %s: %w", path, err)
	}

	w = logWindow{fileSize: info.Size()}
	size := info.Size()
	if size > maxScanBytes {
		w.start = size - maxScanBytes
		size = maxScanBytes
		w.truncated = true
	}
	w.data = make([]byte, size)

	// ReadAt fills the buffer or errors; skip it for an empty window.
	if len(w.data) > 0 {
		if _, err := f.ReadAt(w.data, w.start); err != nil {
			return logWindow{}, false, fmt.Errorf("logging: read %s: %w", path, err)
		}
	}
	return w, true, nil
}

// readLines calls step for each matching entry in the last maxScanBytes of
// path, with the offset after its line. It returns whether the window
// excluded older data and the next cursor. Older data is never read.
func readLines(path string, q Query, cursor int64, step func(e Entry, endOff int64) bool) (truncated bool, cursorOut int64, ok bool, err error) {
	w, found, err := readWindow(path)
	if err != nil || !found {
		return false, 0, false, err
	}
	truncated = w.truncated
	windowStart, data := w.start, w.data

	// Clamp the caller's cursor to the scan window. A cursor below
	// windowStart cannot read older history than Tail would have shown;
	// we clamp upward so the returned cursor stays inside the window.
	if cursor < windowStart {
		cursor = windowStart
	}
	// A cursor at or past EOF means the caller has read everything we
	// can serve. Return ok=false so the caller stops looping; the
	// preserved cursor lets it detect overshoot (file shrank under
	// rotation) versus exact-fit.
	if cursor >= w.fileSize {
		return truncated, cursor, false, nil
	}

	// Convert the file cursor to an offset in data.
	cursorInWindow := min(cursor-windowStart, int64(len(data)))

	// Skip a partial first line at the window start.
	if cursor == windowStart && truncated {
		idx := bytes.IndexByte(data, '\n')
		if idx < 0 {
			return truncated, w.fileSize, false, nil
		}
		cursorInWindow = int64(idx + 1)
	}

	return truncated, walkWindow(data, windowStart, cursorInWindow, q, step), true, nil
}

// walkWindow calls step for each matching entry from offset from, skipping
// undecodable lines, and returns the file offset to resume at. step returns
// false to stop.
func walkWindow(data []byte, windowStart, from int64, q Query, step func(e Entry, endOff int64) bool) int64 {
	pos := from
	for pos < int64(len(data)) {
		// lineEnd indexes the line's terminating '\n'; when the final
		// line is unterminated (daemon killed mid-write) it indexes the
		// end of the buffer and endOff stops there.
		nl := bytes.IndexByte(data[pos:], '\n')
		lineEnd := int64(len(data))
		endOff := windowStart + int64(len(data))
		if nl >= 0 {
			lineEnd = pos + int64(nl)
			endOff = windowStart + lineEnd + 1 // past the '\n'
		}
		raw := data[pos:lineEnd] // excludes the '\n'
		if len(raw) > 0 && raw[len(raw)-1] == '\r' {
			raw = raw[:len(raw)-1] // CRLF
		}
		if entry, decoded := decode(raw); decoded && q.matches(entry) && !step(entry, endOff) {
			return endOff
		}
		if nl < 0 {
			return windowStart + int64(len(data))
		}
		pos = lineEnd + 1
	}
	return windowStart + pos
}

// Tail returns the most recent matching entries in path, oldest first. A
// missing file is not an error. Past Limit, older matches are dropped and
// Truncated is set.
func Tail(path string, q Query) (Result, error) {
	res := Result{Entries: []Entry{}, File: path}
	limit := q.Limit
	if limit <= 0 {
		limit = DefaultTailLines
	}
	if limit > MaxTailLines {
		limit = MaxTailLines
	}

	// Stat for Found without reading the window twice.
	if strings.TrimSpace(path) != "" {
		if _, err := os.Stat(path); err == nil {
			res.Found = true
		} else if !os.IsNotExist(err) {
			return Result{}, fmt.Errorf("logging: stat %s: %w", path, err)
		}
	}

	matches := make([]Entry, 0, limit)
	truncated, _, ok, err := readLines(path, q, 0, func(e Entry, _ int64) bool {
		if len(matches) == limit {
			matches = append(matches[:0], matches[1:]...)
			res.Truncated = true
		}
		matches = append(matches, e)
		return true
	})
	if err != nil {
		return Result{}, err
	}
	if !ok {
		// Truncated stays set even with no entries.
		res.Truncated = truncated
		return res, nil
	}
	res.Truncated = res.Truncated || truncated
	res.Entries = matches
	return res, nil
}

// Page returns up to Limit matching entries from cursor, and the cursor to
// resume at. Truncated means older entries were not examined. A cursor past
// the end returns an empty page.
func Page(path string, q Query, cursor int64) (PageResult, error) {
	res := PageResult{Entries: []Entry{}, File: path}
	limit := q.Limit
	if limit <= 0 {
		limit = DefaultTailLines
	}
	if limit > MaxTailLines {
		limit = MaxTailLines
	}

	type lineAt struct {
		entry  Entry
		endOff int64
	}
	matches := make([]lineAt, 0, limit)
	truncated, endOff, ok, err := readLines(path, q, cursor, func(e Entry, end int64) bool {
		if len(matches) < limit {
			matches = append(matches, lineAt{entry: e, endOff: end})
			return true
		}
		// Once we have limit matches, any further match means more is
		// available; we keep walking only to maintain endOff accuracy.
		res.MoreAvailable = true
		return true
	})
	if err != nil {
		return PageResult{}, err
	}
	if !ok {
		// Preserved endOff carries the cursor the caller asked for
		// (when ok=false because cursor > EOF, so the next call can
		// detect overshoot). The scan-cap signal is also preserved.
		res.Truncated = truncated
		res.Cursor = endOff
		return res, nil
	}
	res.Truncated = truncated
	res.Entries = make([]Entry, len(matches))
	for i, m := range matches {
		res.Entries[i] = m.entry
	}
	// Resume after the last returned entry when the page filled, else at the end
	// of the walk.
	if res.MoreAvailable {
		res.Cursor = matches[len(matches)-1].endOff
	} else {
		res.Cursor = endOff
	}
	return res, nil
}

// decode parses one slog JSON line, lifting the known keys out and keeping the
// rest as structured fields. A line that is not JSON is skipped: a log file may
// contain output from a subprocess that does not share our format.
func decode(line []byte) (Entry, bool) {
	var raw map[string]any
	if err := json.Unmarshal(line, &raw); err != nil {
		return Entry{}, false
	}

	entry := Entry{Fields: map[string]any{}}
	for k, v := range raw {
		switch k {
		case "time":
			if s, ok := v.(string); ok {
				entry.Time, _ = time.Parse(time.RFC3339Nano, s)
			}
		case "level":
			entry.Level, _ = v.(string)
		case "msg":
			entry.Message, _ = v.(string)
		default:
			entry.Fields[k] = v
		}
	}
	if len(entry.Fields) == 0 {
		entry.Fields = nil
	}
	return entry, true
}

// withinTimeBounds reports whether t is within q's inclusive Since/Until
// bounds. An entry with no parsed time fails any bound.
func (q Query) withinTimeBounds(t time.Time) bool {
	if q.Since.IsZero() && q.Until.IsZero() {
		return true
	}
	if t.IsZero() {
		return false
	}
	if !q.Since.IsZero() && t.Before(q.Since) {
		return false
	}
	return q.Until.IsZero() || !t.After(q.Until)
}

func (q Query) matches(e Entry) bool {
	if len(q.Levels) > 0 && !slices.ContainsFunc(q.Levels, func(l string) bool {
		return strings.EqualFold(l, e.Level)
	}) {
		return false
	}
	if q.Component != "" {
		got, _ := e.Fields["component"].(string)
		if !strings.EqualFold(got, q.Component) {
			return false
		}
	}
	if q.Stage != "" {
		got, _ := e.Fields["stage"].(string)
		if !strings.EqualFold(got, q.Stage) {
			return false
		}
	}
	if !q.withinTimeBounds(e.Time) {
		return false
	}
	if q.Contains != "" {
		needle := strings.ToLower(q.Contains)
		if strings.Contains(strings.ToLower(e.Message), needle) {
			return true
		}
		for _, v := range e.Fields {
			if strings.Contains(strings.ToLower(fmt.Sprint(v)), needle) {
				return true
			}
		}
		return false
	}
	return true
}

// Components returns the distinct "component" values present in the tail, so
// the UI can offer a filter built from what is actually there rather than a
// hardcoded list that drifts.
func Components(path string) ([]string, error) {
	res, err := Tail(path, Query{Limit: MaxTailLines})
	if err != nil {
		return nil, err
	}
	seen := map[string]struct{}{}
	out := []string{}
	for _, e := range res.Entries {
		if c, ok := e.Fields["component"].(string); ok && c != "" {
			if _, dup := seen[c]; !dup {
				seen[c] = struct{}{}
				out = append(out, c)
			}
		}
	}
	slices.Sort(out)
	return out, nil
}
