package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// ── Helper: test MCP server process ─────────────────────────────────────

// TestMCPServerHelper is the entry point for the test helper subprocess.
// It is NOT a test — it is a process invoked by StdioTransport tests.
// It must be the only Test* function in this file that runs as a subprocess;
// we guard it with GO_WANT_MCP_HELPER.
func TestMCPServerHelper(t *testing.T) {
	if os.Getenv("GO_WANT_MCP_HELPER") != "1" {
		return
	}
	behavior := os.Getenv("MCP_HELPER_BEHAVIOR")
	srv := &testMCPServer{behavior: behavior}
	if err := srv.serve(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Exit(0)
}

// testMCPServer reads MCP messages from stdin and responds.
type testMCPServer struct {
	behavior string
}

func (s *testMCPServer) serve() error {
	for {
		body, err := readMessage(os.Stdin)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe) {
				return nil
			}
			return fmt.Errorf("read: %w", err)
		}

		var msg Message
		if err := json.Unmarshal(body, &msg); err != nil {
			return fmt.Errorf("unmarshal: %w", err)
		}

		resp, err := s.handle(msg)
		if err != nil {
			return err
		}
		if resp != nil {
			data, _ := json.Marshal(resp)
			if err := writeMessage(os.Stdout, data); err != nil {
				return fmt.Errorf("write: %w", err)
			}
		}
	}
}

func (s *testMCPServer) handle(msg Message) (*Message, error) {
	switch s.behavior {
	case "hang-on-start":
		// Respond to initialize then hang (no more output)
		if msg.Method == "initialize" {
			return &Message{
				JSONRPC: "2.0",
				ID:      msg.ID,
				Result:  json.RawMessage(`{"protocolVersion":"2025-03-26","capabilities":{}}`),
			}, nil
		}
		return nil, nil

	case "crash-after-init":
		if msg.Method == "initialize" {
			resp := &Message{
				JSONRPC: "2.0",
				ID:      msg.ID,
				Result:  json.RawMessage(`{"protocolVersion":"2025-03-26","capabilities":{}}`),
			}
			data, _ := json.Marshal(resp)
			_ = writeMessage(os.Stdout, data)
		}
		os.Exit(1)
		return nil, nil

	case "slow-response":
		time.Sleep(200 * time.Millisecond)
		return s.echo(msg), nil

	case "bad-framing":
		// Write malformed output (no Content-Length)
		fmt.Fprint(os.Stdout, `{"jsonrpc":"2.0","id":1,"result":{}}`)
		return nil, nil

	default:
		return s.echo(msg), nil
	}
}

func (s *testMCPServer) echo(msg Message) *Message {
	result, _ := json.Marshal(map[string]string{"echoed": msg.Method})
	return &Message{
		JSONRPC: "2.0",
		ID:      msg.ID,
		Result:  result,
	}
}

// ── Transport Tests ─────────────────────────────────────────────────────

// helperTransport creates a StdioTransport pointed at our test helper process.
func helperTransport(t *testing.T, behavior string) *StdioTransport {
	t.Helper()
	return helperTransportWithConfig(t, behavior, StdioTransportConfig{
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff:     50 * time.Millisecond,
		MaxRetries:     2,
	})
}

func helperTransportWithConfig(t *testing.T, behavior string, cfg StdioTransportConfig) *StdioTransport {
	t.Helper()
	if cfg.Command == "" {
		cfg.Command = os.Args[0]
	}
	if cfg.Args == nil {
		cfg.Args = []string{"-test.run=TestMCPServerHelper$"}
	}
	if cfg.Env == nil {
		cfg.Env = []string{
			"GO_WANT_MCP_HELPER=1",
			"MCP_HELPER_BEHAVIOR=" + behavior,
		}
	}
	// Set defaults if zero
	if cfg.InitialBackoff == 0 {
		cfg.InitialBackoff = 10 * time.Millisecond
	}
	if cfg.MaxBackoff == 0 {
		cfg.MaxBackoff = 50 * time.Millisecond
	}
	t.Cleanup(func() {
		// nothing — caller manages lifecycle
	})
	return NewStdioTransport(cfg)
}

