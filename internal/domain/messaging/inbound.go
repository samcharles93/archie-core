package messaging

import (
	"context"
	"time"
)

// Inbound is a message arriving from a channel together with the
// transport context that is not part of the canonical record.
//
// Message is the record itself, exactly as it will be persisted. Channels
// construct it directly: ConversationID addresses the chat (a Telegram
// chat ID and topic thread, an email recipient, a webhook path), Sender is
// the channel-native attribution, and Role is always RoleUser --
// a channel only ever carries what a person said. ID is left empty for a
// newly received message so the store derives one from SourceID, and
// honoured when set, which is what lets a read-modify-write of a history
// keep its identities rather than minting new ones.
type Inbound struct {
	Message Message
	// Page is the dashboard route the operator is on when they sent
	// this message. Transport-only: it reaches the system prompt and
	// is never persisted. Empty for non-web channels.
	Page string
}

// SpawnRequest is a chat-originated task creation request. Repo and
// Workflow are optional  --  empty means "the daemon's configured
// default for this identity".
type SpawnRequest struct {
	Title    string
	Body     string // operator instructions carried into the admitted task
	Repo     string // "owner/name"; empty = identity's default repo
	Workflow string // empty = daemon's default workflow routing
	Identity string // the identity spawning this task; propagated from Router.Identity
}

// TaskCreator creates a native (non-forge-backed) task from a chat
// command. The daemon supplies an implementation backed by the store.
// When nil on a Router, /spawn returns "not configured". CreateTask
// must return the task's real, durable database ID  --  never a
// synthetic or fabricated value.
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
