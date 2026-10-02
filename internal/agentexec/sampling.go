package agentexec

import (
	"errors"
	"fmt"

	"github.com/samcharles93/ai-sdk/chat"

	protocol "github.com/samcharles93/archie-core/internal/tools/mcp"
)

// DefaultSamplingMaxTokens bounds a server-requested completion when the
// request does not set maxTokens. A sampling request is a delegated
// sub-completion, not a full turn, so one MCP server cannot spend a
// turn-sized output allowance on a single request.
const DefaultSamplingMaxTokens = 4096

// SamplingMessages maps a server's sampling messages onto chat messages. Both
// MCP sampling host paths use it -- the daemon's chat handler and the agent
// worker's task handler -- so the same server is answered the same way
// wherever the client runs. A non-text content block or an unknown role is
// rejected rather than silently dropped: answering from a partial prompt would
// be a wrong answer, not a degraded one.
func SamplingMessages(req protocol.SamplingRequest) ([]chat.Message, error) {
	if len(req.Messages) == 0 {
		return nil, errors.New("mcp sampling: the request has no messages")
	}
	messages := make([]chat.Message, 0, len(req.Messages))
	for i, message := range req.Messages {
		if message.Content.Type != "" && message.Content.Type != "text" {
			return nil, fmt.Errorf("mcp sampling: message %d has unsupported content type %q", i, message.Content.Type)
		}
		var role chat.Role
		switch message.Role {
		case "user":
			role = chat.RoleUser
		case "assistant":
			role = chat.RoleAssistant
		default:
			return nil, fmt.Errorf("mcp sampling: message %d has unsupported role %q", i, message.Role)
		}
		messages = append(messages, chat.Message{Role: role, Content: message.Content.Text})
	}
	return messages, nil
}

// SamplingMaxTokens bounds a delegated completion to the request's own
// maxTokens, falling back to DefaultSamplingMaxTokens, and never past the
// model's own output ceiling (zero when the ceiling is unknown). A
// reasoning-class model gets no bound at all: its provider rejects the
// `max_tokens` parameter the chat-completions provider emits for a non-zero
// bound. Both the daemon's chat path and the agent worker answer sampling
// with this, so the same server gets the same bound on either path.
func SamplingMaxTokens(requested, modelCeiling int, reasoning bool) int {
	maxTokens := requested
	if maxTokens <= 0 {
		maxTokens = DefaultSamplingMaxTokens
	}
	if modelCeiling > 0 && maxTokens > modelCeiling {
		maxTokens = modelCeiling
	}
	if reasoning {
		return 0
	}
	return maxTokens
}
