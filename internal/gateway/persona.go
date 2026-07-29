package gateway

import "sync"

// Persona is a named communication style that modifies the system prompt.
type Persona struct {
	Name   string
	Prompt string // prepended to the system prompt
}

// PersonaRegistry holds available personas and tracks the active one
// per session. Safe for concurrent use.
type PersonaRegistry struct {
	mu          sync.RWMutex
	personas    map[string]Persona
	active      map[string]string // sessionKey -> persona name
	defaultName string
}

// NewPersonaRegistry creates a registry pre-populated with the given
// personas. The first persona is the default for new sessions.
func NewPersonaRegistry(personas []Persona) *PersonaRegistry {
	r := &PersonaRegistry{
		personas: make(map[string]Persona, len(personas)),
		active:   make(map[string]string),
	}
	for index, p := range personas {
		r.personas[p.Name] = p
		if index == 0 {
			r.defaultName = p.Name
		}
	}
	return r
}

// DefaultPersonas returns Archie as the baseline identity followed by
// optional communication styles.
func DefaultPersonas() []Persona {
	return []Persona{
		{
			Name: "archie",
			Prompt: "You are Archie, a capable coding and project assistant. " +
				"Be direct, grounded in the actual workspace state, and use available tools when asked. " +
				"Do not claim tools, files, memories, actions, or results you have not verified. " +
				"Do not impersonate another assistant, provider, or vendor.",
		},
		{Name: "helpful", Prompt: "You are a helpful, friendly AI assistant."},
		{Name: "concise", Prompt: "You are a concise assistant. Keep responses brief and to the point."},
		{Name: "technical", Prompt: "You are a technical expert. Provide detailed, accurate technical information."},
		{Name: "creative", Prompt: "You are a creative assistant. Think outside the box and offer innovative solutions."},
		{Name: "teacher", Prompt: "You are a patient teacher. Explain concepts clearly with examples."},
	}
}

// List returns all available persona names.
func (r *PersonaRegistry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.personas))
	for _, p := range r.personas {
		names = append(names, p.Name)
	}
	return names
}

// SetActive sets the active persona for a session.
func (r *PersonaRegistry) SetActive(sessionKey, name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.personas[name]; !ok {
		return false
	}
	r.active[sessionKey] = name
	return true
}

// GetActive returns the active persona's prompt for a session, or an
// empty string when no persona is selected.
func (r *PersonaRegistry) GetActive(sessionKey string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	name, ok := r.active[sessionKey]
	if !ok {
		name = r.defaultName
	}
	p, ok := r.personas[name]
	if !ok {
		return ""
	}
	if name == r.defaultName {
		return p.Prompt
	}
	if baseline, ok := r.personas[r.defaultName]; ok && baseline.Prompt != "" {
		return baseline.Prompt + "\n\n" + p.Prompt
	}
	return p.Prompt
}

// Get returns a persona by name, or false when not found.
func (r *PersonaRegistry) Get(name string) (Persona, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.personas[name]
	return p, ok
}
