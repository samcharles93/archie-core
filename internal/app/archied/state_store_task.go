package archied

import (
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres"
)

// openTaskStore serves the task store from the State Store's Postgres pool.
// Serve ownership is the pool's claim (openStateStorePool), so a second State
// Store on the same database already failed before this runs, and the pool's
// cleanup closes what this store reads through.
func (b *boot) openTaskStore() {
	b.st = postgres.New(b.pg)
}
