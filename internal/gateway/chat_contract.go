package gateway

import "context"

// ChatContract is the conversational boundary consumed by channel frontends.
// Results are snapshots: callers must not rely on shared object identity.
type ChatContract interface {
	Snapshot(context.Context) (ChatSnapshot, error)
	GetSession(context.Context, string) (SessionContext, bool, error)
	RecentMessages(context.Context, string, int) ([]Message, error)
	RecentTurns(context.Context, string, int) ([]TurnRecord, error)
	Route(context.Context, Message) (ChatReply, error)
	// Stream emits started, delta/tool/media, then done or error, in order.
	// Callers must drain the stream or cancel the context. Cancellation closes it.
	Stream(context.Context, Message) (<-chan ChatEvent, error)
	Cancel(context.Context, string) (ChatCancellation, error)
	SetPersona(context.Context, string, string) (bool, error)
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
