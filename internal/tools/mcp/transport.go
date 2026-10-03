package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

// ── Errors ──────────────────────────────────────────────────────────────

var (
	// ErrTransportStopped is returned when attempting to send on a stopped transport.
	ErrTransportStopped = errors.New("mcp: transport is stopped")
	// ErrTransportError is returned when the transport is in a permanent error state.
	ErrTransportError = errors.New("mcp: transport is in error state")
	// ErrMissingCommand is returned when Start is called with an empty command.
	ErrMissingCommand = errors.New("mcp: Command is required")
)

// ── Config ──────────────────────────────────────────────────────────────

// StdioTransportConfig configures the stdio MCP transport.
type StdioTransportConfig struct {
	// Command is the MCP server executable path. Required.
	Command string
	// Args are passed to the server executable.
	Args []string
	// Dir is the subprocess working directory. Empty inherits the daemon's
	// working directory.
	Dir string
	// Env is additional environment variables in NAME=value format.
	// The subprocess inherits the parent process environment; these are added
	// on top of it.
	Env []string

	// InitialBackoff is the starting delay before the first auto-restart.
	// Default: 500ms.
	InitialBackoff time.Duration
	// MaxBackoff caps the exponential backoff delay. Default: 30s.
	MaxBackoff time.Duration
	// MaxRetries caps restart attempts. 0 means unlimited (default).
	MaxRetries int
	// SendTimeout is how long to wait for the write to complete (not the
	// response). 0 means no timeout.
	SendTimeout time.Duration

	// ShutdownGrace is how long Stop waits for the subprocess to exit
	// after an interrupt signal before forcibly killing it.
	// Default: 5s.
	ShutdownGrace time.Duration
}

func (c StdioTransportConfig) effectiveInitialBackoff() time.Duration {
	if c.InitialBackoff <= 0 {
		return 500 * time.Millisecond
	}
	return c.InitialBackoff
}

func (c StdioTransportConfig) effectiveMaxBackoff() time.Duration {
	if c.MaxBackoff <= 0 {
		return 30 * time.Second
	}
	return c.MaxBackoff
}

func (c StdioTransportConfig) effectiveShutdownGrace() time.Duration {
	if c.ShutdownGrace <= 0 {
		return 5 * time.Second
	}
	return c.ShutdownGrace
}

// ── Transport ───────────────────────────────────────────────────────────

// StdioTransport runs an MCP server subprocess, exchanging newline-delimited
// JSON-RPC over stdin/stdout, and restarts it with backoff when it dies.
type StdioTransport struct {
	config StdioTransportConfig

	mu    sync.Mutex
	state TransportState
	cmd   *exec.Cmd
	stdin io.WriteCloser

	// writeMu serialises writes to stdin so that header and body from
	// different goroutines are not interleaved.
	writeMu sync.Mutex

	// pending maps message IDs (as raw JSON) to response channels.
	pending map[string]chan []byte

	// stopCh is closed by Stop(). Background goroutines select on this.
	stopCh chan struct{}

	// readerWg tracks the reader goroutine so Stop can wait for it.
	readerWg sync.WaitGroup

	// crashCount is incremented on each unexpected subprocess death and is
	// used to compute the exponential backoff delay. Reset to 0 on explicit
	// Start (not on auto-restart), so repeated crash loops produce
	// increasingly long backoff intervals up to MaxBackoff.
	crashCount int

	// startupFailures tracks consecutive spawn failures (command not found,
	// permissions, etc.). Reset to 0 on a successful spawn. When MaxRetries
	// is set and this exceeds it, the transport enters StateError.
	startupFailures int

	// serverRequestHandler answers requests the server sends to this client
	// (sampling/createMessage). Nil until a Client registers itself.
	serverRequestHandler ServerRequestHandler
	// handlerCancel ends the lifecycle context handed to the server-request
	// handler, so an in-flight sampling completion is not left running after
	// the transport is gone. The handler context itself is carried through
	// the reader call chain rather than stored.
	handlerCancel context.CancelFunc
	// serverRequestWg tracks in-flight handler goroutines so Stop can wait
	// for their responses to be written before the transport goes away.
	serverRequestWg sync.WaitGroup
}

