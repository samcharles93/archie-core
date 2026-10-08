// Package minimax exposes MiniMax video generation as the generate_video
// tool. The HTTP and polling behaviour lives in ai-sdk's
// provider/minimax; this package only shapes the tool schema and maps a tool
// call onto video.Provider.
package minimax

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/samcharles93/ai-sdk/video"

	"github.com/samcharles93/archie-core/internal/tools"
)

// ToolName is the registry name of the video-generation tool.
const ToolName = "generate_video"

// Tool returns the generate_video tool backed by p, or nil when p is nil.
func Tool(p video.Provider) *tools.ToolEntry {
	if p == nil {
		return nil
	}

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
					"description": "Aspect ratio. Defaults to 16:9.",
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

			req := video.GenerateVideoRequest{
				Prompt:     prompt,
				Resolution: resolution,
				Ratio:      ratio,
			}
			// Leave Duration empty when unset so the provider applies its own
			// default; "0" would be clamped to the 4s floor instead of 6s.
			if d, ok := input["duration"].(float64); ok && d > 0 {
				req.Duration = strconv.Itoa(int(d))
			}

			resp, err := p.GenerateVideo(ctx, req)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", ToolName, err)
			}
			if len(resp.Videos) == 0 || resp.Videos[0].URL == "" {
				return nil, fmt.Errorf("%s: provider returned no video", ToolName)
			}

			return tools.MultimodalResult{
				IsMultimodal: true,
				Summary:      "Generated a video from the prompt.",
				URLs:         []tools.MediaRef{{Type: "video", URL: resp.Videos[0].URL}},
			}, nil
		},
	}
}
