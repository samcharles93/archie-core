package agentexec

import (
	"bufio"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestHarnessOutputFixtures(t *testing.T) {
	const missing = "cat: /nonexistent-file: No such file or directory\n"
	cases := []struct {
		adapter string
		fixture string
		// stopAt truncates the stream before the first line containing it,
		// as when the process is killed mid-run.
		stopAt      string
		wantCalls   []ToolCallReport
		wantUsage   Usage
		wantSession string
	}{
		{
			adapter: AdapterCodex, fixture: "codex/run.jsonl",
			wantCalls: []ToolCallReport{
				{Tool: "command_execution", Detail: "hello\n"},
				{Tool: "command_execution", Detail: missing, Failed: true},
			},
			wantUsage:   Usage{PromptTokens: 72082, CompletionTokens: 118, TotalTokens: 72200, CachedTokens: 57600},
			wantSession: "01a0db18-e119-7872-bf13-3644369044fb",
		},
		{
			adapter: AdapterCodex, fixture: "codex/mcp.jsonl",
			wantCalls: []ToolCallReport{
				{Tool: "record_verdict", Detail: "record_verdict recorded"},
				{Tool: "file_change", Detail: "/tmp/hfx/note.txt"},
			},
			wantUsage:   Usage{PromptTokens: 96397, CompletionTokens: 203, TotalTokens: 96600, CachedTokens: 80896},
			wantSession: "01a0db19-f9d6-7ad2-870c-c29b7fd8ab0a",
		},
		{
			adapter: AdapterCodex, fixture: "codex/run.jsonl", stopAt: `"turn.completed"`,
			wantCalls: []ToolCallReport{
				{Tool: "command_execution", Detail: "hello\n"},
				{Tool: "command_execution", Detail: missing, Failed: true},
			},
			wantSession: "01a0db18-e119-7872-bf13-3644369044fb",
		},
		{
			adapter: AdapterPi, fixture: "pi/run.jsonl",
			wantCalls: []ToolCallReport{
				{Tool: "bash", Detail: "hello\n"},
				{Tool: "bash", Detail: missing + "\n\nCommand exited with code 1", Failed: true},
			},
			wantUsage:   Usage{PromptTokens: 109198, CompletionTokens: 38, TotalTokens: 109236, CachedTokens: 62720},
			wantSession: "01a0db18-e183-7432-9e4b-4b16bfc83856",
		},
		{
			adapter: AdapterPi, fixture: "pi/run.jsonl", stopAt: `"agent_end"`,
			wantCalls: []ToolCallReport{
				{Tool: "bash", Detail: "hello\n"},
				{Tool: "bash", Detail: missing + "\n\nCommand exited with code 1", Failed: true},
			},
			wantUsage:   Usage{PromptTokens: 109198, CompletionTokens: 38, TotalTokens: 109236, CachedTokens: 62720},
			wantSession: "01a0db18-e183-7432-9e4b-4b16bfc83856",
		},
		{
			adapter: AdapterOMP, fixture: "omp/run.jsonl",
			wantCalls: []ToolCallReport{
				{Tool: "bash", Detail: "hello\n\n\nWall time: 0.03 seconds"},
				{Tool: "bash", Detail: missing + "\n\nWall time: 0.00 seconds\n\nCommand exited with code 1", Failed: true},
			},
			wantUsage:   Usage{PromptTokens: 75962, CompletionTokens: 161, TotalTokens: 76123, CachedTokens: 50304},
			wantSession: "01a0db18-e4a1-7000-9cbe-7467b0385e65",
		},
		{
			adapter: AdapterCopilot, fixture: "copilot/run.jsonl",
			wantCalls: []ToolCallReport{
				{Tool: "bash", Detail: "hello\n<shellId: 0 completed with exit code 0>"},
				{Tool: "bash", Detail: missing + "<shellId: 1 completed with exit code 1>"},
			},
			wantSession: "bdf5531e-b8a4-4baf-bed2-d914242fa387",
		},
		{
			adapter: AdapterCopilot, fixture: "copilot/mcp.jsonl",
			wantCalls:   []ToolCallReport{{Tool: "record_verdict", Detail: "record_verdict recorded"}},
			wantSession: "46ce5d05-adf6-45ac-82bc-167a3c48a146",
		},
	}

	for _, tc := range cases {
		t.Run(tc.fixture+tc.stopAt, func(t *testing.T) {
			adapter, ok := LookupHarnessAdapter(tc.adapter)
			if !ok {
				t.Fatalf("adapter %q is not registered", tc.adapter)
			}
			stream, err := os.Open("testdata/" + tc.fixture)
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()

			output := adapter.NewOutput()
			var calls []ToolCallReport
			report := func(call ToolCallReport) { calls = append(calls, call) }
			output.Line([]byte("{not json"), report)
			scanner := bufio.NewScanner(stream)
			scanner.Buffer(nil, 1<<20)
			for scanner.Scan() {
				if tc.stopAt != "" && strings.Contains(scanner.Text(), tc.stopAt) {
					break
				}
				output.Line(scanner.Bytes(), report)
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