// SetServerRequestHandler registers the handler for server-initiated
// requests. It implements serverRequestRouter.
func (t *StdioTransport) SetServerRequestHandler(handler ServerRequestHandler) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.serverRequestHandler = handler
}

// NewStdioTransport creates a new transport with the given config.
// Callers must call [*StdioTransport.Start] before sending messages.
func NewStdioTransport(config StdioTransportConfig) *StdioTransport {
	return &StdioTransport{
		config:  config,
		state:   StateStopped,
		pending: make(map[string]chan []byte),
	}
}

// State returns the current transport state.
func (t *StdioTransport) State() TransportState {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.state
}

// Start spawns the MCP server subprocess and begins reading responses.
// It returns after the process has started successfully. If the context
// is cancelled before the process is ready, Start returns the context error.
func (t *StdioTransport) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if t.config.Command == "" {
		return ErrMissingCommand
	}

	t.mu.Lock()
	if t.state == StateRunning {
		t.mu.Unlock()
		return fmt.Errorf("mcp: already running")
	}
	if t.state == StateStarting {
		t.mu.Unlock()
		return fmt.Errorf("mcp: already starting")
	}
	if t.state == StateStopping {
		t.mu.Unlock()
		return fmt.Errorf("mcp: stop in progress")
	}
	t.state = StateStarting
	// Create a fresh stop channel for this lifecycle. If we're restarting
	// from Error state, the previous stop channel is already closed.
	if t.stopCh != nil {
		select {
		case <-t.stopCh:
		default:
			close(t.stopCh)
		}
	}
	t.stopCh = make(chan struct{})
	// The reader (and thus every server-request handler it dispatches) runs
	// on a cancelable context derived from the caller's, decoupled from the
	// caller's own cancellation the same way the subprocess is. Stop cancels
	// it so an in-flight sampling completion ends with the transport.
	handlerCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	t.handlerCancel = cancel
	// Reset crash and failure counters on explicit user start.
	t.crashCount = 0
	t.startupFailures = 0
	t.mu.Unlock()

	return t.startSubprocess(handlerCtx)
}

// stopSubprocess kills the current subprocess and waits for it to exit.
// Must be called with t.mu NOT held (it waits for process exit).
func (t *StdioTransport) stopSubprocess() {
	t.mu.Lock()
	cmd := t.cmd
	stdin := t.stdin
	t.cmd = nil
	t.stdin = nil
	t.mu.Unlock()

	// Close stdin first to signal the server to exit.
	if stdin != nil {
		_ = stdin.Close()
	}

	// Kill the process.
	if cmd != nil && cmd.Process != nil {
		// Try graceful shutdown via SIGTERM first.
		_ = cmd.Process.Signal(os.Interrupt)
		done := make(chan struct{})
		go func() {
			_ = cmd.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(t.config.effectiveShutdownGrace()):
			_ = cmd.Process.Kill()
			<-done
		}
	}
}

// Stop gracefully shuts down the transport. It kills the subprocess,
// cancels pending requests, and waits for background goroutines. Calling
// Stop on a stopped or never-started transport is a no-op.
func (t *StdioTransport) Stop(_ context.Context) error {
	t.mu.Lock()
	if t.state == StateStopped {
		t.mu.Unlock()
		return nil
	}
	// Set StateStopping and cancel the transport context atomically, in the
	// same critical section. Splitting these across two lock acquisitions
	// opens a window where a concurrent Start() could see a state it isn't
	// guarding against and race a fresh subprocess against this shutdown.
	t.state = StateStopping
	if t.stopCh != nil {
		select {
		case <-t.stopCh:
		default:
			close(t.stopCh)
		}
	}
	cancel := t.handlerCancel
	t.mu.Unlock()

	if cancel != nil {
		cancel()
	}

	// Kill the subprocess.
	t.stopSubprocess()

	// Wait for the reader goroutine to finish, then for any in-flight
	// server-request handler it dispatched. Waiting for the reader first
	// guarantees no further handler goroutines can be added.
	t.readerWg.Wait()
	t.serverRequestWg.Wait()

	// Fail all pending requests.
	t.mu.Lock()
	for id, ch := range t.pending {
		close(ch)
		delete(t.pending, id)
	}
	t.state = StateStopped
	t.mu.Unlock()

	return nil
}

