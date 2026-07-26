package mcp

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// SSETransportConfig configures the SSE MCP transport.
type SSETransportConfig struct {
	// SSEEndpoint is the GET URL for the server→client event stream (e.g.
	// "https://mcp.example.com/sse"). Required.
	SSEEndpoint string
	// MessageEndpoint is the POST URL for client→server messages. When
	// empty (the common case), the client discovers it from the
	// "endpoint" SSE event sent by the server after connecting — this
	// is the MCP SSE spec behaviour; the server's endpoint event carries
	// the full POST URL including a session-id query parameter.
	MessageEndpoint string
	// Headers are additional HTTP headers sent with every request.
	Headers map[string]string
	// ReconnectBackoff is the initial delay before the first reconnect
	// after the SSE stream drops. Default: 1s.
	ReconnectBackoff time.Duration
	// MaxReconnectBackoff caps the exponential backoff delay. Default: 30s.
	MaxReconnectBackoff time.Duration
	// POSTTimeout is the timeout for each POST request to the message
	// endpoint. Default: 30s.
	POSTTimeout time.Duration
}

func (c SSETransportConfig) effectiveReconnectBackoff() time.Duration {
	if c.ReconnectBackoff <= 0 {
		return time.Second
	}
	return c.ReconnectBackoff
}

func (c SSETransportConfig) effectiveMaxReconnectBackoff() time.Duration {
	if c.MaxReconnectBackoff <= 0 {
		return 30 * time.Second
	}
	return c.MaxReconnectBackoff
}

func (c SSETransportConfig) effectivePOSTTimeout() time.Duration {
	if c.POSTTimeout <= 0 {
		return 30 * time.Second
	}
	return c.POSTTimeout
}

// SSETransport implements [Transport] over MCP SSE. It maintains a
// long-lived GET connection for server→client events and sends
// client→server messages via HTTP POST. Responses to [Send] are
// correlated with the matching SSE "message" event by JSON-RPC ID.
type SSETransport struct {
	config SSETransportConfig

	mu    sync.Mutex
	state TransportState

	// messageEndpoint is the actual POST URL the server told us to use,
	// discovered from the endpoint event (or the config fallback).
	messageEndpoint string

	pending map[string]chan []byte

	ctx    context.Context
	cancel context.CancelFunc

	// The SSE reader goroutine.
	readerWg sync.WaitGroup
	// Track HTTP response body so Stop can close it.
	respBody io.Closer
}

// NewSSETransport creates a new SSE transport with the given config.
// Callers must call [*SSETransport.Start] before sending messages.
func NewSSETransport(config SSETransportConfig) *SSETransport {
	return &SSETransport{
		config:  config,
		state:   StateStopped,
		pending: map[string]chan []byte{},
	}
}

// Start establishes the SSE connection and waits for the server's endpoint
// event so the caller knows the transport is ready.
func (t *SSETransport) Start(ctx context.Context) error {
	t.mu.Lock()
	if t.state == StateRunning {
		t.mu.Unlock()
		return fmt.Errorf("mcp sse: already running")
	}
	t.state = StateStarting
	t.ctx, t.cancel = context.WithCancel(context.Background())
	t.messageEndpoint = t.config.MessageEndpoint // fallback; endpoint event overrides
	t.mu.Unlock()

	return t.connect(ctx)
}

// Stop closes the SSE connection, fails pending requests, and transitions
// to StateStopped. Calling Stop on a stopped transport is a no-op.
func (t *SSETransport) Stop(_ context.Context) error {
	t.mu.Lock()
	if t.state == StateStopped {
		t.mu.Unlock()
		return nil
	}
	t.state = StateStopping
	if t.cancel != nil {
		t.cancel()
	}
	if t.respBody != nil {
		_ = t.respBody.Close()
		t.respBody = nil
	}
	t.mu.Unlock()

	t.readerWg.Wait()

	t.mu.Lock()
	for id, ch := range t.pending {
		close(ch)
		delete(t.pending, id)
	}
	t.state = StateStopped
	t.mu.Unlock()
	return nil
}

