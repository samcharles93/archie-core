package task

import "context"

// Caller is what a workflow.call step uses to start a callee run and read it
// back while waiting. It is the consumer-owned contract the workflow engine
// holds (the same shape as Store), satisfied by *staterpc.Client for an
// agent-container run: the callee is a first-class task the State Store
// derives from the caller's row (docs/prds/workflow-calls.md).
type Caller interface {
	// StartCall enqueues the callee task for callerTaskID's workflow.call
	// step: org, workspace, identity, owner, repo and issue number are
	// derived from the caller's row, the callee carries the call's inputs,
	// and the callee's depth is the caller's + 1. The store refuses a
	// caller that is not running and a call past the depth limit.
	StartCall(ctx context.Context, callerTaskID int64, workflow string, inputs map[string]any) (*Task, error)
	// CallStatus reads one call's callee: its status and latest transition
	// detail. A store only answers for a task whose call_parent_task_id is
	// callerTaskID, so a caller reads only its own callees.
	CallStatus(ctx context.Context, callerTaskID, callTaskID int64) (status, detail string, err error)
}
