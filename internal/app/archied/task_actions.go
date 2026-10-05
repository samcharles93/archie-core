package archied

import (
	"context"
	"fmt"

	"github.com/samcharles93/archie-core/internal/domain/taskactions"
	"github.com/samcharles93/archie-core/internal/events"
	taskactionstore "github.com/samcharles93/archie-core/internal/infrastructure/taskactions"
)

// taskActions assembles the daemon-owned operator action service. The daemon
// retains execution cancellation, retry policy, forge closure and event
// ownership; the dashboard's webui service and the NATS responder both build
// through the same constructor so the surfaces cannot diverge.
func (b *boot) taskActions() taskactions.Service {
	return taskactionstore.NewService(
		taskactionstore.Store{TaskStore: b.stateStore},
		taskactionstore.MaxRetries(b.cfgHolder),
		b.cancelTask,
		b.closeIssue,
		b.closePR,
		b.mergePR,
		b.removeTaskLogs,
		b.removeTaskWorktree,
		b.publishEvent,
		b.log.Warn,
	)
}

// withdrawIssue declines the task an issue queued, if it has one.
func (b *boot) withdrawIssue(ctx context.Context, owner, repo string, number int, reason string) error {
	task, err := b.stateStore.TaskByIssue(ctx, owner, repo, number)
	if err != nil || task == nil {
		return err
	}
	return b.taskActions().Withdraw(ctx, task.ID, reason)
}

func (b *boot) cancelTask(id int64) bool {
	if b.d == nil {
		return false
	}
	return b.d.CancelTask(id)
}

func (b *boot) closeIssue(ctx context.Context, owner, repo string, number int, comment string) error {
	if b.forgeClient == nil {
		return fmt.Errorf("forge is not configured")
	}
	return b.forgeClient.CloseIssue(ctx, owner, repo, number, comment)
}

func (b *boot) closePR(ctx context.Context, owner, repo string, number int, comment string) error {
	if b.forgeClient == nil {
		return fmt.Errorf("forge is not configured")
	}
	return b.forgeClient.ClosePR(ctx, owner, repo, number, comment)
}

func (b *boot) mergePR(ctx context.Context, owner, repo string, number int) error {
	if b.forgeClient == nil {
		return fmt.Errorf("forge is not configured")
	}
	return b.forgeClient.MergePR(ctx, owner, repo, number)
}

func (b *boot) removeTaskLogs(id int64) error {
	if b.taskLogs == nil {
		return nil
	}
	return b.taskLogs.Remove(id)
}

func (b *boot) removeTaskWorktree(task *taskactions.Task) error {
	if b.d == nil {
		return nil
	}
	return b.d.RemoveWorktree(task.Owner, task.Repo, task.Identity, task.IssueNumber)
}

func (b *boot) publishEvent(e events.Event) {
	if b.bus != nil {
		b.bus.Publish(e)
	}
}
