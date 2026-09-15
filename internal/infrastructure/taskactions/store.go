// Package taskactions adapts legacy task persistence to operator actions.
package taskactions

import (
	"context"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/domain/taskactions"
)

type Store struct{ storecontract.TaskStore }

func (s Store) TaskByID(ctx context.Context, id int64) (*taskactions.Task, error) {
	t, err := s.TaskStore.TaskByID(ctx, id)
	if err != nil || t == nil {
		return nil, err
	}
	return &taskactions.Task{ID: t.ID, Owner: t.Owner, Repo: t.Repo, Identity: t.Identity, Status: t.Status, Stage: t.Stage, ParkReason: t.ParkReason, IssueNumber: t.IssueNumber, RetryCount: t.RetryCount, ForgeBacked: t.IsForgeBacked()}, nil
}

func MaxRetries(cfg *config.Holder) func(*taskactions.Task) int {
	return func(t *taskactions.Task) int {
		if cfg == nil {
			return 0
		}
		c := cfg.Get()
		// A multi-identity deployment declares its repositories under
		// [[identities.repos]] and has no global [[repos]] at all, so the
		// task's owning identity is the only place a per-repo override can
		// live. Without this lookup the override is never matched and the
		// task dies at the global cap instead of the configured one.
		if t.Identity != "" {
			for _, identity := range c.Identities {
				if identity.Name != t.Identity {
					continue
				}
				if n, ok := repoMaxRetries(identity.Repos, t, c.MaxRetries); ok {
					return n
				}
				break
			}
		}
		if n, ok := repoMaxRetries(c.Repos, t, c.MaxRetries); ok {
			return n
		}
		return c.MaxRetries
	}
}

// repoMaxRetries returns the effective cap for the task's repository within one
// repository list, and whether that list knows the repository at all.
func repoMaxRetries(repos []config.Repo, t *taskactions.Task, global int) (int, bool) {
	for _, repo := range repos {
		if repo.Owner == t.Owner && repo.Name == t.Repo {
			return repo.EffectiveMaxRetries(global), true
		}
	}
	return 0, false
}
