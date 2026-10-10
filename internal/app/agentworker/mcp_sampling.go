package agentworker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/samcharles93/ai-sdk/chat"
	"github.com/samcharles93/ai-sdk/core"
	"github.com/samcharles93/ai-sdk/runtime"

	"github.com/samcharles93/archie-core/internal/agentexec/modelloop"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/usage"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/tools/mcp"
)

// taskSamplingHandler answers MCP sampling requests with the task's model.
// Without a runtime or model the request is refused. record receives each
// call's model ref and token usage.
func taskSamplingHandler(llm *runtime.Runtime, cfg config.TaskConfig, record func(context.Context, string, chat.Usage)) mcp.SamplingHandler {
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
		record(ctx, model, result.TotalUsage)
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

// samplingUsageRecorder records a task's MCP sampling calls against the task.
// A failed write is logged, never fails the call.
func samplingUsageRecorder(store workflow.Store, t *workflow.Task, log *slog.Logger) func(context.Context, string, chat.Usage) {
	return func(ctx context.Context, model string, used chat.Usage) {
		if store == nil || t == nil {
			return
		}
		record := usage.Record{
			Source: usage.SourceTask, TaskID: t.ID, Attempt: t.Attempt, Workflow: t.Workflow, Step: "mcp-sampling",
			InputTokens: int64(used.PromptTokens), OutputTokens: int64(used.CompletionTokens),
			CachedTokens: int64(used.CachedTokens), At: time.Now().UTC(),
		}.WithRef(model)
		if err := store.RecordUsage(ctx, record); err != nil && log != nil {
			log.Warn("record model usage failed", "model", model, "err", err)
		}
	}
}
