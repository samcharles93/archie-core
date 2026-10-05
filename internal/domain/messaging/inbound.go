package messaging

import (
	"context"
	"time"
)

// Inbound is a received message plus transport context that is not
// persisted. Message.Role is always RoleUser; an empty ID is derived from
// SourceID.
type Inbound struct {
	Message Message
	// Page is the dashboard route the operator is on when they sent
	// this message. Transport-only: it reaches the system prompt and
	// is never persisted. Empty for non-web channels.
	Page string
	// Platform is the channel that carried the message. Not persisted. Empty
	// falls back to the gateway's name.
	Platform string

	// Media are the message's attachments with their bytes. Sent to the Gateway
	// but never persisted.
	Media []MediaAttachment
}

// SpawnRequest is a chat-originated task creation request. Repo and
// Workflow are optional  --  empty means "the daemon's configured
// default for this identity".
type SpawnRequest struct {
	Title string
	Body  string // operator instructions carried into the admitted task
	Repo  string // "owner/name"; empty = identity's default repo
	// Workflow may name a workflow whose interface declares inputs
	// (pr-review's pr_number, for instance). Inputs assigns them; a
	// workflow that declares none ignores it.
	Workflow string // empty = daemon's default workflow routing
	Inputs   map[string]any
	Identity string // the identity spawning this task; propagated from Router.Identity
}

// TaskCreator creates a chat task and returns its database ID. Nil makes
// /spawn report "not configured".
type TaskCreator interface {
	CreateTask(ctx context.Context, req SpawnRequest) (taskID int64, err error)
}

// DangerousCommandAuthority is the sandbox-owned authority for operations
// that can destroy process or filesystem state. Adapters only request and
// present approval; they never implement these operations themselves.
type DangerousCommandAuthority interface {
	StopProcess(context.Context, string) error
	Rollback(context.Context, int) (string, error)
	ListCheckpoints(context.Context) ([]CheckpointInfo, error)
}

// CheckpointInfo describes a saved sandbox filesystem checkpoint.
type CheckpointInfo struct {
	Number    int
	Timestamp time.Time
	Label     string
	Size      string
}
