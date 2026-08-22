package minimax

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/tools"
)

// Disabled must withdraw the tool entirely: an advertised tool that always
// fails costs the model a wasted round trip to discover it doesn't work,
// the same reasoning webfetch.Tool documents for its own Enabled check.
func TestToolReturnsNilWhenDisabled(t *testing.T) {
	if entry := Tool(Config{Enabled: false}); entry != nil {
		t.Fatal("expected a nil entry when disabled")
	}
}

func TestToolHasRequiredPromptSchema(t *testing.T) {
	entry := Tool(Config{Enabled: true, APIKey: "k"})
	if entry == nil {
		t.Fatal("expected a non-nil entry")
	}
	if entry.Name != ToolName {
		t.Errorf("Name = %q, want %q", entry.Name, ToolName)
	}
	required, _ := entry.Schema["required"].([]any)
	if len(required) != 1 || required[0] != "prompt" {
		t.Errorf("required = %v, want [prompt]", required)
	}
}

// A missing prompt must be rejected before any network call, matching
// webfetch's own required-field check.
func TestToolHandlerRejectsEmptyPrompt(t *testing.T) {
	entry := Tool(Config{Enabled: true, APIKey: "k"})
	if _, err := entry.Handler(context.Background(), map[string]any{}); err == nil {
		t.Fatal("expected an error for a missing prompt")
	}
}

// The handler's success path must produce a tools.MultimodalResult carrying
// the video as a URLs entry: that is the shared envelope the gateway wiring
// (cmd/archied) knows how to turn into a delivered attachment. Returning a
// bare string here would leave the video undeliverable no matter how good
// the rest of the pipeline is.
func TestToolHandlerReturnsMultimodalResultOnSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodPost:
			_, _ = w.Write([]byte(`{"task_id":"task-1"}`))
		default:
			_, _ = w.Write([]byte(`{"task":{"id":"task-1","status":"succeeded","content":{"url":"https://cdn.example.com/v.mp4"}}}`))
		}
	}))
	t.Cleanup(srv.Close)

	entry := Tool(Config{
		Enabled:      true,
		APIKey:       "k",
		BaseURL:      srv.URL,
		Timeout:      5 * time.Second,
		PollInterval: 0,
		MaxWait:      time.Hour,
		Sleep:        func(time.Duration) {},
	})

	out, err := entry.Handler(context.Background(), map[string]any{"prompt": "a cat on a skateboard"})
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}

	data, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal handler output: %v", err)
	}
	var result tools.MultimodalResult
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("handler output is not a MultimodalResult: %v", err)
	}
	if !result.IsMultimodal {
		t.Error("expected IsMultimodal = true")
	}
	if len(result.URLs) != 1 || result.URLs[0].Type != "video" || result.URLs[0].URL != "https://cdn.example.com/v.mp4" {
		t.Errorf("URLs = %+v, want [{video https://cdn.example.com/v.mp4}]", result.URLs)
	}
}

// A generation failure must surface as a handler error, not a
// MultimodalResult with no URLs: a silently empty result reads to the
// model as "it worked but produced nothing" rather than "it failed."
func TestToolHandlerReturnsErrorOnGenerationFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodPost:
			_, _ = w.Write([]byte(`{"task_id":"task-1"}`))
		default:
			_, _ = w.Write([]byte(`{"task":{"id":"task-1","status":"failed","error":{"message":"prompt rejected"}}}`))
		}
	}))
	t.Cleanup(srv.Close)

	entry := Tool(Config{
		Enabled:      true,
		APIKey:       "k",
		BaseURL:      srv.URL,
		Timeout:      5 * time.Second,
		PollInterval: 0,
		MaxWait:      time.Hour,
		Sleep:        func(time.Duration) {},
	})

	if _, err := entry.Handler(context.Background(), map[string]any{"prompt": "a cat"}); err == nil {
		t.Fatal("expected an error")
	}
}
