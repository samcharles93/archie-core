package webui

import (
	"net/http"

	"github.com/samcharles93/archie-core/internal/memory"
)

// MemoryProviderView describes one memory provider for the dashboard.
type MemoryProviderView struct {
	Name string `json:"name"`
	// Role is "builtin" or "external". Archie always has a builtin provider;
	// an external one is optional and configured separately.
	Role string `json:"role"`
	// Available reports the provider's own readiness check -- a provider can
	// be configured but unusable when a binary is missing or a connection is
	// down, and that distinction is exactly what an operator needs to see.
	Available bool `json:"available"`
	// Tools are the capabilities this provider gives the agent, by name.
	Tools []MemoryToolView `json:"tools"`
}

// MemoryToolView is one tool a provider exposes to the agent.
type MemoryToolView struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// handleMemory reports what Archie can remember and with what.
//
// Memory is otherwise invisible: it is a set of tools handed to the model at
// discovery time, so without this the only way to know what the agent can
// recall is to read the provider source.
func (s *Server) handleMemory(w http.ResponseWriter, r *http.Request) {
	if s.Memory == nil {
		writeJSON(w, map[string]any{"providers": []any{}, "enabled": false})
		return
	}

	views := []MemoryProviderView{}
	if p := s.Memory.Builtin(); p != nil {
		views = append(views, providerView(p, "builtin"))
	}
	if p := s.Memory.External(); p != nil {
		views = append(views, providerView(p, "external"))
	}

	writeJSON(w, map[string]any{"providers": views, "enabled": true})
}

func providerView(p memory.MemoryProvider, role string) MemoryProviderView {
	view := MemoryProviderView{
		Name:      p.Name(),
		Role:      role,
		Available: p.IsAvailable(),
		Tools:     []MemoryToolView{},
	}
	for _, t := range p.GetToolSchemas() {
		view.Tools = append(view.Tools, MemoryToolView{
			Name:        t.Name,
			Description: t.Description,
		})
	}
	return view
}
