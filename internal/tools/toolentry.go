// Package tools is the central tool subsystem for archie-core. It defines
// the ToolEntry data type that every tool  --  built-in, MCP, or plugin  --
// registers with. The registry (17.2) and guardrails (17.10–17.15) are
// built on top of this type.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
)

// JSONSchema represents a JSON Schema document describing a tool's input
// parameters. The zero value (nil) means the tool takes no parameters.
type JSONSchema map[string]any

// Handler is the function signature for executing a tool. It receives the
// caller-provided input (deserialized from JSON) and returns structured
// output. The returned error is surfaced to the caller; the output is
// serialized to JSON and included in the tool result.
type Handler func(ctx context.Context, input map[string]any) (output any, err error)

// CheckFn is a predicate that determines whether a tool is currently
// available. A nil CheckFn means the tool is always available. Return
// false to hide the tool from discovery and reject invocations.
type CheckFn func() bool

// SchemaOverrideFn mutates a tool's JSON Schema at runtime. Use it to
// inject dynamic values  --  for example, enumerating MCP server names or
// filesystem paths that aren't known until the daemon starts. A nil
// override means the schema is static.
type SchemaOverrideFn func(schema JSONSchema) JSONSchema

// ToolEntry is the foundational data type for the tools subsystem. Every
// tool  --  built-in, MCP server, plugin-provided, or dynamically registered
//
//	--  is represented by a ToolEntry. Fields with `json:"-"` tags are
//
// runtime-only (not serialized).
type ToolEntry struct {
	// Name uniquely identifies this tool (e.g. "read_file", "write_file",
	// "mcp.github.search_repos"). Must be non-empty.
	Name string `json:"name"`

	// Toolset groups related tools together (e.g. "file", "git", "mcp",
	// "memory", "http"). Used for progressive disclosure and guardrail
	// classification. May be empty for ungrouped tools.
	Toolset string `json:"toolset,omitempty"`

	// Schema is the JSON Schema for the tool's input parameters. The
	// schema is served to LLMs at tool-discovery time so they know how to
	// construct valid invocations. nil means no parameters.
	Schema JSONSchema `json:"schema,omitempty"`

	// Handler executes the tool. It is a runtime field (not serialized).
	// Must be non-nil for the tool to be functional.
	Handler Handler `json:"-"`

	// CheckFn reports whether the tool is currently available  --  for
	// example, an MCP server might be offline, or a required binary might
	// not be installed. A nil CheckFn means always available.
	CheckFn CheckFn `json:"-"`

	// RequiresEnv lists environment variable names the tool needs at
	// runtime (e.g. "GITHUB_TOKEN", "DOCKER_HOST"). Registration logs a
	// warning for each missing variable but does not reject the tool
	// (the check only matters at invocation time).
	RequiresEnv []string `json:"requires_env,omitempty"`

	// Classification is a bitmask describing the tool's behavioral
	// characteristics (idempotent, mutating, etc.). Zero means no
	// special classification. See [ToolClassification] for valid flags.
	Classification ToolClassification `json:"classification,omitempty"`

	// IsAsync distinguishes synchronous tools (complete immediately) from
	// asynchronous ones (start a background operation and return a handle
	// for polling). Default (false) is synchronous.
	IsAsync bool `json:"is_async"`

	// Description is a human-readable explanation of what the tool does,
	// shown in tool-discovery listings. Keep to one sentence.
	Description string `json:"description"`

	// Emoji is a short visual indicator for the tool (e.g. "📁", "🔧",
	// "🌐"). Single character or short sequence. May be empty.
	Emoji string `json:"emoji,omitempty"`

	// BuildApprovalDescription is called with the decoded tool inputs to
	// build the human-facing prompt shown when approval is required. When
	// nil (the default), the tool's Description is used verbatim.
	//
	// The returned string should identify the specific resource being acted
	// on so the human knows what they are consenting to. A prompt that says
	// only "Delete a session" is not adequate; "Delete session 'Refactor
	// auth' (abc12345)" is. This runs before the handler, so only the raw
	// input is available — it cannot resolve references or look up titles.
	BuildApprovalDescription func(input map[string]any) string `json:"-"`

	// MaxResultSizeChars caps this tool's result size in characters.
	// Zero means use the global default from [ToolPolicy.MaxResultChars].
	MaxResultSizeChars int `json:"max_result_size_chars,omitempty"`

	// DynamicSchemaOverrides mutates the schema at discovery time. Use
	// it to inject dynamic enum values, current paths, or live service
	// lists. A nil override means the schema is served as-is.
	DynamicSchemaOverrides SchemaOverrideFn `json:"-"`
}

// Validate checks the entry's required fields. It returns nil when the
// entry can be registered; otherwise an error describing the problem.
func (e ToolEntry) Validate() error {
	if e.Name == "" {
		return fmt.Errorf("ToolEntry.Name is required")
	}
	if e.Handler == nil {
		return fmt.Errorf("ToolEntry.Handler is required (tool %q)", e.Name)
	}
	return nil
}

// Available reports whether the tool is currently usable. Returns true
// when CheckFn is nil or returns true.
func (e ToolEntry) Available() bool {
	if e.CheckFn == nil {
		return true
	}
	return e.CheckFn()
}

// ResolvedSchema returns the tool's schema after applying any dynamic
// overrides. When DynamicSchemaOverrides is nil the static schema is
// returned unchanged.
func (e ToolEntry) ResolvedSchema() JSONSchema {
	if e.DynamicSchemaOverrides == nil {
		return e.Schema
	}
	return e.DynamicSchemaOverrides(e.Schema)
}

// Clone returns a deep copy of the entry. Handler, CheckFn, and
// DynamicSchemaOverrides are shared (not deep-copied). Schema is
// recursively deep-copied so that nested maps and slices are
// independent from the original.
func (e ToolEntry) Clone() ToolEntry {
	clone := e
	if e.RequiresEnv != nil {
		clone.RequiresEnv = make([]string, len(e.RequiresEnv))
		copy(clone.RequiresEnv, e.RequiresEnv)
	}
	if e.Schema != nil {
		clone.Schema = deepCopySchema(e.Schema)
	}
	return clone
}

// deepCopySchema recursively copies a JSONSchema value, ensuring nested
// maps and slices are independent from the source.
func deepCopySchema(s JSONSchema) JSONSchema {
	out := make(JSONSchema, len(s))
	for k, v := range s {
		out[k] = deepCopyAny(v)
	}
	return out
}

// deepCopyAny copies a value that may be a nested map, slice, or scalar.
func deepCopyAny(v any) any {
	switch val := v.(type) {
	case JSONSchema:
		return deepCopySchema(val)
	case map[string]any:
		out := make(map[string]any, len(val))
		for mk, mv := range val {
			out[mk] = deepCopyAny(mv)
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, item := range val {
			out[i] = deepCopyAny(item)
		}
		return out
	case []string:
		out := make([]string, len(val))
		copy(out, val)
		return out
	case []float64:
		out := make([]float64, len(val))
		copy(out, val)
		return out
	case []bool:
		out := make([]bool, len(val))
		copy(out, val)
		return out
	default:
		return v
	}
}

// MarshalJSON implements json.Marshaler, stripping runtime-only fields so
// that a ToolEntry can be serialized for debugging without panicking on
// the function fields.
func (e ToolEntry) MarshalJSON() ([]byte, error) {
	type wire ToolEntry // avoid recursion
	return json.Marshal(wire(e))
}
