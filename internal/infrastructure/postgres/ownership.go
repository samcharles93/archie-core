package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrOwned is returned when another process already owns the named role on
// this database.
var ErrOwned = errors.New("postgres: owned by another process")

// Ownership is one process's exclusive claim to serve a role (the State Store,
// the Gateway) against a database. It is a session-level advisory lock held on
// a connection taken out of the pool for the claim's whole life, so a process
// that crashes or loses its connection releases it without cleanup.
type Ownership struct {
	conn *pgxpool.Conn
	name string
	pid  uint32
}

// AcquireOwnership claims name without blocking. A claim another process holds
// comes back as ErrOwned, never as a wait: a second owner must fail to start,
// not queue behind the first.
func AcquireOwnership(ctx context.Context, pool *pgxpool.Pool, name string) (*Ownership, error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: ownership %s: %w", name, err)
	}
	var taken bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock(hashtextextended('archie.' || $1 || '.owner', 0))", name).Scan(&taken); err != nil {
		conn.Release()
		return nil, fmt.Errorf("postgres: ownership %s: %w", name, err)
	}
	if !taken {
		conn.Release()
		return nil, fmt.Errorf("%s: %w", name, ErrOwned)
	}
	return &Ownership{conn: conn, name: name, pid: conn.Conn().PgConn().PID()}, nil
}

// Release drops the claim and returns the connection. A connection that
// cannot unlock is closed rather than pooled, which ends the session and with
// it the lock. Release is idempotent.
func (o *Ownership) Release(ctx context.Context) error {
	if o == nil || o.conn == nil {
		return nil
	}
	conn := o.conn
	o.conn = nil
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_unlock(hashtextextended('archie.' || $1 || '.owner', 0))", o.name); err != nil {
		return errors.Join(fmt.Errorf("postgres: release ownership %s: %w", o.name, err), conn.Hijack().Close(ctx))
	}
	conn.Release()
	return nil
}
