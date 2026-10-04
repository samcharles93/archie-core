package archied

import (
	"context"
	"fmt"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/forgerpc"
	agentnats "github.com/samcharles93/archie-core/internal/infrastructure/agenttransport/nats"
	"github.com/samcharles93/archie-core/internal/infrastructure/eventbus/nats"
	"github.com/samcharles93/archie-core/internal/worktreerpc"
)

// taskBroker opens a task's own broker subjects to its run credential: its
// handoff, its logs and events, and its identity's forge and push servers.
type taskBroker struct {
	runs interface {
		TaskForCredential(ctx context.Context, token string) (*workflow.Task, error)
	}
}

func (b taskBroker) TaskSubjects(ctx context.Context, taskID int64, credential string) (nats.TaskSubjects, error) {
	task, err := b.runs.TaskForCredential(ctx, credential)
	if err != nil {
		return nats.TaskSubjects{}, err
	}
	if task.ID != taskID {
		return nats.TaskSubjects{}, fmt.Errorf("run credential is for task %d, not %d", task.ID, taskID)
	}
	publish := append([]string{
		agentexec.SubjectForSystem(taskID),
		agentexec.SubjectForEvents(taskID),
		worktreerpc.SubjectFor(task.Identity, worktreerpc.SubjectPush),
	}, forgerpc.Subjects(task.Identity)...)
	return nats.TaskSubjects{Publish: publish, Subscribe: []string{agentnats.SubjectForTask(taskID)}}, nil
}
