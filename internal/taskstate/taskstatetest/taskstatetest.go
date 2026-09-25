// Package taskstatetest seeds task lifecycle states for tests.
//
// A fixture that needs a task in a finished state used to write the pair it
// wanted -- queued -> merged, queued -> parked -- because the store accepted
// it. The transition tables (internal/taskstate) refuse most of those pairs,
// so a fixture that keeps writing one fails the moment the store enforces
// them. Seeding through this package moves the task along the table's legal
// edges instead, and fails loudly when no legal route exists.
package taskstatetest

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/samcharles93/archie-core/internal/taskstate"
)

// Transitioner is the guarded status write the seeder needs: the store's own
// Transition, which refuses a from that is not the task's current status.
type Transitioner interface {
	Transition(ctx context.Context, taskID int64, from, to, detail string) error
}

// Seed moves taskID from the status it holds to want, one legal edge at a
// time, recording detail on every edge. It fails the test on the edge that the
// store refuses, or before any edge when the table has no route at all.
//
// ctx is the caller's, so a fixture that seeds with a context of its own
// chooses it rather than inheriting one the helper invented.
func Seed(ctx context.Context, t testing.TB, st Transitioner, taskID int64, from, want, detail string) {
	t.Helper()
	route, err := Path(from, want)
	if err != nil {
		t.Fatalf("seed task %d: %v", taskID, err)
	}
	at := from
	for _, to := range route {
		if err := st.Transition(ctx, taskID, at, to, detail); err != nil {
			t.Fatalf("seed task %d to %s: %s -> %s: %v", taskID, want, at, to, err)
		}
		at = to
	}
}

// Path returns the statuses a task moves through to get from one status to
// another, as the shortest route through the execution transition table. A nil
// route means from is already want.
//
// It is exported for the fixtures that seed from a goroutine, where t.Fatalf is
// not callable: those compute the route on the test goroutine and apply it in
// the callback.
func Path(from, want string) ([]string, error) {
	statuses := statusIDs()
	if !slices.Contains(statuses, from) {
		return nil, fmt.Errorf("%q is not a task status", from)
	}
	if !slices.Contains(statuses, want) {
		return nil, fmt.Errorf("%q is not a task status", want)
	}
	if from == want {
		return nil, nil
	}

	// Breadth-first, walking each row's edges in catalog order, so the route a
	// fixture takes is the shortest one and the same on every run.
	arrivedFrom := map[string]string{from: from}
	queue := []string{from}
	for len(queue) > 0 {
		at := queue[0]
		queue = queue[1:]
		for _, next := range statuses {
			if _, seen := arrivedFrom[next]; seen || !taskstate.CanTransition(at, next) {
				continue
			}
			arrivedFrom[next] = at
			if next == want {
				return route(arrivedFrom, from, want), nil
			}
			queue = append(queue, next)
		}
	}
	return nil, fmt.Errorf("no legal route from %s to %s", from, want)
}

// route reconstructs the way to want, ending with it and leaving out the
// status the task was already in.
func route(arrivedFrom map[string]string, from, want string) []string {
	var reverse []string
	for at := want; at != from; at = arrivedFrom[at] {
		reverse = append(reverse, at)
	}
	slices.Reverse(reverse)
	return reverse
}

func statusIDs() []string {
	catalog := taskstate.Statuses()
	ids := make([]string, 0, len(catalog))
	for _, meta := range catalog {
		ids = append(ids, meta.ID)
	}
	return ids
}
