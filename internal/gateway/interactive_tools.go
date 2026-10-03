package gateway

import (
	"context"
	"fmt"
	"strings"

	"github.com/samcharles93/archie-core/internal/domain/messaging"
	"github.com/samcharles93/archie-core/internal/tools"
)

// askUserToolName is the model-facing tool that asks the human a question.
const askUserToolName = "ask_user"

// InteractiveTools returns the ask-the-user tool when the turn's channel can
// carry an interaction, and nothing otherwise.
func InteractiveTools(ctx context.Context) []tools.ToolEntry {
	if !InteractiveFromContext(ctx).Carries() {
		return nil
	}
	return []tools.ToolEntry{askUserTool()}
}

func askUserTool() tools.ToolEntry {
	return tools.ToolEntry{
		Name:    askUserToolName,
		Toolset: "chat",
		Description: "Ask the human a question when their answer changes what you do. " +
			"Pass options to ask them to choose exactly one; without options it is a free-text question. " +
			"Blocks until they answer, so ask at most once and only when you cannot proceed without it.",
		Classification: tools.ClassIdempotent,
		Schema: tools.JSONSchema{
			"type": "object",
			"properties": map[string]any{
				"question": map[string]any{
					"type":        "string",
					"description": "The question to ask.",
				},
				"options": map[string]any{
					"type":        "array",
					"description": "Optional choices; when present the human picks exactly one.",
					"items":       map[string]any{"type": "string"},
				},
			},
			"required": []string{"question"},
		},
		Handler: askUserHandler,
	}
}

// askUserHandler carries one question through the turn's channel adapter.
func askUserHandler(ctx context.Context, args map[string]any) (any, error) {
	question := strings.TrimSpace(stringArg(args["question"]))
	if question == "" {
		return nil, fmt.Errorf("ask_user: question is required")
	}
	interactive := InteractiveFromContext(ctx)
	options := stringSliceArg(args["options"])
	if len(options) > 0 {
		return askUserChoice(ctx, interactive, question, options)
	}
	if interactive.Clarifier == nil {
		return nil, fmt.Errorf("ask_user: this channel cannot ask a question")
	}
	answer, err := interactive.Clarifier.RequestClarification(ctx, ClarifyRequest{Question: question})
	if err != nil {
		return nil, err
	}
	return map[string]any{"answer": answer}, nil
}

// askUserChoice asks a single-choice question with the channel's Picker, or
// as numbered text through Clarify.
func askUserChoice(ctx context.Context, interactive Interactive, question string, options []string) (any, error) {
	req := PickerRequest{Prompt: question, Options: interactiveChoices(options)}
	if interactive.Picker != nil {
		choice, err := interactive.Picker.RequestChoice(ctx, req)
		if err != nil {
			return nil, err
		}
		return choiceResult(choice), nil
	}
	if interactive.Clarifier == nil {
		return nil, fmt.Errorf("ask_user: this channel cannot ask a question")
	}
	answer, err := interactive.Clarifier.RequestClarification(ctx, ClarifyRequest{Question: FormatPickerText(req)})
	if err != nil {
		return nil, err
	}
	choice, ok := ParsePickerReply(req, answer)
	if !ok {
		return nil, fmt.Errorf("ask_user: reply %q does not match any offered option", strings.TrimSpace(answer))
	}
	return choiceResult(choice), nil
}

func choiceResult(choice messaging.InteractiveChoice) map[string]any {
	return map[string]any{"value": choice.ID, "choice": choice.Label}
}

func interactiveChoices(options []string) []messaging.InteractiveChoice {
	choices := make([]messaging.InteractiveChoice, 0, len(options))
	for _, option := range options {
		choices = append(choices, messaging.InteractiveChoice{ID: option, Label: option})
	}
	return choices
}

func stringArg(value any) string {
	text, _ := value.(string)
	return text
}

func stringSliceArg(value any) []string {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	options := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
			options = append(options, text)
		}
	}
	return options
}
