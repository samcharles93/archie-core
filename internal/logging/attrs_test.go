package logging

import (
	"encoding/json"
	"errors"
	"fmt"
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

// TestFlattenAttrsConvertsErrorsToTheirMessageString pins the regression that
// shipped archie-core's task/system logs full of "err":{}: SystemLogHandler
// and FeedHandler both json.Marshal Entry.Fields directly rather than going
// through slog's own JSON handler, and encoding/json has no idea an `error`
// value should become its message -- it reflects the concrete type's fields,
// which for a stdlib error are unexported, so the message vanishes into `{}`.
func TestFlattenAttrsConvertsErrorsToTheirMessageString(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "plain error", err: errors.New("boom"), want: "boom"},
		{name: "wrapped error", err: fmt.Errorf("outer: %w", errors.New("inner")), want: "outer: inner"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			record := slog.NewRecord(time.Now(), slog.LevelError, "handle failed", 0)
			record.AddAttrs(slog.Any("err", tt.err))

			got := FlattenAttrs(record, nil, nil)

			value, ok := got["err"].(string)
			if !ok {
				t.Fatalf("FlattenAttrs()[%q] = %#v (%T), want a string", "err", got["err"], got["err"])
			}
			if value != tt.want {
				t.Errorf("FlattenAttrs()[%q] = %q, want %q", "err", value, tt.want)
			}

			encoded, marshalErr := json.Marshal(map[string]any{"err": got["err"]})
			if marshalErr != nil {
				t.Fatalf("json.Marshal: %v", marshalErr)
			}
			if string(encoded) == `{"err":{}}` {
				t.Errorf("json.Marshal(%#v) = %s, error message was discarded", got["err"], encoded)
			}
		})
	}
}

// typedNilError has a pointer receiver Error() that dereferences itself, so
// calling it on a nil *typedNilError panics -- the classic Go footgun where
// a non-nil error interface wraps a nil concrete pointer.
type typedNilError struct{ msg string }

func (e *typedNilError) Error() string { return e.msg }

// TestFlattenAttrsDoesNotPanicOnATypedNilError pins the case
// TestFlattenAttrsConvertsErrorsToTheirMessageString's err != nil check
// misses: `var e *typedNilError; var err error = e` is a non-nil interface,
// so a naive nil check would call Error() on a nil receiver and crash the
// process -- the one failure mode a logging package must never have.
func TestFlattenAttrsDoesNotPanicOnATypedNilError(t *testing.T) {
	var e *typedNilError
	var err error = e

	record := slog.NewRecord(time.Now(), slog.LevelError, "handle failed", 0)
	record.AddAttrs(slog.Any("err", err))

	got := FlattenAttrs(record, nil, nil) // must not panic

	if _, err := json.Marshal(map[string]any{"err": got["err"]}); err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
}

func newTestRecord(kvs ...string) slog.Record {
	r := slog.NewRecord(time.Now(), slog.LevelInfo, "msg", 0)
	for i := 0; i+1 < len(kvs); i += 2 {
		r.AddAttrs(slog.String(kvs[i], kvs[i+1]))
	}
	return r
}
