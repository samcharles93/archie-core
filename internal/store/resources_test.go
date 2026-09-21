package store

import (
	"errors"
	"testing"
	"time"
)

func TestPutResourceRejectsStaleVersion(t *testing.T) {
	s := OpenTest(t)
	defer s.Close()
	ctx := t.Context()
	first, err := s.PutResource(ctx, ResourceWrite{Kind: "settings", Value: []byte(`{"v":1}`), Actor: "a", Source: "test", RequestID: "r1", ExpectedVersion: 0, At: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.PutResource(ctx, ResourceWrite{Kind: "settings", Value: []byte(`{"v":2}`), Actor: "a", Source: "test", RequestID: "r2", ExpectedVersion: 0, At: time.Now()})
	if !errors.Is(err, ErrResourceVersionConflict) {
		t.Fatalf("PutResource() error = %v, want ErrResourceVersionConflict (current %d)", err, first.Version)
	}
}

func TestPutResourceDuplicateRequestIsIdempotent(t *testing.T) {
	s := OpenTest(t)
	defer s.Close()
	ctx := t.Context()
	write := ResourceWrite{Kind: "settings", Value: []byte(`{"v":1}`), Actor: "a", Source: "test", RequestID: "same", ExpectedVersion: 0, At: time.Now()}
	first, err := s.PutResource(ctx, write)
	if err != nil {
		t.Fatal(err)
	}
	write.Value = []byte(`{"v":999}`)
	second, err := s.PutResource(ctx, write)
	if err != nil {
		t.Fatal(err)
	}
	if first.Version != second.Version || string(second.Value) != string(first.Value) {
		t.Fatalf("duplicate = (%d, %s), want (%d, %s)", second.Version, second.Value, first.Version, first.Value)
	}
}

// TestPutResourceRequestIDIsScopedToTheKind: the request ID makes one write
// idempotent for the kind it wrote: the dedup key is (kind, request ID), so a
// request ID reused across two kinds writes the second instead of silently
// replaying the first kind's resource (archie-core-fcvd).
func TestPutResourceRequestIDIsScopedToTheKind(t *testing.T) {
	s := OpenTest(t)
	defer s.Close()
	ctx := t.Context()
	if _, err := s.PutResource(ctx, ResourceWrite{Kind: "settings", Value: []byte(`{"v":1}`), Actor: "a", Source: "test", RequestID: "shared", ExpectedVersion: 0, At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	second, err := s.PutResource(ctx, ResourceWrite{Kind: "other", Value: []byte(`{"v":9}`), Actor: "a", Source: "test", RequestID: "shared", ExpectedVersion: 0, At: time.Now()})
	if err != nil {
		t.Fatalf("PutResource with a request ID another kind already used: %v", err)
	}
	if second.Kind != "other" || second.Version != 1 {
		t.Fatalf("second write = (%s, v%d), want the %q write itself, not a replay of the first kind's resource", second.Kind, second.Version, "other")
	}
}

func TestResourceHistoryReturnsEveryRevisionNewestFirst(t *testing.T) {
	s := OpenTest(t)
	defer s.Close()
	ctx := t.Context()
	for i, write := range []ResourceWrite{
		{Kind: "settings", Value: []byte(`{"v":1}`), Actor: "operator", Source: "archie-ui", RequestID: "r1", ExpectedVersion: 0},
		{Kind: "settings", Value: []byte(`{"v":2}`), Actor: "operator", Source: "messaging", RequestID: "r2", ExpectedVersion: 1},
		{Kind: "other", Value: []byte(`{"v":9}`), Actor: "operator", Source: "archie-ui", RequestID: "r3", ExpectedVersion: 0},
	} {
		write.At = time.Now().UTC()
		if _, err := s.PutResource(ctx, write); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}

	history, err := s.ResourceHistory(ctx, "settings", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 {
		t.Fatalf("history = %d revisions, want 2 (other kinds must not leak in)", len(history))
	}
	if history[0].Version != 2 || string(history[0].Value) != `{"v":2}` || history[0].Source != "messaging" {
		t.Fatalf("newest revision = %+v, want version 2 from messaging", history[0])
	}
	if history[1].Version != 1 || history[1].Actor != "operator" || history[1].At.IsZero() {
		t.Fatalf("oldest revision = %+v, want version 1 with its audit intact", history[1])
	}

	limited, err := s.ResourceHistory(ctx, "settings", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 1 || limited[0].Version != 2 {
		t.Fatalf("limited history = %+v, want the newest revision alone", limited)
	}
}

func TestResourceHistoryForUnknownKindIsEmpty(t *testing.T) {
	s := OpenTest(t)
	defer s.Close()
	history, err := s.ResourceHistory(t.Context(), "missing", 10)
	if err != nil {
		t.Fatalf("history for an unknown kind must not error: %v", err)
	}
	if len(history) != 0 {
		t.Fatalf("history = %+v, want empty", history)
	}
}
