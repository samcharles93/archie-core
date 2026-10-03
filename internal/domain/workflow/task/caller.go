package task

import "context"

// Caller starts a workflow.call callee and reads it back.
type Caller interface {
	// StartCall enqueues the callee for callerTaskID's call step at depth + 1.
	// It fails if the caller is not running or the depth limit is exceeded.
	StartCall(ctx context.Context, callerTaskID int64, workflow string, inputs map[string]any) (*Task, error)
	// CallStatus reads one call's callee: its status, latest transition detail
	// and written outputs. A store only answers for a task whose
	// call_parent_task_id is callerTaskID, so a caller reads only its own
	// callees.
	CallStatus(ctx context.Context, callerTaskID, callTaskID int64) (status, detail string, outputs map[string]any, err error)
}
