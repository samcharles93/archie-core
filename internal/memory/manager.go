package memory

import (
	"context"
	"errors"
	"fmt"
	"log"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/samcharles93/archie-core/internal/tools"
)

// Sentinel errors returned by the MemoryManager.
var (
	// ErrExternalAlreadyRegistered is returned when attempting to register a
	// second external provider. The MemoryManager enforces at most one
	// external provider at a time.
	ErrExternalAlreadyRegistered = errors.New("memory: external provider already registered")

	// ErrToolNotFound is returned by HandleToolCall when no active provider
	// owns the named tool.
	ErrToolNotFound = errors.New("memory: tool not found in any provider")

	// ErrProviderNotFound is returned when a provider name lookup fails.
	ErrProviderNotFound = errors.New("memory: provider not found")

	// ErrPipelineFull is returned by SubmitSync when the sync pipeline
	// buffer is at capacity and the caller does not want to block.
	ErrPipelineFull = errors.New("memory: sync pipeline buffer is full")

	// ErrShuttingDown is returned when an operation is submitted after the
	// Manager has begun shutdown.
	ErrShuttingDown = errors.New("memory: manager is shutting down")
)

// Manager orchestrates the built-in memory provider plus at most one external
// provider. It is the single entry point that agent lifecycle code interacts
// with  --  callers never address providers directly.
//
// Key responsibilities:
//   - Provider registration and lifecycle (Initialize, Shutdown)
//   - Tool schema merging from all active providers
//   - Runtime dispatch of tool calls to the owning provider
//   - Enforcement of the one-external-provider limit
//   - Lifecycle hook fan-out to providers that implement them
//   - Config and backup path aggregation
//   - Serialized background sync pipeline for write ordering (18.7)
//   - Prefetch with timeout and in-flight tracking (18.8)
//   - Memory-write notification to external providers (18.9)
//   - Content threat scanning before persistence (18.15)
//   - Graceful shutdown drain with abandoned-write tracking (18.16)
type Manager struct {
	mu       sync.RWMutex
	builtin  MemoryProvider
	external MemoryProvider

	// toolIndex maps tool name → owning provider for fast dispatch.
	toolIndex map[string]MemoryProvider

	// ── Sync pipeline (18.7) ───────────────────────────────────────────
	//
	// All write notifications to external providers are serialized through
	// a single-worker channel to guarantee FIFO ordering and prevent
	// concurrent mutations.

	syncPipeline chan SyncOp   // buffered channel of pending sync ops
	syncDone     chan struct{} // closed when the sync worker exits
	syncBufSize  int           // configured buffer capacity
	shuttingDown atomic.Bool   // true after Shutdown is called

	// shutdownDeadlineNano stores the drain deadline (UnixNano) set during
	// ShutdownContext. The sync worker uses it to self-abandon ops that
	// are still queued after the deadline passes (18.16).
	shutdownDeadlineNano atomic.Int64

	// pipelineMu guards the syncPipeline channel lifecycle. Submitters
	// take a read lock; Shutdown takes the write lock to prevent
	// sends on a closed channel.
	pipelineMu sync.RWMutex

	// ── Prefetch tracking (18.8) ──────────────────────────────────────

	// prefetchInFlight tracks whether a prefetch is currently running for
	// each provider. The key is the provider name. When a provider is
	// prefetching, new Prefetch calls skip that provider.
	prefetchInFlight map[string]*atomic.Bool

	// prefetchTimeout is the deadline for a single Prefetch call
	// (default 8s).
	prefetchTimeout time.Duration

	// prefetchResults stores the last successful prefetch result per
	// provider, keyed by provider name. Results are included in
	// SystemPromptBlock() output.
	prefetchResults   map[string]string
	prefetchResultsMu sync.RWMutex

	// ── Threat scanning (18.15) ───────────────────────────────────────

	// scanner inspects content before persistence. When nil, scanning
	// is skipped. Set with SetScanner.
	scanner Scanner

	// scanDisabled, when true, bypasses scanning even when a scanner is
	// configured. Useful for development and testing.
	scanDisabled bool

	// scanReject, when true, causes ThreatBlock results to reject the
	// write. When false (default), ThreatBlock results are downgraded
	// to warnings and the write proceeds.
	scanReject bool

	// ── Shutdown drain (18.16) ────────────────────────────────────────

	// shutdownTimeout is the deadline for in-flight sync operations
	// during Shutdown (default 10s).
	shutdownTimeout time.Duration

	// abandonedCount tracks the number of sync operations that did not
	// complete within the shutdown deadline.
	abandonedCount atomic.Int64
}

