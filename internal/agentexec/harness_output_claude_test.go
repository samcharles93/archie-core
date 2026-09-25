package agentexec

import (
	"bufio"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestClaudeCodeHarnessOutputFixtures(t *testing.T) {
	errorDetail := "Exit code 1\ncat: /nonexistent-file: No such file or directory"
	cases := []struct {
		name        string
		wantCalls   []ToolCallReport
		wantUsage   Usage
		wantSession string
	}{
		{
			name: "run.jsonl",
			wantCalls: []ToolCallReport{
				{Tool: "Bash", Detail: "hello"},
				{Tool: "Bash", Detail: errorDetail, Failed: true},
			},
			wantUsage:   Usage{PromptTokens: 6, CompletionTokens: 248, TotalTokens: 254, CachedTokens: 78105, CacheCreationTokens: 24309},
			wantSession: "9c22c15c-80ef-42c6-a503-8d087ec47d92",
		},
		{
			name: "killed-before-result.jsonl",
			wantCalls: []ToolCallReport{
				{Tool: "Bash", Detail: "hello"},
				{Tool: "Bash", Detail: errorDetail, Failed: true},
			},
			wantUsage:   Usage{PromptTokens: 6, CompletionTokens: 121, TotalTokens: 127, CachedTokens: 78105, CacheCreationTokens: 24309},
			wantSession: "9c22c15c-80ef-42c6-a503-8d087ec47d92",
		},
		{
			name: "malformed-line.jsonl",
			wantCalls: []ToolCallReport{
				{Tool: "Bash", Detail: "hello"},
				{Tool: "Bash", Detail: errorDetail, Failed: true},
			},
			wantUsage:   Usage{PromptTokens: 6, CompletionTokens: 248, TotalTokens: 254, CachedTokens: 78105, CacheCreationTokens: 24309},
			wantSession: "9c22c15c-80ef-42c6-a503-8d087ec47d92",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stream, err := os.Open("testdata/claude/" + tc.name)
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()

			output := newClaudeCodeHarnessOutput()
			var calls []ToolCallReport
			scanner := bufio.NewScanner(stream)
			for scanner.Scan() {
				output.Line(scanner.Bytes(), func(call ToolCallReport) { calls = append(calls, call) })
			}
			if err := scanner.Err(); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(calls, tc.wantCalls) {
				t.Errorf("tool calls = %#v, want %#v", calls, tc.wantCalls)
			}
			if got := output.SessionID(); got != tc.wantSession {
				t.Errorf("session ID = %q, want %q", got, tc.wantSession)
			}
			if got := output.Usage(); got != tc.wantUsage {
				t.Errorf("usage = %#v, want %#v", got, tc.wantUsage)
			}
		})
	}
}

func TestClaudeCodeHarnessOutputToolResultContent(t *testing.T) {
	long := strings.Repeat("x", toolCallDetailBytes+25)
	cases := []struct {
		name       string
		content    any
		wantDetail string
	}{
		{name: "string", content: "plain result", wantDetail: "plain result"},
		{name: "text blocks", content: []map[string]string{{"type": "text", "text": "first"}, {"type": "text", "text": "second"}}, wantDetail: "first\nsecond"},
		{name: "clipped", content: long, wantDetail: strings.Repeat("x", toolCallDetailBytes) + "…"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			output := newClaudeCodeHarnessOutput()
			var calls []ToolCallReport
			report := func(call ToolCallReport) { calls = append(calls, call) }
			sendClaudeEvent(t, output, report, map[string]any{
				"type": "assistant", "session_id": "old-session",
				"message": map[string]any{
					"id": "same-message", "usage": claudeUsageFixture(2, 3, 5, 7),
					"content": []any{map[string]any{"type": "tool_use", "id": "call-1", "name": "Read", "input": map[string]any{}}},
				},
			})
			sendClaudeEvent(t, output, report, map[string]any{
				"type": "assistant", "session_id": "newer-session",
				"message": map[string]any{
					"id": "same-message", "usage": claudeUsageFixture(2, 3, 5, 7),
					"content": []any{map[string]any{"type": "tool_use", "id": "call-2", "name": "Write", "input": map[string]any{}}},
				},
			})
			sendClaudeEvent(t, output, report, map[string]any{
				"type": "user", "session_id": "latest-session",
				"message": map[string]any{"content": []any{map[string]any{"type": "tool_result", "tool_use_id": "call-1", "content": tc.content, "is_error": false}}},
			})
			sendClaudeEvent(t, output, report, map[string]any{
				"type": "user", "session_id": "",
				"message": map[string]any{"content": []any{map[string]any{"type": "tool_result", "tool_use_id": "call-2", "content": "done", "is_error": false}}},
			})

			wantCalls := []ToolCallReport{{Tool: "Read", Detail: tc.wantDetail}, {Tool: "Write", Detail: "done"}}
			if !reflect.DeepEqual(calls, wantCalls) {
				t.Errorf("tool calls = %#v, want %#v", calls, wantCalls)
			}
			wantUsage := Usage{PromptTokens: 2, CompletionTokens: 3, TotalTokens: 5, CachedTokens: 5, CacheCreationTokens: 7}
			if got := output.Usage(); got != wantUsage {
				t.Errorf("usage = %#v, want %#v", got, wantUsage)
			}
			if got := output.SessionID(); got != "latest-session" {
				t.Errorf("session ID = %q, want latest non-empty event value", got)
			}
		})
	}
}

func sendClaudeEvent(t *testing.T, output HarnessOutput, report ToolCallReporter, event map[string]any) {
	t.Helper()
	line, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	output.Line(line, report)
}

func claudeUsageFixture(input, output, cacheRead, cacheCreate int) map[string]any {
	return map[string]any{
		"input_tokens": input, "output_tokens": output,
		"cache_read_input_tokens": cacheRead, "cache_creation_input_tokens": cacheCreate,
	}
}