// Send posts a JSON-RPC request to the message endpoint and waits for the
// matching response to arrive through the SSE event stream, correlated by
// JSON-RPC message ID.
func (t *SSETransport) Send(ctx context.Context, body []byte) ([]byte, error) {
	msgID, err := extractMessageID(body)
	if err != nil {
		return nil, fmt.Errorf("mcp sse: extract id: %w", err)
	}

	t.mu.Lock()
	if t.state != StateRunning {
		t.mu.Unlock()
		return nil, fmt.Errorf("mcp sse: transport is not running")
	}
	ch := make(chan []byte, 1)
	t.pending[msgID] = ch
	t.mu.Unlock()

	defer func() {
		t.mu.Lock()
		delete(t.pending, msgID)
		t.mu.Unlock()
	}()

	if err := t.postJSON(ctx, body); err != nil {
		return nil, err
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case resp, ok := <-ch:
		if !ok {
			return nil, fmt.Errorf("mcp sse: transport closed while waiting for response")
		}
		return resp, nil
	}
}

// Notify posts a JSON-RPC notification to the message endpoint without
// waiting for a response (notifications have no "id" by definition).
func (t *SSETransport) Notify(ctx context.Context, body []byte) error {
	t.mu.Lock()
	if t.state != StateRunning {
		t.mu.Unlock()
		return fmt.Errorf("mcp sse: transport is not running")
	}
	t.mu.Unlock()
	return t.postJSON(ctx, body)
}

// ── Internal connection management ───────────────────────────────────────

// connect establishes the SSE GET connection and blocks until the
// endpoint event arrives (or the context is cancelled).
func (t *SSETransport) connect(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.config.SSEEndpoint, nil)
	if err != nil {
		_ = t.setError(fmt.Errorf("mcp sse: build GET request: %w", err))
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	for k, v := range t.config.Headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		_ = t.setError(fmt.Errorf("mcp sse: connect: %w", err))
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_ = resp.Body.Close()
		err := fmt.Errorf("mcp sse: server returned %d", resp.StatusCode)
		_ = t.setError(err)
		return err
	}

	t.mu.Lock()
	t.respBody = resp.Body
	t.mu.Unlock()

	// Read the endpoint event (the first event the server sends).
	reader := bufio.NewReader(resp.Body)
	msgURL, err := readEndpointEvent(reader)
	if err != nil {
		_ = resp.Body.Close()
		_ = t.setError(fmt.Errorf("mcp sse: read endpoint event: %w", err))
		return err
	}
	if msgURL != "" {
		t.mu.Lock()
		t.messageEndpoint = msgURL
		t.mu.Unlock()
	}

	t.mu.Lock()
	t.state = StateRunning
	t.mu.Unlock()

	// Start the background SSE event reader.
	t.readerWg.Add(1)
	go t.runReader(resp.Body, reader)
	return nil
}

// runReader loops reading SSE events from the stream, routing
// "message" events to the corresponding pending channel.
func (t *SSETransport) runReader(body io.Closer, reader *bufio.Reader) {
	defer t.readerWg.Done()
	defer func() { _ = body.Close() }()

	for {
		event, data, err := readSSEEvent(reader)
		if err != nil {
			// Stream closed or errored. Attempt reconnect.
			t.mu.Lock()
			cancelled := t.ctx.Err() != nil
			stopping := t.state == StateStopping || t.state == StateStopped
			t.mu.Unlock()
			if cancelled || stopping {
				return
			}
			t.reconnect()
			return
		}
		switch event {
		case "message":
			t.deliverMessage(data)
		case "endpoint":
			// Server sent a new endpoint URL (session refresh).
			// Already handled on connect; accept mid-stream updates too.
			t.mu.Lock()
			if data != "" {
				t.messageEndpoint = data
			}
			t.mu.Unlock()
		default:
			// ping, heartbeat, etc. — ignore.
		}
	}
}

// reconnect attempts to re-establish the SSE connection with exponential
// backoff. On success, the reader goroutine restarts.
func (t *SSETransport) reconnect() {
	backoff := t.config.effectiveReconnectBackoff()
	for {
		t.mu.Lock()
		if t.ctx.Err() != nil {
			t.state = StateStopped
			t.mu.Unlock()
			return
		}
		if t.state != StateRunning {
			t.mu.Unlock()
			return
		}
		t.mu.Unlock()

		timer := time.NewTimer(backoff)
		select {
		case <-t.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}

		req, err := http.NewRequestWithContext(t.ctx, http.MethodGet, t.config.SSEEndpoint, nil)
		if err != nil {
			backoff = nextBackoff(backoff, t.config.effectiveMaxReconnectBackoff())
			continue
		}
		req.Header.Set("Accept", "text/event-stream")
		req.Header.Set("Cache-Control", "no-cache")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			backoff = nextBackoff(backoff, t.config.effectiveMaxReconnectBackoff())
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			_ = resp.Body.Close()
			backoff = nextBackoff(backoff, t.config.effectiveMaxReconnectBackoff())
			continue
		}

		reader := bufio.NewReader(resp.Body)
		msgURL, err := readEndpointEvent(reader)
		if err != nil {
			_ = resp.Body.Close()
			backoff = nextBackoff(backoff, t.config.effectiveMaxReconnectBackoff())
			continue
		}
		t.mu.Lock()
		if msgURL != "" {
			t.messageEndpoint = msgURL
		}
		t.respBody = resp.Body
		t.mu.Unlock()

		t.readerWg.Add(1)
		go t.runReader(resp.Body, reader)
		return
	}
}

