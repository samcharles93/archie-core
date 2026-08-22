package minimax

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeAPI serves a MiniMax API double. create answers with taskID (or
// createStatus/createBody when set); each call to query pops the next
// entry from queryResponses, repeating the last one once exhausted.
type fakeAPI struct {
	t              *testing.T
	createStatus   int
	createBody     string
	taskID         string
	queryResponses []string
	queryCalls     int
	lastAuth       string
	lastCreateBody map[string]any
}

func (f *fakeAPI) server() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.lastAuth = r.Header.Get("Authorization")
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/v2/video_generation"):
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				f.t.Fatalf("decode create body: %v", err)
			}
			f.lastCreateBody = body

			if f.createStatus != 0 && f.createStatus != http.StatusOK {
				w.WriteHeader(f.createStatus)
				_, _ = w.Write([]byte(f.createBody))
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"task_id":"` + f.taskID + `"}`))

		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/v2/query/video_generation/"):
			idx := f.queryCalls
			if idx >= len(f.queryResponses) {
				idx = len(f.queryResponses) - 1
			}
			f.queryCalls++
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(f.queryResponses[idx]))

		default:
			f.t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
}

func newTestClient(t *testing.T, api *fakeAPI, pollInterval time.Duration) *Client {
	t.Helper()
	srv := api.server()
	t.Cleanup(srv.Close)
	var slept []time.Duration
	return New(Config{
		Enabled:      true,
		APIKey:       "test-key",
		BaseURL:      srv.URL,
		Timeout:      5 * time.Second,
		PollInterval: pollInterval,
		// Large relative to any pollInterval used in these tests (including
		// the 5s default when pollInterval is 0): Sleep is faked, so this
		// costs nothing in real time and just has to outlast however many
		// polls a test's queryResponses table needs.
		MaxWait: 24 * time.Hour,
		Sleep:   func(d time.Duration) { slept = append(slept, d) },
	})
}

// A submitted job must carry the bearer key and the prompt, and once
// MiniMax reports success the client must hand back the hosted URL, not
// just the fact that it succeeded.
func TestGenerateAndWaitReturnsURLOnSuccess(t *testing.T) {
	api := &fakeAPI{t: t, taskID: "task-1", queryResponses: []string{
		`{"task":{"id":"task-1","status":"succeeded","content":{"url":"https://cdn.example.com/v.mp4"}}}`,
	}}
	client := newTestClient(t, api, 0)

	url, err := client.GenerateAndWait(context.Background(), GenerateRequest{Prompt: "a cat on a skateboard"})
	if err != nil {
		t.Fatalf("GenerateAndWait: %v", err)
	}
	if url != "https://cdn.example.com/v.mp4" {
		t.Errorf("url = %q, want the succeeded task's content url", url)
	}
	if api.lastAuth != "Bearer test-key" {
		t.Errorf("Authorization = %q, want Bearer test-key", api.lastAuth)
	}
	if api.lastCreateBody["content"] == nil {
		t.Fatal("create body carried no content")
	}
}

// A job that starts queued and moves through running before succeeding
// must be polled repeatedly, not just once.
func TestGenerateAndWaitPollsUntilTerminal(t *testing.T) {
	api := &fakeAPI{t: t, taskID: "task-1", queryResponses: []string{
		`{"task":{"id":"task-1","status":"queued"}}`,
		`{"task":{"id":"task-1","status":"running"}}`,
		`{"task":{"id":"task-1","status":"succeeded","content":{"url":"https://cdn.example.com/v.mp4"}}}`,
	}}
	client := newTestClient(t, api, 0)

	url, err := client.GenerateAndWait(context.Background(), GenerateRequest{Prompt: "a cat on a skateboard"})
	if err != nil {
		t.Fatalf("GenerateAndWait: %v", err)
	}
	if url != "https://cdn.example.com/v.mp4" {
		t.Errorf("url = %q", url)
	}
	if api.queryCalls != 3 {
		t.Errorf("query calls = %d, want 3 (queued, running, succeeded)", api.queryCalls)
	}
}

// A failed generation must surface the platform's own error message, not
// just "it failed" -- the model needs enough detail to tell the user
// something useful about why.
func TestGenerateAndWaitReturnsErrorOnFailedStatus(t *testing.T) {
	api := &fakeAPI{t: t, taskID: "task-1", queryResponses: []string{
		`{"task":{"id":"task-1","status":"failed","error":{"code":"content_policy","message":"prompt rejected"}}}`,
	}}
	client := newTestClient(t, api, 0)

	_, err := client.GenerateAndWait(context.Background(), GenerateRequest{Prompt: "a cat on a skateboard"})
	if err == nil {
		t.Fatal("expected an error for a failed task")
	}
	if !strings.Contains(err.Error(), "prompt rejected") {
		t.Errorf("error = %v, want it to contain the platform's message", err)
	}
}

// A create call that MiniMax itself rejects (bad request, auth, quota,
// content policy, rate limit) must fail before any polling starts, with
// the platform's own description rather than a generic HTTP error.
func TestGenerateReportsCreateFailures(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		wantSubstr string
	}{
		{
			name:       "auth",
			status:     http.StatusUnauthorized,
			body:       `{"type":"error","error":{"type":"auth_error","message":"invalid API key","http_code":"401"},"request_id":"r1"}`,
			wantSubstr: "invalid API key",
		},
		{
			name:       "insufficient balance",
			status:     http.StatusPaymentRequired,
			body:       `{"type":"error","error":{"type":"balance_error","message":"insufficient balance","http_code":"402"},"request_id":"r2"}`,
			wantSubstr: "insufficient balance",
		},
		{
			name:       "rate limited",
			status:     http.StatusTooManyRequests,
			body:       `{"type":"error","error":{"type":"rate_limit","message":"too many requests","http_code":"429"},"request_id":"r3"}`,
			wantSubstr: "too many requests",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := &fakeAPI{t: t, createStatus: tt.status, createBody: tt.body}
			client := newTestClient(t, api, 0)

			_, err := client.GenerateAndWait(context.Background(), GenerateRequest{Prompt: "a cat"})
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tt.wantSubstr) {
				t.Errorf("error = %v, want it to contain %q", err, tt.wantSubstr)
			}
		})
	}
}

// A job that never reaches a terminal status must not poll forever: it has
// to give up once MaxWait elapses and say so, rather than hanging the tool
// call (and the whole turn) indefinitely.
func TestGenerateAndWaitTimesOutWithoutRealSleep(t *testing.T) {
	responses := make([]string, 100)
	for i := range responses {
		responses[i] = `{"task":{"id":"task-1","status":"running"}}`
	}
	api := &fakeAPI{t: t, taskID: "task-1", queryResponses: responses}

	srv := api.server()
	t.Cleanup(srv.Close)

	elapsed := time.Duration(0)
	client := New(Config{
		Enabled:      true,
		APIKey:       "test-key",
		BaseURL:      srv.URL,
		Timeout:      5 * time.Second,
		PollInterval: time.Second,
		MaxWait:      10 * time.Second,
		Sleep:        func(d time.Duration) { elapsed += d },
	})

	start := time.Now()
	_, err := client.GenerateAndWait(context.Background(), GenerateRequest{Prompt: "a cat"})
	wallClock := time.Since(start)

	if err == nil {
		t.Fatal("expected a timeout error")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error = %v, want a timeout error", err)
	}
	if wallClock > time.Second {
		t.Fatalf("test took %v of real wall-clock time; Sleep was not honored as the delay source", wallClock)
	}
	if elapsed < 10*time.Second {
		t.Errorf("simulated elapsed = %v, want at least MaxWait (10s)", elapsed)
	}
}
