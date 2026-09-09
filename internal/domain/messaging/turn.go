package messaging

import "time"

// TurnStatus is the durable lifecycle state of one chat generation.
type TurnStatus string

const (
	TurnStatusAccepted  TurnStatus = "accepted"
	TurnStatusRunning   TurnStatus = "running"
	TurnStatusPartial   TurnStatus = "partial"
	TurnStatusCompleted TurnStatus = "completed"
	TurnStatusFailed    TurnStatus = "failed"
	TurnStatusCancelled TurnStatus = "cancelled"
)

// TurnRecord is the durable identity and outcome projection for one chat
// generation. ResponseText is retained so a completed duplicate can replay
// without depending on the bounded model-context history window. ToolCalls
// is retained for the same reason and saved in the same call as
// ResponseText, so a completed-duplicate replay (NATS redelivery, restart
// RecoverTurns redelivery) can narrate the tool activity that produced the
// answer instead of showing a tool-less duplicate beside the original.
type TurnRecord struct {
	TurnID             string
	SessionID          string
	SourceID           string
	Status             TurnStatus
	Attempt            int
	OwnerID            string
	InputMessageID     string
	AssistantMessageID string
	PartialText        string
	ResponseText       string
	ToolCalls          []ToolCallEvent
	Error              string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}
