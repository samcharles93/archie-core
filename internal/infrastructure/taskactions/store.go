// Package taskactions adapts legacy task persistence to operator actions.
package taskactions

import (
	"context"
	"fmt"

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
	return &taskactions.Task{ID: t.ID, Owner: t.Owner, Repo: t.Repo, Identity: t.Identity, Status: t.Status, ParkReason: t.ParkReason, IssueNumber: t.IssueNumber, RetryCount: t.RetryCount, Attempt: t.Attempt, Branch: t.Branch, RetryMode: t.RetryMode, ForgeBacked: t.IsForgeBacked(), ReviewGate: t.ReviewGate, RereviewRounds: t.RereviewRounds}, nil
}

// CancelExecution delegates to the store.
func (s Store) CancelExecution(ctx context.Context, taskID int64, reason, to string) ([]int64, error) {
	canceller, ok := s.TaskStore.(storecontract.ExecutionCanceller)
	if !ok {
		return nil, fmt.Errorf("task store cannot cancel executions")
	}
	return canceller.CancelExecution(ctx, taskID, reason, to)
}

// RespondReviewGate delegates to the store.
func (s Store) RespondReviewGate(ctx context.Context, taskID int64, fromStatus, gate string, rereview bool, maxRounds int) error {
	responder, ok := s.TaskStore.(storecontract.ReviewGateResponder)
	if !ok {
		return fmt.Errorf("task store cannot record a review gate response")
	}
	return responder.RespondReviewGate(ctx, taskID, fromStatus, gate, rereview, maxRounds)
}

func MaxRetries(cfg *config.Holder) func(*taskactions.Task) int {
	return func(t *taskactions.Task) int {
		if cfg == nil {
			return 0
		}
		c := cfg.Get()
		// In multi-identity deployments, repos live under the task's identity.
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
