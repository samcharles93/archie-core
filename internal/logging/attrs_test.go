package logging

import (
	"log/slog"
	"testing"
	"time"
)

// TestFlattenAttrs pins the shape FeedHandler and any other slog.Handler
// producing Entry.Fields must agree on: handler-bound attrs, group prefixes,
// and the record's own attrs all merge into one flat map, dotted by group.
func TestFlattenAttrs(t *testing.T) {
	tests := []struct {
		name   string
		base   []slog.Attr
		groups []string
		record func() slog.Record
		want   map[string]any
	}{
		{
			name:   "record attrs only",
			record: func() slog.Record { return newTestRecord("component", "gate") },
			want:   map[string]any{"component": "gate"},
		},
		{
			name:   "base attrs merge with record attrs",
			base:   []slog.Attr{slog.String("task", "42")},
			record: func() slog.Record { return newTestRecord("component", "gate") },
			want:   map[string]any{"task": "42", "component": "gate"},
		},
		{
			name:   "groups prefix every key",
			groups: []string{"agent"},
			record: func() slog.Record { return newTestRecord("component", "gate") },
			want:   map[string]any{"agent.component": "gate"},
		},
		{
			name:   "no attrs at all yields nil, not an empty map",
			record: func() slog.Record { return newTestRecord() },
			want:   nil,
		},
		{
			// base is what WithAttrs accumulates; by the time it reaches
			// FlattenAttrs it must already carry whatever group prefix was
			// active when it was added (see prefixAttrs) -- groups here is
			// the chain active *now*, for the record's own attrs, and must
			// not be re-applied to base. Callers that instead pass raw,
			// un-prefixed attrs as base alongside a non-empty groups chain
			// are the historical bug this case pins: it would wrongly nest
			// "task" under "agent" too.
			name:   "groups only apply to the record's own attrs, not to base",
			base:   []slog.Attr{slog.String("task", "42")}, // pre-grouped by the caller; here, not grouped at all
			groups: []string{"agent"},
			record: func() slog.Record { return newTestRecord("component", "gate") },
			want:   map[string]any{"task": "42", "agent.component": "gate"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FlattenAttrs(tt.record(), tt.base, tt.groups)
			if len(got) != len(tt.want) {
				t.Fatalf("FlattenAttrs() = %#v, want %#v", got, tt.want)
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("FlattenAttrs()[%q] = %v, want %v", k, got[k], v)
				}
			}
			if tt.want == nil && got != nil {
				t.Errorf("FlattenAttrs() = %#v, want nil", got)
			}
		})
	}
}

func newTestRecord(kvs ...string) slog.Record {
	r := slog.NewRecord(time.Now(), slog.LevelInfo, "msg", 0)
	for i := 0; i+1 < len(kvs); i += 2 {
		r.AddAttrs(slog.String(kvs[i], kvs[i+1]))
	}
	return r
}
