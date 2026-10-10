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

// NewPending returns a chain with no engine: Run loads it, retrying until the
// store answers. A chain that is not ready refuses every request, so a process
// that starts before its State Store is closed rather than opened.
func NewPending(load func(context.Context) ([]access.Policy, error), log *slog.Logger) *Live {
	return &Live{load: load, log: log}
}

// Ready reports whether an engine has loaded.
func (l *Live) Ready() bool { return l.engine.Load() != nil }

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

// Run reloads on every interval until ctx ends. A chain with no engine yet
// tries at once and keeps trying, so the first load does not wait an interval
// after the State Store returns.
func (l *Live) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		l.reload(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (l *Live) reload(ctx context.Context) {
	err := l.Reload(ctx)
	if err == nil || ctx.Err() != nil {
		return
	}
	if l.Ready() {
		l.log.Warn("access policies not reloaded; keeping the last chain", "err", err)
		return
	}
	l.log.Warn("access policies not loaded; requests are refused until they load", "err", err)
}

func (l *Live) Authorize(p access.Principal, a access.Action, r access.Resource, c access.Context) access.Decision {
	engine := l.engine.Load()
	if engine == nil {
		return notLoaded()
	}
	return engine.Authorize(p, a, r, c)
}

func (l *Live) AuthorizeDelivery(orgID org.OrgID, sourcePath, addr string) access.Decision {
	engine := l.engine.Load()
	if engine == nil {
		return notLoaded()
	}
	return engine.AuthorizeDelivery(orgID, sourcePath, addr)
}

// notLoaded is the refusal of a chain with no engine: nothing was evaluated.
func notLoaded() access.Decision {
	decision := access.DeniedAt(access.LevelInstance, nil)
	decision.Err = access.ErrChainUnavailable
	return decision
}

// Problems reports the current engine's invalid policies.
func (l *Live) Problems() []Problem {
	engine := l.engine.Load()
	if engine == nil {
		return nil
	}
	return engine.Problems()
}