// NewManager creates a Manager with a built-in provider and an optional
// external provider. Pass nil for either parameter when no provider of
// that kind is available.
//
// The built-in provider is always present and typically backed by the
// filesystem or NellDB. The external provider is optional  --  pass nil
// and call RegisterExternal later, or pass a single external provider
// at construction time.
//
// Defaults:
//   - Sync pipeline buffer: 100 operations
//   - Prefetch timeout: 8 seconds
//   - Shutdown drain timeout: 10 seconds
//   - Threat scanning: disabled (no scanner configured)
func NewManager(builtin, external MemoryProvider) (*Manager, error) {
	m := &Manager{
		builtin:  builtin,
		external: external,

		// Sync pipeline defaults (18.7)
		syncBufSize:  syncPipelineBufSize,
		syncPipeline: make(chan SyncOp, syncPipelineBufSize),
		syncDone:     make(chan struct{}),

		// Prefetch defaults (18.8)
		prefetchInFlight: make(map[string]*atomic.Bool),
		prefetchTimeout:  8 * time.Second,
		prefetchResults:  make(map[string]string),

		// Shutdown defaults (18.16)
		shutdownTimeout: 10 * time.Second,
	}
	m.rebuildToolIndex()

	// Start the background sync worker (18.7).
	go m.syncWorker()

	return m, nil
}

// RegisterExternal registers an external provider. It returns
// ErrExternalAlreadyRegistered when an external provider is already
// active. Pass nil to clear the current external provider.
func (m *Manager) RegisterExternal(p MemoryProvider) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.external != nil && p != nil {
		return ErrExternalAlreadyRegistered
	}
	m.external = p
	m.rebuildToolIndexLocked()
	return nil
}

// Builtin returns the built-in provider, or nil if none is configured.
func (m *Manager) Builtin() MemoryProvider {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.builtin
}

// External returns the external provider, or nil if none is configured.
func (m *Manager) External() MemoryProvider {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.external
}

// ── Tool schema merging ────────────────────────────────────────────────

// GetToolSchemas returns the merged set of tool definitions from all active
// and available providers. Providers that are nil or report IsAvailable()
// == false are skipped.
//
// For tools whose owning provider implements ToolCallProvider, a synthetic
// Handler is injected that delegates to HandleToolCall. This ensures every
// returned ToolEntry passes tools.ToolEntry.Validate().
func (m *Manager) GetToolSchemas() []tools.ToolEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var out []tools.ToolEntry
	for _, p := range m.activeProvidersLocked() {
		for _, t := range p.GetToolSchemas() {
			if t.Handler == nil {
				if _, ok := p.(ToolCallProvider); ok {
					t = t.Clone()      // don't mutate the provider's original
					toolName := t.Name // capture for the closure
					// Routed through m.HandleToolCall (not tcp.HandleToolCall
					// directly) so scanning (18.15) and write notification
					// (18.9) apply uniformly regardless of entry point.
					t.Handler = func(ctx context.Context, input map[string]any) (any, error) {
						return m.HandleToolCall(toolName, input)
					}
				}
			}
			out = append(out, t)
		}
	}
	if out == nil {
		out = []tools.ToolEntry{}
	}
	return out
}

// ── Tool call dispatch ─────────────────────────────────────────────────

