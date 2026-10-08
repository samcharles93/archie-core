package archiegateway

import (
	"context"
	"fmt"

	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/domain/taskactions"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/gateway"
	"github.com/samcharles93/archie-core/internal/logging"
)

type chatTaskControllerAdapter struct {
	taskByID        func(context.Context, int64) (*workflow.Task, error)
	approve         func(context.Context, *string, taskactions.Actor, int64, taskactions.ActionPayload) error
	cancelExecution func(context.Context, int64, string, string) ([]int64, error)
}

func (a chatTaskControllerAdapter) ChatTaskStatus(ctx context.Context, taskID int64) (gateway.ChatTaskStatus, bool, error) {
	task, err := a.taskByID(ctx, taskID)
	if err != nil {
		return gateway.ChatTaskStatus{}, false, err
	}
	if task == nil {
		return gateway.ChatTaskStatus{}, false, nil
	}
	if !task.IsForgeBacked() {
		return gateway.ChatTaskStatus{Status: task.Status, Identity: task.Identity}, true, nil
	}
	return gateway.ChatTaskStatus{}, false, fmt.Errorf("task %d is not chat-originated", taskID)
}

func (a chatTaskControllerAdapter) ApproveChatTask(ctx context.Context, taskID int64, actor taskactions.Actor) error {
	if a.approve == nil {
		return fmt.Errorf("task approval is unavailable")
	}
	// The chat-bound identity is both the scope the task must belong to and
	// the actor the record names: a chat command has no cross-identity
	// authority, and /approve carries no selection syntax, so the review gate
	// answer posts every offered finding.
	scope := string(actor.Identity)
	return a.approve(ctx, &scope, actor, taskID, taskactions.ActionPayload{})
}

func (a chatTaskControllerAdapter) CancelChatTask(ctx context.Context, taskID int64, reason string) error {
	task, err := a.taskByID(ctx, taskID)
	if err != nil {
		return err
	}
	if task == nil {
		return fmt.Errorf("task %d not found", taskID)
	}
	if task.IsForgeBacked() {
		return fmt.Errorf("task %d is not chat-originated", taskID)
	}
	// An operator declining work lands in the same state whichever surface
	// they used, through the one cancel path: the store records the
	// cancellation -- every non-terminal step of the current attempt plus the
	// execution's own move -- and
	// this write is what a late worker's next step write fails against. This
	// used to record StatusRejected while the dashboard recorded
	// StatusClosedWontDo, so the same decision showed up as two different
	// states -- and StatusRejected, which the PR reconciler uses for "the
	// pull request was closed without merging", stopped meaning one thing.
	if a.cancelExecution == nil {
		return fmt.Errorf("execution cancellation is unavailable")
	}
	_, err = a.cancelExecution(ctx, taskID, reason, workflow.StatusClosedWontDo)
	return err
}

// chatTaskListerAdapter gives the gateway a read view of one identity's tasks.
// gateway deliberately does not import the task store, so the projection
// happens here, as it does for the writer and controller adapters below.
type chatTaskListerAdapter struct {
	tasks func(context.Context, int) ([]workflow.Task, error)
}

func (a chatTaskListerAdapter) ListChatTasks(ctx context.Context, identity string, limit int) ([]gateway.ChatTaskSummary, error) {
	// Over-read before filtering: Tasks applies its limit across the whole
	// table, so asking for exactly `limit` would return fewer than that for
	// this identity whenever another identity's work is more recent.
	rows, err := a.tasks(ctx, limit*taskListOverRead)
	if err != nil {
		return nil, err
	}
	out := make([]gateway.ChatTaskSummary, 0, limit)
	for _, task := range rows {
		if task.Identity != identity {
			continue
		}
		if len(out) >= limit {
			break
		}
		out = append(out, gateway.ChatTaskSummary{
			ID:         task.ID,
			Repo:       task.Owner + "/" + task.Repo,
			Title:      task.Title,
			Status:     task.Status,
			Workflow:   task.Workflow,
			PRNumber:   task.PRNumber,
			Attempt:    task.Attempt,
			ParkReason: task.ParkReason,
			UpdatedAt:  task.UpdatedAt,
		})
	}
	return out, nil
}

// chatTaskLogReaderAdapter gives the gateway a read view of a task's
// persisted log history without importing internal/logging or the task store
// into the gateway package. Each identity's reader is scoped to its own
// tasks: the identity bound at construction is used for authorization, so a
// model cannot read another identity's task logs by passing a different
// identity through the tool input.
type chatTaskLogReaderAdapter struct {
	tasks    func(context.Context, int64) (*workflow.Task, error)
	taskLogs storecontract.TaskLogStore
}

func (a chatTaskLogReaderAdapter) ReadChatTaskLogs(
	ctx context.Context, identity string, taskID int64, attempt int, q gateway.ChatTaskLogQuery,
) (gateway.ChatTaskLogResult, error) {
	task, err := a.tasks(ctx, taskID)
	if err != nil {
		return gateway.ChatTaskLogResult{}, err
	}
	if task == nil {
		return gateway.ChatTaskLogResult{}, fmt.Errorf("task %d not found", taskID)
	}
	// Match the filter chatTaskListerAdapter already applies: a model
	// bound to one identity must not read logs for another identity's
	// tasks, and tasks with no identity (forge-sourced) are not
	// readable through a chat tool at all — they belong to the daemon,
	// not a particular identity. "The empty string MUST NOT retain
	// special meaning".
	if task.Identity != identity {
		return gateway.ChatTaskLogResult{}, fmt.Errorf("task %d belongs to %q, not %q", taskID, task.Identity, identity)
	}

	if attempt <= 0 {
		attempt = task.Attempt
	}
	page, err := a.taskLogs.TaskLog(ctx, taskID, attempt, logging.Query{
		Component: q.Component, Contains: q.Contains, Levels: q.Levels, Since: q.Since, Until: q.Until, Limit: q.Limit, BeforeID: q.AfterID,
	})
	if err != nil {
		return gateway.ChatTaskLogResult{}, err
	}

	entries := make([]gateway.ChatTaskLogEntry, len(page.Entries))
	for i, e := range page.Entries {
		entries[i] = gateway.ChatTaskLogEntry{
			Time:    e.Time,
			Level:   e.Level,
			Message: e.Message,
			Fields:  e.Fields,
		}
	}
	return gateway.ChatTaskLogResult{
		Entries:       entries,
		Attempt:       attempt,
		Truncated:     page.Truncated,
		Cursor:        page.Cursor,
		MoreAvailable: page.MoreAvailable,
	}, nil
}

func configureTaskCommands(
	router *gateway.Router,
	tasks gateway.TaskCreator,
	controller gateway.TaskController,
	lister gateway.ChatTaskLister,
	identity string,
) {
	router.Tasks = tasks
	router.Controller = controller
	router.TaskLister = lister
	router.Identity = identity
}

// taskListOverRead multiplies the requested limit when reading rows that are
// filtered by identity afterwards.
const taskListOverRead = 5
