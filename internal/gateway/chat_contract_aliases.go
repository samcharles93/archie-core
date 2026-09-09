// Wire-contract aliases. The chat contract types moved to
// internal/domain/messaging (archie-core-8cda.5.6) so the webui and
// archie-ui processes can hold the contract without linking the gateway
// runtime (and, transitively, its SQLite session store). internal/gateway
// keeps these aliases so daemon-side callers and channel adapters are
// unaffected.
package gateway

import "github.com/samcharles93/archie-core/internal/domain/messaging"

type (
	// ChatContract is the conversational boundary consumed by channel frontends.
	ChatContract = messaging.ChatContract
	// ChatTaskActionContract is the operator task mutation capability.
	ChatTaskActionContract = messaging.ChatTaskActionContract
	// Inbound is a message arriving from a channel.
	Inbound = messaging.Inbound
	// SpawnRequest is a chat-originated task creation request.
	SpawnRequest = messaging.SpawnRequest
	// TaskCreator creates a native task from a chat command.
	TaskCreator = messaging.TaskCreator
	// ChatSnapshot is the full chat state a dashboard renders in one read.
	ChatSnapshot = messaging.ChatSnapshot
	// ChatReply is the blocking reply from Route.
	ChatReply = messaging.ChatReply
	// ChatCancellation is what Cancel returns.
	ChatCancellation = messaging.ChatCancellation
	// ChatEvent is one streamed chat event.
	ChatEvent = messaging.ChatEvent
	// ToolCallEvent reports one completed tool invocation within a turn.
	ToolCallEvent = messaging.ToolCallEvent
	// MediaEvent reports one media attachment produced during a turn.
	MediaEvent = messaging.MediaEvent
	// MediaAttachment describes a file attached to a message.
	MediaAttachment = messaging.MediaAttachment
	// TurnStream receives a turn's output as it is produced.
	TurnStream = messaging.TurnStream
	// TurnStatus is the durable lifecycle state of one chat generation.
	TurnStatus = messaging.TurnStatus
	// TurnRecord is the durable identity and outcome projection for one chat generation.
	TurnRecord = messaging.TurnRecord
	// SessionSource identifies the platform, bot identity, and conversation a session belongs to.
	SessionSource = messaging.SessionSource
	// SessionContext holds the full metadata for an active gateway session.
	SessionContext = messaging.SessionContext
	// SessionStore persists gateway session metadata and conversation history.
	SessionStore = messaging.SessionStore
	// SessionLifecycle manages session CRUD and listing.
	SessionLifecycle = messaging.SessionLifecycle
	// MessageHistory manages conversation messages within a session.
	MessageHistory = messaging.MessageHistory
	// MessageQuery is one page request against a session's message history.
	MessageQuery = messaging.MessageQuery
	// MessagePage is one page of search results.
	MessagePage = messaging.MessagePage
	// CommandSpec describes a local command for adapter-provided discovery.
	CommandSpec = messaging.CommandSpec
	// DangerousCommandAuthority is the sandbox-owned authority for destructive operations.
	DangerousCommandAuthority = messaging.DangerousCommandAuthority
	// CheckpointInfo describes a saved sandbox filesystem checkpoint.
	CheckpointInfo = messaging.CheckpointInfo
	// TaskActionResult is what task_action returns.
	TaskActionResult = messaging.TaskActionResult
	// DashboardNavigateResult is what dashboard_navigate returns.
	DashboardNavigateResult = messaging.DashboardNavigateResult
)

// Turn lifecycle states.
const (
	TurnStatusAccepted  = messaging.TurnStatusAccepted
	TurnStatusRunning   = messaging.TurnStatusRunning
	TurnStatusPartial   = messaging.TurnStatusPartial
	TurnStatusCompleted = messaging.TurnStatusCompleted
	TurnStatusFailed    = messaging.TurnStatusFailed
	TurnStatusCancelled = messaging.TurnStatusCancelled
)

// Page-size bounds for MessageQuery.
const (
	DefaultMessagePageSize     = messaging.DefaultMessagePageSize
	MaxMessagePageSize         = messaging.MaxMessagePageSize
	MaxMessageSearchQueryBytes = messaging.MaxMessageSearchQueryBytes
	MaxMessageSearchQueryTerms = messaging.MaxMessageSearchQueryTerms
)

// LocalCommands returns the command names Route answers from local state.
func LocalCommands() []string { return messaging.LocalCommands() }

// LocalCommandSpecs returns a copy safe for adapters to enrich with their
// optional capabilities.
func LocalCommandSpecs() []CommandSpec { return messaging.LocalCommandSpecs() }

// RoleForSender classifies a message sender: a message sent by the
// session's bot is the assistant's, anything else is the user's.
func RoleForSender(sender, botUser string) messaging.Role {
	return messaging.RoleForSender(sender, botUser)
}
