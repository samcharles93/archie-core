package main

import (
	"context"
	"encoding/json"

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
	llm      *runtime.Runtime
	registry *tools.Registry
	maxSteps int
	limits   agentexec.ToolLimits
}

func newChatTurnModel(
	llm *runtime.Runtime,
	registry *tools.Registry,
	maxSteps int,
	limits agentexec.ToolLimits,
) gateway.TurnModel {
	return &chatTurnModel{
		llm:      llm,
		registry: registry,
		maxSteps: maxSteps,
		limits:   limits,
	}
}

func (m *chatTurnModel) Prepare(
	ctx context.Context,
	model string,
	extra []tools.ToolEntry,
) (gateway.PreparedTurnModel, error) {
	options, err := chatGenerateOptions(ctx, nil, m.registry, m.maxSteps, m.limits, extra)
	if err != nil {
		return nil, err
	}
	toolSchema, err := json.Marshal(options.Tools)
	if err != nil {
		return nil, err
	}
	return &preparedChatTurnModel{
		llm:        m.llm,
		model:      model,
		options:    options,
		toolInfo:   toolSummaries(options.Tools),
		toolTokens: gateway.EstimateTokens(string(toolSchema)),
	}, nil
}

type preparedChatTurnModel struct {
	llm        *runtime.Runtime
	model      string
	options    core.GenerateOptions
	toolInfo   []gateway.ToolSummary
	toolTokens int
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
	onDelta func(string),
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
	options.MaxTokens = request.MaxTokens
	return sendChatTurn(ctx, m.llm, m.model, options, onDelta)
}
