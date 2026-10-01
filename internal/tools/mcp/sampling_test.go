package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// samplingRouterTransport is a Transport double that also receives
// server-initiated requests, so the client's dispatch can be tested without
// a subprocess. It captures the handler the client registers.
type samplingRouterTransport struct {
	mu      sync.Mutex
	handler ServerRequestHandler
}

func (t *samplingRouterTransport) Send(context.Context, []byte) ([]byte, error) {
	return nil, errors.New("samplingRouterTransport: Send is not used by these tests")
}

func (t *samplingRouterTransport) Notify(context.Context, []byte) error { return nil }

func (t *samplingRouterTransport) SetServerRequestHandler(handler ServerRequestHandler) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.handler = handler
}

func (t *samplingRouterTransport) registered() ServerRequestHandler {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.handler
}

func samplingParams(t *testing.T) json.RawMessage {
	t.Helper()
	return json.RawMessage(`{"messages":[{"role":"user","content":{"type":"text","text":"2+2?"}}],"maxTokens":16}`)
}

func TestServerSamplingRequestIsAnswered(t *testing.T) {
	transport := &samplingRouterTransport{}
	var got SamplingRequest
	NewClient(transport, "sampling-server", false, WithSamplingHandler(
		func(_ context.Context, req SamplingRequest) (SamplingResult, error) {
			got = req
			return SamplingResult{
				Model:      "openai/gpt-5.6",
				Role:       "assistant",
				Content:    SamplingContent{Type: "text", Text: "four"},
				StopReason: "stop",
			}, nil
		},
	))

	handler := transport.registered()
	if handler == nil {
		t.Fatal("client did not register a server request handler with the transport")
	}

	result, rpcErr := handler(context.Background(), "sampling/createMessage", samplingParams(t))
	if rpcErr != nil {
		t.Fatalf("sampling request answered with RPC error %+v, want a result", rpcErr)
	}
	if len(got.Messages) != 1 || got.Messages[0].Content.Text != "2+2?" {
		t.Errorf("handler received %+v, want the request's message text", got)
	}
	if got.MaxTokens != 16 {
		t.Errorf("handler received maxTokens %d, want 16", got.MaxTokens)
	}

	var decoded SamplingResult
	if err := json.Unmarshal(result, &decoded); err != nil {
		t.Fatalf("unmarshal sampling result: %v", err)
	}
	if decoded.Content.Text != "four" || decoded.Model != "openai/gpt-5.6" {
		t.Errorf("sampling result = %+v, want the handler's completion", decoded)
	}
}

func TestServerSamplingRequestWithoutHandlerIsMethodNotFound(t *testing.T) {
	transport := &samplingRouterTransport{}
	NewClient(transport, "sampling-server", false)

	handler := transport.registered()
	if handler == nil {
		t.Fatal("client did not register a server request handler with the transport")
	}

	_, rpcErr := handler(context.Background(), "sampling/createMessage", samplingParams(t))
	if rpcErr == nil {
		t.Fatal("sampling request with no configured handler returned a result, want a JSON-RPC error")
	}
	if rpcErr.Code != ErrCodeMethodNotFound {
		t.Errorf("error code = %d, want %d (method not found)", rpcErr.Code, ErrCodeMethodNotFound)
	}
}

func TestServerSamplingHandlerFailureIsInternalError(t *testing.T) {
	transport := &samplingRouterTransport{}
	NewClient(transport, "sampling-server", false, WithSamplingHandler(
		func(context.Context, SamplingRequest) (SamplingResult, error) {
			return SamplingResult{}, errors.New("model unavailable")
		},
	))

	_, rpcErr := transport.registered()(context.Background(), "sampling/createMessage", samplingParams(t))
	if rpcErr == nil {
		t.Fatal("handler failure returned a result, want a JSON-RPC error")
	}
	if rpcErr.Code != ErrCodeInternal {
		t.Errorf("error code = %d, want %d (internal)", rpcErr.Code, ErrCodeInternal)
	}
	if !strings.Contains(rpcErr.Message, "model unavailable") {
		t.Errorf("error message = %q, want the handler's own error text", rpcErr.Message)
	}
}

func TestServerUnknownRequestIsMethodNotFound(t *testing.T) {
	transport := &samplingRouterTransport{}
	NewClient(transport, "sampling-server", false, WithSamplingHandler(
		func(context.Context, SamplingRequest) (SamplingResult, error) {
			t.Error("sampling handler must not run for an unknown method")
			return SamplingResult{}, nil
		},
	))

	_, rpcErr := transport.registered()(context.Background(), "roots/list", json.RawMessage(`{}`))
	if rpcErr == nil || rpcErr.Code != ErrCodeMethodNotFound {
		t.Fatalf("unknown method error = %+v, want method not found", rpcErr)
	}
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

// TestStdioTransportAnswersServerSamplingRequest drives the transport's
// reader directly: a server-initiated frame read off stdout must reach the
// handler and its response must be written back to stdin with the server's
// own request id, or the server hangs waiting for a completion it never
// gets.
func TestStdioTransportAnswersServerSamplingRequest(t *testing.T) {
	var out bytes.Buffer
	transport := NewStdioTransport(StdioTransportConfig{Command: "unused"})
	transport.stdin = nopWriteCloser{&out}
	// No subprocess is spawned here: the reader path under test only writes a
	// response when the transport considers itself live.
	transport.mu.Lock()
	transport.state = StateRunning
	transport.mu.Unlock()

	handled := make(chan SamplingRequest, 1)
	transport.SetServerRequestHandler(
		func(_ context.Context, method string, params json.RawMessage) (json.RawMessage, *ErrorData) {
			if method != "sampling/createMessage" {
				return nil, &ErrorData{Code: ErrCodeMethodNotFound, Message: method}
			}
			var req SamplingRequest
			if err := json.Unmarshal(params, &req); err != nil {
				return nil, &ErrorData{Code: ErrCodeInvalidParams, Message: err.Error()}
			}
			handled <- req
			body, err := json.Marshal(SamplingResult{
				Model:   "openai/gpt-5.6",
				Role:    "assistant",
				Content: SamplingContent{Type: "text", Text: "four"},
			})
			if err != nil {
				return nil, &ErrorData{Code: ErrCodeInternal, Message: err.Error()}
			}
			return body, nil
		},
	)

	transport.deliverResponse(t.Context(), []byte(
		`{"jsonrpc":"2.0","id":42,"method":"sampling/createMessage",`+
			`"params":{"messages":[{"role":"user","content":{"type":"text","text":"ping"}}]}}`,
	))

	select {
	case req := <-handled:
		if len(req.Messages) != 1 || req.Messages[0].Content.Text != "ping" {
			t.Errorf("handler received %+v, want the request's message text", req)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server sampling request was not routed to the handler")
	}

	// The handler goroutine writes the response after it returns; wait for it
	// before reading the shared buffer.
	transport.serverRequestWg.Wait()
	if !strings.Contains(out.String(), `"id":42`) {
		t.Fatalf("stdio transport did not write a response for request id 42; wrote %q", out.String())
	}
}
