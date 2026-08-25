package logging

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"
)

// Entry is one decoded log line.
//
// Reading lives here rather than in the dashboard because this package owns
// the on-disk format. If the handler shape or field names change, the reader
// changes with them in the same file, and no consumer has to know slog wrote
// JSON with a "msg" key.
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
}

// PageResult is what a single forward-paged call returns.
//
// Cursor is the byte offset within path at which the next page should
// resume -- pass it back as Page's cursor argument. A Cursor equal to
// or past the file's current size means there is nothing more to read.
// MoreAvailable is true iff the scan saw at least one matching entry it
// did not return because Limit was already satisfied; it does NOT imply
// the file has been read to the end (combine with Truncated).
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

	// ReadAt, never Read: io.ReaderAt guarantees it either fills the
	// buffer or returns a non-nil error, whereas Read may legally return
	// fewer bytes with a nil error. A silent short read here would leave
	// the tail of the buffer as zero bytes, which decode rejects -- so
	// the NEWEST entries would vanish from every result while the call
	// still reported success. That is the worst possible failure for a
	// log reader, and it is the one Read invites.
	//
	// Guarded on len>0 because a zero-length ReadAt at end-of-file is
	// permitted to return io.EOF, and an empty log is a valid empty
	// result rather than an error.
	if len(w.data) > 0 {
		if _, err := f.ReadAt(w.data, w.start); err != nil {
			return logWindow{}, false, fmt.Errorf("logging: read %s: %w", path, err)
		}
	}
	return w, true, nil
}

// readLines walks every JSONL line in path within the scan window
// (tail-most maxScanBytes), invoking step with each decoded entry that
// matches q and the file offset just past the line's terminating '\n'.
// It returns whether the scan hit maxScanBytes before EOF (so older
// matching entries exist that were not examined) and the file offset
// the caller should use as its next-page Cursor.
//
// cursor=0 begins at the scan window's start (same as Tail); a cursor
// past the scan window's start resumes partway through. A cursor at or
// past EOF returns ok=false without invoking step -- either the caller
// has read everything, or the file shrank under rotation.
//
// SCAN WINDOW IS A HARD CEILING, and it bounds pagination too. For a
// file larger than maxScanBytes the oldest bytes are never examined by
// ANY call, no matter how the caller pages: every call re-derives the
// window from the CURRENT file size, and the cursor is clamped up into
// it. Paging therefore walks the window exhaustively and stops; it does
// not walk backwards into history. truncated=true is the signal that
// this happened, and it is the caller's job to surface that rather than
// implying the log was read whole. Rotated generations
// (<path>.1, .2, ...) are separate files this function never opens.
//
// Implementation: the window is read into memory in one Read, then
// walked by exact slice offsets. Offsets into a single contiguous
// buffer are trivially correct, which an incremental reader over the
// file is not: bufio.Reader buffers ahead, so its underlying file
// position runs up to one buffer past its logical position and cannot
// be used to derive a cursor.
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

	// The caller's cursor is a file offset. Convert to an offset within
	// `data` (which begins at windowStart). The clamp above guarantees
	// cursor >= windowStart, so this cannot go negative; min() only
	// guards against a caller-supplied cursor inside the window but past
	// its end, which the EOF check above does not catch when the file is
	// larger than maxScanBytes.
	cursorInWindow := min(cursor-windowStart, int64(len(data)))

	// When the caller resumes at the very start of the scan window, the
	// first line may have been truncated by the size-cap seek. Drop
	// bytes up to and including the first '\\n' so the caller never sees
	// a partial first entry. If there is no '\\n' at all, the file (or
	// the visible window) is one giant unterminated line: report it as
	// truncated (already true) and stop -- there is nothing for the
	// caller to page through.
	if cursor == windowStart && truncated {
		idx := bytes.IndexByte(data, '\n')
		if idx < 0 {
			return truncated, w.fileSize, false, nil
		}
		cursorInWindow = int64(idx + 1)
	}

	return truncated, walkWindow(data, windowStart, cursorInWindow, q, step), true, nil
}

