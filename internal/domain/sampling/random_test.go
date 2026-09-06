package sampling

import (
	"context"
	"testing"
	"time"
)

func candidatesFor(n int) []Candidate {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	out := make([]Candidate, n)
	for i := range out {
		out[i] = Candidate{ID: string(rune('a' + i)), At: base.Add(time.Duration(i) * time.Hour)}
	}
	return out
}

func TestRandomSamplerName(t *testing.T) {
	t.Parallel()
	if got := NewRandom().Name(); got != "random" {
		t.Fatalf("Name() = %q, want %q", got, "random")
	}
}

func TestRandomSamplerCap(t *testing.T) {
	t.Parallel()
	candidates := candidatesFor(10)

	tests := []struct {
		name    string
		cap     int
		wantLen int
	}{
		{name: "no cap returns every candidate", cap: 0, wantLen: 10},
		{name: "cap smaller than count", cap: 3, wantLen: 3},
		{name: "cap larger than count returns every candidate", cap: 100, wantLen: 10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := NewRandom().Sample(context.Background(), candidates, Request{Cap: tt.cap, Seed: 1})
			if err != nil {
				t.Fatalf("Sample() error = %v", err)
			}
			if len(got) != tt.wantLen {
				t.Fatalf("len(Sample()) = %d, want %d", len(got), tt.wantLen)
			}
		})
	}
}

func TestRandomSamplerSelectionIsASubsetOfInput(t *testing.T) {
	t.Parallel()
	candidates := candidatesFor(10)
	got, err := NewRandom().Sample(context.Background(), candidates, Request{Cap: 5, Seed: 42})
	if err != nil {
		t.Fatalf("Sample() error = %v", err)
	}

	valid := make(map[string]bool, len(candidates))
	for _, c := range candidates {
		valid[c.ID] = true
	}
	seen := make(map[string]bool, len(got))
	for _, c := range got {
		if !valid[c.ID] {
			t.Fatalf("Sample() returned candidate %q not present in input", c.ID)
		}
		if seen[c.ID] {
			t.Fatalf("Sample() returned candidate %q more than once", c.ID)
		}
		seen[c.ID] = true
	}
}

func TestRandomSamplerDeterministicForSameSeed(t *testing.T) {
	t.Parallel()
	candidates := candidatesFor(20)
	s := NewRandom()

	first, err := s.Sample(context.Background(), candidates, Request{Cap: 8, Seed: 99})
	if err != nil {
		t.Fatalf("Sample() error = %v", err)
	}
	second, err := s.Sample(context.Background(), candidates, Request{Cap: 8, Seed: 99})
	if err != nil {
		t.Fatalf("Sample() error = %v", err)
	}

	wantIDs := make([]string, len(first))
	for i, c := range first {
		wantIDs[i] = c.ID
	}
	assertIDs(t, second, wantIDs)
}

func TestRandomSamplerDifferentSeedsCanDiffer(t *testing.T) {
	t.Parallel()
	candidates := candidatesFor(20)
	s := NewRandom()

	a, err := s.Sample(context.Background(), candidates, Request{Cap: 8, Seed: 1})
	if err != nil {
		t.Fatalf("Sample() error = %v", err)
	}
	b, err := s.Sample(context.Background(), candidates, Request{Cap: 8, Seed: 2})
	if err != nil {
		t.Fatalf("Sample() error = %v", err)
	}

	same := len(a) == len(b)
	if same {
		for i := range a {
			if a[i].ID != b[i].ID {
				same = false
				break
			}
		}
	}
	if same {
		t.Fatalf("Sample() with different seeds produced identical output: %v", a)
	}
}

func TestRandomSamplerEmptyInput(t *testing.T) {
	t.Parallel()
	got, err := NewRandom().Sample(context.Background(), nil, Request{Seed: 1})
	if err != nil {
		t.Fatalf("Sample() error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Sample() = %v, want empty", got)
	}
}

func TestRandomSamplerDoesNotMutateInput(t *testing.T) {
	t.Parallel()
	assertInputNotMutated(t, NewRandom())
}