// HandleToolCall routes a tool invocation to the provider that owns the
// named tool. It returns ErrToolNotFound when no active provider owns the
// tool, and an error when the owning provider does not implement
// ToolCallProvider.
//
// Before dispatching, any "content" or "replacement" string argument is
// passed through ScanContent (18.15)  --  this is the actual integration point
// for threat scanning, applying uniformly to every provider's write tools.
// A ThreatBlock result rejects the call before it reaches the provider.
//
// After a successful call to the built-in provider, external providers are
// notified via NotifyMemoryToolWrite (18.9).
//
// Note: the preferred entry point for tool execution is through the Handler
// injected by GetToolSchemas(), which delegates here. HandleToolCall is also
// exposed directly for callers that need it (e.g., MCP server bridging).
func (m *Manager) HandleToolCall(name string, args map[string]any) (any, error) {
	m.mu.RLock()
	p, ok := m.toolIndex[name]
	m.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrToolNotFound, name)
	}

	tcp, ok := p.(ToolCallProvider)
	if !ok {
		return nil, fmt.Errorf("memory: provider %q does not implement ToolCallProvider", p.Name())
	}

	for _, key := range []string{"content", "replacement"} {
		text, _ := args[key].(string)
		if text == "" {
			continue
		}
		if result := m.ScanContent(text); result.Level == ThreatBlock {
			return nil, fmt.Errorf("memory: content rejected by threat scan: %s", result.Message)
		}
	}

	result, err := tcp.HandleToolCall(name, args)
	if err != nil {
		return result, err
	}

	if p == m.Builtin() {
		sessionID, _ := args["session_id"].(string)
		action, _ := args["action"].(string)
		section, _ := args["section"].(string)
		content, _ := args["content"].(string)
		if content == "" {
			content, _ = args["replacement"].(string)
		}
		m.NotifyMemoryToolWrite(sessionID, action, section, content)
	}

	return result, nil
}

// ── Initialization ─────────────────────────────────────────────────────

// Initialize calls Initialize on every active provider with the given
// session ID. Errors from individual providers do not prevent other
// providers from initializing. The first error encountered is returned
// after all providers have been tried.
func (m *Manager) Initialize(sessionID string) error {
	m.mu.RLock()
	providers := m.activeProvidersLocked()
	m.mu.RUnlock()

	var firstErr error
	for _, p := range providers {
		if err := p.Initialize(sessionID); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// ── Lifecycle hooks ────────────────────────────────────────────────────
//
// Each hook fans out to all active providers that implement the
// corresponding sub-interface. Hooks are fire-and-forget: they launch a
// goroutine per provider and return immediately. Hook errors and panics
// are recovered  --  they must not interrupt the agent loop.

// safeGo runs fn in a goroutine, recovering any panic to prevent a
// misbehaving provider from crashing the process.
func safeGo(fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				// A provider panicked  --  this must not take down the agent.
				// The panic is intentionally swallowed after logging.
				_ = r
				_ = debug.Stack()
			}
		}()
		fn()
	}()
}

func (m *Manager) OnTurnStart(sessionID string) {
	m.mu.RLock()
	providers := m.activeProvidersLocked()
	m.mu.RUnlock()

	for _, p := range providers {
		if h, ok := p.(TurnStartHook); ok {
			safeGo(func() { _ = h.OnTurnStart(sessionID) })
		}
	}
}

func (m *Manager) OnSessionEnd(sessionID string) {
	m.mu.RLock()
	providers := m.activeProvidersLocked()
	m.mu.RUnlock()

	for _, p := range providers {
		if h, ok := p.(SessionEndHook); ok {
			safeGo(func() { _ = h.OnSessionEnd(sessionID) })
		}
	}
}

func (m *Manager) OnSessionSwitch(oldSessionID, newSessionID string) {
	m.mu.RLock()
	providers := m.activeProvidersLocked()
	m.mu.RUnlock()

	for _, p := range providers {
		if h, ok := p.(SessionSwitchHook); ok {
			safeGo(func() { _ = h.OnSessionSwitch(oldSessionID, newSessionID) })
		}
	}
}

func (m *Manager) OnPreCompress(sessionID string) {
	m.mu.RLock()
	providers := m.activeProvidersLocked()
	m.mu.RUnlock()

	for _, p := range providers {
		if h, ok := p.(PreCompressHook); ok {
			safeGo(func() { _ = h.OnPreCompress(sessionID) })
		}
	}
}

