package archiegateway

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/samcharles93/ai-sdk/chat"
	"github.com/samcharles93/ai-sdk/core"
	"github.com/samcharles93/ai-sdk/runtime"
	"github.com/samcharles93/archie-core/internal/agentexec/modelloop"
	"github.com/samcharles93/archie-core/internal/domain/curator"
	"github.com/samcharles93/archie-core/internal/events"
	infraMemory "github.com/samcharles93/archie-core/internal/infrastructure/memory"
	"github.com/samcharles93/archie-core/internal/infrastructure/sessioncurator"
	"github.com/samcharles93/archie-core/internal/infrastructure/skillcurator"
	"github.com/samcharles93/archie-core/internal/infrastructure/toolbuilder"
)

func (b *server) setupCurators(ctx context.Context) {
	log := b.log.With("component", "curator")
	skillsRoot := b.cfg.SkillsDir
	if skillsRoot == "" {
		skillsRoot = b.cfg.WorkDir
	}
	b.curatorRegistry = curator.NewRegistry(curator.Registrar{
		Log:           log.With("component", "curator"),
		Events:        curatorEventSink{b.bus},
		Tools:         toolbuilder.New(b.toolReg),
		MemoryEngines: b.memEngines,
		Skills:        skillcurator.NewStore(skillsRoot),
		Conversations: sessioncurator.NewAdapter(b.chatSessionStore, b.cfg.BotUser),
		LLM:           curatorLLMRunner{llm: b.chatLLM, outcomes: b.providerOutcomes},
		Model:         b.chatModels.ActiveModel(),
	})

	if err := b.curatorRegistry.Register(skillcurator.New(skillcurator.DefaultInterval)); err != nil {
		log.Error("skill curator registration failed", "err", err)
	}

	if err := b.curatorRegistry.Register(sessioncurator.New(sessioncurator.DefaultInterval, infraMemory.EngineName)); err != nil {
		log.Error("session-memory curator registration failed", "err", err)
	}

	for _, def := range b.cfg.Curators {
		if !def.Enabled {
			continue
		}
		engine := curator.NewDefinitionEngine(curator.Definition{
			Name:         def.Name,
			Enabled:      def.Enabled,
			Instructions: def.Instructions,
			Manifest: curator.Manifest{
				Interval:      def.Interval.Std(),
				Cooldown:      def.Cooldown.Std(),
				OnInput:       def.OnInput,
				Tools:         def.Tools,
				Skills:        def.Skills,
				MemoryEngine:  def.MemoryEngine,
				Conversations: def.Conversations,
				Model:         def.Model,
			},
		})
		if err := b.curatorRegistry.Register(engine); err != nil {
			log.Error("config curator registration failed", "curator", def.Name, "err", err)
		}
	}

	b.curatorRuntime = curator.NewRuntime(b.curatorRegistry, curator.RuntimeConfig{})
	curator.WakeOnPrimaryInput(ctx, b.bus, b.curatorRuntime, events.KindTurnCompleted)
	rt := b.curatorRuntime
	b.addCleanup(shutdownCuratorRuntime(rt, log))
	reg := b.curatorRegistry
	b.addCleanup(shutdownCuratorRegistry(reg, log))
}

//nolint:contextcheck // shutdown runs after the parent context is cancelled
func shutdownCuratorRuntime(rt *curator.Runtime, log *slog.Logger) func() {
	return func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := rt.Stop(stopCtx); err != nil {
			log.Error("curator runtime shutdown", "err", err)
		}
	}
}

//nolint:contextcheck // shutdown runs after the parent context is cancelled
func shutdownCuratorRegistry(reg *curator.Registry, log *slog.Logger) func() {
	return func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := reg.Stop(stopCtx); err != nil {
			log.Error("curator registry shutdown", "err", err)
		}
	}
}

// curatorEventSink adapts the in-process event bus to the curator family's
// narrow emission contract. Bus publish is non-blocking with bounded,
// dropping per-subscriber buffers, so curator activity can never
// backpressure the daemon or a chat turn.
type curatorEventSink struct {
	b *events.Bus
}

func (s curatorEventSink) Emit(kind, detail string, data map[string]any) {
	s.b.Publish(events.Event{Kind: kind, Detail: detail, Data: data})
}

// curatorLLMRunner adapts the shared ai-sdk runtime to the curator
// family's narrow LLMRunner contract: one model reference, plain
// messages, and the declared tool set -- no streaming. Tools are built
// through the same agentexec path a chat turn uses, so a curator's
// declared set is converted to a runnable core.ToolSet rather than a
// parallel catalogue.
type curatorLLMRunner struct {
	// llm resolves the runtime at each call, so a live model-settings update
	// that swaps it wholesale is the one the next curator run reads; a
	// snapshot taken at construction would keep curators on the providers
	// boot started with.
	llm func() *runtime.Runtime
	// outcomes records this call for /status alongside sendChatTurn's. A
	// curator call is a model call this process made, and it does not pass
	// through sendChatTurn, so without this a daemon whose only recent model
	// traffic was curator work reports "no calls attempted yet" while the
	// provider is demonstrably reachable -- or worse, keeps showing a much
	// older chat outcome as current.
	outcomes *providerOutcomeRecorder
}

func (r curatorLLMRunner) Chat(ctx context.Context, req curator.ChatRequest) (curator.ChatResult, error) {
	// modelloop.NewRuntime returns nil when no provider is configured, and a
	// nil *runtime.Runtime panics on the first method call. A curator asking
	// for a completion on a daemon with no providers is a misconfiguration,
	// not a reason to take the process down.
	if r.llm == nil {
		return curator.ChatResult{}, fmt.Errorf("curator chat: no model runtime is configured")
	}
	llm := r.llm()
	if llm == nil {
		return curator.ChatResult{}, fmt.Errorf("curator chat: no model runtime is configured")
	}
	msgs := make([]chat.Message, 0, len(req.Messages))
	for _, m := range req.Messages {
		msgs = append(msgs, chat.Message{Role: chat.Role(m.Role), Content: m.Content})
	}
	toolSet, err := modelloop.BuildToolSetFrom(req.Tools, modelloop.ToolSetOptions{})
	if err != nil {
		r.outcomes.record(req.Model, err)
		return curator.ChatResult{}, err
	}
	res, err := llm.Chat(ctx, req.Model, core.GenerateOptions{
		Messages: msgs,
		Tools:    toolSet,
		MaxSteps: max(req.MaxSteps, 1),
	})
	r.outcomes.record(req.Model, err)
	if err != nil {
		return curator.ChatResult{}, err
	}

	calls := make([]curator.ToolCall, 0, len(res.ToolCalls))
	for _, call := range res.ToolCalls {
		calls = append(calls, curator.ToolCall{Name: call.ToolName, Input: call.Input})
	}
	return curator.ChatResult{Text: res.Text, ToolCalls: calls}, nil
}
