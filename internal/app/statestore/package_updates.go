package statestore

import (
	"context"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/storepkg"
)

// packageUpdateInterval is how often auto packages are checked against the
// catalogue. Catalogue releases are infrequent, so hours is enough.
const packageUpdateInterval = 6 * time.Hour

// autoUpdatePackages updates auto packages now and then every interval until
// ctx ends. A failed tick is logged and retried on the next one.
func (b *server) autoUpdatePackages(ctx context.Context, packages storepkg.Manager) {
	if packages == nil {
		return
	}
	ticker := time.NewTicker(packageUpdateInterval)
	defer ticker.Stop()
	for {
		if err := packages.UpdateAuto(ctx); err != nil && ctx.Err() == nil {
			b.log.Error("auto-update packages", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
