package taskstatetest

import (
	"context"
	"slices"
	"testing"

	"github.com/samcharles93/archie-core/internal/taskstate"
)

// The routes the fixtures ask for, written out rather than derived from the
// table: a route that changed shape -- a longer way round, or through a status
// whose transition writes something else -- would otherwise be invisible.
func TestPathIsTheShortLegalRoute(t *testing.T) {
	tests := []struct {
		name, from, target string
		want               []string
	}{
		{name: "already there", from: taskstate.Queued, target: taskstate.Queued},
		{name: "claim", from: taskstate.Queued, target: taskstate.Running, want: []string{taskstate.Running}},
		{name: "await approval", from: taskstate.Queued, target: taskstate.WaitingHuman, want: []string{taskstate.Running, taskstate.WaitingHuman}},
		{name: "park", from: taskstate.Queued, target: taskstate.Parked, want: []string{taskstate.Running, taskstate.Parked}},
		{name: "spend the retries", from: taskstate.Queued, target: taskstate.Dead, want: []string{taskstate.Running, taskstate.Parked, taskstate.Dead}},
		{name: "merge", from: taskstate.Queued, target: taskstate.Merged, want: []string{taskstate.Running, taskstate.PROpen, taskstate.Merged}},
		{name: "rejected pull request", from: taskstate.Queued, target: taskstate.Rejected, want: []string{taskstate.Running, taskstate.PROpen, taskstate.Rejected}},
		{name: "finish with no pull request", from: taskstate.Queued, target: taskstate.Completed, want: []string{taskstate.Running, taskstate.Completed}},
		{name: "refuse the work", from: taskstate.Queued, target: taskstate.Declined, want: []string{taskstate.Declined}},
		{name: "merge a running task", from: taskstate.Running, target: taskstate.Merged, want: []string{taskstate.PROpen, taskstate.Merged}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Path(tt.from, tt.target)
			if err != nil {
				t.Fatalf("Path(%s, %s) error = %v", tt.from, tt.target, err)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("Path(%s, %s) = %v, want %v", tt.from, tt.target, got, tt.want)
			}
		})
	}
}

// Every route is walkable. A route through a pair the table refuses is exactly
// what this package exists to prevent, so it is checked here rather than
// discovered later at the store.
func TestRoutesOnlyTakeLegalEdges(t *testing.T) {
	for _, from := range statusIDs() {
		for _, want := range statusIDs() {
			got, err := Path(from, want)
			if err != nil {
				continue
			}
			at := from
			for _, to := range got {
				if !taskstate.CanTransition(at, to) {
					t.Errorf("route %s -> %s takes %s -> %s, which the table refuses", from, want, at, to)
				}
				at = to
			}
			if at != want {
				t.Errorf("route %s -> %s ends at %s", from, want, at)
			}
		}
	}
}

// Nothing follows a terminal status, so no fixture can seed out of one: it
// would have to have been written there illegitimately in the first place.
func TestTerminalStatusIsNotAStartingPoint(t *testing.T) {
	for _, from := range statusIDs() {
		if !taskstate.Terminal(from) {
			continue
		}
		for _, want := range statusIDs() {
			if want == from {
				continue
			}
			got, err := Path(from, want)
			if err == nil {
				t.Errorf("Path(%s, %s) = %v, want an error", from, want, got)
			}
		}
	}
}

func TestUnknownStatusIsNeverARoute(t *testing.T) {
	const unknown = "half_written"
	if got, err := Path(unknown, taskstate.Running); err == nil {
		t.Errorf("Path(%s, running) = %v, want an error", unknown, got)
	}
	if got, err := Path(taskstate.Queued, unknown); err == nil {
		t.Errorf("Path(queued, %s) = %v, want an error", unknown, got)
	}
	if got, err := Path(unknown, unknown); err == nil {
		t.Errorf("Path(%s, %s) = %v, want an error", unknown, unknown, got)
	}
}

// Seed applies the route edge by edge, in order, carrying the fixture's own
// reason: a park seeded this way keeps the reason the fixture passed, which is
// what the retry path reads back as the previous failure.
func TestSeedWalksTheRouteThroughTheStore(t *testing.T) {
	st := &recorder{}
	Seed(t.Context(), t, st, 7, taskstate.Queued, taskstate.Merged, "fixture reason")

	want := []edge{
		{from: taskstate.Queued, to: taskstate.Running, detail: "fixture reason"},
		{from: taskstate.Running, to: taskstate.PROpen, detail: "fixture reason"},
		{from: taskstate.PROpen, to: taskstate.Merged, detail: "fixture reason"},
	}
	if !slices.Equal(st.edges, want) {
		t.Errorf("Seed wrote %v, want %v", st.edges, want)
	}
}

// A task already in the wanted status is left untouched rather than written to
// itself: a guarded transition from running to running matches no row.
func TestSeedOfTheSameStatusWritesNothing(t *testing.T) {
	st := &recorder{}
	Seed(t.Context(), t, st, 7, taskstate.Completed, taskstate.Completed, "")
	if len(st.edges) != 0 {
		t.Errorf("Seed wrote %v, want no writes", st.edges)
	}
}

type edge struct{ from, to, detail string }

type recorder struct{ edges []edge }

func (r *recorder) Transition(_ context.Context, _ int64, from, to, detail string) error {
	r.edges = append(r.edges, edge{from: from, to: to, detail: detail})
	return nil
}
