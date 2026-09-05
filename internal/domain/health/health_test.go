package health

import (
	"context"
	"errors"
	"testing"
)

type fakeProbe struct {
	name  string
	check func(context.Context) Result
}

func (f fakeProbe) Name() string                     { return f.name }
func (f fakeProbe) Check(ctx context.Context) Result { return f.check(ctx) }

func TestRegistry_EmptyReportsOK(t *testing.T) {
	if got := NewRegistry().Run(context.Background()).Status; got != StatusOK {
		t.Fatalf("empty registry status = %q, want %q", got, StatusOK)
	}
}

func TestRegistry_AggregatesEveryComponent(t *testing.T) {
	r := NewRegistry(
		fakeProbe{name: "state_db", check: func(context.Context) Result {
			return Result{Status: StatusOK}
		}},
		fakeProbe{name: "config", check: func(context.Context) Result {
			return Result{Status: StatusOK}
		}},
	)
	report := r.Run(context.Background())

	if report.Status != StatusOK {
		t.Fatalf("status = %q, want %q", report.Status, StatusOK)
	}
	if len(report.Components) != 2 {
		t.Fatalf("components = %d, want 2", len(report.Components))
	}
	for _, c := range report.Components {
		if !c.Ready || c.Status != StatusOK {
			t.Fatalf("component %q should be ready, got %+v", c.Name, c)
		}
	}
}

func TestRegistry_OneDegradedDegradesTheWhole(t *testing.T) {
	r := NewRegistry(
		fakeProbe{name: "state_db", check: func(context.Context) Result {
			return Result{Status: StatusOK}
		}},
		fakeProbe{name: "disk", check: func(context.Context) Result {
			return Result{Status: StatusDegraded, Detail: "93% used"}
		}},
	)
	report := r.Run(context.Background())

	if report.Status != StatusDegraded {
		t.Fatalf("status = %q, want %q", report.Status, StatusDegraded)
	}
	if len(report.Components) != 2 {
		t.Fatalf("components = %d, want 2", len(report.Components))
	}
	if report.Components[1].Ready {
		t.Fatalf("disk component should not be ready, got %+v", report.Components[1])
	}
	if report.Components[1].Detail != "93% used" {
		t.Fatalf("disk detail = %q, want %q", report.Components[1].Detail, "93% used")
	}
}

func TestRegistry_OrderIsPreserved(t *testing.T) {
	r := NewRegistry(
		fakeProbe{name: "first", check: func(context.Context) Result { return Result{Status: StatusOK} }},
		fakeProbe{name: "second", check: func(context.Context) Result { return Result{Status: StatusOK} }},
	)
	report := r.Run(context.Background())
	if report.Components[0].Name != "first" || report.Components[1].Name != "second" {
		t.Fatalf("component order not preserved: %+v", report.Components)
	}
}

func TestRegistry_ProbeErrorMeansDegraded(t *testing.T) {
	r := NewRegistry(
		fakeProbe{name: "state_db", check: func(context.Context) Result {
			return Result{Status: StatusDegraded, Detail: errors.New("db locked").Error()}
		}},
	)
	report := r.Run(context.Background())
	if report.Status != StatusDegraded {
		t.Fatalf("status = %q, want %q", report.Status, StatusDegraded)
	}
	if report.Components[0].Ready {
		t.Fatalf("state_db should not be ready, got %+v", report.Components[0])
	}
}
