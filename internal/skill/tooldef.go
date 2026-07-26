package skill

import (
	"context"
	"fmt"

	"github.com/samcharles93/archie-core/internal/tools"
)

// ActivateTool bridges a skill catalog into a single tools.ToolEntry a
// chat turn's agent loop can offer the model: one tool named
// "skill_activate", not one per skill  --  this is the same progressive
// disclosure Catalog/LoadBody already implement (Tier 1 catalog entries
// cost ~100 tokens each and are always in context via the tool schema;
// Tier 2 full bodies are loaded only when the model actually activates a
// skill). Returns nil when catalog is empty, so an empty skills
// directory doesn't add a dead tool to the set.
func ActivateTool(dir string, catalog []CatalogEntry) *tools.ToolEntry {
	if len(catalog) == 0 {
		return nil
	}

	names := make([]any, len(catalog))
	descByName := make(map[string]string, len(catalog))
	dirByName := make(map[string]string, len(catalog))
	for i, e := range catalog {
		names[i] = e.Name
		descByName[e.Name] = e.Description
		dirByName[e.Name] = e.Dir
	}

	return &tools.ToolEntry{
		Name:        "skill_activate",
		Toolset:     "skill",
		Description: "Load the full instructions for a named skill. Call this before attempting a task a cataloged skill covers.",
		Schema: tools.JSONSchema{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "The skill name to activate, from the catalog below.",
					"enum":        names,
				},
			},
			"required": []any{"name"},
		},
		Handler: func(_ context.Context, input map[string]any) (any, error) {
			name, _ := input["name"].(string)
			if name == "" {
				return nil, fmt.Errorf("skill_activate: %q is required", "name")
			}
			skillDir, ok := dirByName[name]
			if !ok {
				return nil, fmt.Errorf("skill_activate: unknown skill %q", name)
			}
			body := LoadBody(dir, skillDir)
			if body == "" {
				return nil, fmt.Errorf("skill_activate: %q has no body (SKILL.md missing or empty)", name)
			}
			return body, nil
		},
	}
}
