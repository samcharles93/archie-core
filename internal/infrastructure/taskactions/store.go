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
		for _, repo := range c.Repos {
			if repo.Owner == t.Owner && repo.Name == t.Repo {
				return repo.EffectiveMaxRetries(c.MaxRetries)
			}
		}
		return c.MaxRetries
	}
}
