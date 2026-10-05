package modelloop

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

// SamplingMessages converts sampling messages to chat messages. Non-text
// content and unknown roles are errors.
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

// SamplingMaxTokens returns the request's maxTokens or the default, capped at
// the model's ceiling. The ai-sdk runtime picks max_completion_tokens or
// max_tokens from the model's catalog metadata, so there is no reasoning
// branch here.
func SamplingMaxTokens(requested, modelCeiling int) int {
	maxTokens := requested
	if maxTokens <= 0 {
		maxTokens = DefaultSamplingMaxTokens
	}
	if modelCeiling > 0 && maxTokens > modelCeiling {
		maxTokens = modelCeiling
	}
	return maxTokens
}
