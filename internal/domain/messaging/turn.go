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

// TurnRecord is one chat generation's identity and outcome. ResponseText and
// ToolCalls are kept so a duplicate can be replayed.
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
