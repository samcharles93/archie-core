package logging

import (
	"encoding/json"
	"slices"
)

// Read returns matching recent entries, bounded for both RPC and NATS replies.
func (f *Feed) Read(q Query) (Result, error) {
	entries := f.Snapshot()
	limit := q.Limit
	if limit <= 0 {
		limit = DefaultTailLines
	}
	if limit > MaxTailLines {
		limit = MaxTailLines
	}
	out := Result{Entries: []Entry{}}
	size := 0
	for _, entry := range slices.Backward(entries) {

		if !q.matches(entry) {
			continue
		}
		if len(out.Entries) == limit {
			out.Truncated = true
			break
		}
		raw, err := json.Marshal(entry)
		if err != nil {
			return Result{}, err
		}
		if size+len(raw) > 512<<10 {
			out.Truncated = true
			continue
		}
		size += len(raw)
		out.Entries = append(out.Entries, entry)
	}
	slices.Reverse(out.Entries)
	return out, nil
}
