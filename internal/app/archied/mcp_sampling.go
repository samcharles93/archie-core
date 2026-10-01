package archied

import (
	"context"
	"errors"
	"fmt"

	"github.com/samcharles93/ai-sdk/chat"
	"github.com/samcharles93/ai-sdk/core"

	"github.com/samcharles93/archie-core/internal/tools/mcp"
)

// samplingDefaultMaxTokens bounds a server-requested completion when the
// request does not set maxTokens. A sampling request is a delegated
// sub-completion, not a full turn; this keeps one MCP server from spending a
// turn-sized output allowance on a single request.
const samplingDefaultMaxTokens = 4096

// mcpSamplingHandler answers an MCP server's sampling/createMessage request
// from the daemon's own chat model, so a server can ask for a completion
// without archie opening a second provider path. A missing runtime or model
// is a refused request, never a dropped one: the client maps the returned
// error to a JSON-RPC error response for that request alone, leaving the
// server session intact.
func (b *boot) mcpSamplingHandler() mcp.SamplingHandler {
	return func(ctx context.Context, req mcp.SamplingRequest) (mcp.SamplingResult, error) {
		llm := b.chatLLM()
		if llm == nil {
			return mcp.SamplingResult{}, errors.New("mcp sampling: no model runtime is configured")
		}
		if b.chatModels == nil {
			return mcp.SamplingResult{}, errors.New("mcp sampling: no chat model is configured")
		}
		model := b.chatModels.ActiveModel()
		if model == "" {
			return mcp.SamplingResult{}, errors.New("mcp sampling: no chat model is configured")
		}
		messages, err := samplingMessages(req)
		if err != nil {
			return mcp.SamplingResult{}, err
		}
		options := core.GenerateOptions{
			Messages:  messages,
			System:    req.SystemPrompt,
			MaxSteps:  1,
			MaxTokens: samplingMaxTokens(req, b.chatModels, model),
		}
		if req.Temperature != nil {
			options.Temperature = float32(*req.Temperature)
		}
		result, err := llm.Chat(ctx, model, options)
		// A sampling call is a model call this process made, so it is
		// recorded for /status like every other one; a nil recorder records
		// nothing.
		b.providerOutcomes.record(model, err)
		if err != nil {
			return mcp.SamplingResult{}, fmt.Errorf("mcp sampling: %w", err)
		}
		return mcp.SamplingResult{
			Model:      model,
			Role:       "assistant",
			Content:    mcp.SamplingContent{Type: "text", Text: result.Text},
			StopReason: string(result.FinishReason),
		}, nil
	}
}

// samplingMessages maps the server's messages onto chat messages. A
// non-text content block or an unknown role is rejected rather than silently
// dropped: answering from a partial prompt would be a wrong answer, not a
// degraded one.
func samplingMessages(req mcp.SamplingRequest) ([]chat.Message, error) {
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

// samplingMaxTokens bounds the completion to the request's own maxTokens,
// falling back to a conservative default, and never past the model's own
// output ceiling.
func samplingMaxTokens(req mcp.SamplingRequest, models *chatModelManager, model string) int {
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = samplingDefaultMaxTokens
	}
	if details, ok := models.ModelDetails(model); ok && details.MaxOutputTokens > 0 && maxTokens > details.MaxOutputTokens {
		return details.MaxOutputTokens
	}
	return maxTokens
}
