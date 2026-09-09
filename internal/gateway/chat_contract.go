package gateway

import (
	"context"

	"github.com/samcharles93/archie-core/internal/domain/messaging"
	"github.com/samcharles93/archie-core/internal/taskstate"
)

// ChatContract is the conversational boundary consumed by channel frontends.
// Results are snapshots: callers must not rely on shared object identity.
type ChatContract interface { //nolint:interfacebloat // wire contract intentionally covers the complete Gateway facade
	Snapshot(context.Context) (ChatSnapshot, error)
	GetSession(context.Context, string) (SessionContext, bool, error)
	RecentMessages(context.Context, string, int) ([]messaging.Message, error)
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
	// may only act on its own tasks.
	ApplyTaskAction(context.Context, string, int64, taskstate.Action) (TaskActionResult, error)
	// ApplyOperatorTaskAction applies an action on behalf of an authenticated
	// dashboard operator, who acts across identities. It is a separate method
	// rather than an empty identity because "" is a real identity in a
	// single-identity deployment (see chatTaskProfiles in the daemon).
	ApplyOperatorTaskAction(context.Context, int64, taskstate.Action) (TaskActionResult, error)
}

type ChatSnapshot struct {
	Sessions              []SessionContext
	Models                []string
	ModelsByProvider      map[string][]string
	Providers             []string
	ActiveModel           string
	ActiveProvider        string
	Personas              []string
	ActivePersonas        map[string]string
	RestartAvailable      bool
	CancellationAvailable bool
	PersonasAvailable     bool
}

type ChatReply struct {
	Text      string
	SessionID string
}

type ChatCancellation struct {
	Cancelled bool
	Dropped   int
}

// ChatEvent contains values only; Text holds the error message for error events.
type ChatEvent struct {
	Kind      string
	Text      string
	SessionID string
	Tool      ToolCallEvent
	Media     MediaEvent
}
