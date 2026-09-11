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
		b.removeTaskLogs,
		b.publishEvent,
		b.log.Warn,
	)
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

func (b *boot) removeTaskLogs(id int64) error {
	if b.taskLogs == nil {
		return nil
	}
	return b.taskLogs.Remove(id)
}

func (b *boot) publishEvent(e events.Event) {
	if b.bus != nil {
		b.bus.Publish(e)
	}
}