// Send writes a request and waits for the response with the same id.
func (t *StdioTransport) Send(ctx context.Context, body []byte) ([]byte, error) {
	// Extract the message ID from the body for response correlation.
	msgID, err := extractMessageID(body)
	if err != nil {
		return nil, fmt.Errorf("mcp: extract id: %w", err)
	}

	t.mu.Lock()

	if t.state != StateRunning {
		t.mu.Unlock()
		return nil, ErrTransportStopped
	}

	stdin := t.stdin // capture under lock

	// Register the pending response channel.
	ch := make(chan []byte, 1)
	t.pending[msgID] = ch

	t.mu.Unlock()

	// Write the framed message under writeMu to prevent interleaving with
	// concurrent Send calls on the same pipe.
	t.writeMu.Lock()
	err = writeMessageWithTimeout(stdin, body, t.config.SendTimeout)
	t.writeMu.Unlock()

	if err != nil {
		// Cleanup the pending entry on write failure.
		t.mu.Lock()
		// Check if someone else already consumed the channel (e.g., reader
		// error handling closed it). Only close if it's still ours.
		if ch2, ok := t.pending[msgID]; ok && ch2 == ch {
			close(ch)
			delete(t.pending, msgID)
		}
		t.mu.Unlock()
		return nil, fmt.Errorf("mcp: write: %w", err)
	}

	// Wait for the response.
	select {
	case <-ctx.Done():
		// De-register on context cancellation  --  no cleanup needed on the
		// channel since the reader will close it when it tries to deliver.
		t.mu.Lock()
		delete(t.pending, msgID)
		t.mu.Unlock()
		return nil, ctx.Err()

	case resp, ok := <-ch:
		if !ok {
			// Channel closed  --  the transport was stopped or the subprocess
			// crashed before we got a response.
			return nil, errors.New("mcp: transport closed while waiting for response")
		}
		return resp, nil
	}
}

// Notify writes a notification without waiting for a response.
func (t *StdioTransport) Notify(ctx context.Context, body []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	t.mu.Lock()
	if t.state != StateRunning {
		t.mu.Unlock()
		return ErrTransportStopped
	}
	stdin := t.stdin // capture under lock
	t.mu.Unlock()

	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	if err := writeMessageWithTimeout(stdin, body, t.config.SendTimeout); err != nil {
		return fmt.Errorf("mcp: notify write: %w", err)
	}
	return nil
}

// ── Internal subprocess management ─────────────────────────────────────

// startSubprocess spawns the MCP server subprocess and starts the reader
// goroutine. Must be called without t.mu held.
func (t *StdioTransport) startSubprocess(ctx context.Context) error {
	cmd, stdin, stdout, err := t.spawnProcess(ctx)
	if err != nil {
		t.mu.Lock()
		t.state = StateError
		t.mu.Unlock()
		return err
	}
	return t.commitSpawnedProcess(ctx, cmd, stdin, stdout, false)
}

// commitSpawnedProcess installs a spawned subprocess and starts its reader,
// unless Stop has already run, in which case the process is killed.
// resetStartupFailures resets the spawn-failure counter.
func (t *StdioTransport) commitSpawnedProcess(ctx context.Context, cmd *exec.Cmd, stdin io.WriteCloser, stdout *bufio.Reader, resetStartupFailures bool) error {
	t.mu.Lock()
	if t.isStopped() {
		t.state = StateStopped
		t.mu.Unlock()
		_ = stdin.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
		return errTransportStopped
	}
	t.cmd = cmd
	t.stdin = stdin
	t.state = StateRunning
	if resetStartupFailures {
		t.startupFailures = 0
	}
	t.mu.Unlock()

	// Start the reader goroutine.
	t.readerWg.Add(1)
	go t.runReader(ctx, stdout)

	return nil
}

// spawnProcess creates and starts the subprocess. Returns the cmd,
// stdin writer, and buffered stdout reader.
func (t *StdioTransport) spawnProcess(ctx context.Context) (*exec.Cmd, io.WriteCloser, *bufio.Reader, error) {
	cmd := exec.CommandContext(ctx, t.config.Command, t.config.Args...)
	cmd.Env = append(os.Environ(), t.config.Env...)
	cmd.Dir = t.config.Dir

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, nil, nil, fmt.Errorf("stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, nil, nil, fmt.Errorf("start: %w", err)
	}

	return cmd, stdin, bufio.NewReader(stdout), nil
}

