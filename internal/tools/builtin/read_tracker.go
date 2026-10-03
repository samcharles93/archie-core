package builtin

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
)

// lineRange is an inclusive 1-based line interval.
type lineRange struct{ start, end int }

// fileReads is what a session has already been shown of one file. identity is
// a cheap stat fingerprint; when it changes, served is discarded because the
// previously shown lines no longer describe the file.
type fileReads struct {
	identity string
	served   []lineRange // sorted, non-overlapping, non-adjacent
}

// ReadTracker records which files the model has read so that mutation
// tools (write, edit) can enforce a read-before-write safety check, and which
// line ranges of each file it has already been shown so read can avoid
// re-sending content the model already has.
type ReadTracker struct {
	mu    sync.Mutex
	reads map[string]bool       // absolute paths that have been read
	seen  map[string]*fileReads // absolute path -> already-served ranges
}

// NewReadTracker creates a new ReadTracker.
func NewReadTracker() *ReadTracker {
	return &ReadTracker{
		reads: make(map[string]bool),
		seen:  make(map[string]*fileReads),
	}
}

// FileIdentity fingerprints a file by size and modification time.
func FileIdentity(info os.FileInfo) string {
	return fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UnixNano())
}

// Novel returns the part of [start,end] not yet shown for this file and
// records the request. ok is false when all of it was shown. A fragmented
// remainder is served whole.
func (rt *ReadTracker) Novel(cwd, path, identity string, start, end int) (ns, ne int, ok bool) {
	abs := resolvePath(cwd, path)

	rt.mu.Lock()
	defer rt.mu.Unlock()

	st := rt.seen[abs]
	if st == nil || st.identity != identity {
		st = &fileReads{identity: identity}
		rt.seen[abs] = st
	}

	gaps := subtract(lineRange{start, end}, st.served)
	st.served = merge(append(st.served, lineRange{start, end}))

	switch len(gaps) {
	case 0:
		return 0, 0, false
	case 1:
		return gaps[0].start, gaps[0].end, true
	default:
		return start, end, true
	}
}

// subtract returns the parts of want not covered by served (sorted, disjoint).
func subtract(want lineRange, served []lineRange) []lineRange {
	var gaps []lineRange
	cursor := want.start
	for _, s := range served {
		if s.end < cursor {
			continue
		}
		if s.start > want.end {
			break
		}
		if s.start > cursor {
			gaps = append(gaps, lineRange{cursor, min(s.start-1, want.end)})
		}
		cursor = max(cursor, s.end+1)
		if cursor > want.end {
			return gaps
		}
	}
	if cursor <= want.end {
		gaps = append(gaps, lineRange{cursor, want.end})
	}
	return gaps
}

// merge normalises ranges into a sorted, non-overlapping, non-adjacent set.
// Adjacent ranges are coalesced so that [1,100] and [101,200] become [1,200],
// keeping the served set compact across many small reads.
func merge(ranges []lineRange) []lineRange {
	if len(ranges) < 2 {
		return ranges
	}
	slices.SortFunc(ranges, func(a, b lineRange) int { return a.start - b.start })

	out := ranges[:1]
	for _, r := range ranges[1:] {
		last := &out[len(out)-1]
		if r.start <= last.end+1 {
			last.end = max(last.end, r.end)
			continue
		}
		out = append(out, r)
	}
	return out
}

// MarkRead records that a file at the given path has been read by the
// model. The path is normalised to absolute form before recording.
func (rt *ReadTracker) MarkRead(cwd, path string) {
	abs := resolvePath(cwd, path)
	rt.mu.Lock()
	rt.reads[abs] = true
	rt.mu.Unlock()
}

// CheckRead returns an error if the file at the given path has not been
// read by the model in this session. The path is normalised to absolute
// form. A file must be read (via the read tool) before it can be written,
// edited.
func (rt *ReadTracker) CheckRead(cwd, path string) error {
	abs := resolvePath(cwd, path)
	rt.mu.Lock()
	read := rt.reads[abs]
	rt.mu.Unlock()

	if !read {
		rel, _ := filepath.Rel(cwd, abs)
		if rel == "" {
			rel = abs
		}
		return fmt.Errorf(
			"file %q has not been read in this session - use the read tool first to read the file before writing or editing it",
			rel,
		)
	}
	return nil
}
