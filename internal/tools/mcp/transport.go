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

// ── Transport ───────────────────────────────────────────────────────────

// StdioTransport manages an MCP server subprocess, sending JSON-RPC 2.0
// messages to its stdin and receiving responses from its stdout. Messages
// are framed with Content-Length headers.
//
// The transport supports automatic restart with exponential backoff when
// the subprocess dies unexpectedly. Use [StdioTransport.Start] to begin,
// [StdioTransport.Send] to exchange messages, and [StdioTransport.Stop]
// to shut down cleanly.
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

	// ctx is cancelled by Stop(). Background goroutines select on this.
	ctx    context.Context
	cancel context.CancelFunc

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
	t.state = StateStarting
	// Create a fresh context for this lifecycle. If we're restarting from
	// Error state, the previous context was already cancelled; make a new one.
	if t.ctx != nil {
		t.cancel()
	}
	t.ctx, t.cancel = context.WithCancel(context.Background())
	// Reset crash and failure counters on explicit user start.
	t.crashCount = 0
	t.startupFailures = 0
	t.mu.Unlock()

	return t.startSubprocess()
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
		case <-time.After(5 * time.Second):
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
	t.state = StateStopping
	t.mu.Unlock()

	// Cancel the transport context — this signals all background
	// goroutines (reader, restart timer) to stop.
	t.mu.Lock()
	if t.cancel != nil {
		t.cancel()
	}
	t.mu.Unlock()

	// Kill the subprocess.
	t.stopSubprocess()

	// Wait for the reader goroutine to finish.
	t.readerWg.Wait()

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

// Send writes a JSON-RPC 2.0 request to the subprocess stdin and waits for
// the matching response on stdout. It uses the "id" field from the JSON
// body to correlate the response.
//
// Send returns an error if the transport is not running, if the write fails,
// if the context is cancelled, or if the transport is stopped while waiting.
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
	err = writeMessage(stdin, body)
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
		// De-register on context cancellation — no cleanup needed on the
		// channel since the reader will close it when it tries to deliver.
		t.mu.Lock()
		delete(t.pending, msgID)
		t.mu.Unlock()
		return nil, ctx.Err()

	case resp, ok := <-ch:
		if !ok {
			// Channel closed — the transport was stopped or the subprocess
			// crashed before we got a response.
			return nil, errors.New("mcp: transport closed while waiting for response")
		}
		return resp, nil
	}
}

// ── Internal subprocess management ─────────────────────────────────────

// startSubprocess spawns the MCP server subprocess and starts the reader
// goroutine. Must be called without t.mu held.
func (t *StdioTransport) startSubprocess() error {
	cmd, stdin, stdout, err := t.spawnProcess()
	if err != nil {
		t.mu.Lock()
		t.state = StateError
		t.mu.Unlock()
		return err
	}

	t.mu.Lock()
	t.cmd = cmd
	t.stdin = stdin
	t.state = StateRunning
	t.mu.Unlock()

	// Start the reader goroutine.
	t.readerWg.Add(1)
	go t.runReader(stdout)

	return nil
}

// spawnProcess creates and starts the subprocess. Returns the cmd,
// stdin writer, and buffered stdout reader.
func (t *StdioTransport) spawnProcess() (*exec.Cmd, io.WriteCloser, *bufio.Reader, error) {
	cmd := exec.CommandContext(t.ctx, t.config.Command, t.config.Args...)
	cmd.Env = append(os.Environ(), t.config.Env...)

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

// runReader loops reading Content-Length framed messages from the
// subprocess's stdout, routing responses to the matching pending
// channels. On error (process crash, pipe close), it initiates the
// auto-restart sequence.
func (t *StdioTransport) runReader(reader *bufio.Reader) {
	defer t.readerWg.Done()

	for {
		body, err := readMessage(reader)
		if err != nil {
			// The subprocess is dead or the pipe is broken. Attempt restart
			// if we weren't asked to stop.
			t.mu.Lock()
			cancelled := t.ctx.Err() != nil
			shuttingDown := t.state == StateStopping || t.state == StateStopped
			t.mu.Unlock()

			if cancelled || shuttingDown {
				return
			}

			t.handleProcessDeath()
			return
		}

		t.deliverResponse(body)
	}
}

// deliverResponse routes a response body to the caller waiting for it.
func (t *StdioTransport) deliverResponse(body []byte) {
	msgID, err := extractMessageID(body)
	if err != nil {
		// Cannot route a message without an ID. This could be a server
		// notification (no ID) — we silently drop it rather than blocking.
		// In a full MCP client implementation, notifications would be
		// handled via a callback.
		return
	}

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
			// Caller is no longer waiting — discard.
		}
	}
}

// handleProcessDeath is called when the reader detects the subprocess
// has died. It increments crashCount for backoff, transitions to Starting,
// closes pending channels, and initiates the auto-restart sequence.
func (t *StdioTransport) handleProcessDeath() {
	t.mu.Lock()
	if t.state != StateRunning {
		t.mu.Unlock()
		return
	}

	t.crashCount++
	t.state = StateStarting
	t.cmd = nil
	t.stdin = nil

	// Fail all pending requests — the subprocess is gone.
	t.failAllPendingLocked()
	t.mu.Unlock()

	// Attempt restart (outside lock because it waits).
	t.attemptRestart()
}

// failAllPendingLocked closes all pending response channels with an error.
// Must hold t.mu.
func (t *StdioTransport) failAllPendingLocked() {
	for id, ch := range t.pending {
		close(ch)
		delete(t.pending, id)
	}
}

// attemptRestart runs the auto-restart loop with exponential backoff.
// It is called after a subprocess crash and runs until either the
// transport is stopped, max retries (for spawn failures) are exceeded,
// or the subprocess successfully starts.
//
// Crash loops where the process starts but immediately dies are bounded
// by exponential backoff growing up to MaxBackoff. Spawn failures
// (binary not found, permissions, etc.) count against MaxRetries.
func (t *StdioTransport) attemptRestart() {
	for {
		// Check preconditions under lock.
		t.mu.Lock()
		if t.ctx.Err() != nil {
			t.state = StateStopped
			t.mu.Unlock()
			return
		}
		if t.state != StateStarting {
			t.mu.Unlock()
			return
		}

		backoff := t.computeBackoffLocked()

		// Check spawn-failure limit (not crash limit — crash loops are
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
		case <-t.ctx.Done():
			timer.Stop()
			t.mu.Lock()
			t.state = StateStopped
			t.mu.Unlock()
			return
		case <-timer.C:
		}

		// Try to start the subprocess.
		cmd, stdin, stdout, err := t.spawnProcess()
		if err != nil {
			t.mu.Lock()
			t.startupFailures++
			t.mu.Unlock()
			continue
		}

		// Success — update state and start the reader.
		t.mu.Lock()
		t.cmd = cmd
		t.stdin = stdin
		t.state = StateRunning
		t.startupFailures = 0 // reset on successful spawn
		t.mu.Unlock()

		t.readerWg.Add(1)
		go t.runReader(stdout)
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

// ── Helpers ─────────────────────────────────────────────────────────────

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
