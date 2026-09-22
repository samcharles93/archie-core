// Lifted from tau internal/agent/tools/registry.go
// tau commit f5289ea3782c099339c2d26fe3af8ebcf42ba52d (2026-07-27).
//
// Mutations from upstream:
//   - package renamed tools -> builtin (archie-core already has an
//     internal/tools package holding the registry these are registered into).
//
// Refresh by diffing against that path at a newer tau commit. Do not
// edit without recording the change above.
// Package tools provides the tool registry and built-in tool implementations
// for the Tau agent coordinator.
package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Schema describes a tool's interface for LLM function-calling.
type Schema struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"` // JSON Schema object
}

// Result is the output of a tool execution.
type Result struct {
	Content   string `json:"content"`
	Details   any    `json:"details,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`
	ErrorKind string `json:"error_kind,omitempty"`
	// MetricLabels adds tool-specific low-cardinality dimensions to the
	// coordinator's authoritative completion metric.
	MetricLabels map[string]string `json:"-"`

	// Execution fields are populated by the coordinator after execution so
	// the same facts drive events, metrics, and persisted tool messages.
	Duration    time.Duration `json:"duration,omitempty"`
	ResultBytes int           `json:"result_bytes,omitempty"`
	Truncated   bool          `json:"truncated,omitempty"`
	StartedAt   time.Time     `json:"started_at,omitzero"`
	CompletedAt time.Time     `json:"completed_at,omitzero"`
}

// DiffDetails is carried in Result.Details by tools that replace file
// content (edit, write), so callers such as the TUI can render a
// before/after diff instead of just a summary string.
type DiffDetails struct {
	Path       string `json:"path"`
	OldContent string `json:"old_content"`
	NewContent string `json:"new_content"`
}

// Executor is a function that executes a tool with the given parameters.
type Executor func(ctx context.Context, params json.RawMessage, ui UIBridge) (Result, error)

// Tool is a registered tool comprising its schema and executor.
type Tool struct {
	Schema  Schema
	Execute Executor
	Source  string // "builtin", "extension:<name>"
	// Emoji is the icon chat surfaces show for this tool. It is deliberately
	// not part of Schema: Schema is serialised to the model as the tool's
	// contract, and an icon is presentation. Empty means no icon.
	Emoji string
}

// Registry holds all registered tools and provides thread-safe access.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
	order []string // insertion order for deterministic iteration
}

// NewRegistry creates an empty tool registry.
func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]Tool),
	}
}

// Register adds a tool to the registry. Returns an error if a tool with
// the same name is already registered (use Replace for overrides).
func (r *Registry) Register(tool Tool) error {
	if tool.Schema.Name == "" {
		return errors.New("tool name is required")
	}
	if tool.Execute == nil {
		return errors.New("tool executor is required")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.tools[tool.Schema.Name]; exists {
		return fmt.Errorf("tool %q is already registered", tool.Schema.Name)
	}

	r.tools[tool.Schema.Name] = tool
	r.order = append(r.order, tool.Schema.Name)
	return nil
}

// Replace registers a tool, overriding any existing tool with the same name.
func (r *Registry) Replace(tool Tool) error {
	if tool.Schema.Name == "" {
		return errors.New("tool name is required")
	}
	if tool.Execute == nil {
		return errors.New("tool executor is required")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.tools[tool.Schema.Name]; !exists {
		r.order = append(r.order, tool.Schema.Name)
	}
	r.tools[tool.Schema.Name] = tool
	return nil
}

// Unregister removes a tool from the registry.
func (r *Registry) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.tools, name)
	for i, n := range r.order {
		if n == name {
			r.order = append(r.order[:i], r.order[i+1:]...)
			break
		}
	}
}

// Get retrieves a tool by name.
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	tool, ok := r.tools[name]
	return tool, ok
}

// All returns all registered tools in insertion order.
func (r *Registry) All() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]Tool, 0, len(r.order))
	for _, name := range r.order {
		if tool, ok := r.tools[name]; ok {
			result = append(result, tool)
		}
	}
	return result
}

// Schemas returns the schemas of all registered tools in insertion order.
// This is the slice sent to the LLM in the tools[] field.
func (r *Registry) Schemas() []Schema {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]Schema, 0, len(r.order))
	for _, name := range r.order {
		if tool, ok := r.tools[name]; ok {
			result = append(result, tool.Schema)
		}
	}
	return result
}

// Names returns the names of all registered tools.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]string, len(r.order))
	copy(out, r.order)
	return out
}

// Count returns the number of registered tools.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return len(r.tools)
}
