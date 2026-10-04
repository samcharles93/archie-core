package access

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/access"
	"github.com/samcharles93/archie-core/internal/domain/org"
)

// Live is an engine rebuilt from the stored policies, so a policy edited in
// the State Store reaches every process that authorizes without a restart.
// A failed reload keeps the last engine.
type Live struct {
	load   func(context.Context) ([]access.Policy, error)
	log    *slog.Logger
	engine atomic.Pointer[Engine]
}

// NewLive builds the first engine from load.
func NewLive(ctx context.Context, load func(context.Context) ([]access.Policy, error), log *slog.Logger) (*Live, error) {
	l := &Live{load: load, log: log}
	if err := l.Reload(ctx); err != nil {
		return nil, err
	}
	return l, nil
}

// Reload rebuilds the engine from the store now.
func (l *Live) Reload(ctx context.Context) error {
	stored, err := l.load(ctx)
	if err != nil {
		return err
	}
	engine, err := New(stored)
	if err != nil {
		return err
	}
	for _, problem := range engine.Problems() {
		l.log.Error("stored access policy is invalid and denies its level",
			"policy", problem.Policy.ID, "level", problem.Policy.Level, "err", problem.Err)
	}
	l.engine.Store(engine)
	return nil
}

// Run reloads on every interval until ctx ends.
func (l *Live) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := l.Reload(ctx); err != nil && ctx.Err() == nil {
				l.log.Warn("access policies not reloaded; keeping the last chain", "err", err)
			}
		}
	}
}

func (l *Live) Authorize(p access.Principal, a access.Action, r access.Resource, c access.Context) access.Decision {
	return l.engine.Load().Authorize(p, a, r, c)
}

func (l *Live) AuthorizeDelivery(orgID org.OrgID, sourcePath, addr string) access.Decision {
	return l.engine.Load().AuthorizeDelivery(orgID, sourcePath, addr)
}

// Problems reports the current engine's invalid policies.
func (l *Live) Problems() []Problem { return l.engine.Load().Problems() }
