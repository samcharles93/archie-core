package servicekit

import (
	"context"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/presence"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/releaseupdate"
)

// RunningVersions reports the daemon version archied's own presence record
// carries, so an update report is checked against what archied compiled in
// rather than this process's build. A down or unreachable daemon, or a
// missing store, leaves the daemon unverified. The agent stays unverified:
// only archied observes it.
func RunningVersions(ctx context.Context, store storecontract.PresenceStore, timeout time.Duration) func() map[string]string {
	return func() map[string]string {
		if store == nil {
			return nil
		}
		if timeout <= 0 {
			timeout = 5 * time.Second
		}
		callCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		records, err := store.ListPresence(callCtx)
		if err != nil {
			return nil
		}
		for _, service := range presence.Mesh(records, time.Now()) {
			if service.Service == presence.Daemon && service.State != presence.StateDown {
				return map[string]string{releaseupdate.ComponentDaemon: service.Version}
			}
		}
		return nil
	}
}
