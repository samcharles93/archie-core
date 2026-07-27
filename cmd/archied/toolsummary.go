package main

import (
	"slices"
	"strings"

	aicore "github.com/samcharles93/ai-sdk/core"

	"github.com/samcharles93/archie-core/internal/gateway"
)

// toolSummaries converts the toolset handed to the model into the capability
// metadata advertised in its system prompt, so the two can never disagree.
//
// core.ToolSet is a map, so the result is sorted by name: an unstable tool
// order would change the system prompt on every turn and defeat provider-side
// prompt caching.
func toolSummaries(set aicore.ToolSet) []gateway.ToolSummary {
	summaries := make([]gateway.ToolSummary, 0, len(set))
	for name, tool := range set {
		if tool == nil {
			continue
		}
		summaries = append(summaries, gateway.ToolSummary{
			Name:        name,
			Description: tool.Description,
		})
	}
	if len(summaries) == 0 {
		return nil
	}
	slices.SortFunc(summaries, func(a, b gateway.ToolSummary) int {
		return strings.Compare(a.Name, b.Name)
	})
	return summaries
}