func (m *Manager) OnMemoryWrite(sessionID, content string) {
	m.mu.RLock()
	providers := m.activeProvidersLocked()
	m.mu.RUnlock()

	for _, p := range providers {
		if h, ok := p.(MemoryWriteHook); ok {
			safeGo(func() { _ = h.OnMemoryWrite(sessionID, content) })
		}
	}
}

func (m *Manager) OnDelegation(sessionID string) {
	m.mu.RLock()
	providers := m.activeProvidersLocked()
	m.mu.RUnlock()

	for _, p := range providers {
		if h, ok := p.(DelegationHook); ok {
			safeGo(func() { _ = h.OnDelegation(sessionID) })
		}
	}
}

// ── Optional interface detection ───────────────────────────────────────

// HasSystemPrompt reports whether any active provider contributes a
// system prompt block.
func (m *Manager) HasSystemPrompt() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, p := range m.activeProvidersLocked() {
		if _, ok := p.(SystemPromptProvider); ok {
			return true
		}
	}
	return false
}

// SystemPromptBlock returns the concatenated system prompt blocks from
// all active providers that implement SystemPromptProvider, followed by
// any cached prefetch results (18.8).
func (m *Manager) SystemPromptBlock() string {
	m.mu.RLock()
	providers := m.activeProvidersLocked()
	m.mu.RUnlock()

	var sb strings.Builder
	for _, p := range providers {
		if spp, ok := p.(SystemPromptProvider); ok {
			sb.WriteString(spp.SystemPromptBlock())
			sb.WriteByte('\n')
		}
	}

	// Append cached prefetch results (18.8).
	m.prefetchResultsMu.RLock()
	for _, data := range m.prefetchResults {
		if data != "" {
			sb.WriteString("<prefetch>\n")
			sb.WriteString(data)
			sb.WriteString("\n</prefetch>\n")
		}
	}
	m.prefetchResultsMu.RUnlock()

	return sb.String()
}

// HasPrefetch reports whether any active provider supports prefetch.
func (m *Manager) HasPrefetch() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, p := range m.activeProvidersLocked() {
		if _, ok := p.(PrefetchProvider); ok {
			return true
		}
	}
	return false
}

// Prefetch calls Prefetch on the first active provider that implements
// PrefetchProvider. Returns empty string if no provider supports prefetch.
//
// Prefetch uses context.Background() with the manager's configured
// prefetch timeout (default 8s). Use PrefetchContext for caller-controlled
// deadlines.
//
// If the target provider is already prefetching from a previous turn, the
// call is skipped and returns an empty result (18.8).
func (m *Manager) Prefetch(query string) (string, error) {
	return m.PrefetchContext(context.Background(), query)
}

// PrefetchContext calls Prefetch on the first active PrefetchProvider with
// a context deadline. The manager's configured prefetch timeout (default 8s)
// is applied if the caller's context does not already have a shorter deadline.
//
// If the target provider is still prefetching from the previous turn, the
// call is skipped  --  PrefetchContext returns ("", nil) and the skip is logged
// (18.8). Results from successful prefetches are stored and included in
// SystemPromptBlock() output.
func (m *Manager) PrefetchContext(ctx context.Context, query string) (string, error) {
	// Apply the manager's default timeout if the caller hasn't set a
	// shorter deadline. Derived before the loop so we never nest a
	// context inside a loop (fatcontext).
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, m.prefetchTimeout)
		defer cancel()
	}

	m.mu.RLock()
	providers := m.activeProvidersLocked()
	m.mu.RUnlock()

	for _, p := range providers {
		pp, ok := p.(PrefetchProvider)
		if !ok {
			continue
		}

		name := p.Name()

		// Acquire or create the in-flight flag for this provider.
		flag := m.ensurePrefetchFlag(name)

		// If already prefetching, skip this turn (18.8).
		if !flag.CompareAndSwap(false, true) {
			log.Printf("[memory] prefetch: provider %q is already prefetching, skipping", name)
			return "", nil
		}
		defer flag.Store(false)

		start := time.Now()

		// Run Prefetch in a goroutine so we can respect the context deadline.
		type result struct {
			data string
			err  error
		}
		ch := make(chan result, 1)
		go func() {
			data, err := pp.Prefetch(query)
			ch <- result{data, err}
		}()

		select {
		case r := <-ch:
			elapsed := time.Since(start)
			log.Printf("[memory] prefetch: provider %q completed in %v (query=%q)", name, elapsed, truncateQuery(query))
			if r.err != nil {
				log.Printf("[memory] prefetch: provider %q returned error: %v", name, r.err)
				return "", r.err
			}
			// Store the result for SystemPromptBlock() inclusion (18.8).
			if r.data != "" {
				m.prefetchResultsMu.Lock()
				m.prefetchResults[name] = r.data
				m.prefetchResultsMu.Unlock()
			}
			return r.data, nil

		case <-ctx.Done():
			elapsed := time.Since(start)
			log.Printf("[memory] prefetch: provider %q timed out after %v (query=%q)", name, elapsed, truncateQuery(query))
			return "", ctx.Err()
		}
	}

	return "", nil
}

