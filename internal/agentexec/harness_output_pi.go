package agentexec

import (
	"encoding/json"
	"strings"
)

// piHarnessOutput reads `--mode json` from Pi and OMP, which share the
// event stream. Usage is summed from each assistant message_end, so an
// interrupted run still reports what it spent.
type piHarnessOutput struct {
	sessionID string
	usage     Usage
}

func (o *piHarnessOutput) Line(line []byte, report ToolCallReporter) {
	var event struct {
		Type     string `json:"type"`
		ID       string `json:"id"`
		ToolName string `json:"toolName"`
		IsError  bool   `json:"isError"`
		Result   struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
		Message struct {
			Role  string `json:"role"`
			Usage *struct {
				Input      int `json:"input"`
				Output     int `json:"output"`
				CacheRead  int `json:"cacheRead"`
				CacheWrite int `json:"cacheWrite"`
			} `json:"usage"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &event) != nil {
		return
	}
	switch event.Type {
	case "session":
		o.sessionID = event.ID
	case "message_end":
		// Pi's input excludes cache reads and writes, as Claude's does.
		if u := event.Message.Usage; event.Message.Role == "assistant" && u != nil {
			o.usage = addUsage(o.usage, usageFromFields(u.Input, u.Output, u.CacheRead, u.CacheWrite))
		}
	case "tool_execution_end":
		if event.ToolName == "" || report == nil {
			return
		}
		texts := make([]string, 0, len(event.Result.Content))
		for _, block := range event.Result.Content {
			if block.Type == "text" {
				texts = append(texts, block.Text)
			}
		}
		report(ToolCallReport{Tool: event.ToolName, Detail: clipToolCallDetail(strings.Join(texts, "\n")), Failed: event.IsError})
	}
}

func (o *piHarnessOutput) SessionID() string { return o.sessionID }
func (o *piHarnessOutput) Usage() Usage      { return o.usage }
