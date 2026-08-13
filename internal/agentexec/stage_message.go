package agentexec

import "context"

// StageMessage is one decoded single-stage request together with the transport
// operations that complete its delivery. Infrastructure owns the wire payload,
// reply inbox, and acknowledgement mechanism; the worker only sees typed data.
type StageMessage interface {
	Request() (AgentRequestMessage, error)
	Respond(context.Context, *AgentResponseEnvelope) error
	Ack() error
	Nak() error
	LogAttributes() []any
}

// StageConsumer receives decoded single-stage requests.
type StageConsumer interface {
	FetchStage(context.Context) (StageMessage, error)
}
