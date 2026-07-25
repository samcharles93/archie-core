package gateway

import (
	"context"
	"fmt"
	"time"
)

// StoreTaskCreator implements TaskCreator backed by a store interface.
// Chat-spawned tasks use a timestamp-based synthetic issue number to
// avoid colliding with Gitea-issued tasks. The first configured repo
// provides owner/name for the task row.
type StoreTaskCreator struct {
	store storeWriter
	owner string
	repo  string
}

// storeWriter is the write surface StoreTaskCreator needs.
type storeWriter interface {
	EnqueueIssue(ctx context.Context, owner, repo string, number int, title, body, labels string) (bool, error)
}

// NewStoreTaskCreator returns a TaskCreator that enqueues chat-spawned
// tasks via the daemon's store. owner and repo identify the repo the
// task will work against (typically the identity's first repo). When
// empty, /spawn returns a configuration error.
func NewStoreTaskCreator(sw storeWriter, owner, repo string) *StoreTaskCreator {
	return &StoreTaskCreator{store: sw, owner: owner, repo: repo}
}

func (c *StoreTaskCreator) CreateTask(ctx context.Context, title string) (int64, error) {
	if c.owner == "" || c.repo == "" {
		return 0, fmt.Errorf("no repo configured for chat-spawned tasks")
	}
	// Synthetic issue number  --  prevents collisions with real Gitea issues
	// (which are small sequential ints) while staying within the store's
	// existing (owner, repo, number) uniqueness constraint.
	number := int(time.Now().UnixNano())
	_, err := c.store.EnqueueIssue(ctx, c.owner, c.repo, number, title, "", "chat")
	if err != nil {
		return 0, err
	}
	// The store doesn't return the task ID from EnqueueIssue  --  ClaimByIssue
	// gives us back the row. For the chat reply, the synthetic number is
	// all the user needs to reference the task.
	return int64(number), nil
}