// runReader loops reading newline-delimited JSON messages from the
// subprocess's stdout, routing responses to the matching pending
// channels. On error (process crash, pipe close), it initiates the
// auto-restart sequence.
func (t *StdioTransport) runReader(ctx context.Context, reader *bufio.Reader) {
	defer t.readerWg.Done()

	for {
		body, err := readMessage(reader)
		if err != nil {
			// The subprocess is dead or the pipe is broken. Attempt restart
			// if we weren't asked to stop.
			t.mu.Lock()
			cancelled := t.isStopped()
			shuttingDown := t.state == StateStopping || t.state == StateStopped
			t.mu.Unlock()

			if cancelled || shuttingDown {
				return
			}

			t.handleProcessDeath(ctx)
			return
		}

		t.deliverResponse(ctx, body)
	}
}

// deliverResponse routes one message read from the subprocess's stdout. A
// response goes to the caller waiting on its id; a server-initiated request
// goes to the registered handler; a notification has no response to route.
func (t *StdioTransport) deliverResponse(ctx context.Context, body []byte) {
	var msg Message
	if err := json.Unmarshal(body, &msg); err != nil {
		return
	}
	if msg.IsRequest() {
		t.dispatchServerRequest(ctx, msg)
		return
	}
	if len(msg.ID) == 0 {
		// A server notification carries no response to route.
		return
	}

	msgID := string(msg.ID)
	t.mu.Lock()
	ch, ok := t.pending[msgID]
	if ok {
		delete(t.pending, msgID)
	}
	t.mu.Unlock()

	if ok {
		// Non-blocking send: if the caller has already timed out, the
		// channel might still be in the map but has a full buffer.
		// Since we use a buffered channel (cap 1), this should succeed.
		select {
		case ch <- body:
		default:
			// Caller is no longer waiting  --  discard.
		}
	}
}

// dispatchServerRequest runs the registered handler for a server-initiated
// request without blocking the reader, and writes its response back. A
// missing handler is answered method-not-found, never dropped: the server is
// blocked on a reply for its own request id.
func (t *StdioTransport) dispatchServerRequest(ctx context.Context, msg Message) {
	t.mu.Lock()
	handler := t.serverRequestHandler
	t.mu.Unlock()

	t.serverRequestWg.Go(func() {
		var (
			result json.RawMessage
			rpcErr *ErrorData
		)
		if handler == nil {
			rpcErr = &ErrorData{Code: ErrCodeMethodNotFound, Message: "method not found: " + msg.Method}
		} else {
			result, rpcErr = handler(ctx, msg.Method, msg.Params)
		}
		t.respondToServerRequest(msg.ID, result, rpcErr)
	})
}

// respondToServerRequest writes a JSON-RPC response for a server-initiated
// request back to the subprocess's stdin.
func (t *StdioTransport) respondToServerRequest(id, result json.RawMessage, rpcErr *ErrorData) {
	data, err := json.Marshal(Message{JSONRPC: "2.0", ID: id, Result: result, Error: rpcErr})
	if err != nil {
		return
	}

	t.mu.Lock()
	stdin := t.stdin
	running := t.state == StateRunning
	t.mu.Unlock()
	if !running || stdin == nil {
		return
	}

	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	// The error is unreportable here: the transport's only job is to answer
	// the server's own request, and a failed write is exactly a subprocess
	// that has already gone away (its reader handles the restart).
	_ = writeMessageWithTimeout(stdin, data, t.config.SendTimeout)
}

// handleProcessDeath is called when the reader detects the subprocess
// has died. It increments crashCount for backoff, transitions to Starting,
// closes pending channels, and initiates the auto-restart sequence.
func (t *StdioTransport) handleProcessDeath(ctx context.Context) {
	t.mu.Lock()
	if t.state != StateRunning {
		t.mu.Unlock()
		return
	}

	t.crashCount++
	t.state = StateStarting
	cmd := t.cmd
	t.cmd = nil
	t.stdin = nil

	// Fail all pending requests  --  the subprocess is gone.
	t.failAllPendingLocked()
	t.mu.Unlock()

	if cmd != nil {
		_ = cmd.Wait()
	}

	// Attempt restart (outside lock because it waits).
	t.attemptRestart(ctx)
}

