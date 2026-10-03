package agentworker

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/samcharles93/ai-sdk/core"
	"github.com/samcharles93/ai-sdk/runtime"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/tools/mcp"
)

// taskSamplingModel returns the task's builder model, or "".
func taskSamplingModel(cfg config.TaskConfig) string {
	return strings.TrimSpace(cfg.Models["builder"])
}

// taskSamplingHandler answers MCP sampling requests with the task's model.
// Without a runtime or model the request is refused.
func taskSamplingHandler(llm *runtime.Runtime, cfg config.TaskConfig) mcp.SamplingHandler {
	return func(ctx context.Context, req mcp.SamplingRequest) (mcp.SamplingResult, error) {
		if llm == nil {
			return mcp.SamplingResult{}, errors.New("mcp sampling: no model runtime is configured")
		}
		model := taskSamplingModel(cfg)
		if model == "" {
			return mcp.SamplingResult{}, errors.New("mcp sampling: no builder model is configured")
		}
		messages, err := agentexec.SamplingMessages(req)
		if err != nil {
			return mcp.SamplingResult{}, err
		}
		limits := cfg.ModelLimits[model]
		options := core.GenerateOptions{
			Messages:  messages,
			System:    req.SystemPrompt,
			MaxSteps:  1,
			MaxTokens: agentexec.SamplingMaxTokens(req.MaxTokens, limits.MaxOutputTokens, limits.Reasoning),
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
