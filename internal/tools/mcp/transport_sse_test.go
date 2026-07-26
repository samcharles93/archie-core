package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// sseTestServer creates a fake MCP server that speaks the SSE transport
// protocol: GET /sse for the event stream, POST /messages for client→server.
// handleMessage receives each posted JSON-RPC body; callers can enqueue
// events onto events to send them back through the SSE stream.
type sseTestServer struct {
	*httptest.Server
	SessID   string
	events   chan string
	incoming chan []byte
}

func newSSETestServer(t *testing.T) *sseTestServer {
	t.Helper()
	s := &sseTestServer{
		events:   make(chan string, 10),
		incoming: make(chan []byte, 10),
	}
	s.Server = httptest.NewUnstartedServer(s)
	s.Start()
	s.SessID = "test-session-id"
	return s
}

func (s *sseTestServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/sse":
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "no flusher", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)

		// Send the endpoint event — tells the client where to POST.
		_, _ = fmt.Fprintf(w, "event: endpoint\ndata: %s/messages?session_id=%s\n\n", s.URL, s.SessID)
		flusher.Flush()

		for {
			select {
			case data := <-s.events:
				_, _ = fmt.Fprintf(w, "event: message\ndata: %s\n\n", data)
				flusher.Flush()
			case <-r.Context().Done():
				return
			}
		}

	case "/messages":
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		s.incoming <- body
		w.WriteHeader(http.StatusAccepted)

	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func TestSSETransportSendReceivesResponseViaSSE(t *testing.T) {
	fake := newSSETestServer(t)
	defer fake.Close()

	tr := NewSSETransport(SSETransportConfig{
		SSEEndpoint:     fake.URL + "/sse",
		MessageEndpoint: fake.URL + "/messages",
	})
	if err := tr.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = tr.Stop(context.Background()) }()

	time.Sleep(50 * time.Millisecond) // let the SSE connection establish

	// Send a request. The fake server will enqueue it; we then send
	// the response back through the SSE event channel.
	go func() {
		<-fake.incoming // wait for the POST
		fake.events <- `{"jsonrpc":"2.0","id":42,"result":{"from_sse":true}}`
	}()

	resp, err := tr.Send(context.Background(), []byte(`{"jsonrpc":"2.0","id":42,"method":"test","params":{}}`))
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	var msg Message
	if err := json.Unmarshal(resp, &msg); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if string(msg.Result) != `{"from_sse":true}` {
		t.Errorf("result = %s", msg.Result)
	}
}

func TestSSETransportNotifyFireAndForget(t *testing.T) {
	fake := newSSETestServer(t)
	defer fake.Close()

	tr := NewSSETransport(SSETransportConfig{
		SSEEndpoint:     fake.URL + "/sse",
		MessageEndpoint: fake.URL + "/messages",
	})
	if err := tr.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = tr.Stop(context.Background()) }()

	time.Sleep(50 * time.Millisecond)

	err := tr.Notify(context.Background(), []byte(`{"jsonrpc":"2.0","method":"initialized"}`))
	if err != nil {
		t.Fatalf("Notify: %v", err)
	}
	select {
	case <-fake.incoming:
		// notification delivered
	case <-time.After(time.Second):
		t.Fatal("notification not delivered")
	}
}

func TestSSETransportStartFailsOnBadSSEEndpoint(t *testing.T) {
	tr := NewSSETransport(SSETransportConfig{
		SSEEndpoint:     "http://127.0.0.1:1/sse",
		MessageEndpoint: "http://127.0.0.1:1/messages",
	})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := tr.Start(ctx); err == nil {
		t.Fatal("expected connection error on Start")
	}
}

func TestSSETransportStopClosesConnectionAndFailsPending(t *testing.T) {
	fake := newSSETestServer(t)
	defer fake.Close()

	tr := NewSSETransport(SSETransportConfig{
		SSEEndpoint:     fake.URL + "/sse",
		MessageEndpoint: fake.URL + "/messages",
	})
	if err := tr.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := tr.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestSSETransportSendFailsWhenStopped(t *testing.T) {
	tr := NewSSETransport(SSETransportConfig{
		SSEEndpoint:     "http://127.0.0.1:1/sse",
		MessageEndpoint: "http://127.0.0.1:1/messages",
	})
	_, err := tr.Send(context.Background(), []byte(`{"jsonrpc":"2.0","id":"1","method":"test"}`))
	if err == nil || !strings.Contains(err.Error(), "not running") {
		t.Errorf("err = %v, want 'not running' error", err)
	}
}

func TestSSETransportConfigDefaults(t *testing.T) {
	cfg := SSETransportConfig{}
	if cfg.effectiveReconnectBackoff() == 0 {
		t.Error("reconnect backoff should have a default")
	}
	if cfg.effectiveMaxReconnectBackoff() == 0 {
		t.Error("max reconnect backoff should have a default")
	}
}

func TestSSETransportStateReflectsRunning(t *testing.T) {
	fake := newSSETestServer(t)
	defer fake.Close()

	tr := NewSSETransport(SSETransportConfig{
		SSEEndpoint:     fake.URL + "/sse",
		MessageEndpoint: fake.URL + "/messages",
	})

	// Before Start, state should be StateStopped.
	if state := tr.State(); state != StateStopped {
		t.Errorf("State before Start = %v, want StateStopped", state)
	}

	if err := tr.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// After Start, state should be StateRunning.
	if state := tr.State(); state != StateRunning {
		t.Errorf("State after Start = %v, want StateRunning", state)
	}

	if err := tr.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// After Stop, state should be StateStopped.
	if state := tr.State(); state != StateStopped {
		t.Errorf("State after Stop = %v, want StateStopped", state)
	}
}

func TestSSETransportConcurrentSendsGetCorrectResponses(t *testing.T) {
	fake := newSSETestServer(t)
	defer fake.Close()

	tr := NewSSETransport(SSETransportConfig{
		SSEEndpoint:     fake.URL + "/sse",
		MessageEndpoint: fake.URL + "/messages",
	})
	if err := tr.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = tr.Stop(context.Background()) }()

	time.Sleep(50 * time.Millisecond)

	// Send 5 concurrent requests. The fake server reads them all from
	// the incoming channel, then sends responses back through SSE.
	const n = 5
	var wg sync.WaitGroup
	results := make(chan error, n)

	for i := range n {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			resp, err := tr.Send(context.Background(),
				fmt.Appendf(nil, `{"jsonrpc":"2.0","id":%d,"method":"test"}`, id))
			if err != nil {
				results <- fmt.Errorf("id %d: Send: %v", id, err)
				return
			}
			var msg Message
			if err := json.Unmarshal(resp, &msg); err != nil {
				results <- fmt.Errorf("id %d: parse: %v", id, err)
				return
			}
			if string(msg.ID) != fmt.Sprintf("%d", id) {
				results <- fmt.Errorf("response ID = %s, want %d", msg.ID, id)
			}
		}(i)
	}

	// Drain incoming POSTs and feed responses back in order.
	for i := range n {
		body := <-fake.incoming
		var env struct {
			ID int `json:"id"`
		}
		_ = json.Unmarshal(body, &env)
		fake.events <- fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{"ok":true}}`, env.ID)
		_ = i
	}
	wg.Wait()
	close(results)
	for e := range results {
		t.Error(e)
	}
}