// failAllPendingLocked closes all pending response channels with an error.
// Must hold t.mu.
func (t *StdioTransport) failAllPendingLocked() {
	for id, ch := range t.pending {
		close(ch)
		delete(t.pending, id)
	}
}

// attemptRestart restarts a crashed subprocess with exponential backoff until
// it starts, the transport stops, or spawn failures reach MaxRetries.
func (t *StdioTransport) attemptRestart(ctx context.Context) {
	for {
		// Check preconditions under lock.
		t.mu.Lock()
		if t.isStopped() {
			t.state = StateStopped
			t.mu.Unlock()
			return
		}
		if t.state != StateStarting {
			t.mu.Unlock()
			return
		}

		backoff := t.computeBackoffLocked()

		// Check spawn-failure limit (not crash limit  --  crash loops are
		// bounded by the growing backoff cap).
		if t.config.MaxRetries > 0 && t.startupFailures >= t.config.MaxRetries {
			t.state = StateError
			t.mu.Unlock()
			return
		}
		if t.config.MaxRetries > 0 && t.crashCount >= t.config.MaxRetries {
			// Crash loops (spawn succeeds, process immediately dies) also
			// count toward the retry limit so we don't restart forever.
			t.state = StateError
			t.mu.Unlock()
			return
		}
		t.mu.Unlock()

		// Wait for the backoff period, but abort if the transport is stopped.
		timer := time.NewTimer(backoff)
		select {
		case <-t.stopCh:
			timer.Stop()
			t.mu.Lock()
			t.state = StateStopped
			t.mu.Unlock()
			return
		case <-timer.C:
		}

		// Try to start the subprocess.
		cmd, stdin, stdout, err := t.spawnProcess(ctx)
		if err != nil {
			t.mu.Lock()
			t.startupFailures++
			t.mu.Unlock()
			continue
		}

		// Success  --  commit via the same guarded path startSubprocess uses,
		// so a concurrent Stop() that cancelled the context while
		// spawnProcess was in flight discards this subprocess instead of
		// clobbering the Stopped state Stop() already reported.
		_ = t.commitSpawnedProcess(ctx, cmd, stdin, stdout, true)
		return
	}
}

// computeBackoffLocked returns the exponential backoff delay based on
// the current crashCount. Must hold t.mu.
func (t *StdioTransport) computeBackoffLocked() time.Duration {
	d := t.config.effectiveInitialBackoff()
	for i := 1; i < t.crashCount; i++ {
		d *= 2
		if d > t.config.effectiveMaxBackoff() {
			return t.config.effectiveMaxBackoff()
		}
	}
	return d
}

// writeMessageWithTimeout writes a framed message, giving up after timeout. A
// non-positive timeout waits indefinitely.
func writeMessageWithTimeout(w io.Writer, data []byte, timeout time.Duration) error {
	if timeout <= 0 {
		return writeMessage(w, data)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- writeMessage(w, data)
	}()

	select {
	case err := <-errCh:
		return err
	case <-time.After(timeout):
		return fmt.Errorf("mcp: write timed out after %s", timeout)
	}
}

// ── Helpers ─────────────────────────────────────────────────────────────

// isStopped returns true if the transport stop channel has been closed.
// Must be called with t.mu held or from a goroutine that doesn't need
// the lock for ordering.
func (t *StdioTransport) isStopped() bool {
	select {
	case <-t.stopCh:
		return true
	default:
		return false
	}
}

// errTransportStopped is returned by the lifecycle context when the
// transport is stopped.
var errTransportStopped = errors.New("mcp: transport stopped")

// extractMessageID extracts the "id" field from a JSON body as a raw JSON
// string. This is used as the key for correlating requests with responses.
// A missing "id" returns an empty string (for notifications, which don't
// expect responses).
func extractMessageID(body []byte) (string, error) {
	var raw struct {
		ID json.RawMessage `json:"id"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return "", fmt.Errorf("parse id: %w", err)
	}
	return string(raw.ID), nil
}
