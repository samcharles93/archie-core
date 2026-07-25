package memory

import (
	"fmt"
	"log"
	"time"
)

// SyncOpType classifies a sync operation submitted to the background pipeline.
type SyncOpType int

const (
	// SyncOpMemoryWrite indicates a write to the built-in memory tool
	// (add/replace/remove). External providers receive this notification
	// so they can stay in sync.
	SyncOpMemoryWrite SyncOpType = iota
)

// String returns a human-readable name for the operation type.
func (t SyncOpType) String() string {
	switch t {
	case SyncOpMemoryWrite:
		return "memory_write"
	default:
		return fmt.Sprintf("unknown(%d)", t)
	}
}

// SyncOp is a single unit of work submitted to the background sync pipeline.
// Write operations from memory tools are serialized through the pipeline to
// guarantee FIFO ordering and prevent concurrent mutations on external stores.
type SyncOp struct {
	// Type classifies the operation.
	Type SyncOpType

	// Provider is the name of the external provider that should receive
	// this notification. Only external providers are notified; the
	// built-in provider handles writes directly.
	Provider string

	// SessionID identifies the session that performed the write.
	SessionID string

	// Content is the full text that was written (the new content, not a diff).
	Content string

	// Action is the memory_edit action: "add", "replace", or "remove".
	Action string

	// Section is the section name the action was performed on.
	Section string

	// Done is an optional channel that receives the outcome of the operation.
	// When non-nil, the pipeline sends the error (or nil) when the operation
	// completes. Callers that don't need completion notification may leave
	// this nil.
	Done chan error

	// CreatedAt is the time the operation was submitted. Used for
	// abandoned-write tracking during shutdown.
	CreatedAt time.Time
}

// ── Pipeline control ────────────────────────────────────────────────────

// syncPipelineBufSize is the default capacity of the sync pipeline channel.
// Operations submitted after the buffer is full will block until space is
// available (or until the caller's context expires).
const syncPipelineBufSize = 100

// syncWorker runs the serialized background sync loop. It reads operations
// from the pipeline channel and dispatches them to the appropriate handler.
// The worker exits when the pipeline channel is closed (via Shutdown).
func (m *Manager) syncWorker() {
	defer close(m.syncDone)
	for op := range m.syncPipeline {
		m.processSyncOp(op)
	}
}

// processSyncOp handles a single sync operation. Errors are logged but never
// crash the pipeline — a misbehaving external provider must not take down
// the agent.
func (m *Manager) processSyncOp(op SyncOp) {
	if deadlineNano := m.shutdownDeadlineNano.Load(); deadlineNano != 0 && time.Now().UnixNano() > deadlineNano {
		age := time.Since(op.CreatedAt)
		m.abandonedCount.Add(1)
		log.Printf("[memory] shutdown: abandoned sync op provider=%q type=%s age=%v (exceeded drain deadline)",
			op.Provider, op.Type, age)
		m.signalDone(op.Done, ErrShuttingDown)
		return
	}

	if op.Provider == "" {
		m.signalDone(op.Done, nil)
		return
	}

	m.mu.RLock()
	p := m.providerByNameLocked(op.Provider)
	m.mu.RUnlock()

	if p == nil {
		log.Printf("[memory] sync pipeline: provider %q not found for op %s (session=%s)",
			op.Provider, op.Type, op.SessionID)
		m.signalDone(op.Done, fmt.Errorf("memory: provider %q not found", op.Provider))
		return
	}

	// Dispatch by operation type.
	var err error
	switch op.Type {
	case SyncOpMemoryWrite:
		err = m.syncMemoryWrite(p, op)
	default:
		log.Printf("[memory] sync pipeline: unknown op type %s for provider %q",
			op.Type, op.Provider)
	}

	if err != nil {
		log.Printf("[memory] sync pipeline: op %s failed for provider %q (session=%s): %v",
			op.Type, op.Provider, op.SessionID, err)
	}

	m.signalDone(op.Done, err)
}

// syncMemoryWrite notifies an external provider of a memory write by calling
// its MemoryWriteHook (if implemented). External providers that don't
// implement the hook silently ignore the notification.
func (m *Manager) syncMemoryWrite(p MemoryProvider, op SyncOp) error {
	if h, ok := p.(MemoryWriteHook); ok {
		return h.OnMemoryWrite(op.SessionID, op.Content)
	}
	return nil
}

// signalDone sends err to the Done channel if non-nil, without blocking.
func (m *Manager) signalDone(done chan error, err error) {
	if done == nil {
		return
	}
	select {
	case done <- err:
	default:
		// Receiver stopped waiting — don't block the pipeline.
	}
}
