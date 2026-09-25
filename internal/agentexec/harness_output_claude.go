package agentexec

import (
	"bytes"
	"encoding/json"
	"strings"
)

// claudeCodeHarnessOutput reads Claude Code's newline-delimited stream-json
// output. It keeps tool-use IDs until the corresponding user tool_result
// arrives and retains assistant usage as a fallback for interrupted runs.
type claudeCodeHarnessOutput struct {
	sessionID string
	pending   map[string]string

	messageUsage Usage
	seenMessages map[string]struct{}
	hasResult    bool
	resultUsage  Usage
}

func newClaudeCodeHarnessOutput() *claudeCodeHarnessOutput {
	return &claudeCodeHarnessOutput{
		pending:      make(map[string]string),
		seenMessages: make(map[string]struct{}),
	}
}

func (o *claudeCodeHarnessOutput) Line(line []byte, report ToolCallReporter) {
	var event struct {
		Type      string          `json:"type"`
		SessionID string          `json:"session_id"`
		Message   json.RawMessage `json:"message"`
		Usage     json.RawMessage `json:"usage"`
	}
	if json.Unmarshal(line, &event) != nil {
		return
	}
	if event.SessionID != "" {
		o.sessionID = event.SessionID
	}

	switch event.Type {
	case "assistant":
		o.assistant(event.Message, report)
	case "user":
		o.user(event.Message, report)
	case "result":
		if usage, ok := decodeClaudeUsage(event.Usage); ok {
			o.resultUsage = usage
			o.hasResult = true
		}
	}
}

func (o *claudeCodeHarnessOutput) assistant(raw json.RawMessage, _ ToolCallReporter) {
	var message struct {
		ID    string `json:"id"`
		Usage *struct {
			InputTokens              int `json:"input_tokens"`
			OutputTokens             int `json:"output_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		} `json:"usage"`
		Content []struct {
			Type  string `json:"type"`
			ID    string `json:"id"`
			Name  string `json:"name"`
			Input any    `json:"input"`
		} `json:"content"`
	}
	if json.Unmarshal(raw, &message) != nil {
		return
	}
	if message.Usage != nil && (message.ID == "" || o.markMessage(message.ID)) {
		o.messageUsage = addUsage(o.messageUsage, usageFromFields(
			message.Usage.InputTokens,
			message.Usage.OutputTokens,
			message.Usage.CacheReadInputTokens,
			message.Usage.CacheCreationInputTokens,
		))
	}
	for _, block := range message.Content {
		if block.Type == "tool_use" && block.ID != "" && block.Name != "" {
			o.pending[block.ID] = block.Name
		}
	}
}

func (o *claudeCodeHarnessOutput) user(raw json.RawMessage, report ToolCallReporter) {
	var message struct {
		Content []struct {
			Type      string          `json:"type"`
			ToolUseID string          `json:"tool_use_id"`
			Content   json.RawMessage `json:"content"`
			IsError   bool            `json:"is_error"`
		} `json:"content"`
	}
	if json.Unmarshal(raw, &message) != nil {
		return
	}
	for _, block := range message.Content {
		if block.Type != "tool_result" || block.ToolUseID == "" {
			continue
		}
		tool, ok := o.pending[block.ToolUseID]
		if !ok {
			continue
		}
		delete(o.pending, block.ToolUseID)
		if report != nil {
			report(ToolCallReport{
				Tool: tool, Detail: clipToolCallDetail(claudeResultContent(block.Content)),
				Failed: block.IsError,
			})
		}
	}
}

func (o *claudeCodeHarnessOutput) markMessage(id string) bool {
	if _, seen := o.seenMessages[id]; seen {
		return false
	}
	o.seenMessages[id] = struct{}{}
	return true
}

func (o *claudeCodeHarnessOutput) SessionID() string { return o.sessionID }

func (o *claudeCodeHarnessOutput) Usage() Usage {
	if o.hasResult {
		return o.resultUsage
	}
	return o.messageUsage
}

func decodeClaudeUsage(raw json.RawMessage) (Usage, bool) {
	var fields struct {
		InputTokens              int `json:"input_tokens"`
		OutputTokens             int `json:"output_tokens"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	}
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &fields) != nil {
		return Usage{}, false
	}
	return usageFromFields(fields.InputTokens, fields.OutputTokens, fields.CacheReadInputTokens, fields.CacheCreationInputTokens), true
}

func usageFromFields(input, output, cacheRead, cacheCreation int) Usage {
	return Usage{
		PromptTokens: input, CompletionTokens: output, TotalTokens: input + output,
		CachedTokens: cacheRead, CacheCreationTokens: cacheCreation,
	}
}

func claudeResultContent(raw json.RawMessage) string {
	var content string
	if json.Unmarshal(raw, &content) == nil {
		return content
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	texts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if block.Type == "text" {
			texts = append(texts, block.Text)
		}
	}
	return strings.Join(texts, "\n")
}