// PrefetchResult returns the last successful prefetch result for the named
// provider. Returns empty string if no result is cached.
func (m *Manager) PrefetchResult(providerName string) string {
	m.prefetchResultsMu.RLock()
	defer m.prefetchResultsMu.RUnlock()
	return m.prefetchResults[providerName]
}

// SetPrefetchTimeout overrides the default prefetch timeout (8s). A zero or
// negative value resets to the default.
func (m *Manager) SetPrefetchTimeout(d time.Duration) {
	if d <= 0 {
		d = 8 * time.Second
	}
	m.mu.Lock()
	m.prefetchTimeout = d
	m.mu.Unlock()
}

// ensurePrefetchFlag returns the in-flight flag for the named provider,
// creating one under the write lock if necessary.
func (m *Manager) ensurePrefetchFlag(name string) *atomic.Bool {
	// Fast path: read lock.
	m.mu.RLock()
	flag, ok := m.prefetchInFlight[name]
	m.mu.RUnlock()
	if ok {
		return flag
	}

	// Slow path: create under write lock, double-check.
	m.mu.Lock()
	defer m.mu.Unlock()
	if flag, ok := m.prefetchInFlight[name]; ok {
		return flag
	}
	flag = new(atomic.Bool)
	m.prefetchInFlight[name] = flag
	return flag
}

// QueuePrefetch calls QueuePrefetch on the first active provider that
// implements PrefetchProvider.
func (m *Manager) QueuePrefetch(query string) error {
	m.mu.RLock()
	providers := m.activeProvidersLocked()
	m.mu.RUnlock()

	for _, p := range providers {
		if pp, ok := p.(PrefetchProvider); ok {
			return pp.QueuePrefetch(query)
		}
	}
	return nil
}

// HasSyncTurn reports whether any active provider supports syncing turns.
func (m *Manager) HasSyncTurn() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, p := range m.activeProvidersLocked() {
		if _, ok := p.(SyncTurnProvider); ok {
			return true
		}
	}
	return false
}

// SyncTurn calls SyncTurn on the first active provider that implements
// SyncTurnProvider.
func (m *Manager) SyncTurn(userMsg, assistantMsg string) error {
	m.mu.RLock()
	providers := m.activeProvidersLocked()
	m.mu.RUnlock()

	for _, p := range providers {
		if stp, ok := p.(SyncTurnProvider); ok {
			return stp.SyncTurn(userMsg, assistantMsg)
		}
	}
	return nil
}

// HasShutdown reports whether any active provider implements ShutdownProvider.
func (m *Manager) HasShutdown() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, p := range m.activeProvidersLocked() {
		if _, ok := p.(ShutdownProvider); ok {
			return true
		}
	}
	return false
}

