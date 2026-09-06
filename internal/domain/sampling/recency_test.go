package sampling

import (
	"context"
	"testing"
	"time"
)

func TestRecencySampler(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	candidates := []Candidate{
		{ID: "oldest", At: base},
		{ID: "middle", At: base.Add(time.Hour)},
		{ID: "newest", At: base.Add(2 * time.Hour)},
	}

	tests := []struct {
		name    string
		cap     int
		wantIDs []string
	}{
		{name: "no cap orders newest first", cap: 0, wantIDs: []string{"newest", "middle", "oldest"}},
		{name: "cap keeps only the most recent", cap: 2, wantIDs: []string{"newest", "middle"}},
		{name: "cap larger than count returns everything", cap: 100, wantIDs: []string{"newest", "middle", "oldest"}},
	}

	s := NewRecency()
	if s.Name() != "recency" {
		t.Fatalf("Name() = %q, want %q", s.Name(), "recency")
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := s.Sample(context.Background(), candidates, Request{Cap: tt.cap})
			if err != nil {
				t.Fatalf("Sample() error = %v", err)
			}
			assertIDs(t, got, tt.wantIDs)
		})
	}
}

func TestRecencySamplerTiesKeepInputOrder(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	candidates := []Candidate{
		{ID: "first", At: at},
		{ID: "second", At: at},
		{ID: "third", At: at},
	}
	got, err := NewRecency().Sample(context.Background(), candidates, Request{})
	if err != nil {
		t.Fatalf("Sample() error = %v", err)
	}
	assertIDs(t, got, []string{"first", "second", "third"})
}

func TestRecencySamplerDoesNotMutateInput(t *testing.T) {
	t.Parallel()
	assertInputNotMutated(t, NewRecency())
}
