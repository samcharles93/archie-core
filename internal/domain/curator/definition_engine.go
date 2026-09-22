package curator

import (
	"context"
	"fmt"
	"strings"

	"github.com/samcharles93/archie-core/internal/tools"
)

// ActionToolCalled is the Action.Type recorded for each tool the model
// loop invoked, so a definition-driven pass stays attributable through the
// registry's activity surface and events.
const ActionToolCalled = "tool.called"

// defaultDefinitionMaxSteps bounds the model/tool loop of one definition
// pass. It is not a definition value and is deliberately not configurable
// yet: the PRD's no-live-path-constants rule covers interval, cooldown,
// tools, model, and memory engine, not loop length.
const defaultDefinitionMaxSteps = 10

// DefinitionEngine interprets a Definition: one generic engine serving
// every data-defined curator. Its pass composes the definition's
// instructions with the built declared tool set, calls the model, and
// records an Action per tool invocation.
type DefinitionEngine struct {
	def  Definition
	host Registrar
}

// NewDefinitionEngine builds the generic engine for one definition.
func NewDefinitionEngine(def Definition) *DefinitionEngine {
	return &DefinitionEngine{def: def}
}

func (e *DefinitionEngine) Name() string       { return e.def.Name }
func (e *DefinitionEngine) Version() string    { return "1" }
func (e *DefinitionEngine) Manifest() Manifest { return e.def.Manifest }

func (e *DefinitionEngine) Bind(host Registrar) { e.host = host }

func (e *DefinitionEngine) Start(context.Context) error { return nil }

func (e *DefinitionEngine) Health(context.Context) Health {
	return Health{Status: HealthHealthy}
}

func (e *DefinitionEngine) Stop(context.Context) error { return nil }

// Check reports a definition is eligible whenever the runtime schedules it:
// a definition has no input state of its own to read, so eligibility is the
// manifest interval (and optional OnInput nudge) the runtime already owns.
func (e *DefinitionEngine) Check(context.Context) (bool, error) { return true, nil }

// Pass resolves the declared tools, composes the instructions with the
// built set, runs the model loop, and records an Action per tool call. A
// declared name with no implementation fails the pass (fail closed); the
// model receives exactly the declared set, never a broader registry.
func (e *DefinitionEngine) Pass(ctx context.Context, in PassInput) (PassResult, error) {
	declared := e.def.Manifest.Tools
	var built []tools.ToolEntry
	if len(declared) > 0 {
		if e.host.Tools == nil {
			return PassResult{}, fmt.Errorf("curator %s: declares tools but no ToolBuilder is bound", e.def.Name)
		}
		var err error
		built, err = e.host.Tools.Build(ctx, declared)
		if err != nil {
			return PassResult{}, fmt.Errorf("curator %s: build tools: %w", e.def.Name, err)
		}
	}
	if e.host.LLM == nil {
		return PassResult{}, fmt.Errorf("curator %s: no model runner is bound", e.def.Name)
	}

	model := e.def.Manifest.Model
	if model == "" {
		model = e.host.Model
	}
	res, err := e.host.LLM.Chat(ctx, ChatRequest{
		Model:    model,
		Messages: definitionMessages(e.def.Instructions, built, in.Reason),
		Tools:    built,
		MaxSteps: defaultDefinitionMaxSteps,
	})
	if err != nil {
		return PassResult{}, err
	}

	actions := make([]Action, 0, len(res.ToolCalls))
	for _, call := range res.ToolCalls {
		actions = append(actions, Action{
			At:     e.host.Clock.Now(),
			Type:   ActionToolCalled,
			Detail: call.Name,
			Reason: in.Reason,
		})
	}
	return PassResult{Actions: actions}, nil
}

// definitionMessages builds the prompt for one pass: the definition's
// instructions plus a summary of the built tool set as the system prompt,
// and the pass reason as the user turn. The tool summaries come from the
// same set that was built, never from a wider list.
func definitionMessages(instructions string, built []tools.ToolEntry, reason string) []Message {
	system := instructions
	if len(built) > 0 {
		var b strings.Builder
		b.WriteString(instructions)
		b.WriteString("\n\nAvailable tools:\n")
		for _, t := range built {
			desc := strings.TrimSpace(t.Description)
			if desc == "" {
				desc = "(no description)"
			}
			fmt.Fprintf(&b, "- %s: %s\n", t.Name, desc)
		}
		system = b.String()
	}

	user := strings.TrimSpace(reason)
	if user == "" {
		user = "Run your scheduled pass now."
	}
	return []Message{
		{Role: "system", Content: system},
		{Role: "user", Content: user},
	}
}
