package tools

import (
	"errors"
	"fmt"
	"sync"
)

// ErrDuplicateTool is returned by [Registry.Register] when a tool with the
// same name has already been registered.
var ErrDuplicateTool = errors.New("duplicate tool name")

// Registry is a thread-safe tool registry. It maps tool names to
// [ToolEntry] values and supports lookup by toolset and availability.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]ToolEntry
}

// NewRegistry creates a new, empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]ToolEntry),
	}
}

// Register adds a tool entry to the registry. It returns an error if the
// entry fails [ToolEntry.Validate], or if a tool with the same name is
// already registered (wrapping [ErrDuplicateTool]).
func (r *Registry) Register(e ToolEntry) error {
	return r.RegisterBatch([]ToolEntry{e})
}

// RegisterBatch adds all entries or none.
func (r *Registry) RegisterBatch(entries []ToolEntry) error {
	clones := make([]ToolEntry, len(entries))
	names := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if err := entry.Validate(); err != nil {
			return err
		}
		if _, exists := names[entry.Name]; exists {
			return fmt.Errorf("%w: %q", ErrDuplicateTool, entry.Name)
		}
		names[entry.Name] = struct{}{}
	}
	for i, entry := range entries {
		clones[i] = entry.Clone()
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	for name := range names {
		if _, exists := r.tools[name]; exists {
			return fmt.Errorf("%w: %q", ErrDuplicateTool, name)
		}
	}
	for _, entry := range clones {
		r.tools[entry.Name] = entry
	}
	return nil
}

// Unregister removes the named tools. Unknown names are ignored.
func (r *Registry) Unregister(names ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, name := range names {
		delete(r.tools, name)
	}
}

// Get returns the tool entry registered under name, and whether it was
// found. The returned entry is a defensive copy  --  mutating it does not
// affect the registry.
func (r *Registry) Get(name string) (ToolEntry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	e, ok := r.tools[name]
	if !ok {
		return ToolEntry{}, false
	}
	return e.Clone(), true
}

// All returns all registered tool entries. The returned slice is a
// snapshot  --  mutating it does not affect the registry.
func (r *Registry) All() []ToolEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]ToolEntry, 0, len(r.tools))
	for _, e := range r.tools {
		out = append(out, e.Clone())
	}
	return out
}

// ByToolset returns tools belonging to the given toolset. Pass an empty
// string to match tools that have no toolset.
func (r *Registry) ByToolset(toolset string) []ToolEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var out []ToolEntry
	for _, e := range r.tools {
		if e.Toolset == toolset {
			out = append(out, e.Clone())
		}
	}
	return out
}

// Available returns tools whose [ToolEntry.Available] method returns true.
//
// CheckFn is evaluated outside the registry lock to avoid deadlock if
// the function re-enters the registry (e.g. calling Register or All).
func (r *Registry) Available() []ToolEntry {
	r.mu.RLock()
	snapshot := make([]ToolEntry, 0, len(r.tools))
	for _, e := range r.tools {
		snapshot = append(snapshot, e.Clone())
	}
	r.mu.RUnlock()

	// Evaluate CheckFn outside the lock  --  no deadlock risk.
	var out []ToolEntry
	for _, e := range snapshot {
		if e.Available() {
			out = append(out, e)
		}
	}
	return out
}
