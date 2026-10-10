package archiegateway

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/samcharles93/ai-sdk/chat"
	"github.com/samcharles93/ai-sdk/core"
	"github.com/samcharles93/ai-sdk/runtime"

	"github.com/samcharles93/archie-core/internal/agentexec/modelloop"
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
	// limits resolves the tool-output policy at each turn, so a live
	// tool-settings update is the policy the next turn builds its tool options
	// with; a snapshot taken at construction would keep the boot limits.
	limits func() modelloop.ToolLimits
	// outcomes records each call's result for /status (see sendChatTurn).
	outcomes *providerOutcomeRecorder
	usage    *usageSink
}

func newChatTurnModel(
	llm func() *runtime.Runtime,
	registry *tools.Registry,
	maxSteps int,
	limits func() modelloop.ToolLimits,
	outcomes *providerOutcomeRecorder,
	usageRecords *usageSink,
) gateway.TurnModel {
	return &chatTurnModel{
		llm:      llm,
		registry: registry,
		maxSteps: maxSteps,
		limits:   limits,
		outcomes: outcomes,
		usage:    usageRecords,
	}
}

func (m *chatTurnModel) Prepare(
	ctx context.Context,
	req gateway.TurnPrepareContext,
) (gateway.PreparedTurnModel, error) {
	limits := modelloop.ToolLimits{}
	if m.limits != nil {
		limits = m.limits()
	}
	options, err := chatGenerateOptions(ctx, nil, m.registry, m.maxSteps, limits, req.Extra, req.ContextWindow)
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
		usage:      m.usage,
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
	usage      *usageSink
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

// buildTurnMessages maps the gateway's compressed history onto ai-sdk chat
// messages. The turn's inbound media is not part of stored history, so it
// is merged here into the final user message as content parts: the model
// sees the caption text plus each attachment's bytes (or URL), exactly for
// the turn it arrived in.
func buildTurnMessages(request gateway.TurnModelRequest) []chat.Message {
	messages := make([]chat.Message, len(request.Messages))
	for i, message := range request.Messages {
		role := chat.RoleUser
		switch message.Role {
		case "assistant":
			role = chat.RoleAssistant
		case "system":
			role = chat.RoleSystem
		}
		messages[i] = chat.Message{Role: role, Content: message.Content}
	}
	parts := mediaParts(request.Media)
	if len(parts) == 0 {
		return messages
	}
	last := len(messages) - 1
	// The media belongs to the inbound message, which is always the final
	// user entry; if the view ever ends differently, content stays intact
	// over silently attaching parts to an unrelated message.
	if last < 0 || messages[last].Role != chat.RoleUser {
		return messages
	}
	userMessages := messages[last]
	userMessages.Parts = append(chat.Parts{chat.TextPart{Text: userMessages.Content}}, parts...)
	userMessages.Content = ""
	messages[last] = userMessages
	return messages
}

// mediaParts converts inbound media attachments into ai-sdk content
// parts: images get ImagePart, everything else FilePart. An attachment
// carrying neither bytes nor a URL names nothing consumable and is
// dropped -- the turn's text still reaches the model.
func mediaParts(media []gateway.MediaAttachment) []chat.Part {
	if len(media) == 0 {
		return nil
	}
	parts := make([]chat.Part, 0, len(media))
	for _, att := range media {
		if att.Type == "image" {
			switch {
			case len(att.Data) > 0 && att.MIMEType != "":
				parts = append(parts, chat.ImagePart{Data: att.Data, MediaType: att.MIMEType})
			case att.URL != "":
				parts = append(parts, chat.ImagePart{URL: att.URL})
			}
			continue
		}
		part := chat.FilePart{Name: att.FileName, MediaType: att.MIMEType}
		if len(att.Data) > 0 {
			part.Data = att.Data
		} else {
			part.URL = att.URL
		}
		if len(part.Data) == 0 && part.URL == "" {
			continue
		}
		if part.MediaType == "" {
			part.MediaType = "application/octet-stream"
		}
		parts = append(parts, part)
	}
	if len(parts) == 0 {
		return nil
	}
	return parts
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
	options.Messages = buildTurnMessages(request)
	// This is one provider response's output allowance, not a turn-
	// continuation budget. Tool loops remain free to continue. The ai-sdk
	// runtime picks max_completion_tokens or max_tokens from the model's
	// catalog metadata.
	options.MaxTokens = request.MaxOutputTokens
	// A runtime swapped to nil -- a live update that left no usable provider
	// configured -- is a refused turn, not a panic.
	if m.llm == nil {
		return "", fmt.Errorf("llm chat: no model runtime is configured")
	}
	return sendChatTurn(ctx, m.llm, m.model, options, stream, m.outcomes, m.usage, m.icons)
}
