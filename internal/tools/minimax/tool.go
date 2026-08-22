package minimax

import (
	"context"
	"fmt"
	"strings"

	"github.com/samcharles93/archie-core/internal/tools"
)

// ToolName is the registry name of the video-generation tool.
const ToolName = "generate_video"

// Tool builds the registry entry for cfg, or nil when disabled  --  the
// same "withdraw entirely rather than advertise a broken capability"
// reasoning webfetch.Tool documents.
//
// Classified ClassMutating, not RequiresApproval: it creates a hosted
// resource and spends real API credits, which is a mutating action, but
// gating every call behind human approval would make the tool unusable
// for its purpose. Revisit if usage shows this needs a spend guard.
func Tool(cfg Config) *tools.ToolEntry {
	if !cfg.Enabled {
		return nil
	}

	client := New(cfg)

	return &tools.ToolEntry{
		Name:    ToolName,
		Toolset: "media",
		Description: "Generate a short video from a text prompt using MiniMax. " +
			"Generation runs synchronously and can take several minutes; the call " +
			"blocks until the video is ready or generation fails. Returns a hosted " +
			"URL for the finished video.",
		Classification: tools.ClassMutating,
		Schema: tools.JSONSchema{
			"type": "object",
			"properties": map[string]any{
				"prompt": map[string]any{
					"type":        "string",
					"description": "Description of the video to generate. Up to 7000 characters.",
				},
				"resolution": map[string]any{
					"type":        "string",
					"enum":        []any{"768P", "2K"},
					"description": "Video resolution. Defaults to 768P.",
				},
				"duration": map[string]any{
					"type":        "integer",
					"minimum":     4,
					"maximum":     15,
					"description": "Clip length in seconds, 4-15. Defaults to 6.",
				},
				"ratio": map[string]any{
					"type":        "string",
					"enum":        []any{"adaptive", "21:9", "16:9", "4:3", "1:1", "3:4", "9:16"},
					"description": "Aspect ratio. Defaults to adaptive.",
				},
			},
			"required": []any{"prompt"},
		},
		Handler: func(ctx context.Context, input map[string]any) (any, error) {
			prompt, _ := input["prompt"].(string)
			if strings.TrimSpace(prompt) == "" {
				return nil, fmt.Errorf("%s: prompt is required", ToolName)
			}
			resolution, _ := input["resolution"].(string)
			ratio, _ := input["ratio"].(string)
			duration := 0
			if d, ok := input["duration"].(float64); ok {
				duration = int(d)
			}

			url, err := client.GenerateAndWait(ctx, GenerateRequest{
				Prompt:     prompt,
				Resolution: resolution,
				Duration:   duration,
				Ratio:      ratio,
			})
			if err != nil {
				return nil, fmt.Errorf("%s: %w", ToolName, err)
			}

			return tools.MultimodalResult{
				IsMultimodal: true,
				Summary:      "Generated a video from the prompt.",
				URLs:         []tools.MediaRef{{Type: "video", URL: url}},
			}, nil
		},
	}
}
