package store

import (
	"testing"
	"time"
)

func TestInsertCaptureRoundTrips(t *testing.T) {
	s := openTest(t)
	id, err := s.InsertCapture(t.Context(), CapturedEvent{
		Source:        "github",
		RemoteAddr:    "203.0.113.5",
		ContentType:   "application/json",
		Headers:       `{"x-hub-signature-256":"[redacted]"}`,
		Body:          `{"action":"opened"}`,
		Authenticated: false,
	}, 7*24*time.Hour, 0)
	if err != nil {
		t.Fatalf("InsertCapture: %v", err)
	}
	if id == 0 {
		t.Fatalf("InsertCapture returned zero id")
	}

	got, err := s.ListCaptures(t.Context(), 10)
	if err != nil {
		t.Fatalf("ListCaptures: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListCaptures = %d rows, want 1", len(got))
	}
	c := got[0]
	if c.ID != id || c.Source != "github" || c.RemoteAddr != "203.0.113.5" ||
		c.ContentType != "application/json" || c.Body != `{"action":"opened"}` ||
		c.Authenticated {
		t.Fatalf("round-tripped capture = %+v, want matching fields", c)
	}
	if c.ReceivedAt.IsZero() {
		t.Fatalf("ReceivedAt is zero, want a timestamp stamped at insert")
	}
}

func TestListCapturesOrdersNewestFirst(t *testing.T) {
	s := openTest(t)
	for i := range 3 {
		if _, err := s.InsertCapture(t.Context(), CapturedEvent{Source: "src", Body: "{}"}, 0, 0); err != nil {
			t.Fatalf("InsertCapture[%d]: %v", i, err)
		}
	}
	got, err := s.ListCaptures(t.Context(), 10)
	if err != nil {
		t.Fatalf("ListCaptures: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("ListCaptures = %d rows, want 3", len(got))
	}
	if got[0].ID <= got[1].ID || got[1].ID <= got[2].ID {
		t.Fatalf("ListCaptures not newest-first: ids = %d, %d, %d", got[0].ID, got[1].ID, got[2].ID)
	}
}

func TestInsertCapturePrunesBeyondMaxEvents(t *testing.T) {
	s := openTest(t)
	var lastID int64
	for i := range 5 {
		id, err := s.InsertCapture(t.Context(), CapturedEvent{Source: "src", Body: "{}"}, 0, 3)
		if err != nil {
			t.Fatalf("InsertCapture[%d]: %v", i, err)
		}
		lastID = id
	}
	got, err := s.ListCaptures(t.Context(), 100)
	if err != nil {
		t.Fatalf("ListCaptures: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("row count = %d, want 3 (maxEvents cap)", len(got))
	}
	if got[0].ID != lastID {
		t.Fatalf("newest surviving row id = %d, want %d (the last insert)", got[0].ID, lastID)
	}
	// The two oldest of the five inserts must have been pruned.
	for _, c := range got {
		if c.ID <= lastID-3 {
			t.Fatalf("found pruned row id %d still present after maxEvents=3 cap", c.ID)
		}
	}
}

func TestInsertCapturePrunesOlderThanRetention(t *testing.T) {
	s := openTest(t)
	// Insert a row and manually age it beyond the retention window, since
	// InsertCapture always stamps "now" and the test cannot wait real days.
	id, err := s.InsertCapture(t.Context(), CapturedEvent{Source: "old", Body: "{}"}, 0, 0)
	if err != nil {
		t.Fatalf("InsertCapture: %v", err)
	}
	stale := time.Now().UTC().Add(-8 * 24 * time.Hour).Format(captureTimeLayout)
	if _, err := s.db.ExecContext(t.Context(),
		`UPDATE captured_events SET received_at = ? WHERE id = ?`, stale, id); err != nil {
		t.Fatalf("backdate row: %v", err)
	}

	// A fresh insert with a 7-day retention should prune the now-stale row.
	if _, err := s.InsertCapture(t.Context(), CapturedEvent{Source: "new", Body: "{}"}, 7*24*time.Hour, 0); err != nil {
		t.Fatalf("InsertCapture: %v", err)
	}

	got, err := s.ListCaptures(t.Context(), 100)
	if err != nil {
		t.Fatalf("ListCaptures: %v", err)
	}
	for _, c := range got {
		if c.ID == id {
			t.Fatalf("stale row id %d survived a 7-day retention prune", id)
		}
	}
	if len(got) != 1 {
		t.Fatalf("row count = %d, want 1 (only the fresh insert)", len(got))
	}
}

// TestCaptureTimeLayoutSortsLexicographicallyLikeChronologically pins a real
// defect that time.RFC3339Nano has: it trims trailing fractional-second
// zeros and omits the fractional part entirely when it is exactly zero, so
// "15:04:05Z" (an exact second) sorts LEXICOGRAPHICALLY AFTER "15:04:05.1Z"
// (0.1s later) because 'Z' (0x5A) > '.' (0x2E) -- backwards from
// chronological order. A retention prune using `WHERE received_at < ?` on a
// column written that way is unreliable within the same second.
// captureTimeLayout must be fixed-width so string order and time order
// always agree.
//
// This compares captureTimeLayout's string output directly rather than
// going through InsertCapture's real prune, because the defect only shows up
// when two timestamps share the same whole-second value and differ only in
// fractional presence -- a case InsertCapture's own cutoff (computed from
// the real wall clock) cannot be steered into deterministically in a test.
func TestCaptureTimeLayoutSortsLexicographicallyLikeChronologically(t *testing.T) {
	exactSecond := time.Date(2026, 1, 1, 15, 4, 5, 0, time.UTC)
	fractionLater := time.Date(2026, 1, 1, 15, 4, 5, 100_000_000, time.UTC) // same second, 0.1s later

	if !exactSecond.Before(fractionLater) {
		t.Fatalf("test setup: %v must be before %v", exactSecond, fractionLater)
	}
	es, fs := exactSecond.Format(captureTimeLayout), fractionLater.Format(captureTimeLayout)
	// The layout is second-precision, so two timestamps within the same
	// second are allowed to format identically (that's the accepted
	// precision tradeoff, not a bug) -- but a later time must never sort as
	// lexicographically LESS than an earlier one.
	if es > fs {
		t.Fatalf("captureTimeLayout string order disagrees with chronological order: %q should not sort after %q", es, fs)
	}
}

func TestInsertCaptureZeroRetentionAndMaxEventsMeansUnbounded(t *testing.T) {
	s := openTest(t)
	for i := range 10 {
		if _, err := s.InsertCapture(t.Context(), CapturedEvent{Source: "src", Body: "{}"}, 0, 0); err != nil {
			t.Fatalf("InsertCapture[%d]: %v", i, err)
		}
	}
	got, err := s.ListCaptures(t.Context(), 100)
	if err != nil {
		t.Fatalf("ListCaptures: %v", err)
	}
	if len(got) != 10 {
		t.Fatalf("row count = %d, want 10 (retention=0, maxEvents=0 disables both prunes)", len(got))
	}
}
