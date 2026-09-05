package tools

import (
	"context"
	"fmt"
	"maps"
	"strings"
	"sync"
)

// ---------------------------------------------------------------------------
// 17.14  --  Progressive tool disclosure (bridge tools)
// ---------------------------------------------------------------------------

// BridgeToolName is the set of reserved tool names used by the progressive
// disclosure bridge. These names are reserved and must not be used by
// registered tools.
const (
	BridgeToolSearch   = "tool_search"
	BridgeToolDescribe = "tool_describe"
	BridgeToolCall     = "tool_call"
)

// bridgeToolNames is the complete set of reserved bridge tool names.
var bridgeToolNames = map[string]bool{
	BridgeToolSearch:   true,
	BridgeToolDescribe: true,
	BridgeToolCall:     true,
}

// IsBridgeTool reports whether name is a reserved bridge tool.
func IsBridgeTool(name string) bool {
	return bridgeToolNames[name]
}

// ToolSummary is a lightweight representation of a tool used in bridge
// tool search results. It contains only name, description, and emoji  --
// enough for the LLM to decide whether to call tool_describe for full schema.
type ToolSummary struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Emoji       string `json:"emoji,omitempty"`
}

// bridgeSearchHandler implements the tool_search bridge tool.
// It searches the registry for tools matching the given keyword.
func bridgeSearchHandler(reg *Registry) Handler {
	return func(ctx context.Context, input map[string]any) (any, error) {
		keyword, _ := input["keyword"].(string)

		all := reg.All()
		var matches []ToolEntry
		for _, e := range all {
			if keyword == "" || matchesKeyword(e, keyword) {
				// Never return bridge tools themselves in search results.
				if IsBridgeTool(e.Name) {
					continue
				}
				matches = append(matches, e)
			}
		}

		// Return lightweight summaries (name + description only).
		summaries := make([]ToolSummary, len(matches))
		for i, m := range matches {
			summaries[i] = ToolSummary{
				Name:        m.Name,
				Description: m.Description,
				Emoji:       m.Emoji,
			}
		}
		return map[string]any{"tools": summaries}, nil
	}
}

// matchesKeyword reports whether a tool entry matches a search keyword.
// Matching is case-insensitive and checks name, description, and toolset.
func matchesKeyword(e ToolEntry, keyword string) bool {
	lower := strings.ToLower(keyword)
	if strings.Contains(strings.ToLower(e.Name), lower) {
		return true
	}
	if strings.Contains(strings.ToLower(e.Description), lower) {
		return true
	}
	if strings.Contains(strings.ToLower(e.Toolset), lower) {
		return true
	}
	return false
}

// bridgeDescribeHandler implements the tool_describe bridge tool.
// It returns the full schema and metadata for a single tool.
func bridgeDescribeHandler(reg *Registry) Handler {
	return func(ctx context.Context, input map[string]any) (any, error) {
		name, _ := input["name"].(string)
		if name == "" {
			return nil, fmt.Errorf("tool_describe: 'name' parameter is required")
		}

		all := reg.All()
		for _, e := range all {
			if e.Name == name {
				return map[string]any{
					"tool": toolDescribeResult(e),
				}, nil
			}
		}
		return nil, fmt.Errorf("tool_describe: tool %q not found", name)
	}
}

// toolDescribeResult builds the describe payload for a tool.
func toolDescribeResult(e ToolEntry) map[string]any {
	result := map[string]any{
		"name":        e.Name,
		"description": e.Description,
		"toolset":     e.Toolset,
		"emoji":       e.Emoji,
		"is_async":    e.IsAsync,
	}
	if e.Schema != nil {
		result["schema"] = e.ResolvedSchema()
	}
	if e.RequiresEnv != nil {
		result["requires_env"] = e.RequiresEnv
	}
	return result
}

// bridgeCallHandler implements the tool_call bridge tool.
// It invokes a tool by name after discovery.
func bridgeCallHandler(reg *Registry) Handler {
	return func(ctx context.Context, input map[string]any) (any, error) {
		name, _ := input["name"].(string)
		if name == "" {
			return nil, fmt.Errorf("tool_call: 'name' parameter is required")
		}

		// Prevent recursive bridge calls.
		if IsBridgeTool(name) {
			return nil, fmt.Errorf("tool_call: cannot invoke bridge tool %q", name)
		}

		callInput, _ := input["input"].(map[string]any)

		all := reg.All()
		for _, e := range all {
			if e.Name == name {
				if e.Handler == nil {
					return nil, fmt.Errorf("tool_call: tool %q has no handler", name)
				}
				if e.Classification.IsApprovalRequired() {
					return nil, fmt.Errorf("tool_call: tool %q requires human approval and cannot be invoked through the bridge", name)
				}
				return e.Handler(ctx, callInput)
			}
		}
		return nil, fmt.Errorf("tool_call: tool %q not found", name)
	}
}

