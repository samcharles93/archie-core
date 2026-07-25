package memory

import (
	"fmt"

	"github.com/samcharles93/archie-core/internal/tools"
)

// NormalizedTool is the canonical internal representation of a memory
// provider's tool schema, produced by NormalizeToolSchema from either a
// bare or OpenAI-wrapped input.
type NormalizedTool struct {
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Parameters  tools.JSONSchema `json:"parameters,omitempty"`
}

// NormalizeToolSchema converts a provider-supplied tool schema into the
// canonical NormalizedTool representation. It accepts two input shapes:
//
//   - Bare: {"name": ..., "description": ..., "parameters": {...}}
//   - Wrapped (OpenAI-style): {"type": "function", "function": {"name": ...,
//     "description": ..., "parameters": {...}}}
//
// Normalization happens once, at provider registration time, since the
// shape a provider exports does not change between turns. Unknown extra
// fields are ignored. An error is returned when raw is nil, when a
// wrapped schema's "type" is not "function" or its "function" object is
// missing, or when the resolved name is missing or not a string.
func NormalizeToolSchema(raw map[string]any) (NormalizedTool, error) {
	if raw == nil {
		return NormalizedTool{}, fmt.Errorf("memory: cannot normalize nil tool schema")
	}

	body := raw
	if typ, present := raw["type"]; present {
		typStr, ok := typ.(string)
		if !ok || typStr != "function" {
			return NormalizedTool{}, fmt.Errorf("memory: unsupported tool schema type %v", typ)
		}
		fn, ok := raw["function"].(map[string]any)
		if !ok {
			return NormalizedTool{}, fmt.Errorf("memory: wrapped tool schema missing \"function\" object")
		}
		body = fn
	}

	name, ok := body["name"].(string)
	if !ok || name == "" {
		return NormalizedTool{}, fmt.Errorf("memory: tool schema missing required \"name\" field")
	}

	nt := NormalizedTool{Name: name}
	if desc, ok := body["description"].(string); ok {
		nt.Description = desc
	}
	if params, ok := body["parameters"].(map[string]any); ok {
		nt.Parameters = tools.JSONSchema(params)
	}

	return nt, nil
}
