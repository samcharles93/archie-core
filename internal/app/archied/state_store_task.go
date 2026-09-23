package archied

import (
	"context"

	"github.com/samcharles93/archie-core/internal/store"
)

// openTaskStore claims ownership of the task database and opens the production
// task store. The ownership claim is what makes "the State Store is stopped" a
// fact an offline recovery command can check, and a second State Store on the
// same database fails here. It is a separate composition file from openEDAStore
// so L1 can replace openProductionTaskStore (and the ownership claim, per the
// serve-ownership decision) here without colliding with L2's edastore port.
func (b *boot) openTaskStore(ctx context.Context, path string) error {
	ownership, err := store.AcquireOwnership(path)
	if err != nil {
		b.log.Error("claim state store ownership", "err", err)
		return err
	}
	b.addCleanup(func() {
		if err := ownership.Release(); err != nil {
			b.log.Error("release state store ownership", "err", err)
		}
	})

	st, err := openProductionTaskStore(ctx, path)
	if err != nil {
		b.log.Error("open state store", "err", err)
		return err
	}
	b.st = st
	b.addCleanup(func() {
		if err := st.Close(); err != nil {
			b.log.Error("close state store", "err", err)
		}
	})
	return nil
}
