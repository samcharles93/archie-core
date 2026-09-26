package agentexec

import "encoding/json"

// copilotHarnessOutput reads GitHub Copilot CLI's `--output-format json`.
// The stream carries no token counts, so a Copilot run reports no usage and
// its budget is wall clock.
type copilotHarnessOutput struct {
	sessionID string
	pending   map[string]string
}

func newCopilotHarnessOutput() *copilotHarnessOutput {
	return &copilotHarnessOutput{pending: make(map[string]string)}
}

func (o *copilotHarnessOutput) Line(line []byte, report ToolCallReporter) {
	var event struct {
		Type      string `json:"type"`
		SessionID string `json:"sessionId"`
		Data      struct {
			ToolCallID  string `json:"toolCallId"`
			ToolName    string `json:"toolName"`
			MCPToolName string `json:"mcpToolName"`
			Success     bool   `json:"success"`
			Result      struct {
				Content string `json:"content"`
			} `json:"result"`
		} `json:"data"`
	}
	if json.Unmarshal(line, &event) != nil {
		return
	}
	switch event.Type {
	case "result":
		o.sessionID = event.SessionID
	case "tool.execution_start":
		name := event.Data.MCPToolName
		if name == "" {
			name = event.Data.ToolName
		}
		if event.Data.ToolCallID != "" && name != "" {
			o.pending[event.Data.ToolCallID] = name
		}
	case "tool.execution_complete":
		tool, ok := o.pending[event.Data.ToolCallID]
		if !ok {
			return
		}
		delete(o.pending, event.Data.ToolCallID)
		if report != nil {
			report(ToolCallReport{Tool: tool, Detail: clipToolCallDetail(event.Data.Result.Content), Failed: !event.Data.Success})
		}
	}
}

func (o *copilotHarnessOutput) SessionID() string { return o.sessionID }
func (o *copilotHarnessOutput) Usage() Usage      { return Usage{} }
