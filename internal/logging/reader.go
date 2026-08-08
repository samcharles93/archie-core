package logging

import (
	"bufio"
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

// Tail returns the most recent entries in path matching q.
//
// Entries are returned oldest-first so the UI can append newer ones from the
// live stream without reordering. A missing file is not an error: file logging
// is optional, and an empty result with a clear File value lets the caller say
// so rather than showing a failure.
func Tail(path string, q Query) (Result, error) {
	res := Result{Entries: []Entry{}, File: path}
	if strings.TrimSpace(path) == "" {
		return res, nil
	}

	limit := q.Limit
	if limit <= 0 {
		limit = DefaultTailLines
	}
	limit = min(limit, MaxTailLines)

	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return res, nil
		}
		return res, fmt.Errorf("logging: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return res, fmt.Errorf("logging: stat %s: %w", path, err)
	}

	// Read only the tail: a long-lived daemon's log is far larger than any
	// page of it, and seeking past the head keeps memory bounded.
	if info.Size() > maxScanBytes {
		if _, err := f.Seek(info.Size()-maxScanBytes, 0); err != nil {
			return res, fmt.Errorf("logging: seek %s: %w", path, err)
		}
		res.Truncated = true
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	if res.Truncated {
		scanner.Scan() // discard the partial first line after seeking
	}

	// Ring buffer: keep only the newest `limit` matches without holding the
	// whole file.
	matches := make([]Entry, 0, limit)
	for scanner.Scan() {
		entry, ok := decode(scanner.Bytes())
		if !ok || !q.matches(entry) {
			continue
		}
		if len(matches) == limit {
			matches = append(matches[:0], matches[1:]...)
			res.Truncated = true
		}
		matches = append(matches, entry)
	}
	if err := scanner.Err(); err != nil {
		return res, fmt.Errorf("logging: read %s: %w", path, err)
	}

	res.Entries = matches
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