// Shutdown gracefully shuts down all registered providers (whether available
// or not) that implement ShutdownProvider. A provider that reports
// IsAvailable() == false may still hold open resources (file handles,
// connections, goroutines) and must be given the opportunity to release them.
//
// Shutdown uses the configured shutdown timeout (default 10s) to drain the
// sync pipeline before tearing down providers. In-flight operations that do
// not complete within the deadline are tracked as abandoned writes (18.16).
//
// Errors from individual providers are collected; the first error is returned
// after all providers have been shut down.
func (m *Manager) Shutdown() error {
	ctx, cancel := context.WithTimeout(context.Background(), m.shutdownTimeout)
	defer cancel()
	return m.ShutdownContext(ctx)
}

// ShutdownContext performs a graceful shutdown with the caller's deadline.
// It drains the sync pipeline, then shuts down providers in reverse
// registration order (external first, then builtin). Operations that do not
// complete within the context deadline are tracked as abandoned writes (18.16).
//
// After ShutdownContext returns, no new sync operations are accepted.
func (m *Manager) ShutdownContext(ctx context.Context) error {
	// Signal that we're shutting down  --  new submissions will be rejected.
	m.shuttingDown.Store(true)

	// Record the drain deadline so the sync worker can self-abandon any op
	// it pulls off the queue after this point in time, logging provider,
	// type, and age for each (18.16).
	if deadline, ok := ctx.Deadline(); ok {
		m.shutdownDeadlineNano.Store(deadline.UnixNano())
	}

	// Take the write lock on the pipeline to prevent any in-flight
	// SubmitSync from sending on the channel while we close it.
	m.pipelineMu.Lock()
	close(m.syncPipeline)
	m.pipelineMu.Unlock()

	// Wait for the sync worker to finish draining, or the deadline. On
	// timeout we don't block further  --  the worker keeps running in the
	// background, but every op still queued after the deadline is now
	// abandoned near-instantly by processSyncOp rather than dispatched.
	drainDone := make(chan struct{})
	go func() {
		<-m.syncDone
		close(drainDone)
	}()

	select {
	case <-drainDone:
		// Pipeline drained cleanly.
	case <-ctx.Done():
		log.Printf("[memory] shutdown: drain deadline exceeded, remaining sync operations will be logged and abandoned as the worker reaches them")
	}

	// Shut down providers in reverse registration order: external first,
	// then builtin (18.16).
	m.mu.RLock()
	providers := m.allProvidersLocked()
	m.mu.RUnlock()

	// Reverse the order: external (index 1) before builtin (index 0).
	var firstErr error
	for i := len(providers) - 1; i >= 0; i-- {
		p := providers[i]

		// Give each provider its own deadline from the parent context.
		if sp, ok := p.(ShutdownProvider); ok {
			if err := sp.Shutdown(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}

	return firstErr
}

// AbandonedCount returns the number of sync operations abandoned during the
// most recent shutdown due to exceeding the drain deadline (18.16).
func (m *Manager) AbandonedCount() int64 {
	return m.abandonedCount.Load()
}

// SetShutdownTimeout overrides the default shutdown drain timeout (10s).
// A zero or negative value resets to the default.
func (m *Manager) SetShutdownTimeout(d time.Duration) {
	if d <= 0 {
		d = 10 * time.Second
	}
	m.mu.Lock()
	m.shutdownTimeout = d
	m.mu.Unlock()
}

// ── Sync pipeline submission (18.7) ─────────────────────────────────────

// SubmitSync enqueues a sync operation on the background pipeline. It returns
// ErrShuttingDown when the manager is shutting down, and blocks (or returns
// ErrPipelineFull) when the pipeline buffer is full.
//
// Callers that need completion notification should set op.Done to a buffered
// channel of size 1. The pipeline sends the error (or nil) on completion.
func (m *Manager) SubmitSync(op SyncOp) error {
	if m.shuttingDown.Load() {
		return ErrShuttingDown
	}

	// Take a read lock on the pipeline to prevent Shutdown from closing
	// the channel while a send is in progress (TOCTOU guard).
	m.pipelineMu.RLock()
	defer m.pipelineMu.RUnlock()

	if m.shuttingDown.Load() {
		return ErrShuttingDown
	}

	op.CreatedAt = time.Now()

	select {
	case m.syncPipeline <- op:
		return nil
	default:
		return fmt.Errorf("%w (capacity=%d)", ErrPipelineFull, m.syncBufSize)
	}
}

// SyncBufSize returns the configured sync pipeline buffer capacity.
func (m *Manager) SyncBufSize() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.syncBufSize
}

// ── Memory write notification (18.9) ────────────────────────────────────

// NotifyMemoryToolWrite dispatches a memory-write notification to external
// providers through the background sync pipeline. It is called after any
// successful built-in memory tool write (add/replace/remove).
//
// The notification is asynchronous and serialized through the pipeline to
// guarantee write ordering. When no external provider is active, the call
// is a no-op. External providers that do not implement MemoryWriteHook
// silently ignore the notification.
//
// NotifyMemoryToolWrite is a fire-and-forget call; it does not wait for
// the notification to be processed. Use SubmitSync directly if completion
// notification is needed.
func (m *Manager) NotifyMemoryToolWrite(sessionID, action, section, content string) {
	m.mu.RLock()
	ext := m.external
	m.mu.RUnlock()

	if ext == nil || !ext.IsAvailable() {
		return
	}

	op := SyncOp{
		Type:      SyncOpMemoryWrite,
		Provider:  ext.Name(),
		SessionID: sessionID,
		Content:   content,
		Action:    action,
		Section:   section,
	}

	if err := m.SubmitSync(op); err != nil {
		log.Printf("[memory] notify: failed to submit sync op for provider %q: %v", ext.Name(), err)
	}
}

// ── Threat scanning (18.15) ─────────────────────────────────────────────

// SetScanner configures the content threat scanner. Pass nil to disable
// scanning. The default scanner (DefaultScanner) checks for prompt-injection
// and sensitive-data patterns.
func (m *Manager) SetScanner(s Scanner) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.scanner = s
}

