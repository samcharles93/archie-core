package postgres_test

import (
	"slices"
	"testing"

	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgstore"
	"github.com/samcharles93/archie-core/internal/taskstate"
)

// A conversation's /stop finds the queued and running tasks it created, and
// only those: not another conversation's, not finished ones, not originless.
func TestActiveTasksByOrigin(t *testing.T) {
	ctx := t.Context()
	db := pgstore.Open(t)
	enqueue := func(origin string) int64 {
		created, err := db.EnqueueChatTask(ctx, "", "", "work", "", "tdd", "", origin, nil)
		if err != nil {
			t.Fatal(err)
		}
		return created.ID
	}
	running, queued, finished := enqueue("telegram:1"), enqueue("telegram:1"), enqueue("telegram:1")
	enqueue("telegram:2")
	enqueue("")
	if err := db.Transition(ctx, running, taskstate.Queued, taskstate.Running, ""); err != nil {
		t.Fatal(err)
	}
	if err := db.Transition(ctx, finished, taskstate.Queued, taskstate.Declined, ""); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		origin string
		want   []int64
	}{
		{"telegram:1", []int64{running, queued}},
		{"telegram:2", []int64{finished + 1}},
		{"", nil},
	}
	for _, tc := range cases {
		tasks, err := db.ActiveTasksByOrigin(ctx, tc.origin)
		if err != nil {
			t.Fatal(err)
		}
		var got []int64
		for _, task := range tasks {
			got = append(got, task.ID)
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("origin %q: tasks %v, want %v", tc.origin, got, tc.want)
		}
	}
}
