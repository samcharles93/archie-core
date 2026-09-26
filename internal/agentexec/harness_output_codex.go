package agentexec

import (
	"encoding/json"
	"strings"
)

// codexHarnessOutput reads `codex exec --json`. Each tool item is reported
// when its item.completed event arrives; usage comes from turn.completed, so
// a run killed mid-turn reports none.
type codexHarnessOutput struct {
	threadID string
	usage    Usage
}

func (o *codexHarnessOutput) Line(line []byte, report ToolCallReporter) {
	var event struct {
		Type     string `json:"type"`
		ThreadID string `json:"thread_id"`
		Usage    *struct {
			InputTokens       int `json:"input_tokens"`
			CachedInputTokens int `json:"cached_input_tokens"`
			CacheWriteTokens  int `json:"cache_write_input_tokens"`
			OutputTokens      int `json:"output_tokens"`
		} `json:"usage"`
		Item codexItem `json:"item"`
	}
	if json.Unmarshal(line, &event) != nil {
		return
	}
	switch event.Type {
	case "thread.started":
		o.threadID = event.ThreadID
	case "turn.completed":
		if u := event.Usage; u != nil {
			// Codex follows OpenAI: input_tokens already includes cached input.
			o.usage = addUsage(o.usage, Usage{
				PromptTokens: u.InputTokens, CompletionTokens: u.OutputTokens,
				TotalTokens:  u.InputTokens + u.OutputTokens,
				CachedTokens: u.CachedInputTokens, CacheCreationTokens: u.CacheWriteTokens,
			})
		}
	case "item.completed":
		if call, ok := event.Item.report(); ok && report != nil {
			report(call)
		}
	}
}

type codexItem struct {
	Type             string `json:"type"`
	Status           string `json:"status"`
	AggregatedOutput string `json:"aggregated_output"`
	Tool             string `json:"tool"`
	Result           *struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"result"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
	Changes []struct {
		Path string `json:"path"`
	} `json:"changes"`
	Query string `json:"query"`
}

func (item codexItem) report() (ToolCallReport, bool) {
	call := ToolCallReport{Tool: item.Type, Failed: item.Status == "failed"}
	switch item.Type {
	case "command_execution":
		call.Detail = item.AggregatedOutput
	case "mcp_tool_call":
		call.Tool = item.Tool
		switch {
		case item.Error != nil:
			call.Detail, call.Failed = item.Error.Message, true
		case item.Result != nil:
			texts := make([]string, 0, len(item.Result.Content))
			for _, block := range item.Result.Content {
				if block.Type == "text" {
					texts = append(texts, block.Text)
				}
			}
			call.Detail = strings.Join(texts, "\n")
		}
	case "file_change":
		paths := make([]string, 0, len(item.Changes))
		for _, change := range item.Changes {
			paths = append(paths, change.Path)
		}
		call.Detail = strings.Join(paths, "\n")
	case "web_search":
		call.Detail = item.Query
	default:
		return ToolCallReport{}, false
	}
	call.Detail = clipToolCallDetail(call.Detail)
	return call, true
}

func (o *codexHarnessOutput) SessionID() string { return o.threadID }
func (o *codexHarnessOutput) Usage() Usage      { return o.usage }
