package sampling

import (
	"context"
	"testing"
	"time"
)

func TestAllSampler(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	candidates := []Candidate{
		{ID: "a", At: base},
		{ID: "b", At: base.Add(time.Hour)},
		{ID: "c", At: base.Add(2 * time.Hour)},
	}

	tests := []struct {
		name    string
		cap     int
		wantIDs []string
	}{
		{name: "no cap returns everything in input order", cap: 0, wantIDs: []string{"a", "b", "c"}},
		{name: "cap truncates from the front", cap: 2, wantIDs: []string{"a", "b"}},
		{name: "cap larger than candidate count returns everything", cap: 100, wantIDs: []string{"a", "b", "c"}},
		{name: "negative cap means unlimited", cap: -1, wantIDs: []string{"a", "b", "c"}},
	}

	s := NewAll()
	if s.Name() != "all" {
		t.Fatalf("Name() = %q, want %q", s.Name(), "all")
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

func TestAllSamplerEmptyInput(t *testing.T) {
	t.Parallel()
	got, err := NewAll().Sample(context.Background(), nil, Request{})
	if err != nil {
		t.Fatalf("Sample() error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Sample() = %v, want empty", got)
	}
}

func TestAllSamplerDoesNotMutateInput(t *testing.T) {
	t.Parallel()
	assertInputNotMutated(t, NewAll())
}
