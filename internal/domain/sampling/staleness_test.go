package sampling

import (
	"context"
	"testing"
	"time"
)

func TestStalenessSampler(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	candidates := []Candidate{
		{ID: "newest", At: base.Add(2 * time.Hour)},
		{ID: "middle", At: base.Add(time.Hour)},
		{ID: "oldest", At: base},
	}

	tests := []struct {
		name    string
		cap     int
		wantIDs []string
	}{
		{name: "no cap orders oldest first", cap: 0, wantIDs: []string{"oldest", "middle", "newest"}},
		{name: "cap keeps only the stalest", cap: 2, wantIDs: []string{"oldest", "middle"}},
		{name: "cap larger than count returns everything", cap: 100, wantIDs: []string{"oldest", "middle", "newest"}},
	}

	s := NewStaleness()
	if s.Name() != "staleness" {
		t.Fatalf("Name() = %q, want %q", s.Name(), "staleness")
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

func TestStalenessSamplerTiesKeepInputOrder(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	candidates := []Candidate{
		{ID: "first", At: at},
		{ID: "second", At: at},
		{ID: "third", At: at},
	}
	got, err := NewStaleness().Sample(context.Background(), candidates, Request{})
	if err != nil {
		t.Fatalf("Sample() error = %v", err)
	}
	assertIDs(t, got, []string{"first", "second", "third"})
}

func TestStalenessSamplerDoesNotMutateInput(t *testing.T) {
	t.Parallel()
	assertInputNotMutated(t, NewStaleness())
}
