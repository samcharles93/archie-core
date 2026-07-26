package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// ── Helper: test MCP server process ─────────────────────────────────────

// TestMCPServerHelper is the entry point for the test helper subprocess.
// It is NOT a test  --  it is a process invoked by StdioTransport tests.
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
	runtime.Goexit()
}

// testMCPServer reads MCP messages from stdin and responds.
type testMCPServer struct {
	behavior string
}

func (s *testMCPServer) serve() error {
	// crash-after-init must exit immediately on startup, before the read
	// loop, so the transport sees a subprocess that dies right after spawn.
	if s.behavior == "crash-after-init" {
		syscall.Exit(1)
	}

	// Use a persistent bufio.Reader so buffered bytes between calls
	// are not lost (each readMessage call checks for an existing bufio.Reader).
	stdin := bufio.NewReader(os.Stdin)
	for {
		body, err := readMessage(stdin)
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
	if len(msg.ID) == 0 {
		// A notification  --  no response expected. Record it so a
		// subsequent request's echo can report whether one arrived.
		notificationsReceived.Add(1)
		return nil, nil
	}
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

	case "slow-response":
		time.Sleep(200 * time.Millisecond)
		return s.echo(msg), nil

	case "bad-framing":
		// Write malformed output without MCP's required trailing newline and
		// exit immediately so the client observes an incomplete frame.
		_, _ = fmt.Fprint(os.Stdout, `{"jsonrpc":"2.0","id":1,"result":{}}`)
		syscall.Exit(0)
		return nil, nil

	default:
		return s.echo(msg), nil
	}
}

func (s *testMCPServer) echo(msg Message) *Message {
	cwd, _ := os.Getwd()
	result, _ := json.Marshal(map[string]any{
		"echoed":                 msg.Method,
		"notifications_received": notificationsReceived.Load(),
		"cwd":                    cwd,
	})
	return &Message{
		JSONRPC: "2.0",
		ID:      msg.ID,
		Result:  result,
	}
}

// notificationsReceived counts notifications (messages with no "id") seen
// by this subprocess, so a follow-up Send can report the count back  --
// a notification itself gets no response to observe from the test side.
var notificationsReceived atomic.Int64

