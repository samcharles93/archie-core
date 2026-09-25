package agentexec

import "context"

// AdapterClaudeCode names the Claude Code stream-json output adapter.
const AdapterClaudeCode = "claude-code"

// HarnessAdapter is what archie knows about one CLI: how to read its output
// stream and how to register archie's MCP server with it.
type HarnessAdapter struct {
	NewOutput func() HarnessOutput
	MCPConfig []string
}

var harnessAdapters = map[string]HarnessAdapter{
	AdapterClaudeCode: {
		NewOutput: func() HarnessOutput { return newClaudeCodeHarnessOutput() },
		MCPConfig: []string{"--mcp-config", mcpConfigPlaceholder},
	},
}

// LookupHarnessAdapter returns the named adapter. The empty name is a CLI
// with no adapter: it reports nothing and serves no capture tools.
func LookupHarnessAdapter(name string) (HarnessAdapter, bool) {
	a, ok := harnessAdapters[name]
	return a, ok
}

// HarnessStages runs every stage of a task on one harness: a Kit task's
// container is built from its harness, so no stage in it runs anywhere else.
type HarnessStages struct {
	Runner Runner
	Spec   HarnessSpec
}

func (r HarnessStages) Run(ctx context.Context, workspace string, req Request, report ToolCallReporter) (Result, error) {
	spec := r.Spec
	req.Harness = &spec
	return r.Runner.Run(ctx, workspace, req, report)
}
