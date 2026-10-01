package archied

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/tools/mcp"
)

func newSamplingTestRuntime(t *testing.T) *httptest.Server {
	t.Helper()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"chatcmpl-1","object":"chat.completion","created":1,"model":"gpt-5.6",`+
			`"choices":[{"index":0,"message":{"role":"assistant","content":"four"},"finish_reason":"stop"}],`+
			`"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	t.Cleanup(api.Close)
	return api
}

// TestMCPSamplingHandlerAnswersFromConfiguredModel proves a server-initiated
// sampling request is answered by the daemon's own chat model and recorded on
// /status like every other model call this process makes.
func TestMCPSamplingHandlerAnswersFromConfiguredModel(t *testing.T) {
	api := newSamplingTestRuntime(t)

	b := newBootstrap()
	b.setLLM(agentexec.NewRuntime(map[string]agentexec.Provider{
		"openai": {Class: "openai", BaseURL: api.URL},
	}))
	b.chatModels = newChatModelManager(map[string]string{"chat": "openai/gpt-5.6"})
	b.providerOutcomes = newProviderOutcomeRecorder()

	result, err := b.mcpSamplingHandler()(t.Context(), mcp.SamplingRequest{
		Messages: []mcp.SamplingMessage{{
			Role:    "user",
			Content: mcp.SamplingContent{Type: "text", Text: "2+2?"},
		}},
		MaxTokens: 16,
	})
	if err != nil {
		t.Fatalf("sampling handler = %v, want a completion", err)
	}
	if result.Model != "openai/gpt-5.6" {
		t.Errorf("result.Model = %q, want the configured chat model", result.Model)
	}
	if result.Role != "assistant" || result.Content.Type != "text" || result.Content.Text != "four" {
		t.Errorf("result = %+v, want an assistant text completion", result)
	}
	outcome, attempted := b.providerOutcomes.LastChatModelOutcome()
	if !attempted || outcome.Model != "openai/gpt-5.6" || outcome.Err != "" {
		t.Errorf("recorded outcome = %+v (attempted %v), want the sampling call recorded as successful", outcome, attempted)
	}
}

// TestMCPSamplingHandlerRefusesCleanlyWithNoModel proves a daemon with no
// usable model answers the request with an error instead of panicking or
// dropping it: the server sees a failed request, not a dead session.
func TestMCPSamplingHandlerRefusesCleanlyWithNoModel(t *testing.T) {
	tests := []struct {
		name  string
		wired bool
		want  string
	}{
		{name: "no model runtime", wired: false, want: "no model runtime"},
		{name: "no chat model configured", wired: true, want: "no chat model"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := newBootstrap()
			if tc.wired {
				b.setLLM(agentexec.NewRuntime(map[string]agentexec.Provider{
					"openai": {Class: "openai"},
				}))
			}
			b.chatModels = newChatModelManager(nil)

			_, err := b.mcpSamplingHandler()(t.Context(), mcp.SamplingRequest{
				Messages: []mcp.SamplingMessage{{
					Role:    "user",
					Content: mcp.SamplingContent{Type: "text", Text: "hi"},
				}},
			})
			if err == nil {
				t.Fatal("sampling handler returned no error with no usable model")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestSamplingMessagesRejectsUnsupportedContent(t *testing.T) {
	_, err := samplingMessages(mcp.SamplingRequest{
		Messages: []mcp.SamplingMessage{{
			Role:    "user",
			Content: mcp.SamplingContent{Type: "image"},
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported content type") {
		t.Fatalf("samplingMessages error = %v, want an unsupported-content refusal", err)
	}
}
