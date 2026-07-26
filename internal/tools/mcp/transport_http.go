package mcp

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// HTTPTransportConfig configures the HTTP/StreamableHTTP MCP transport.
type HTTPTransportConfig struct {
	// Endpoint is the URL of the MCP server (e.g. "https://mcp.example.com/mcp").
	// Required.
	Endpoint string
	// Headers are additional HTTP headers sent with every request (e.g.
	// Authorization, custom API keys).
	Headers map[string]string
	// Timeout is the per-request timeout. Default: 30s.
	Timeout time.Duration
}

func (c HTTPTransportConfig) effectiveTimeout() time.Duration {
	if c.Timeout <= 0 {
		return 30 * time.Second
	}
	return c.Timeout
}

// HTTPTransport implements [Transport] over HTTP/StreamableHTTP. Each
// [Send] makes an HTTP POST with the JSON-RPC request body and returns the
// JSON-RPC response. [Notify] fires an HTTP POST and discards the response
// body — notifications don't expect a JSON-RPC reply.
//
// This transport is stateless (no persistent connection), so Start/Stop
// are not needed. It satisfies the [Transport] interface directly.
type HTTPTransport struct {
	config HTTPTransportConfig
	client *http.Client
}

// NewHTTPTransport creates a new HTTP transport with the given config.
func NewHTTPTransport(config HTTPTransportConfig) *HTTPTransport {
	return &HTTPTransport{
		config: config,
		client: &http.Client{Timeout: config.effectiveTimeout()},
	}
}

// Send posts a JSON-RPC request body to the server endpoint and returns the
// response body. The response body must be a valid JSON-RPC response.
func (t *HTTPTransport) Send(ctx context.Context, body []byte) ([]byte, error) {
	resp, err := t.do(ctx, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Read up to 4 KB of the error body for diagnostics.
		discard := io.LimitReader(resp.Body, 4096)
		msg, _ := io.ReadAll(discard)
		return nil, fmt.Errorf("mcp: HTTP %d: %s", resp.StatusCode, string(msg))
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("mcp: read response body: %w", err)
	}
	return data, nil
}

// Notify posts a JSON-RPC notification body to the server and discards the
// response. Notifications carry no "id" and never receive a reply.
func (t *HTTPTransport) Notify(ctx context.Context, body []byte) error {
	resp, err := t.do(ctx, body)
	if err != nil {
		return err
	}
	// Drain and close the response body to return the connection to the pool.
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return nil
}

// do sends a POST request with the given body and returns the HTTP response.
func (t *HTTPTransport) do(ctx context.Context, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.config.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("mcp: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range t.config.Headers {
		req.Header.Set(k, v)
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mcp: %w", err)
	}
	return resp, nil
}