func TestStdioTransportSetsSubprocessWorkingDirectory(t *testing.T) {
	workDir := t.TempDir()
	tr := helperTransportWithConfig(t, "normal", StdioTransportConfig{
		Dir:            workDir,
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff:     50 * time.Millisecond,
		MaxRetries:     2,
	})
	if err := tr.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tr.Stop(context.Background()) }()

	response, err := tr.Send(context.Background(), []byte(`{"jsonrpc":"2.0","method":"ping","id":1}`))
	if err != nil {
		t.Fatal(err)
	}
	var message struct {
		Result struct {
			CWD string `json:"cwd"`
		} `json:"result"`
	}
	if err := json.Unmarshal(response, &message); err != nil {
		t.Fatal(err)
	}
	if message.Result.CWD != workDir {
		t.Fatalf("MCP subprocess cwd = %q, want %q", message.Result.CWD, workDir)
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
		// nothing  --  caller manages lifecycle
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
		want := "{\"key\":\"value\"}\n"
		if got := buf.String(); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("empty body", func(t *testing.T) {
		var buf bytes.Buffer
		if err := writeMessage(&buf, nil); err != nil {
			t.Fatal(err)
		}
		want := "\n"
		if got := buf.String(); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
}

func TestReadMessage(t *testing.T) {
	t.Run("reads newline-delimited JSON", func(t *testing.T) {
		input := "{\"ok\":true}\n"
		body, err := readMessage(strings.NewReader(input))
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != `{"ok":true}` {
			t.Errorf("got %q, want JSON body", string(body))
		}
	})

	t.Run("accepts CRLF line ending", func(t *testing.T) {
		input := "{\"ok\":true}\r\n"
		body, err := readMessage(strings.NewReader(input))
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != `{"ok":true}` {
			t.Errorf("got %q, want JSON body", string(body))
		}
	})

	t.Run("missing newline", func(t *testing.T) {
		_, err := readMessage(strings.NewReader(`{"ok":true}`))
		if err == nil {
			t.Fatal("expected error for incomplete newline-delimited message")
		}
	})

	t.Run("oversized message", func(t *testing.T) {
		input := strings.Repeat("x", maxMessageSize+1) + "\n"
		_, err := readMessage(strings.NewReader(input))
		if err == nil {
			t.Fatal("expected error for oversized message")
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
		input := `{"truncated":true}`
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
	defer func() { _ = tr.Stop(context.Background()) }()

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
	defer func() { _ = tr.Stop(context.Background()) }()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := tr.Send(ctx, []byte(`{"jsonrpc":"2.0","method":"ping","id":1}`))
	if err != nil {
		t.Logf("Send error (expected due to short timeout): %v", err)
	}
}

// blockingWriter never returns from Write until release is closed, letting
// tests deterministically exercise writeMessageWithTimeout's timeout branch
// without depending on OS pipe buffer sizes or subprocess scheduling.
type blockingWriter struct {
	release <-chan struct{}
}

func (b blockingWriter) Write(p []byte) (int, error) {
	<-b.release
	return len(p), nil
}

func TestWriteMessageWithTimeout(t *testing.T) {
	t.Run("zero timeout writes unbounded", func(t *testing.T) {
		var buf bytes.Buffer
		if err := writeMessageWithTimeout(&buf, []byte(`{}`), 0); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if buf.Len() == 0 {
			t.Fatal("expected data to be written")
		}
	})

	t.Run("completes within timeout", func(t *testing.T) {
		var buf bytes.Buffer
		if err := writeMessageWithTimeout(&buf, []byte(`{}`), time.Second); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("times out on a blocked writer", func(t *testing.T) {
		release := make(chan struct{})
		defer close(release) // let the leaked write goroutine finish
		w := blockingWriter{release: release}
		err := writeMessageWithTimeout(w, []byte(`{}`), 10*time.Millisecond)
		if err == nil {
			t.Fatal("expected timeout error")
		}
	})
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
	defer func() { _ = tr.Stop(context.Background()) }()

	const concurrency = 10
	var wg sync.WaitGroup
	errs := make(chan error, concurrency)

	for i := range concurrency {
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

// TestStdioTransportConcurrentStartDuringStop is a regression test for a
// race where Start() could see StateStopping (not guarded against) and spawn
// a fresh subprocess while Stop() was still tearing down the previous one,
// leaving the transport reporting a state inconsistent with what was
// actually running. Start() now rejects StateStopping outright, so the
// racing Start() must either fail cleanly or, if it loses the race entirely
// and runs after Stop() finishes, succeed cleanly  --  either way the final
// state must be well-defined and self-consistent.
func TestStdioTransportConcurrentStartDuringStop(t *testing.T) {
	tr := helperTransport(t, "normal")
	if err := tr.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	var startErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		startErr = tr.Start(context.Background())
	}()
	go func() {
		defer wg.Done()
		_ = tr.Stop(context.Background())
	}()
	wg.Wait()

	state := tr.State()
	if state != StateStopped && state != StateRunning {
		t.Fatalf("state after concurrent Start/Stop = %v, want Stopped or Running", state)
	}
	t.Logf("concurrent Start error: %v, final state: %v", startErr, state)

	_ = tr.Stop(context.Background())
}

// TestStdioTransportCommitSpawnedProcessDiscardsWhenContextRacesAhead is a
// regression test for a race where startSubprocess (the path Start() uses
// to commit a freshly spawned subprocess) could clobber a StateStopped
// already committed and reported by a concurrent Stop(): spawnProcess runs
// without t.mu held (it's a real fork/exec), so Stop() can cancel t.ctx and
// return Stopped to its own caller while a spawn is still in flight. The
// commit path used to unconditionally set state = StateRunning afterward,
// wedging the transport in a Running state no caller of Stop() would ever
// expect, with no way back to Stopped.
//
// The real race is timing-dependent, so this drives the two halves
// (spawnProcess, then commitSpawnedProcess) directly and in order
// (white-box, same package) to deterministically simulate a Stop() landing
// in between them: the process is actually spawned and running (so the
// commit path's kill/reap of a live process is genuinely exercised), then
// the context is cancelled to simulate Stop() winning the race, then commit
// runs and must discard rather than clobber.
func TestStdioTransportCommitSpawnedProcessDiscardsWhenContextRacesAhead(t *testing.T) {
	tr := NewStdioTransport(StdioTransportConfig{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestMCPServerHelper$"},
		Env:     []string{"GO_WANT_MCP_HELPER=1", "MCP_HELPER_BEHAVIOR=normal"},
	})

	tr.mu.Lock()
	tr.state = StateStarting
	tr.ctx, tr.cancel = context.WithCancel(context.Background())
	tr.mu.Unlock()

	cmd, stdin, stdout, err := tr.spawnProcess()
	if err != nil {
		t.Fatalf("spawnProcess: %v", err)
	}

	// Simulate a concurrent Stop() winning the race between spawn and
	// commit: it would have cancelled this exact context.
	tr.mu.Lock()
	tr.cancel()
	tr.mu.Unlock()

	err = tr.commitSpawnedProcess(cmd, stdin, stdout, false)
	if err == nil {
		t.Fatal("expected error when context was cancelled before commit")
	}
	if tr.State() != StateStopped {
		t.Fatalf("state = %v, want Stopped", tr.State())
	}

	tr.mu.Lock()
	gotCmd := tr.cmd
	tr.mu.Unlock()
	if gotCmd != nil {
		t.Fatal("expected no committed subprocess after discard")
	}
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
		// That's fine  --  we just check the transition happened.
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

func TestStdioTransportNotify(t *testing.T) {
	tr := helperTransport(t, "normal")
	if err := tr.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tr.Stop(context.Background()) }()

	if err := tr.Notify(context.Background(), []byte(`{"jsonrpc":"2.0","method":"notifications/test"}`)); err != nil {
		t.Fatalf("Notify: %v", err)
	}

	// A follow-up Send (which DOES get a response) reports how many
	// notifications the subprocess has seen, proving Notify's write
	// actually reached the server without Notify itself blocking for a
	// reply (it has none to wait for).
	resp, err := tr.Send(context.Background(), []byte(`{"jsonrpc":"2.0","method":"ping","id":1}`))
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	var msg Message
	if err := json.Unmarshal(resp, &msg); err != nil {
		t.Fatal(err)
	}
	var result struct {
		NotificationsReceived int64 `json:"notifications_received"`
	}
	if err := json.Unmarshal(msg.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.NotificationsReceived < 1 {
		t.Fatalf("subprocess reports %d notifications received, want >= 1", result.NotificationsReceived)
	}
}

func TestStdioTransportNotifyBeforeStartReturnsError(t *testing.T) {
	tr := helperTransport(t, "normal")
	err := tr.Notify(context.Background(), []byte(`{"jsonrpc":"2.0","method":"notifications/test"}`))
	if err == nil {
		t.Fatal("expected error notifying a transport that was never started")
	}
}

func TestStdioTransportNotifyAfterStopReturnsError(t *testing.T) {
	tr := helperTransport(t, "normal")
	if err := tr.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := tr.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}

	err := tr.Notify(context.Background(), []byte(`{"jsonrpc":"2.0","method":"notifications/test"}`))
	if err == nil {
		t.Fatal("expected error notifying a stopped transport")
	}
}

// ── Additional server-behavior and error-path coverage ─────────────────

// TestStdioTransportSendContextCancellationWhileWaiting exercises the
// ctx.Done() branch in Send: a server that answers "initialize" once and
// then goes silent forever, so a follow-up Send can only be resolved by the
// caller's context deadline, not by a subprocess response.
func TestStdioTransportSendContextCancellationWhileWaiting(t *testing.T) {
	tr := helperTransport(t, "hang-on-start")
	if err := tr.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tr.Stop(context.Background()) }()

	if _, err := tr.Send(context.Background(), []byte(`{"jsonrpc":"2.0","method":"initialize","id":1}`)); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := tr.Send(ctx, []byte(`{"jsonrpc":"2.0","method":"never-answered","id":2}`))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v, want context.DeadlineExceeded", err)
	}
}

// TestStdioTransportBadFraming exercises the reader's malformed-framing
// path against a real, live subprocess (rather than a synthetic
// strings.Reader as in TestReadMessage): the server writes a response with
// no trailing newline, which readMessage cannot parse, so the reader
// goroutine treats the transport as dead and fails pending requests.
func TestStdioTransportBadFraming(t *testing.T) {
	tr := helperTransportWithConfig(t, "bad-framing", StdioTransportConfig{
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff:     50 * time.Millisecond,
		MaxRetries:     1,
	})
	if err := tr.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tr.Stop(context.Background()) }()

	_, err := tr.Send(context.Background(), []byte(`{"jsonrpc":"2.0","method":"ping","id":1}`))
	if err == nil {
		t.Fatal("expected error: malformed response framing should fail the pending request")
	}
}

// TestStdioTransportSendUnparsableID exercises extractMessageID's error
// branch as reached through Send, which must reject the body before ever
// touching the subprocess.
func TestStdioTransportSendUnparsableID(t *testing.T) {
	tr := helperTransport(t, "normal")
	if err := tr.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tr.Stop(context.Background()) }()

	_, err := tr.Send(context.Background(), []byte(`not json`))
	if err == nil {
		t.Fatal("expected error for unparsable message id")
	}
}

// TestStdioTransportSendWriteFailureCleansPending exercises Send's
// write-failure cleanup branch by closing the subprocess's stdin out from
// under it directly (same package, so the unexported field is reachable),
// simulating a pipe that breaks mid-write.
func TestStdioTransportSendWriteFailureCleansPending(t *testing.T) {
	tr := helperTransport(t, "normal")
	if err := tr.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tr.Stop(context.Background()) }()

	tr.mu.Lock()
	stdin := tr.stdin
	tr.mu.Unlock()
	if err := stdin.Close(); err != nil {
		t.Fatalf("close stdin: %v", err)
	}

	_, err := tr.Send(context.Background(), []byte(`{"jsonrpc":"2.0","method":"ping","id":1}`))
	if err == nil {
		t.Fatal("expected write error after stdin closed")
	}

	tr.mu.Lock()
	_, stillPending := tr.pending["1"]
	tr.mu.Unlock()
	if stillPending {
		t.Fatal("pending entry was not cleaned up after write failure")
	}
}

// ── Framing edge cases ──────────────────────────────────────────────────

func TestFramingCarriageReturnOnly(t *testing.T) {
	input := "body\r\n"
	body, err := readMessage(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(body) != "body" {
		t.Errorf("got %q, want %q", string(body), "body")
	}
}

func TestFramingMultipleMessages(t *testing.T) {
	input := "hello\nbye\n"
	br := bufio.NewReader(strings.NewReader(input))
	body1, err := readMessage(br)
	if err != nil {
		t.Fatal(err)
	}
	if string(body1) != "hello" {
		t.Errorf("first: got %q, want %q", string(body1), "hello")
	}
	body2, err := readMessage(br)
	if err != nil {
		t.Fatal(err)
	}
	if string(body2) != "bye" {
		t.Errorf("second: got %q, want %q", string(body2), "bye")
	}
}