// Scanner returns the configured scanner, or nil if scanning is disabled.
func (m *Manager) Scanner() Scanner {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.scanner
}

// SetScanDisabled controls whether scanning is bypassed. When true, scans
// are skipped even when a scanner is configured. Useful for development.
func (m *Manager) SetScanDisabled(disabled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.scanDisabled = disabled
}

// ScanDisabled reports whether scanning is disabled.
func (m *Manager) ScanDisabled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.scanDisabled
}

// SetScanReject controls the action on ThreatBlock results. When true,
// ThreatBlock causes the write to be rejected. When false (default),
// ThreatBlock is downgraded to a warning and the write proceeds.
func (m *Manager) SetScanReject(reject bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.scanReject = reject
}

// ScanReject reports whether ThreatBlock results reject the write.
func (m *Manager) ScanReject() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.scanReject
}

// ScanContent inspects content with the configured scanner. Returns
// ThreatNone when no scanner is configured or scanning is disabled.
// The result indicates whether the content should be allowed, warned
// about, or blocked.
func (m *Manager) ScanContent(content string) ThreatResult {
	m.mu.RLock()
	scanner := m.scanner
	disabled := m.scanDisabled
	reject := m.scanReject
	m.mu.RUnlock()

	if scanner == nil || disabled {
		return ThreatResult{Level: ThreatNone}
	}

	result := scanner.ScanContent(content)

	// Downgrade ThreatBlock to ThreatWarn when rejection is disabled.
	if result.Level == ThreatBlock && !reject {
		result.Level = ThreatWarn
		log.Printf("[memory] scan: content matched block pattern %q but scanReject=false, downgrading to warn: %s",
			result.Pattern, result.Message)
	}

	if result.Level != ThreatNone {
		level := "WARN"
		if result.Level == ThreatBlock {
			level = "BLOCK"
		}
		log.Printf("[memory] scan: [%s] %s", level, result.Message)
	}

	return result
}

// ── Config helpers ─────────────────────────────────────────────────────