// BridgeTools returns the three bridge tool entries for progressive
// disclosure: tool_search, tool_describe, and tool_call. The returned
// entries are pre-configured with handlers wired to the given registry.
//
// These tools are classified as idempotent (read-only) since they do
// not directly modify state.
func BridgeTools(reg *Registry) []ToolEntry {
	return []ToolEntry{
		{
			Name:           BridgeToolSearch,
			Toolset:        "bridge",
			Description:    "Search available tools by keyword. Returns matching tool names and descriptions.",
			Emoji:          "🔍",
			Classification: ClassIdempotent,
			Handler:        bridgeSearchHandler(reg),
			Schema: JSONSchema{
				"type": "object",
				"properties": JSONSchema{
					"keyword": JSONSchema{
						"type":        "string",
						"description": "Keyword to search for in tool names and descriptions",
					},
				},
			},
		},
		{
			Name:           BridgeToolDescribe,
			Toolset:        "bridge",
			Description:    "Get the full schema and metadata for a specific tool. Use after tool_search to inspect a tool before calling it.",
			Emoji:          "📋",
			Classification: ClassIdempotent,
			Handler:        bridgeDescribeHandler(reg),
			Schema: JSONSchema{
				"type": "object",
				"properties": JSONSchema{
					"name": JSONSchema{
						"type":        "string",
						"description": "The exact name of the tool to describe",
					},
				},
				"required": []any{"name"},
			},
		},
		{
			Name:           BridgeToolCall,
			Toolset:        "bridge",
			Description:    "Invoke a tool by name. Use after tool_describe to confirm the schema.",
			Emoji:          "⚡",
			Classification: ClassIdempotent,
			Handler:        bridgeCallHandler(reg),
			Schema: JSONSchema{
				"type": "object",
				"properties": JSONSchema{
					"name": JSONSchema{
						"type":        "string",
						"description": "The exact name of the tool to invoke",
					},
					"input": JSONSchema{
						"type":        "object",
						"description": "Tool input parameters (tool-specific)",
					},
				},
				"required": []any{"name"},
			},
		},
	}
}

// ComposeForTurn builds a per-turn Registry that combines the base registry's
// tools, the per-turn extra tools, and the three bridge tools. The returned
// registry is detached from the process-wide registry: extra tools are
// per-gateway identity-bound (see agentexec.BuildToolSetFrom) and must never be
// registered globally, so composing them into a turn-local registry is the only
// way the bridge handlers (which enumerate reg.All) can discover and invoke
// them.
func ComposeForTurn(reg *Registry, extra []ToolEntry) (*Registry, error) {
	composed := NewRegistry()
	if reg != nil {
		for _, e := range reg.All() {
			if err := composed.Register(e); err != nil {
				return nil, err
			}
		}
	}
	for _, e := range extra {
		if err := composed.Register(e); err != nil {
			return nil, err
		}
	}
	if err := composed.RegisterBatch(BridgeTools(composed)); err != nil {
		return nil, err
	}
	return composed, nil
}

// ---------------------------------------------------------------------------
// 17.15  --  Context-pressure threshold gating
// ---------------------------------------------------------------------------

// DisclosureMode controls whether the tool list is served in full or
// through the bridge tools (progressive disclosure).
type DisclosureMode int

const (
	// DisclosureFull serves all tool schemas directly to the LLM.
	DisclosureFull DisclosureMode = iota

	// DisclosureBridge serves only the bridge tools (tool_search,
	// tool_describe, tool_call) plus any critical always-visible tools.
	DisclosureBridge
)

// String returns a human-readable representation of the mode.
func (m DisclosureMode) String() string {
	switch m {
	case DisclosureFull:
		return "full"
	case DisclosureBridge:
		return "bridge"
	default:
		return "unknown"
	}
}

// ContextPressureGate monitors the size of tool schemas relative to the
// LLM context window and dynamically switches between full disclosure
// and bridge-tool mode.
type ContextPressureGate struct {
	mu sync.RWMutex

	// ThresholdFraction is the fraction of the context window at which
	// the gate switches to bridge mode. Default 0.5 (50%).
	ThresholdFraction float64

	// ContextWindowSize is the total context window size in characters.
	// Zero means the gate is disabled (always full disclosure).
	ContextWindowSize int

	// AlwaysVisible is a set of tool names that are always included in
	// the tool list even in bridge mode (e.g. essential system tools).
	AlwaysVisible map[string]bool

	// currentMode is the active disclosure mode.
	currentMode DisclosureMode
}

