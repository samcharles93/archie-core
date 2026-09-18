package workintake

import (
	"context"
	"sync/atomic"

	workflowtask "github.com/samcharles93/archie-core/internal/domain/workflow/task"
)

// ReviewTaskLookup is the authorization query review reactions require.
type ReviewTaskLookup interface {
	OpenTaskByPR(ctx context.Context, owner, repo string, number int) (*workflowtask.Task, error)
}

// ReviewTaskResolver admits only reactions for live Archie-owned pull requests.
type ReviewTaskResolver struct {
	Tasks   ReviewTaskLookup
	dropped atomic.Int64
}

// Resolve returns the live task that owns reaction's pull request.
func (r *ReviewTaskResolver) Resolve(ctx context.Context, reaction ReviewCommentEnvelope) (*workflowtask.Task, error) {
	task, err := r.Tasks.OpenTaskByPR(ctx, reaction.Owner, reaction.Repo, reaction.PRNumber)
	if err != nil {
		return nil, err
	}
	if task == nil {
		r.dropped.Add(1)
	}
	return task, nil
}

// Dropped reports how many reactions did not resolve to a live owned task.
func (r *ReviewTaskResolver) Dropped() int64 { return r.dropped.Load() }
