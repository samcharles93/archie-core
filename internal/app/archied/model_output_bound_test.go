package archied

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/samcharles93/ai-sdk/runtime"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/gateway"
	"github.com/samcharles93/archie-core/internal/infrastructure/modelcatalog"
	"github.com/samcharles93/archie-core/internal/tools"
	"github.com/samcharles93/archie-core/internal/tools/mcp"
)

// capturedChatRequest is the JSON body the OpenAI-compatible provider sent for
// the last call, which is where the max-token parameter name is chosen.
type capturedChatRequest struct {
	mu   sync.Mutex
	body map[string]any
}

func (c *capturedChatRequest) get() map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.body
}

// newCapturingChatAPI answers every completion and records the wire body, so a
// test can assert the parameter the provider actually emitted rather than an
// option the composition root merely set.
func newCapturingChatAPI(t *testing.T, capture *capturedChatRequest) *httptest.Server {
	t.Helper()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		capture.mu.Lock()
		capture.body = body
		capture.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"chatcmpl-1","object":"chat.completion","created":1,"model":"test",`+
			`"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],`+
			`"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	t.Cleanup(api.Close)
	return api
}

// assertWireBound asserts the max-token parameter the provider emitted: absent
// entirely for a reasoning model (its only accepted form with this ai-sdk
// release), present with the caller's bound for a classic one.
func assertWireBound(t *testing.T, capture *capturedChatRequest, wantBound bool, wantValue int) {
	t.Helper()
	bound, present := capture.get()["max_tokens"]
	if present != wantBound {
		t.Fatalf("request body max_tokens present = %v (value %v), want %v; body = %v", present, bound, wantBound, capture.get())
	}
	if present && bound != float64(wantValue) {
		t.Fatalf("request body max_tokens = %v, want %d; body = %v", bound, wantValue, capture.get())
	}
}

// TestChatTurnParameterNameFollowsModelClass proves the chat turn path reaches
// the provider without a max-token bound for a reasoning-class model, while a
// classic model still carries one. The bound itself is unchanged; only the
// parameter the chat-completions provider would serialise it into differs, and
// reasoning models reject `max_tokens` with HTTP 400.
func TestChatTurnParameterNameFollowsModelClass(t *testing.T) {
	tests := []struct {
		name      string
		reasoning bool
		wantBound bool
		modelRef  string
		maxOutput int
		wantValue int
	}{
		{name: "reasoning model", reasoning: true, wantBound: false, modelRef: "openai/gpt-5.4", maxOutput: 4096},
		{name: "classic model", reasoning: false, wantBound: true, modelRef: "openai/gpt-4o", maxOutput: 4096, wantValue: 4096},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			capture := &capturedChatRequest{}
			api := newCapturingChatAPI(t, capture)
			llm := agentexec.NewRuntime(map[string]agentexec.Provider{
				"openai": {Class: "openai", BaseURL: api.URL},
			})
			model := newChatTurnModel(func() *runtime.Runtime { return llm }, tools.NewRegistry(), 1, agentexec.ToolLimits{}, nil)

			prepared, err := model.Prepare(t.Context(), gateway.TurnPrepareContext{
				Model: tc.modelRef, Reasoning: tc.reasoning,
			})
			if err != nil {
				t.Fatalf("Prepare() error = %v", err)
			}
			_, err = prepared.Generate(t.Context(), gateway.TurnModelRequest{
				Messages:        []gateway.CompressedMessage{{Role: "user", Content: "hi"}},
				MaxOutputTokens: tc.maxOutput,
			}, nil)
			if err != nil {
				t.Fatalf("Generate() error = %v", err)
			}

			assertWireBound(t, capture, tc.wantBound, tc.wantValue)
		})
	}
}

// TestMCPSamplingParameterNameFollowsModelClass proves the second place the
// process sets a max-token bound -- server-requested sampling, which also
// applies a default when the request is silent -- preserves it for a classic
// model and drops it entirely for a reasoning one.
func TestMCPSamplingParameterNameFollowsModelClass(t *testing.T) {
	tests := []struct {
		name      string
		modelRef  string
		model     modelcatalog.Model
		wantBound bool
		wantValue int
	}{
		{
			name:     "reasoning model",
			modelRef: "openai/gpt-5.4",
			model:    modelcatalog.Model{ID: "gpt-5.4", Reasoning: true, MaxOutputTokens: 128000},
		},
		{
			name:      "classic model",
			modelRef:  "openai/gpt-4o",
			model:     modelcatalog.Model{ID: "gpt-4o", MaxOutputTokens: 128000},
			wantBound: true,
			wantValue: samplingDefaultMaxTokens,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			capture := &capturedChatRequest{}
			api := newCapturingChatAPI(t, capture)
			b := newBootstrap()
			b.setLLM(agentexec.NewRuntime(map[string]agentexec.Provider{
				"openai": {Class: "openai", BaseURL: api.URL},
			}))
			b.chatModels = newChatModelManager(map[string]string{"chat": tc.modelRef})
			b.chatModels.SetModelCatalog(modelcatalog.Snapshot{
				Providers: []modelcatalog.Provider{{ID: "openai", Models: []modelcatalog.Model{tc.model}}},
			}, []string{tc.modelRef})
			b.providerOutcomes = newProviderOutcomeRecorder()

			_, err := b.mcpSamplingHandler()(t.Context(), mcp.SamplingRequest{
				Messages: []mcp.SamplingMessage{{Role: "user", Content: mcp.SamplingContent{Type: "text", Text: "hi"}}},
			})
			if err != nil {
				t.Fatalf("sampling handler error = %v", err)
			}

			assertWireBound(t, capture, tc.wantBound, tc.wantValue)
		})
	}
}
