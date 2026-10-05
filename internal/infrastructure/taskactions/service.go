package taskactions

import (
	"context"

	"github.com/samcharles93/archie-core/internal/domain/taskactions"
	"github.com/samcharles93/archie-core/internal/events"
)

// NewService assembles the operator task-action service from its production
// dependencies. Both the dashboard's in-process service (webui) and the
// daemon's NATS responder build through this one constructor so the two
// surfaces cannot diverge.
func NewService(
	store taskactions.Store,
	maxRetries func(*taskactions.Task) int,
	cancel func(int64) bool,
	closeIssue func(context.Context, string, string, int, string) error,
	closePR func(context.Context, string, string, int, string) error,
	removeLogs func(int64) error,
	removeWorktree func(*taskactions.Task) error,
	publish func(events.Event),
	warn func(string, ...any),
) taskactions.Service {
	return taskactions.Service{
		Store:          store,
		MaxRetries:     maxRetries,
		CancelTask:     cancel,
		CloseIssue:     closeIssue,
		ClosePR:        closePR,
		RemoveLogs:     removeLogs,
		RemoveWorktree: removeWorktree,
		Publish:        publish,
		Warn:           warn,
	}
}
