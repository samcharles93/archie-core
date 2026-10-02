package agentworker

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/tools/mcp"
)

// capturedChatRequest is the JSON body the OpenAI-compatible provider sent for
// the last call, which is where the max-token parameter is chosen.
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
// option the handler merely set.
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

// newSamplingModelServer stands in for an OpenAI-compatible provider so a
// sampling request can be answered without a real backend.
func newSamplingModelServer(t *testing.T) *httptest.Server {
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

func testSamplingRequest() mcp.SamplingRequest {
	return mcp.SamplingRequest{
		Messages: []mcp.SamplingMessage{{
			Role:    "user",
			Content: mcp.SamplingContent{Type: "text", Text: "2+2?"},
		}},
		MaxTokens: 16,
	}
}

// TestTaskSamplingHandlerAnswersFromWorkerModel proves a task-scoped MCP
// server's sampling request is answered by the worker's own model. A task
// hosts the same configured MCP servers as a chat turn, so without this the
// server is answered on the chat path and refused on the task path.
func TestTaskSamplingHandlerAnswersFromWorkerModel(t *testing.T) {
	api := newSamplingModelServer(t)
	llm := agentexec.NewRuntime(map[string]agentexec.Provider{
		"openai": {Class: "openai", BaseURL: api.URL},
	})
	handler := taskSamplingHandler(llm, config.TaskConfig{Models: map[string]string{"builder": "openai/gpt-5.6"}})

	result, err := handler(t.Context(), testSamplingRequest())
	if err != nil {
		t.Fatalf("sampling handler = %v, want a completion", err)
	}
	if result.Model != "openai/gpt-5.6" {
		t.Errorf("result.Model = %q, want the task's builder model", result.Model)
	}
	if result.Role != "assistant" || result.Content.Type != "text" || result.Content.Text != "four" {
		t.Errorf("result = %+v, want an assistant text completion", result)
	}
}

// TestTaskSamplingHandlerRefusesCleanly proves a worker with no usable model
// answers the request with an error instead of panicking or dropping it: the
// server sees a failed request, not a dead session.
func TestTaskSamplingHandlerRefusesCleanly(t *testing.T) {
	tests := []struct {
		name  string
		llm   bool
		model string
		want  string
	}{
		{name: "no model runtime", llm: false, model: "openai/gpt-5.6", want: "no model runtime"},
		{name: "no builder model configured", llm: true, want: "no builder model"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			llm := agentexec.NewRuntime(nil)
			if tc.llm {
				llm = agentexec.NewRuntime(map[string]agentexec.Provider{"openai": {Class: "openai"}})
			}
			models := map[string]string{}
			if tc.model != "" {
				models["builder"] = tc.model
			}
			_, err := taskSamplingHandler(llm, config.TaskConfig{Models: models})(t.Context(), testSamplingRequest())
			if err == nil {
				t.Fatal("sampling handler returned no error with no usable model")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

// TestTaskSamplingBoundFollowsModelClass proves the worker consumes the
// reasoning flag carried in TaskConfig.ModelLimits: a reasoning-class builder
// model gets no max-token bound (its provider rejects the max_tokens parameter
// the chat-completions provider emits), while a classic model gets the
// request's own bound.
func TestTaskSamplingBoundFollowsModelClass(t *testing.T) {
	tests := []struct {
		name      string
		reasoning bool
		wantBound bool
		wantValue int
	}{
		{name: "reasoning model", reasoning: true},
		{name: "classic model", wantBound: true, wantValue: 16},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			capture := &capturedChatRequest{}
			api := newCapturingChatAPI(t, capture)
			llm := agentexec.NewRuntime(map[string]agentexec.Provider{
				"openai": {Class: "openai", BaseURL: api.URL},
			})
			cfg := config.TaskConfig{
				Models: map[string]string{"builder": "openai/gpt-5.6"},
				ModelLimits: map[string]config.ModelLimits{
					"openai/gpt-5.6": {MaxOutputTokens: 128000, Reasoning: tc.reasoning},
				},
			}
			if _, err := taskSamplingHandler(llm, cfg)(t.Context(), testSamplingRequest()); err != nil {
				t.Fatalf("sampling handler error = %v", err)
			}
			bound, present := capture.get()["max_tokens"]
			if present != tc.wantBound {
				t.Fatalf("request body max_tokens present = %v (value %v), want %v; body = %v", present, bound, tc.wantBound, capture.get())
			}
			if present && bound != float64(tc.wantValue) {
				t.Fatalf("request body max_tokens = %v, want %d; body = %v", bound, tc.wantValue, capture.get())
			}
		})
	}
}

// TestBuildMCPProviderWiresTaskSamplingHandler proves the worker's MCP
// provider builder hands the provider the sampling handler, so the seam a task
// run builds providers through is the one that answers sampling -- not only
// the handler in isolation.
func TestBuildMCPProviderWiresTaskSamplingHandler(t *testing.T) {
	handler := taskSamplingHandler(agentexec.NewRuntime(map[string]agentexec.Provider{
		"openai": {Class: "openai"},
	}), config.TaskConfig{Models: map[string]string{"builder": "openai/gpt-5.6"}})

	provider, _, err := buildMCPProvider(config.MCPServer{Name: "sampling", Command: "server"}, handler)
	if err != nil {
		t.Fatalf("buildMCPProvider: %v", err)
	}
	field := reflect.ValueOf(provider).Elem().FieldByName("samplingHandler")
	if field.IsNil() {
		t.Fatal("task MCP provider carries no sampling handler: a task-scoped server's sampling/createMessage request is refused")
	}
}
