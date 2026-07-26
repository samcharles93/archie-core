package agentexec

import (
	"context"
	"encoding/json"
	"fmt"

	aicore "github.com/samcharles93/ai-sdk/core"

	"github.com/samcharles93/archie-core/internal/tools"
)

// BuildToolSet converts every currently-available entry in reg into an
// ai-sdk core.ToolSet, ready to pass as core.GenerateOptions.Tools for a
// multi-step chat turn. A nil registry (memory/tools disabled) returns
// an empty set. Invalid availability callbacks and dynamic schemas return
// errors instead of panicking or silently hiding an advertised tool.
//
// core.Tool.Execute exchanges JSON-encoded strings with the model;
// tools.Handler exchanges a decoded map. The returned Execute functions
// do the JSON <-> map translation and surface decode failures as errors
// rather than silently dropping malformed model input.
func BuildToolSet(reg *tools.Registry) (aicore.ToolSet, error) {
	set := aicore.ToolSet{}
	if reg == nil {
		return set, nil
	}
	for _, entry := range reg.All() {
		available, err := safeToolAvailable(entry)
		if err != nil {
			return nil, fmt.Errorf("tool %s availability: %w", entry.Name, err)
		}
		if !available {
			continue
		}
		schema, err := safeResolvedSchema(entry)
		if err != nil {
			return nil, fmt.Errorf("tool %s schema: %w", entry.Name, err)
		}
		if schema == nil {
			schema = tools.JSONSchema{
				"type":       "object",
				"properties": map[string]any{},
			}
		}
		params, err := json.Marshal(schema)
		if err != nil {
			return nil, fmt.Errorf("tool %s schema: %w", entry.Name, err)
		}
		set[entry.Name] = aicore.NewTool(entry.Name, entry.Description, params, toolExecute(entry))
	}
	return set, nil
}

func safeToolAvailable(entry tools.ToolEntry) (available bool, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("panic: %v", recovered)
		}
	}()
	return entry.Available(), nil
}

func safeResolvedSchema(entry tools.ToolEntry) (schema tools.JSONSchema, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("panic: %v", recovered)
		}
	}()
	return entry.ResolvedSchema(), nil
}

// toolExecute adapts a tools.ToolEntry's Handler to core.Tool's
// Execute(ctx, jsonInput string) (jsonOutput string, err error) shape.
func toolExecute(entry tools.ToolEntry) func(context.Context, string) (string, error) {
	return func(ctx context.Context, input string) (string, error) {
		var args map[string]any
		if input != "" {
			if err := json.Unmarshal([]byte(input), &args); err != nil {
				return "", fmt.Errorf("tool %s: decode input: %w", entry.Name, err)
			}
		}
		out, err := entry.Handler(ctx, args)
		if err != nil {
			return "", err
		}
		data, err := json.Marshal(out)
		if err != nil {
			return "", fmt.Errorf("tool %s: encode output: %w", entry.Name, err)
		}
		return string(data), nil
	}
}
