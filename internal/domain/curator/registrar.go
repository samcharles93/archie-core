package curator

import (
	"context"
	"time"

	domainmemory "github.com/samcharles93/archie-core/internal/domain/memory"
	"github.com/samcharles93/archie-core/internal/tools"
)

// Registrar is the narrow typed host access a curator receives at
// registration. Only the services the curator's declared shape requires are
// ever populated in the view the registry binds (see Registry.filter); the
// daemon itself is never part of it. Fields are typed contracts declared
// here — this package never names who implements them.
type Registrar struct {
	// Model is the default model reference ("provider/model") for
	// curators whose manifest leaves Model empty.
	Model string
	// LLM provides model access for agentic passes. Bound when the curator
	// declares an agentic capability (tools, skills, or a memory engine).
	LLM LLMRunner
	// Tools resolves the declared tool names into a runnable tool set.
	// Resolution is the enforcement point: a pass receives exactly the
	// declared set, never a broader registry. Bound when tools are
	// declared.
	Tools ToolBuilder
	// Skills is the skill-maintenance capability (skill curator,
	// archie-core-i7i). Bound when the curator declares Skills.
	Skills SkillStore
	// MemoryEngines resolves a named memory engine, for a curator that
	// declared Manifest.MemoryEngine (archie-core-1786637499636). Bound
	// only when declared. Like Tools, resolution is by name at call time,
	// not a value pre-fetched at Bind: a curator calls
	// MemoryEngines.Get(its own Manifest().MemoryEngine), the same trust
	// model ToolBuilder.Build(ctx, declared) already uses -- the caller is
	// trusted to pass its own declared identifier, not handed a
	// pre-scoped instance.
	MemoryEngines MemoryEngineSource
	// Events publishes curator activity. Implementations must be
	// non-blocking and bounded (drop on overflow), so a curator can never
	// apply backpressure to a chat turn or the daemon. Always bound.
	Events EventSink
	// Clock is injectable time. Always bound; nil at construction is
	// replaced with the system clock.
	Clock Clock
}

// LLMRunner is the model access a curator needs for a pass. The app layer
// adapts the shared runtime to this narrow contract.
type LLMRunner interface {
	Chat(ctx context.Context, req ChatRequest) (ChatResult, error)
}

// ChatRequest is one model call: messages plus the runnable tool set.
type ChatRequest struct {
	Model    string
	Messages []Message
	Tools    []tools.ToolEntry
	MaxSteps int
}

// Message is one conversation message.
type Message struct {
	Role    string // e.g. "system", "user", "assistant", "tool"
	Content string
}

// ChatResult is the model's final answer.
type ChatResult struct {
	Text string
}

// ToolBuilder resolves declared tool names into a runnable tool set. A
// declared name with no implementation fails the pass rather than silently
// running with fewer tools, and no tool outside the declared set is
// returned.
type ToolBuilder interface {
	Build(ctx context.Context, declared []string) ([]tools.ToolEntry, error)
}

// SkillStore is the skill-maintenance capability. The implementation owns
// the on-disk format and catalog reload rules (archie-core-i7i).
type SkillStore interface {
	List(ctx context.Context) ([]SkillRef, error)
	Read(ctx context.Context, name string) (Skill, error)
	Write(ctx context.Context, s Skill) error
	Delete(ctx context.Context, name string) error
}

// SkillRef identifies one skill on disk.
type SkillRef struct {
	Name string
	Path string
}

// Skill is one skill definition.
type Skill struct {
	Name        string
	Content     string
	Description string
}

// MemoryEngineSource resolves a named memory engine. Mirrors
// domain/memory.Registry.Get's exact signature so the real registry
// satisfies this without an adapter, while this package stays decoupled
// from that package's concrete Registry type.
type MemoryEngineSource interface {
	Get(name string) (domainmemory.MemoryEngine, bool)
}

// EventSink publishes curator activity (what ran, what changed, why). It is
// the emission side only; wake signals arrive through the runtime's own
// subscription. Implementations MUST be non-blocking and
// bounded, matching the in-process event bus semantics.
type EventSink interface {
	Emit(kind, detail string, data map[string]any)
}

// Clock is injectable time: Now for timestamps and After for loop sleeps.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

type systemClock struct{}

func (systemClock) Now() time.Time                         { return time.Now() }
func (systemClock) After(d time.Duration) <-chan time.Time { return time.After(d) }