func nextBackoff(d, max time.Duration) time.Duration {
	d *= 2
	if d > max {
		return max
	}
	return d
}

// ── Delivery ─────────────────────────────────────────────────────────────

// deliverMessage routes an incoming SSE message event to the caller waiting
// for it, keyed by JSON-RPC ID.
func (t *SSETransport) deliverMessage(data string) {
	msgID, err := extractMessageID([]byte(data))
	if err != nil {
		return
	}

	t.mu.Lock()
	ch, ok := t.pending[msgID]
	if ok {
		delete(t.pending, msgID)
	}
	t.mu.Unlock()

	if ok {
		select {
		case ch <- []byte(data):
		default:
		}
	}
}

// ── POST ─────────────────────────────────────────────────────────────────

// postJSON sends a JSON-RPC body to the message endpoint via POST.
func (t *SSETransport) postJSON(ctx context.Context, body []byte) error {
	t.mu.Lock()
	endpoint := t.messageEndpoint
	t.mu.Unlock()

	if endpoint == "" {
		return fmt.Errorf("mcp sse: no message endpoint (awaiting server's endpoint event)")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("mcp sse: build POST: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range t.config.Headers {
		req.Header.Set(k, v)
	}

	client := &http.Client{Timeout: t.config.effectivePOSTTimeout()}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("mcp sse: post: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("mcp sse: HTTP %d: %s", resp.StatusCode, string(msg))
	}
	return nil
}

// ── State helpers ────────────────────────────────────────────────────────

func (t *SSETransport) setError(err error) error {
	t.mu.Lock()
	t.state = StateError
	t.mu.Unlock()
	return err
}

// ── SSE parsing ──────────────────────────────────────────────────────────

// readEndpointEvent reads the mandatory first event from an SSE stream.
// Per the MCP SSE spec, the server MUST send an "endpoint" event as its
// first message, whose data payload is the URL the client should POST to.
func readEndpointEvent(r *bufio.Reader) (string, error) {
	event, data, err := readSSEEvent(r)
	if err != nil {
		return "", err
	}
	if event != "endpoint" {
		return "", fmt.Errorf("expected endpoint event, got %q", event)
	}
	return strings.TrimSpace(data), nil
}

// readSSEEvent reads one complete SSE event from the stream. It returns the
// event type and data payload. SSE fields are:
//
//	event: <type>\n
//	data: <payload>\n
//	\n   (blank line terminates the event)
//
// Lines beginning with ':' are comments and are skipped. Multiple "data:"
// lines are concatenated with newlines (per the SSE spec).
func readSSEEvent(r *bufio.Reader) (event, data string, err error) {
	var dataLines []string
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return "", "", err
		}
		line = strings.TrimRight(line, "\r\n")

		if line == "" {
			// Empty line terminates the event.
			if event == "" {
				event = "message" // default event type per SSE spec
			}
			return event, strings.Join(dataLines, "\n"), nil
		}
		if strings.HasPrefix(line, ":") {
			continue // comment
		}
		if after, ok := strings.CutPrefix(line, "event: "); ok {
			event = after
			continue
		}
		if after, ok := strings.CutPrefix(line, "event:"); ok {
			event = strings.TrimSpace(after)
			continue
		}
		if after, ok := strings.CutPrefix(line, "data: "); ok {
			dataLines = append(dataLines, after)
			continue
		}
		if after, ok := strings.CutPrefix(line, "data:"); ok {
			dataLines = append(dataLines, strings.TrimSpace(after))
			continue
		}
		// Other fields (id:, retry:) are ignored.
	}
}

// Compile-time interface check.
var _ Transport = (*SSETransport)(nil)
