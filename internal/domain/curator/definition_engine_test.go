package curator

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/tools"
)

// recordingLLM captures the ChatRequest a definition pass builds and returns
// a fixed result, so tests assert exactly what reached the model.
type recordingLLM struct {
	req    ChatRequest
	result ChatResult
	err    error
}

func (l *recordingLLM) Chat(_ context.Context, req ChatRequest) (ChatResult, error) {
	l.req = req
	return l.result, l.err
}

// mapBuilder resolves declared names from a fixed map: the same resolution
// contract as the infrastructure ToolBuilder, without domain tests importing
// infrastructure.
type mapBuilder struct {
	entries map[string]tools.ToolEntry
}

func (b mapBuilder) Build(_ context.Context, declared []string) ([]tools.ToolEntry, error) {
	out := make([]tools.ToolEntry, 0, len(declared))
	for _, name := range declared {
		entry, ok := b.entries[name]
		if !ok {
			return nil, fmt.Errorf("declared tool %q has no implementation", name)
		}
		out = append(out, entry)
	}
	return out, nil
}

func definitionTool(name string) tools.ToolEntry {
	return tools.ToolEntry{
		Name:        name,
		Description: name + " tool",
		Handler:     func(context.Context, map[string]any) (any, error) { return nil, nil },
	}
}

func TestDefinitionEnginePassReachesExactlyDeclaredTools(t *testing.T) {
	t.Parallel()

	def := Definition{
		Name:         "project-curator",
		Enabled:      true,
		Instructions: "Review the repository and report what you changed.",
		Manifest: Manifest{
			Interval: time.Hour,
			Model:    "provider/config-model",
			Tools:    []string{"read"},
		},
	}
	llm := &recordingLLM{result: ChatResult{
		Text:      "done",
		ToolCalls: []ToolCall{{Name: "read", Input: "{}"}},
	}}
	engine := NewDefinitionEngine(def)
	engine.Bind(Registrar{
		LLM:   llm,
		Model: "provider/default-model",
		Tools: mapBuilder{entries: map[string]tools.ToolEntry{
			"read":  definitionTool("read"),
			"write": definitionTool("write"),
		}},
		Clock: testClock{now: time.Unix(0, 0)},
	})

	res, err := engine.Pass(context.Background(), PassInput{Reason: "check-in"})
	if err != nil {
		t.Fatalf("Pass() error = %v, want nil", err)
	}

	if got := len(llm.req.Tools); got != 1 {
		t.Fatalf("model received %d tools, want exactly the declared 1 (never a broader registry)", got)
	}
	if llm.req.Tools[0].Name != "read" {
		t.Fatalf("model received tool %q, want the declared %q", llm.req.Tools[0].Name, "read")
	}
	if llm.req.Model != "provider/config-model" {
		t.Fatalf("model reference = %q, want the definition's %q (no fallback to the default)", llm.req.Model, "provider/config-model")
	}
	if llm.req.MaxSteps <= 1 {
		t.Fatalf("MaxSteps = %d, want more than one so the tool loop can run", llm.req.MaxSteps)
	}
	if len(llm.req.Messages) == 0 || llm.req.Messages[0].Role != "system" {
		t.Fatalf("messages = %+v, want a leading system prompt", llm.req.Messages)
	}
	if sys := llm.req.Messages[0].Content; !strings.Contains(sys, def.Instructions) || !strings.Contains(sys, "read") {
		t.Fatalf("system prompt = %q, want the definition instructions plus the declared tool summary", sys)
	}

	if len(res.Actions) != 1 {
		t.Fatalf("Pass() actions = %d, want one per tool call", len(res.Actions))
	}
	a := res.Actions[0]
	if a.Type != ActionToolCalled || a.Detail != "read" || a.Reason != "check-in" {
		t.Fatalf("action = %+v, want the invoked tool attributed with the pass reason", a)
	}
}

func TestDefinitionEnginePassFailsOnUnknownDeclaredTool(t *testing.T) {
	t.Parallel()

	def := Definition{
		Name: "project-curator",
		Manifest: Manifest{
			Interval: time.Hour,
			Tools:    []string{"gh.issue.create"},
		},
	}
	llm := &recordingLLM{}
	engine := NewDefinitionEngine(def)
	engine.Bind(Registrar{
		LLM:   llm,
		Tools: mapBuilder{entries: map[string]tools.ToolEntry{}},
		Clock: testClock{now: time.Unix(0, 0)},
	})

	if _, err := engine.Pass(context.Background(), PassInput{Reason: "check-in"}); err == nil {
		t.Fatal("Pass() with an unknown declared tool = nil error, want the build failure to fail the pass")
	}
	if llm.req.Messages != nil {
		t.Fatalf("model was called despite the failed build: %+v", llm.req)
	}
}

func TestDefinitionEngineManifestIsTheDefinitionNoCodeFallback(t *testing.T) {
	t.Parallel()

	def := Definition{
		Name:         "config-defined",
		Enabled:      true,
		Instructions: "do the thing",
		Manifest: Manifest{
			Interval: 17 * time.Minute,
			Cooldown: 3 * time.Minute,
			OnInput:  true,
			Tools:    []string{"read", "shell"},
			Model:    "provider/config-model",
		},
	}
	engine := NewDefinitionEngine(def)

	got := engine.Manifest()
	if got.Interval != 17*time.Minute {
		t.Fatalf("Manifest().Interval = %v, want the definition's %v (no code constant)", got.Interval, 17*time.Minute)
	}
	if got.Cooldown != 3*time.Minute || !got.OnInput {
		t.Fatalf("Manifest() = %+v, want the definition's cooldown/on_input", got)
	}
	if len(got.Tools) != 2 || got.Tools[0] != "read" || got.Tools[1] != "shell" {
		t.Fatalf("Manifest().Tools = %v, want the definition's declared set", got.Tools)
	}
	if got.Model != "provider/config-model" {
		t.Fatalf("Manifest().Model = %q, want the definition's model", got.Model)
	}
}

func TestDefinitionEngineRegistrationRequiresToolBuilder(t *testing.T) {
	t.Parallel()

	reg := NewRegistry(Registrar{})
	err := reg.Register(NewDefinitionEngine(Definition{
		Name:     "project-curator",
		Manifest: Manifest{Interval: time.Hour, Tools: []string{"read"}},
	}))
	if err == nil {
		t.Fatal("Register(tools-declaring definition) = nil, want the existing no-ToolBuilder refusal")
	}
}
