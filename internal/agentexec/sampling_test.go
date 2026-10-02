package agentexec

import (
	"strings"
	"testing"

	"github.com/samcharles93/ai-sdk/chat"

	protocol "github.com/samcharles93/archie-core/internal/tools/mcp"
)

func TestSamplingMessagesMapsRolesAndRejectsUnsupportedInput(t *testing.T) {
	tests := []struct {
		name    string
		request protocol.SamplingRequest
		want    []chat.Message
		wantErr string
	}{
		{
			name: "user and assistant text map to chat roles",
			request: protocol.SamplingRequest{Messages: []protocol.SamplingMessage{
				{Role: "user", Content: protocol.SamplingContent{Type: "text", Text: "2+2?"}},
				{Role: "assistant", Content: protocol.SamplingContent{Type: "", Text: "4"}},
			}},
			want: []chat.Message{
				{Role: chat.RoleUser, Content: "2+2?"},
				{Role: chat.RoleAssistant, Content: "4"},
			},
		},
		{
			name:    "no messages is a refusal",
			request: protocol.SamplingRequest{},
			wantErr: "the request has no messages",
		},
		{
			name: "non-text content is a refusal",
			request: protocol.SamplingRequest{Messages: []protocol.SamplingMessage{
				{Role: "user", Content: protocol.SamplingContent{Type: "image"}},
			}},
			wantErr: "unsupported content type",
		},
		{
			name: "unknown role is a refusal",
			request: protocol.SamplingRequest{Messages: []protocol.SamplingMessage{
				{Role: "system", Content: protocol.SamplingContent{Type: "text", Text: "hi"}},
			}},
			wantErr: "unsupported role",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := SamplingMessages(tc.request)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("SamplingMessages error = %v, want it to mention %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("SamplingMessages error = %v, want nil", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("SamplingMessages returned %d messages, want %d", len(got), len(tc.want))
			}
			for i := range got {
				if got[i].Role != tc.want[i].Role || got[i].Content != tc.want[i].Content {
					t.Errorf("message %d = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestSamplingMaxTokens(t *testing.T) {
	tests := []struct {
		name       string
		requested  int
		ceiling    int
		reasoning  bool
		wantTokens int
	}{
		{name: "requested bound is honoured", requested: 64, ceiling: 1024, wantTokens: 64},
		{name: "absent request falls back to the default", requested: 0, ceiling: 8192, wantTokens: DefaultSamplingMaxTokens},
		{name: "request is capped by the model ceiling", requested: 8192, ceiling: 1024, wantTokens: 1024},
		{name: "unknown ceiling keeps the request", requested: 8192, ceiling: 0, wantTokens: 8192},
		{name: "reasoning model gets no bound", requested: 64, ceiling: 1024, reasoning: true, wantTokens: 0},
		{name: "reasoning model with absent request gets no bound", requested: 0, ceiling: 0, reasoning: true, wantTokens: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := SamplingMaxTokens(tc.requested, tc.ceiling, tc.reasoning); got != tc.wantTokens {
				t.Fatalf("SamplingMaxTokens(%d, %d, %v) = %d, want %d", tc.requested, tc.ceiling, tc.reasoning, got, tc.wantTokens)
			}
		})
	}
}
