package postgres

import (
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/postgresdb"
)

// TestEventInsertsCommitInIDOrder pins the event-append lock: an insert
// waits while another transaction holds an uncommitted event, so a reader
// paging by (at, id) never sees a later id commit before an earlier one.
func TestEventInsertsCommitInIDOrder(t *testing.T) {
	pool, _ := migrated(t)
	st := New(pool)

	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	firstID, err := insertEventQ(t.Context(), postgresdb.New(tx), events.Event{Kind: "first"})
	if err != nil {
		t.Fatalf("insert in tx: %v", err)
	}

	type result struct {
		id  int64
		err error
	}
	done := make(chan result, 1)
	go func() {
		id, err := st.InsertEvent(t.Context(), events.Event{Kind: "second"})
		done <- result{id, err}
	}()

	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		if err := pool.QueryRow(t.Context(),
			"SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND NOT granted").Scan(&waiting); err != nil {
			t.Fatalf("pg_locks: %v", err)
		}
		if waiting > 0 {
			break
		}
		select {
		case r := <-done:
			t.Fatalf("second insert returned (id %d, err %v) while the first was uncommitted; want it to wait", r.id, r.err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("second insert never waited on the event-append lock")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := tx.Commit(t.Context()); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	r := <-done
	if r.err != nil {
		t.Fatalf("second insert: %v", r.err)
	}
	if r.id <= firstID {
		t.Fatalf("second id %d, want greater than first id %d", r.id, firstID)
	}
}
