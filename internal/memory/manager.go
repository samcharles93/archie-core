package memory

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"strings"
	"sync"

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
)

// Manager orchestrates the built-in memory provider plus at most one external
// provider. It is the single entry point that agent lifecycle code interacts
// with — callers never address providers directly.
//
// Key responsibilities:
//   - Provider registration and lifecycle (Initialize, Shutdown)
//   - Tool schema merging from all active providers
//   - Runtime dispatch of tool calls to the owning provider
//   - Enforcement of the one-external-provider limit
//   - Lifecycle hook fan-out to providers that implement them
//   - Config and backup path aggregation
type Manager struct {
	mu       sync.RWMutex
	builtin  MemoryProvider
	external MemoryProvider

	// toolIndex maps tool name → owning provider for fast dispatch.
	toolIndex map[string]MemoryProvider
}

// NewManager creates a Manager with a built-in provider and an optional
// external provider. Pass nil for either parameter when no provider of
// that kind is available.
//
// The built-in provider is always present and typically backed by the
// filesystem or NellDB. The external provider is optional — pass nil
// and call RegisterExternal later, or pass a single external provider
// at construction time.
func NewManager(builtin MemoryProvider, external MemoryProvider) (*Manager, error) {
	m := &Manager{
		builtin:  builtin,
		external: external,
	}
	m.rebuildToolIndex()
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
				if tcp, ok := p.(ToolCallProvider); ok {
					t = t.Clone()           // don't mutate the provider's original
					toolName := t.Name      // capture for the closure
					t.Handler = func(ctx context.Context, input map[string]any) (any, error) {
						return tcp.HandleToolCall(toolName, input)
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
// Note: the preferred entry point for tool execution is through the Handler
// injected by GetToolSchemas(). HandleToolCall is exposed for callers that
// need direct access (e.g., MCP server bridging).
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

	return tcp.HandleToolCall(name, args)
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
// are recovered — they must not interrupt the agent loop.

// safeGo runs fn in a goroutine, recovering any panic to prevent a
// misbehaving provider from crashing the process.
func safeGo(fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				// A provider panicked — this must not take down the agent.
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
			safeGo(func() { h.OnTurnStart(sessionID) })
		}
	}
}

func (m *Manager) OnSessionEnd(sessionID string) {
	m.mu.RLock()
	providers := m.activeProvidersLocked()
	m.mu.RUnlock()

	for _, p := range providers {
		if h, ok := p.(SessionEndHook); ok {
			safeGo(func() { h.OnSessionEnd(sessionID) })
		}
	}
}

func (m *Manager) OnSessionSwitch(oldSessionID, newSessionID string) {
	m.mu.RLock()
	providers := m.activeProvidersLocked()
	m.mu.RUnlock()

	for _, p := range providers {
		if h, ok := p.(SessionSwitchHook); ok {
			safeGo(func() { h.OnSessionSwitch(oldSessionID, newSessionID) })
		}
	}
}

func (m *Manager) OnPreCompress(sessionID string) {
	m.mu.RLock()
	providers := m.activeProvidersLocked()
	m.mu.RUnlock()

	for _, p := range providers {
		if h, ok := p.(PreCompressHook); ok {
			safeGo(func() { h.OnPreCompress(sessionID) })
		}
	}
}

func (m *Manager) OnMemoryWrite(sessionID string, content string) {
	m.mu.RLock()
	providers := m.activeProvidersLocked()
	m.mu.RUnlock()

	for _, p := range providers {
		if h, ok := p.(MemoryWriteHook); ok {
			safeGo(func() { h.OnMemoryWrite(sessionID, content) })
		}
	}
}

func (m *Manager) OnDelegation(sessionID string) {
	m.mu.RLock()
	providers := m.activeProvidersLocked()
	m.mu.RUnlock()

	for _, p := range providers {
		if h, ok := p.(DelegationHook); ok {
			safeGo(func() { h.OnDelegation(sessionID) })
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
// all active providers that implement SystemPromptProvider.
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
func (m *Manager) Prefetch(query string) (string, error) {
	m.mu.RLock()
	providers := m.activeProvidersLocked()
	m.mu.RUnlock()

	for _, p := range providers {
		if pp, ok := p.(PrefetchProvider); ok {
			return pp.Prefetch(query)
		}
	}
	return "", nil
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
// Errors from individual providers are collected; the first error is returned
// after all providers have been shut down.
func (m *Manager) Shutdown() error {
	m.mu.RLock()
	providers := m.allProvidersLocked()
	m.mu.RUnlock()

	var firstErr error
	for _, p := range providers {
		if sp, ok := p.(ShutdownProvider); ok {
			if err := sp.Shutdown(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
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
func (m *Manager) SaveConfig(providerName string, values map[string]any, hermesHome string) error {
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

	return scp.SaveConfig(values, hermesHome)
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
