package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/nats-io/nats.go"

	"github.com/samcharles93/archie-core/internal/container"
	"github.com/samcharles93/archie-core/internal/taskrun"
)

const (
	// taskRunSubjectWildcard matches full-task handoffs (archie.taskrun.<id>).
	// A queue group means only one archie-agent instance answers each
	// message, whether deployed as a shared worker pool (today) or spawned
	// one-per-task (the sandboxed container target).
	taskRunSubjectWildcard = "archie.taskrun.>"
	taskRunQueueGroup      = "archie-taskrun-workers"
)

type taskRunSubscriber interface {
	Subscribe(subject string, handler nats.MsgHandler) (*nats.Subscription, error)
	QueueSubscribe(subject, queue string, handler nats.MsgHandler) (*nats.Subscription, error)
}

// subscribeTaskRuns prefers a dedicated per-task subscription over the
// shared queue group whenever this container was spawned for a specific
// task. taskID/hasTaskID come from bootTaskID (.git/task.json present at the
// worktree mount) -- the caller resolves it once, since it also decides
// whether to attach a per-task log publisher to log before this is called.
func subscribeTaskRuns(ctx context.Context, nc *nats.Conn, log *slog.Logger, taskID int64, hasTaskID bool) (*nats.Subscription, error) {
	return subscribeTaskRunsWith(nc, log, taskID, hasTaskID, func(msg *nats.Msg) {
		handleTaskRun(ctx, msg, nc, log)
	})
}

func subscribeTaskRunsWith(
	subscriber taskRunSubscriber,
	log *slog.Logger,
	taskID int64,
	hasTaskID bool,
	handler nats.MsgHandler,
) (*nats.Subscription, error) {
	if hasTaskID {
		log.Info("taskrun: dedicated per-task subscription", "task", taskID)
		return subscriber.Subscribe(taskrun.SubjectForTask(taskID), handler)
	}
	log.Info("taskrun: shared queue-group subscription (no .git/task.json found)")
	return subscriber.QueueSubscribe(taskRunSubjectWildcard, taskRunQueueGroup, handler)
}

// bootTaskID reads the boot-time task.json brief the daemon writes into
// the worktree before container acquire (container.WriteTaskJSON) and
// returns its ID. It lives under .git so the agent's own commit can't sweep
// it onto the task branch. Returns (0, false) when the file is absent,
// unparseable, or has no ID  --  any of which mean "shared pool mode, no
// dedicated task."
func bootTaskID(mountDir string, log *slog.Logger) (int64, bool) {
	data, err := os.ReadFile(filepath.Join(mountDir, ".git", "task.json"))
	if err != nil {
		return 0, false
	}
	var payload container.TaskPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		log.Warn("task.json present but unparseable  --  falling back to shared queue subscription", "err", err)
		return 0, false
	}
	if payload.ID <= 0 {
		return 0, false
	}
	return payload.ID, true
}
