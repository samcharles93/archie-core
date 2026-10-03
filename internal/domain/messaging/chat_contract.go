package messaging

import (
	"context"

	"github.com/samcharles93/archie-core/internal/domain/taskactions"
	"github.com/samcharles93/archie-core/internal/taskstate"
)

// ChatContract is the chat boundary channel frontends call. Results are
// snapshots.
type ChatContract interface { //nolint:interfacebloat // wire contract intentionally covers the complete Gateway facade
	Snapshot(context.Context) (ChatSnapshot, error)
	GetSession(context.Context, string) (SessionContext, bool, error)
	RecentMessages(context.Context, string, int) ([]Message, error)
	RecentTurns(context.Context, string, int) ([]TurnRecord, error)
	Route(context.Context, Inbound) (ChatReply, error)
	// Stream emits started, delta/tool/media, then done or error, in order.
	// Callers must drain the stream or cancel the context. Cancellation closes it.
	Stream(context.Context, Inbound) (<-chan ChatEvent, error)
	Cancel(context.Context, string) (ChatCancellation, error)
	SetPersona(context.Context, string, string) (bool, error)
	ChatTaskActionContract
}

// ChatTaskActionContract is the operator task mutation capability exposed by
// the Gateway. It is separate so read/turn consumers do not need to model it.
type ChatTaskActionContract interface {
	// ApplyTaskAction applies an action on behalf of a chat identity, which
	// may only act on its own tasks. res carries the review gate answer
	// payload; the chat surface has no instruction or selection syntax, so it
	// is the zero value there.
	ApplyTaskAction(context.Context, string, int64, taskstate.Action, taskactions.ActionPayload) (TaskActionResult, error)
	// ApplyOperatorTaskAction applies an action for an authenticated dashboard
	// operator acting across identities. A zero actor is recorded unattributed.
	// res carries the review gate answer.
	ApplyOperatorTaskAction(context.Context, taskactions.Actor, int64, taskstate.Action, taskactions.ActionPayload) (TaskActionResult, error)
}

// TaskActionResult is what task_action returns.
type TaskActionResult struct {
	TaskID  int64  `json:"task_id"`
	Action  string `json:"action"`
	Message string `json:"message"`
}
