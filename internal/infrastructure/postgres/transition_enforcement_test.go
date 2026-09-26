package postgres

import (
	"errors"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/taskstate"
	"github.com/samcharles93/archie-core/internal/taskstate/taskstatetest"
)

// Enforcement of the shared transition table (docs/prds/execution-tree-state-machine.md):
// a status write whose from->to pair the table does not route is refused with
// ErrIllegalTransition, leaves the row untouched and writes no audit row --
// the same three guarantees a stale from already had under ErrStaleTransition.

// TestIllegalTransitionsAreRejected drives every caller-supplied status write
// through an illegal pair and asserts the refusal is total: sentinel, status,
// audit trail.
func TestIllegalTransitionsAreRejected(t *testing.T) {
	tests := []struct {
		name  string
		seed  string // status the task is parked in before the write
		write func(s *Store, taskID int64) error
	}{
		{name: "transition queued->parked", seed: taskstate.Queued, write: func(s *Store, taskID int64) error {
			return s.Transition(t.Context(), taskID, taskstate.Queued, taskstate.Parked, "why")
		}},
		{name: "transition running->merged", seed: taskstate.Running, write: func(s *Store, taskID int64) error {
			return s.Transition(t.Context(), taskID, taskstate.Running, taskstate.Merged, "why")
		}},
		{name: "transition running->rejected", seed: taskstate.Running, write: func(s *Store, taskID int64) error {
			return s.Transition(t.Context(), taskID, taskstate.Running, taskstate.Rejected, "why")
		}},
		{name: "transition parked->running", seed: taskstate.Parked, write: func(s *Store, taskID int64) error {
			return s.Transition(t.Context(), taskID, taskstate.Parked, taskstate.Running, "why")
		}},
		{name: "transition waiting_human->pr_open", seed: taskstate.WaitingHuman, write: func(s *Store, taskID int64) error {
			return s.Transition(t.Context(), taskID, taskstate.WaitingHuman, taskstate.PROpen, "why")
		}},
		{name: "transition out of terminal merged", seed: taskstate.Merged, write: func(s *Store, taskID int64) error {
			return s.Transition(t.Context(), taskID, taskstate.Merged, taskstate.Queued, "why")
		}},
		{name: "park from queued", seed: taskstate.Queued, write: func(s *Store, taskID int64) error {
			return s.ParkTask(t.Context(), taskID, taskstate.Queued, "why", taskstate.ParkTransient)
		}},
		{name: "park from pr_open", seed: taskstate.PROpen, write: func(s *Store, taskID int64) error {
			return s.ParkTask(t.Context(), taskID, taskstate.PROpen, "why", taskstate.ParkTransient)
		}},
		{name: "requeue from terminal merged", seed: taskstate.Merged, write: func(s *Store, taskID int64) error {
			return s.Requeue(t.Context(), taskID, taskstate.Merged, "implement")
		}},
		{name: "retry from terminal merged", seed: taskstate.Merged, write: func(s *Store, taskID int64) error {
			return s.RetryTask(t.Context(), taskID, taskstate.Merged, "implement")
		}},
		{name: "retry from queued self-pair", seed: taskstate.Queued, write: func(s *Store, taskID int64) error {
			return s.RetryTask(t.Context(), taskID, taskstate.Queued, "implement")
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := storeFor(t)
			task, err := s.EnqueueChatTask(t.Context(), "acme", "widgets", "t", "b", "implement", "")
			if err != nil || task == nil {
				t.Fatalf("EnqueueChatTask: %+v %v", task, err)
			}
			taskstatetest.Seed(t.Context(), t, s, task.ID, taskstate.Queued, tt.seed, "seed")
			before := len(transitions(t, s, task.ID))

			err = tt.write(s, task.ID)
			if !errors.Is(err, storecontract.ErrIllegalTransition) {
				t.Fatalf("illegal write = %v, want ErrIllegalTransition", err)
			}
			got, err := s.TaskByID(t.Context(), task.ID)
			if err != nil || got == nil {
				t.Fatalf("TaskByID: %+v %v", got, err)
			}
			if got.Status != tt.seed {
				t.Fatalf("status after refused write = %q, want %q", got.Status, tt.seed)
			}
			if n := len(transitions(t, s, task.ID)); n != before {
				t.Fatalf("refused write added %d audit row(s), want none", n-before)
			}
		})
	}
}

// Staleness still wins when the from is both untruthful and illegal: the
// row's own status is the ground truth a caller first has to be right about
// (a queued->merged write against a running task is stale, as the wire
// conformance battery has always asserted).
func TestStaleFromBeatsIllegalPair(t *testing.T) {
	s := storeFor(t)
	task, err := s.EnqueueChatTask(t.Context(), "acme", "widgets", "t", "b", "implement", "")
	if err != nil || task == nil {
		t.Fatalf("EnqueueChatTask: %+v %v", task, err)
	}
	taskstatetest.Seed(t.Context(), t, s, task.ID, taskstate.Queued, taskstate.Running, "claim")
	err = s.Transition(t.Context(), task.ID, taskstate.Queued, taskstate.Merged, "why")
	if !errors.Is(err, storecontract.ErrStaleTransition) {
		t.Fatalf("untruthful from = %v, want ErrStaleTransition", err)
	}
}

// A legal pair still lands: the guard must not reject what the table routes,
// and the audit row is written as before.
func TestLegalTransitionStillWrites(t *testing.T) {
	s := storeFor(t)
	task, err := s.EnqueueChatTask(t.Context(), "acme", "widgets", "t", "b", "implement", "")
	if err != nil || task == nil {
		t.Fatalf("EnqueueChatTask: %+v %v", task, err)
	}
	taskstatetest.Seed(t.Context(), t, s, task.ID, taskstate.Queued, taskstate.Running, "claim")
	if err := s.Transition(t.Context(), task.ID, taskstate.Running, taskstate.Completed, "done"); err != nil {
		t.Fatalf("legal transition running->completed = %v, want nil", err)
	}
	got, err := s.TaskByID(t.Context(), task.ID)
	if err != nil || got == nil {
		t.Fatalf("TaskByID: %+v %v", got, err)
	}
	if got.Status != taskstate.Completed {
		t.Fatalf("status = %q, want %q", got.Status, taskstate.Completed)
	}
	rows := transitions(t, s, task.ID)
	if n := len(rows); n == 0 || rows[n-1] != (transitionRow{taskstate.Running, taskstate.Completed, "done"}) {
		t.Fatalf("last audit row = %+v, want the running->completed edge", rows)
	}
}

// The status-changing methods whose from/to is pinned in SQL rather than
// supplied by a caller (claim, BeginRemediation, RecoverStale) can only write
// the pairs asserted here. Pinning them to the shared table keeps a query
// edit from moving a write off the table without this file failing.
func TestSQLPinnedTransitionsAreTableLegal(t *testing.T) {
	tests := []struct {
		name     string
		from, to string
	}{
		{name: "claim pins queued->running", from: taskstate.Queued, to: taskstate.Running},
		{name: "begin remediation pins pr_open->queued", from: taskstate.PROpen, to: taskstate.Queued},
		{name: "crash recovery pins running->queued", from: taskstate.Running, to: taskstate.Queued},
	}
	for _, tt := range tests {
		if !taskstate.CanTransition(tt.from, tt.to) {
			t.Errorf("%s: %s -> %s is not in the shared transition table", tt.name, tt.from, tt.to)
		}
	}
}