// NewContextPressureGate creates a [ContextPressureGate] with sensible
// defaults: 50% threshold, full disclosure until pressure is detected.
func NewContextPressureGate(contextWindowSize int) *ContextPressureGate {
	return &ContextPressureGate{
		ThresholdFraction: 0.5,
		ContextWindowSize: contextWindowSize,
		AlwaysVisible:     make(map[string]bool),
		currentMode:       DisclosureFull,
	}
}

// Evaluate scans the given tool entries and updates the disclosure mode
// based on the total schema size relative to the context window.
// Returns the current disclosure mode after evaluation.
func (g *ContextPressureGate) Evaluate(tools []ToolEntry) DisclosureMode {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.ContextWindowSize <= 0 {
		g.currentMode = DisclosureFull
		return g.currentMode
	}

	totalSchemaChars := totalSchemaSize(tools)
	threshold := int(float64(g.ContextWindowSize) * g.ThresholdFraction)

	if totalSchemaChars > threshold {
		g.currentMode = DisclosureBridge
	} else {
		g.currentMode = DisclosureFull
	}

	return g.currentMode
}

// Mode returns the current disclosure mode.
func (g *ContextPressureGate) Mode() DisclosureMode {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.currentMode
}

// FilterTools returns the set of tools that should be disclosed given the
// current mode. In full mode, all tools are returned. In bridge mode, only
// bridge tools and always-visible tools are returned.
func (g *ContextPressureGate) FilterTools(reg *Registry) []ToolEntry {
	g.mu.RLock()
	mode := g.currentMode
	alwaysVisible := g.alwaysVisibleCopy()
	g.mu.RUnlock()

	all := reg.All()

	if mode == DisclosureFull {
		// Full disclosure serves the real arsenal directly. Bridge tools are
		// the on-demand fallback -- they are only exposed under context
		// pressure (bridge mode). Leaking them into full mode would inflate
		// the toolset the model must carry for no benefit.
		out := make([]ToolEntry, 0, len(all))
		for _, e := range all {
			if !IsBridgeTool(e.Name) {
				out = append(out, e)
			}
		}
		return out
	}

	// Bridge mode: only bridge tools + always-visible tools.
	var filtered []ToolEntry
	for _, e := range all {
		if IsBridgeTool(e.Name) || alwaysVisible[e.Name] {
			filtered = append(filtered, e)
		}
	}
	return filtered
}

// SetAlwaysVisible marks tools that are always visible regardless of mode.
func (g *ContextPressureGate) SetAlwaysVisible(names ...string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, name := range names {
		g.AlwaysVisible[name] = true
	}
}

// Reset returns the gate to full disclosure mode.
func (g *ContextPressureGate) Reset() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.currentMode = DisclosureFull
}

// alwaysVisibleCopy returns a copy of the always-visible set.
func (g *ContextPressureGate) alwaysVisibleCopy() map[string]bool {
	out := make(map[string]bool, len(g.AlwaysVisible))
	maps.Copy(out, g.AlwaysVisible)
	return out
}

// totalSchemaSize estimates the total size in characters of the JSON
// schema representations for the given tools. This is a conservative
// estimate  --  the actual serialized size may be slightly larger.
func totalSchemaSize(tools []ToolEntry) int {
	total := 0
	for _, e := range tools {
		if e.Schema != nil {
			total += schemaSizeEstimate(e.Schema)
		}
		// Also account for name + description overhead.
		total += len(e.Name) + len(e.Description)
	}
	return total
}

// schemaSizeEstimate returns a rough character count for a schema value.
func schemaSizeEstimate(v any) int {
	switch val := v.(type) {
	case JSONSchema:
		n := 0
		for k, sv := range val {
			n += len(k) + schemaSizeEstimate(sv)
		}
		return n
	case map[string]any:
		n := 0
		for k, sv := range val {
			n += len(k) + schemaSizeEstimate(sv)
		}
		return n
	case []any:
		n := 0
		for _, item := range val {
			n += schemaSizeEstimate(item)
		}
		return n
	case string:
		return len(val)
	case float64:
		return 8
	case bool:
		return 5
	default:
		return 20 // conservative fallback
	}
}

// Ensure bridge tools implement the expected interface.
var _ = BridgeTools // prevent unused warning when bridge tools aren't registered
