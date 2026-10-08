package task

import "context"

// Caller starts a workflow.call callee and reads it back.
type Caller interface {
	// StartCall enqueues the callee for callerTaskID's call step at depth + 1.
	// callKey is the call site's durable identity -- the call step's declared
	// path within the run -- and is required: the same caller and key return
	// the child already started, so a retry of a call site starts one child.
	// It fails if the caller is not running or the depth limit is exceeded.
	StartCall(ctx context.Context, callerTaskID int64, callKey, workflow string, inputs map[string]any) (*Task, error)
	// CallStatus reads one call's callee: its status, latest transition detail
	// and written outputs. A store only answers for a task whose
	// call_parent_task_id is callerTaskID, so a caller reads only its own
	// callees.
	CallStatus(ctx context.Context, callerTaskID, callTaskID int64) (status, detail string, outputs map[string]any, err error)
}