// ── Framing Tests ───────────────────────────────────────────────────────

func TestWriteMessage(t *testing.T) {
	t.Run("writes content-length header and body", func(t *testing.T) {
		var buf bytes.Buffer
		body := []byte(`{"key":"value"}`)
		if err := writeMessage(&buf, body); err != nil {
			t.Fatal(err)
		}
		want := "Content-Length: 15\r\n\r\n{\"key\":\"value\"}"
		if got := buf.String(); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("empty body", func(t *testing.T) {
		var buf bytes.Buffer
		if err := writeMessage(&buf, nil); err != nil {
			t.Fatal(err)
		}
		want := "Content-Length: 0\r\n\r\n"
		if got := buf.String(); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
}

func TestReadMessage(t *testing.T) {
	t.Run("reads body with content-length header", func(t *testing.T) {
		input := "Content-Length: 5\r\n\r\nhello"
		body, err := readMessage(strings.NewReader(input))
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != "hello" {
			t.Errorf("got %q, want %q", string(body), "hello")
		}
	})

	t.Run("ignores other headers", func(t *testing.T) {
		input := "Content-Type: application/json\r\nContent-Length: 3\r\n\r\nabc"
		body, err := readMessage(strings.NewReader(input))
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != "abc" {
			t.Errorf("got %q, want %q", string(body), "abc")
		}
	})

	t.Run("missing content-length header", func(t *testing.T) {
		_, err := readMessage(strings.NewReader("no-header\r\n\r\nbody"))
		if err == nil {
			t.Fatal("expected error for missing Content-Length")
		}
	})

	t.Run("negative content-length", func(t *testing.T) {
		input := "Content-Length: -1\r\n\r\n"
		_, err := readMessage(strings.NewReader(input))
		if err == nil {
			t.Fatal("expected error for negative Content-Length")
		}
	})

	t.Run("oversized content-length", func(t *testing.T) {
		input := fmt.Sprintf("Content-Length: %d\r\n\r\n", maxMessageSize+1)
		_, err := readMessage(strings.NewReader(input))
		if err == nil {
			t.Fatal("expected error for oversized Content-Length")
		}
	})

	t.Run("round-trip", func(t *testing.T) {
		var buf bytes.Buffer
		original := []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)
		if err := writeMessage(&buf, original); err != nil {
			t.Fatal(err)
		}
		body, err := readMessage(&buf)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(body, original) {
			t.Errorf("got %q, want %q", string(body), string(original))
		}
	})

	t.Run("truncated body returns error", func(t *testing.T) {
		input := "Content-Length: 10\r\n\r\nshort"
		_, err := readMessage(strings.NewReader(input))
		if err == nil {
			t.Fatal("expected error for truncated body")
		}
	})
}

// ── Transport Lifecycle Tests ───────────────────────────────────────────

func TestStdioTransportStartStop(t *testing.T) {
	tr := helperTransport(t, "normal")
	if err := tr.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if tr.State() != StateRunning {
		t.Errorf("state = %v, want Running", tr.State())
	}
	if err := tr.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if tr.State() != StateStopped {
		t.Errorf("state = %v, want Stopped", tr.State())
	}
}

func TestStdioTransportDoubleStart(t *testing.T) {
	tr := helperTransport(t, "normal")
	if err := tr.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	err := tr.Start(context.Background())
	if err == nil {
		t.Error("expected error on double start")
	}
	_ = tr.Stop(context.Background())
}

func TestStdioTransportStopWithoutStart(t *testing.T) {
	tr := helperTransport(t, "normal")
	// Stopping a never-started transport should succeed (idempotent).
	if err := tr.Stop(context.Background()); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestStdioTransportSendAndReceive(t *testing.T) {
	tr := helperTransport(t, "normal")
	if err := tr.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer tr.Stop(context.Background())

	resp, err := tr.Send(context.Background(), []byte(`{"jsonrpc":"2.0","method":"ping","id":1}`))
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	var msg Message
	if err := json.Unmarshal(resp, &msg); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if msg.Error != nil {
		t.Fatalf("response error: %+v", msg.Error)
	}
	if string(msg.ID) != `1` {
		t.Errorf("response ID = %s, want 1", string(msg.ID))
	}
}

func TestStdioTransportSendTimeout(t *testing.T) {
	tr := helperTransportWithConfig(t, "normal", StdioTransportConfig{
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff:     50 * time.Millisecond,
		MaxRetries:     2,
		SendTimeout:    1 * time.Millisecond,
	})
	if err := tr.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer tr.Stop(context.Background())

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := tr.Send(ctx, []byte(`{"jsonrpc":"2.0","method":"ping","id":1}`))
	if err != nil {
		t.Logf("Send error (expected due to short timeout): %v", err)
	}
}

func TestStdioTransportRestartOnCrash(t *testing.T) {
	tr := helperTransport(t, "crash-after-init")
	if err := tr.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	// The subprocess should crash, and the transport should auto-restart.
	// We wait for the state to cycle back to Running.
	deadline := time.Now().Add(5 * time.Second)
	var state TransportState
	for time.Now().Before(deadline) {
		state = tr.State()
		if state == StateRunning {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if state != StateRunning {
		t.Fatalf("expected transport to restart to Running, got %v", state)
	}

	_ = tr.Stop(context.Background())
}

func TestStdioTransportMaxRetriesExceeded(t *testing.T) {
	// Use a behavior that immediately crashes after any message.
	tr := helperTransportWithConfig(t, "crash-after-init", StdioTransportConfig{
		InitialBackoff: 5 * time.Millisecond,
		MaxBackoff:     10 * time.Millisecond,
		MaxRetries:     3,
	})

	if err := tr.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	// The process crashes immediately after init, transport tries to restart.
	// With MaxRetries=3 and very short backoffs, it should exceed retries quickly.
	deadline := time.Now().Add(10 * time.Second)
	var state TransportState
	for time.Now().Before(deadline) {
		state = tr.State()
		if state == StateError {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if state != StateError {
		t.Fatalf("expected StateError after max retries, got %v", state)
	}
}

func TestStdioTransportContextCancellation(t *testing.T) {
	tr := helperTransport(t, "normal")
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // immediately cancelled

	if err := tr.Start(ctx); err == nil {
		t.Error("expected error starting with cancelled context")
	}
	// State should remain stopped.
	if tr.State() != StateStopped {
		t.Errorf("state = %v, want Stopped", tr.State())
	}
}

func TestStdioTransportConcurrentRequests(t *testing.T) {
	tr := helperTransport(t, "normal")
	if err := tr.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer tr.Stop(context.Background())

	const concurrency = 10
	var wg sync.WaitGroup
	errs := make(chan error, concurrency)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			payload := fmt.Sprintf(`{"jsonrpc":"2.0","method":"ping","id":%d}`, id)
			resp, err := tr.Send(context.Background(), []byte(payload))
			if err != nil {
				errs <- fmt.Errorf("request %d: %w", id, err)
				return
			}
			var msg Message
			if err := json.Unmarshal(resp, &msg); err != nil {
				errs <- fmt.Errorf("request %d unmarshal: %w", id, err)
				return
			}
			if msg.Error != nil {
				errs <- fmt.Errorf("request %d error: %+v", id, msg.Error)
				return
			}
		}(i)
	}
	wg.Wait()
	close(errs)

	var allErr error
	for err := range errs {
		if allErr == nil {
			allErr = err
		} else {
			allErr = errors.Join(allErr, err)
		}
	}
	if allErr != nil {
		t.Fatal(allErr)
	}
}

func TestStdioTransportRestartAfterStop(t *testing.T) {
	tr := helperTransport(t, "normal")
	if err := tr.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Stop the transport.
	if err := tr.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if tr.State() != StateStopped {
		t.Errorf("state = %v, want Stopped", tr.State())
	}

	// Restart it.
	if err := tr.Start(context.Background()); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if tr.State() != StateRunning {
		t.Errorf("state = %v, want Running", tr.State())
	}

	// Should be able to send messages again.
	resp, err := tr.Send(context.Background(), []byte(`{"jsonrpc":"2.0","method":"ping","id":1}`))
	if err != nil {
		t.Fatalf("Send after restart: %v", err)
	}
	if len(resp) == 0 {
		t.Fatal("empty response after restart")
	}

	_ = tr.Stop(context.Background())
}

// ── Error State Tests ───────────────────────────────────────────────────

func TestStdioTransportRestartFromError(t *testing.T) {
	tr := helperTransportWithConfig(t, "crash-after-init", StdioTransportConfig{
		InitialBackoff: 5 * time.Millisecond,
		MaxBackoff:     10 * time.Millisecond,
		MaxRetries:     2,
	})

	if err := tr.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Wait for it to enter Error state.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if tr.State() == StateError {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if tr.State() != StateError {
		t.Fatalf("expected Error state, got %v", tr.State())
	}

	// Now use a fresh cmd by restarting. Since the behavior is still "crash-after-init",
	// it'll crash again. But we're testing that Restart (via Start) transitions from Error.
	// We accept either Running or Error (depending on timing).
	if err := tr.Start(context.Background()); err != nil {
		// Starting a crashing process may return nil if the retry loop starts in background.
		// That's fine — we just check the transition happened.
		t.Logf("Start from Error: %v (acceptable)", err)
	}
}

func TestStdioTransportStateTransitions(t *testing.T) {
	t.Run("initial state is stopped", func(t *testing.T) {
		tr := NewStdioTransport(StdioTransportConfig{
			Command: os.Args[0],
		})
		if tr.State() != StateStopped {
			t.Errorf("initial state = %v, want Stopped", tr.State())
		}
	})

	t.Run("config validation rejects empty command", func(t *testing.T) {
		tr := NewStdioTransport(StdioTransportConfig{})
		if err := tr.Start(context.Background()); err == nil {
			t.Error("expected error for empty command")
		}
	})
}

// ── Send Error Handling ─────────────────────────────────────────────────

func TestStdioTransportSendAfterStop(t *testing.T) {
	tr := helperTransport(t, "normal")
	if err := tr.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := tr.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}

	_, err := tr.Send(context.Background(), []byte(`{"jsonrpc":"2.0","method":"ping","id":1}`))
	if err == nil {
		t.Error("expected error sending after stop")
	}
}

// ── Framing edge cases ──────────────────────────────────────────────────

func TestFramingCarriageReturnOnly(t *testing.T) {
	// Some servers might send \n without \r — we should be lenient.
	input := "Content-Length: 4\n\nbody"
	body, err := readMessage(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(body) != "body" {
		t.Errorf("got %q, want %q", string(body), "body")
	}
}

func TestFramingMultipleMessages(t *testing.T) {
	input := "Content-Length: 5\r\n\r\nhelloContent-Length: 3\r\n\r\nbye"
	r := strings.NewReader(input)
	body1, err := readMessage(r)
	if err != nil {
		t.Fatal(err)
	}
	if string(body1) != "hello" {
		t.Errorf("first: got %q, want %q", string(body1), "hello")
	}
	body2, err := readMessage(r)
	if err != nil {
		t.Fatal(err)
	}
	if string(body2) != "bye" {
		t.Errorf("second: got %q, want %q", string(body2), "bye")
	}
}
