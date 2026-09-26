package archied

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/samcharles93/ai-sdk/chat"
	"github.com/samcharles93/ai-sdk/core"
	"github.com/samcharles93/ai-sdk/runtime"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/gateway"
	"github.com/samcharles93/archie-core/internal/tools"
)

// chatTurnModel adapts the provider-specific ai-sdk runtime to the gateway's
// provider-neutral TurnModel seam. Tool construction remains here because it
// depends on runtime registries and configured execution limits.
type chatTurnModel struct {
	// llm resolves the provider runtime at each turn, so a runtime swapped by
	// a live model-settings update is the one the turn after it runs on.
	llm      func() *runtime.Runtime
	registry *tools.Registry
	maxSteps int
	limits   agentexec.ToolLimits
	// outcomes records each call's result for /status (see sendChatTurn).
	outcomes *providerOutcomeRecorder
}

func newChatTurnModel(
	llm func() *runtime.Runtime,
	registry *tools.Registry,
	maxSteps int,
	limits agentexec.ToolLimits,
	outcomes *providerOutcomeRecorder,
) gateway.TurnModel {
	return &chatTurnModel{
		llm:      llm,
		registry: registry,
		maxSteps: maxSteps,
		limits:   limits,
		outcomes: outcomes,
	}
}

func (m *chatTurnModel) Prepare(
	ctx context.Context,
	req gateway.TurnPrepareContext,
) (gateway.PreparedTurnModel, error) {
	options, err := chatGenerateOptions(ctx, nil, m.registry, m.maxSteps, m.limits, req.Extra, req.ContextWindow)
	if err != nil {
		return nil, err
	}
	toolSchema, err := json.Marshal(options.Tools)
	if err != nil {
		return nil, err
	}
	return &preparedChatTurnModel{
		llm:        m.llm(),
		model:      req.Model,
		options:    options,
		toolInfo:   toolSummaries(options.Tools),
		toolTokens: gateway.EstimateTokens(string(toolSchema)),
		outcomes:   m.outcomes,
		icons:      toolIcons(m.registry),
	}, nil
}

type preparedChatTurnModel struct {
	llm        *runtime.Runtime
	model      string
	options    core.GenerateOptions
	toolInfo   []gateway.ToolSummary
	toolTokens int
	outcomes   *providerOutcomeRecorder
	// icons maps tool name to the icon that tool registered, for chat
	// surfaces to render. A nil map yields "" for every lookup, which is
	// the no-icon rendering.
	icons map[string]string
}

// toolIcons snapshots the icon each registered tool declares. It is taken at
// prepare time alongside the tool set itself, so a turn renders the icons of
// the tools it was actually given.
func toolIcons(registry *tools.Registry) map[string]string {
	if registry == nil {
		return nil
	}
	icons := make(map[string]string)
	for _, entry := range registry.All() {
		if entry.Emoji != "" {
			icons[entry.Name] = entry.Emoji
		}
	}
	return icons
}

func (m *preparedChatTurnModel) ToolSummaries() []gateway.ToolSummary {
	return append([]gateway.ToolSummary(nil), m.toolInfo...)
}

func (m *preparedChatTurnModel) ToolSchemaTokens() int {
	return m.toolTokens
}

func (m *preparedChatTurnModel) Generate(
	ctx context.Context,
	request gateway.TurnModelRequest,
	stream gateway.TurnStream,
) (string, error) {
	options := m.options
	options.Messages = make([]chat.Message, len(request.Messages))
	for i, message := range request.Messages {
		role := chat.RoleUser
		switch message.Role {
		case "assistant":
			role = chat.RoleAssistant
		case "system":
			role = chat.RoleSystem
		}
		options.Messages[i] = chat.Message{Role: role, Content: message.Content}
	}
	// This is one provider response's output allowance, not a turn-
	// continuation budget. Tool loops remain free to continue.
	options.MaxTokens = request.MaxOutputTokens
	// A runtime swapped to nil -- a live update that left no usable provider
	// configured -- is a refused turn, not a panic.
	if m.llm == nil {
		return "", fmt.Errorf("llm chat: no model runtime is configured")
	}
	return sendChatTurn(ctx, m.llm, m.model, options, stream, m.outcomes, m.icons)
}
