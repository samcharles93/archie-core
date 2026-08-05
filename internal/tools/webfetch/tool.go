package webfetch

import (
	"context"
	"fmt"
	"strings"

	"github.com/samcharles93/archie-core/internal/tools"
)

// ToolName is the registry name of the fetch tool.
const ToolName = "web_fetch"

// Tool builds the registry entry for cfg, or nil when the fetcher is
// disabled. Returning nil rather than an entry that always refuses keeps a
// capability the operator turned off out of the model's tool list entirely --
// an advertised tool that never works costs a round trip to discover.
func Tool(cfg Config) *tools.ToolEntry {
	if !cfg.Enabled {
		return nil
	}

	client := New(cfg)

	return &tools.ToolEntry{
		Name:    ToolName,
		Toolset: "web",
		Description: "Fetch a web page or API response over http/https and return its readable text. " +
			"HTML is reduced to text; JSON, XML and plain text are returned as received. " +
			"Use it to read documentation, changelogs, issues or any URL the user provides.",
		// Reading a URL has no side effect on this host, so it is safe to
		// run alongside other reads.
		Classification: tools.ClassIdempotent,
		Schema: tools.JSONSchema{
			"type": "object",
			"properties": map[string]any{
				"url": map[string]any{
					"type":        "string",
					"description": "Absolute http:// or https:// URL to fetch.",
				},
				"raw": map[string]any{
					"type":        "boolean",
					"description": "Return the response body unchanged instead of extracting readable text from HTML. Defaults to false.",
				},
			},
			"required": []any{"url"},
		},
		Handler: func(ctx context.Context, input map[string]any) (any, error) {
			target, _ := input["url"].(string)
			if strings.TrimSpace(target) == "" {
				return nil, fmt.Errorf("%s: url is required", ToolName)
			}
			raw, _ := input["raw"].(bool)

			body, err := client.Fetch(ctx, target, raw)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", ToolName, err)
			}
			return body, nil
		},
	}
}
