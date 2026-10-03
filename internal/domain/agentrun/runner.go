package agentrun

import "context"

// Runner executes one autonomous stage against an already prepared workspace.
type Runner interface {
	// report, when non-nil, is notified once per completed tool call.
	Run(ctx context.Context, workspace string, req Request, report ToolCallReporter) (Result, error)
}

// ToolCallReport is one completed tool invocation, reported through a
// ToolCallReporter.
type ToolCallReport struct {
	Tool string
	// Detail is a clipped one-line summary: the tool's result, or
	// "error: <message>" if it failed.
	Detail string
	Failed bool
}

// ToolCallReporter receives one ToolCallReport per completed tool call. A nil
// reporter is valid and reports nothing.
type ToolCallReporter func(ToolCallReport)
