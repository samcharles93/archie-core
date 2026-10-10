package agentworker

import (
	"context"
	"errors"
	"fmt"

	"github.com/samcharles93/ai-sdk/core"
	"github.com/samcharles93/ai-sdk/runtime"

	"github.com/samcharles93/archie-core/internal/agentexec/modelloop"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/tools/mcp"
)

// taskSamplingHandler answers MCP sampling requests with the task's model.
// Without a runtime or model the request is refused.
func taskSamplingHandler(llm *runtime.Runtime, cfg config.TaskConfig) mcp.SamplingHandler {
	return func(ctx context.Context, req mcp.SamplingRequest) (mcp.SamplingResult, error) {
		if llm == nil {
			return mcp.SamplingResult{}, errors.New("mcp sampling: no model runtime is configured")
		}
		model, err := config.ResolveModel(cfg.Models, "", config.PurposeAgent)
		if err != nil {
			return mcp.SamplingResult{}, fmt.Errorf("mcp sampling: %w", err)
		}
		messages, err := modelloop.SamplingMessages(req)
		if err != nil {
			return mcp.SamplingResult{}, err
		}
		limits := cfg.ModelLimits[model]
		options := core.GenerateOptions{
			Messages:  messages,
			System:    req.SystemPrompt,
			MaxSteps:  1,
			MaxTokens: modelloop.SamplingMaxTokens(req.MaxTokens, limits.MaxOutputTokens),
		}
		if req.Temperature != nil {
			options.Temperature = float32(*req.Temperature)
		}
		result, err := llm.Chat(ctx, model, options)
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