// walkWindow invokes step for each entry in data at or after the
// in-buffer offset from that decodes and matches q, and returns the FILE
// offset at which the next page should resume.
//
// step receives the file offset just past the line it was given, and
// returns false to stop the walk; walkWindow then returns the cursor
// exactly there. A line that fails to decode is skipped rather than
// ending the walk -- a log may carry subprocess output that does not
// share our format.
//
// NOTE: a `false` return from step does NOT mean "no more matches". It
// means "you have enough for this page; stop here so you can return to
// the caller." The next call, started from the cursor walkWindow
// returns, will revisit the file offset step saw and continue walking.
// The caller is responsible for distinguishing the two outcomes via
// the limit it passed.
//
// The returned cursor is the FILE offset of the first byte after the
// last line walked, regardless of whether step stopped the walk early
// or the buffer was exhausted. Cursor values are in the same coordinate
// system across calls (file offset), so passing one straight back into
// the next Page call resumes the walk cleanly.
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

// Tail returns the most recent entries in path matching q.
//
// Entries are returned oldest-first so the UI can append newer ones from the
// live stream without reordering. A missing file is not an error: file logging
// is optional, and an empty result with a clear File value lets the caller say
// so rather than showing a failure.
//
// Tail preserves the legacy ring-buffer semantics: when more than Limit
// matching entries exist within the scan window, the OLDEST matches are
// dropped and Truncated is set. Page is the forward-paged alternative --
// callers that want every entry reachable should use Page, not Tail.
func Tail(path string, q Query) (Result, error) {
	res := Result{Entries: []Entry{}, File: path}
	limit := q.Limit
	if limit <= 0 {
		limit = DefaultTailLines
	}
	limit = min(limit, MaxTailLines)

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
		// A "nothing to read" early return (file > maxScanBytes with no
		// terminator in the window) is still Truncated: the scan cap
		// fired. Preserve that signal even though we have no entries to
		// surface -- the caller must know that older matching entries
		// exist beyond what we examined.
		res.Truncated = truncated
		return res, nil
	}
	res.Truncated = res.Truncated || truncated
	res.Entries = matches
	return res, nil
}

// Page walks path forward from cursor, returning up to Limit matching
// entries (clamped to MaxTailLines). A cursor of 0 starts at the
// beginning of the scanned window, exactly as Tail does.
//
// The returned Cursor is the byte offset at which the next call should
// resume; pass it back unchanged. When the file has been read to EOF
// within maxScanBytes, Cursor equals the file's size and MoreAvailable
// is false. Truncated is true when the scan cap fired before EOF,
// meaning older matching entries exist that were never examined -- the
// caller must decide whether to walk the rotation history or surface
// the gap.
//
// A cursor that points past the file's current size returns an empty
// page with MoreAvailable=false: rotation may have shrunk the file, and
// the next page is whatever the file now contains.
//
// Cursor=0 with a file larger than maxScanBytes begins at the first
// complete line AFTER the size-cap seek, exactly as Tail does --
// identical output to Tail under the same query when Limit is large
// enough to hold everything.
func Page(path string, q Query, cursor int64) (PageResult, error) {
	res := PageResult{Entries: []Entry{}, File: path}
	limit := q.Limit
	if limit <= 0 {
		limit = DefaultTailLines
	}
	limit = min(limit, MaxTailLines)

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
	// The cursor must resume AFTER the last entry this page returned, so
	// the next call does not re-walk the same entries. When the walk
	// stopped because the limit was reached (MoreAvailable), that resume
	// point is the end of the last matching line. When the walk ran the
	// whole buffer to completion (no more matches), the cursor is the
	// walk's own end offset -- which can sit past trailing lines that
	// failed the filter or failed to decode, and must, otherwise the next
	// call would re-walk those trailing bytes forever. The two values
	// differ only in the MoreAvailable=false case, which is exactly the
	// case where the doc comment promises "Cursor equals the file's size".
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

// withinTimeBounds reports whether t satisfies q's Since/Until bounds.
// Both bounds are inclusive, and an unset (zero) bound is no bound.
//
// An entry whose timestamp did not parse -- missing, non-string, or
// malformed "time", all of which leave decode's Entry.Time zero rather
// than dropping the record -- cannot be placed in a range, so ANY
// time-bounded query excludes it. That check is explicit rather than
// implied, because the two comparisons are not symmetric about the zero
// time on their own: zero.Before(since) is true so Since would drop such
// entries, while zero.After(until) is false so Until would silently keep
// them, and "errors before noon" would come back carrying records with
// no known time at all.
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
