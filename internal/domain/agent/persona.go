package agent

import (
	"fmt"
	"strings"
)

type Persona struct {
	Name   string `json:"name"`
	Prompt string `json:"prompt"`
}

type PersonaCollection struct {
	Default  string    `json:"default"`
	Personas []Persona `json:"personas"`
}

func (c PersonaCollection) Validate() error {
	if len(c.Personas) == 0 {
		return fmt.Errorf("at least one persona is required")
	}
	defaultName := normalizePersonaName(c.Default)
	seen := make(map[string]struct{}, len(c.Personas))
	for _, persona := range c.Personas {
		name := normalizePersonaName(persona.Name)
		if name == "" || strings.TrimSpace(persona.Prompt) == "" {
			return fmt.Errorf("persona name and prompt are required")
		}
		if _, exists := seen[name]; exists {
			return fmt.Errorf("duplicate persona %q", name)
		}
		seen[name] = struct{}{}
	}
	if _, exists := seen[defaultName]; !exists {
		return fmt.Errorf("default persona %q does not exist", c.Default)
	}
	return nil
}

func normalizePersonaName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func ShippedPersonas() PersonaCollection {
	return PersonaCollection{Default: "archie", Personas: []Persona{
		{Name: "archie", Prompt: "You are Archie, a capable coding and project assistant. Be direct without being brusque, grounded in the actual workspace state, and use available tools when asked. Do not claim tools, files, memories, actions, or results you have not verified. Do not impersonate another assistant, provider, or vendor."},
		{Name: "helpful", Prompt: "You are a helpful, friendly AI assistant."},
		{Name: "concise", Prompt: "You are a concise assistant. Keep responses brief and to the point."},
		{Name: "technical", Prompt: "You are a technical expert. Provide detailed, accurate technical information."},
		{Name: "creative", Prompt: "You are a creative assistant. Think outside the box and offer innovative solutions."},
		{Name: "teacher", Prompt: "You are a patient teacher. Explain concepts clearly with examples."},
	}}
}