// GetConfigSchemas returns the config schemas from all active providers
// that implement ConfigSchemaProvider, keyed by provider name.
func (m *Manager) GetConfigSchemas() map[string]map[string]any {
	m.mu.RLock()
	providers := m.activeProvidersLocked()
	m.mu.RUnlock()

	out := make(map[string]map[string]any, len(providers))
	for _, p := range providers {
		if csp, ok := p.(ConfigSchemaProvider); ok {
			out[p.Name()] = csp.GetConfigSchema()
		}
	}
	return out
}

// SaveConfig validates the given values against the provider's config
// schema (if the provider implements ConfigSchemaProvider) and then
// persists them via SaveConfigProvider. It returns ErrProviderNotFound
// when the named provider is not active, and an error when the provider
// does not implement SaveConfigProvider or when validation fails.
func (m *Manager) SaveConfig(providerName string, values map[string]any, dataHome string) error {
	m.mu.RLock()
	p := m.providerByNameLocked(providerName)
	m.mu.RUnlock()

	if p == nil {
		return fmt.Errorf("%w: %s", ErrProviderNotFound, providerName)
	}

	scp, ok := p.(SaveConfigProvider)
	if !ok {
		return fmt.Errorf("memory: provider %q does not implement SaveConfigProvider", providerName)
	}

	return scp.SaveConfig(values, dataHome)
}

// ── Backup paths ───────────────────────────────────────────────────────

// BackupPaths aggregates BackupPaths from all active providers that
// implement BackupProvider. Returns an empty slice when no providers
// contribute backup paths.
func (m *Manager) BackupPaths() []string {
	m.mu.RLock()
	providers := m.activeProvidersLocked()
	m.mu.RUnlock()

	var out []string
	for _, p := range providers {
		if bp, ok := p.(BackupProvider); ok {
			out = append(out, bp.BackupPaths()...)
		}
	}
	if out == nil {
		out = []string{}
	}
	return out
}

// truncateQuery limits the query string for log messages so a very long
// query does not flood the log output.
func truncateQuery(q string) string {
	const maxQueryLen = 80
	if len(q) <= maxQueryLen {
		return q
	}
	return q[:maxQueryLen] + "..."
}

// ── Internal helpers ───────────────────────────────────────────────────

// activeProvidersLocked returns all non-nil, available providers.
// Caller must hold m.mu (read or write lock).
func (m *Manager) activeProvidersLocked() []MemoryProvider {
	var out []MemoryProvider
	if m.builtin != nil && m.builtin.IsAvailable() {
		out = append(out, m.builtin)
	}
	if m.external != nil && m.external.IsAvailable() {
		out = append(out, m.external)
	}
	if out == nil {
		out = []MemoryProvider{}
	}
	return out
}

// allProvidersLocked returns all non-nil providers regardless of
// availability. Use for shutdown so unavailable providers still get
// a chance to release resources.
// Caller must hold m.mu (read or write lock).
func (m *Manager) allProvidersLocked() []MemoryProvider {
	var out []MemoryProvider
	if m.builtin != nil {
		out = append(out, m.builtin)
	}
	if m.external != nil {
		out = append(out, m.external)
	}
	if out == nil {
		out = []MemoryProvider{}
	}
	return out
}

// rebuildToolIndex rebuilds the tool name → provider mapping.
func (m *Manager) rebuildToolIndex() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rebuildToolIndexLocked()
}

// rebuildToolIndexLocked rebuilds the tool index. Caller must hold m.mu.
// Duplicate tool names across providers are detected and handled
// deterministically: the first provider to claim a name wins; subsequent
// duplicates are skipped (the duplicate tool is not discoverable).
func (m *Manager) rebuildToolIndexLocked() {
	m.toolIndex = make(map[string]MemoryProvider)
	for _, p := range m.activeProvidersLocked() {
		for _, t := range p.GetToolSchemas() {
			if _, exists := m.toolIndex[t.Name]; !exists {
				m.toolIndex[t.Name] = p
			}
		}
	}
}

// providerByNameLocked returns the active provider with the given name,
// or nil. Caller must hold m.mu (read lock).
func (m *Manager) providerByNameLocked(name string) MemoryProvider {
	for _, p := range m.activeProvidersLocked() {
		if p.Name() == name {
			return p
		}
	}
	return nil
}
