package archiegateway

import (
	"context"
	"errors"
	"fmt"

	"github.com/samcharles93/ai-sdk/core"

	"github.com/samcharles93/archie-core/internal/agentexec/modelloop"
	"github.com/samcharles93/archie-core/internal/tools/mcp"
)

// mcpSamplingHandler answers an MCP server's sampling/createMessage request
// from the Gateway's chat model, so a server can ask for a completion
// without archie opening a second provider path. A missing runtime or model
// is a refused request, never a dropped one: the client maps the returned
// error to a JSON-RPC error response for that request alone, leaving the
// server session intact.
func (b *server) mcpSamplingHandler() mcp.SamplingHandler {
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
		messages, err := modelloop.SamplingMessages(req)
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

// samplingMaxTokens bounds the completion to the request's own maxTokens,
// falling back to a conservative default, and never past the model's own
// output ceiling. The bound itself is shared with the agent worker's sampling
// path (modelloop.SamplingMaxTokens) so the same server sees the same bound on
// either path.
func samplingMaxTokens(req mcp.SamplingRequest, models *chatModelManager, model string) int {
	details, ok := models.ModelDetails(model)
	return modelloop.SamplingMaxTokens(req.MaxTokens, details.MaxOutputTokens, ok && details.Reasoning)
}
