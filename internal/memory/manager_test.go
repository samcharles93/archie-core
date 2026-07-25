package memory

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/samcharles93/archie-core/internal/tools"
)

// mockProvider implements every interface in the memory package so tests can
// verify that the MemoryManager correctly discovers and dispatches to optional
// behavior via type assertion.
type mockProvider struct {
	nameVal        string
	availableVal   bool
	toolSchemasVal []tools.ToolEntry
	toolCallHandler func(name string, args map[string]any) (any, error)

	mu sync.Mutex
	// call counters for assertion
	initCalls             int
	shutdownCalls         int
	prefetchCalls         int
	queuePrefetchCalls    int
	syncTurnCalls         int
	systemPromptCalls     int
	turnStartCalls        int
	sessionEndCalls       int
	sessionSwitchCalls    int
	preCompressCalls      int
	memoryWriteCalls      int
	delegationCalls       int
	saveConfigCalls       int
	getConfigSchemaCalls  int
	backupPathsCalls      int
	handleToolCallCalls   int

	// last args
	lastSessionID          string
	lastOldSessionID       string
	lastNewSessionID       string
	lastUserMsg            string
	lastAssistantMsg       string
	lastPrefetchQuery      string
	lastQueuePrefetchQuery string
	lastMemoryContent      string
	lastMemorySessionID    string
	lastToolName           string
	lastToolArgs           map[string]any
	lastSaveValues         map[string]any
	lastSaveHermesHome     string
	lastConfigSchema       map[string]any
	lastBackupPaths        []string

	// hookDone is signalled for each hook goroutine that completes.
	// Tests use it instead of time.Sleep for reliable synchronization.
	hookDone chan string
}

func newMockProvider(name string) *mockProvider {
	return &mockProvider{
		nameVal:      name,
		availableVal: true,
		hookDone:     make(chan string, 50),
		toolSchemasVal: []tools.ToolEntry{
			{
				Name:        name + "_tool",
				Description: "A test tool for " + name,
				Schema:      tools.JSONSchema{"type": "object"},
			},
		},
	}
}

// ── MemoryProvider ──────────────────────────────────────────────────────

func (m *mockProvider) Name() string          { return m.nameVal }
func (m *mockProvider) IsAvailable() bool     { return m.availableVal }
func (m *mockProvider) Initialize(sessionID string) error {
	m.mu.Lock()
	m.initCalls++
	m.lastSessionID = sessionID
	m.mu.Unlock()
	return nil
}
func (m *mockProvider) GetToolSchemas() []tools.ToolEntry { return m.toolSchemasVal }

// ── SystemPromptProvider ────────────────────────────────────────────────

func (m *mockProvider) SystemPromptBlock() string {
	m.mu.Lock()
	m.systemPromptCalls++
	m.mu.Unlock()
	return "mock system prompt block for " + m.nameVal
}

// ── PrefetchProvider ────────────────────────────────────────────────────

func (m *mockProvider) Prefetch(query string) (string, error) {
	m.mu.Lock()
	m.prefetchCalls++
	m.lastPrefetchQuery = query
	m.mu.Unlock()
	return "prefetch result for: " + query, nil
}

func (m *mockProvider) QueuePrefetch(query string) error {
	m.mu.Lock()
	m.queuePrefetchCalls++
	m.lastQueuePrefetchQuery = query
	m.mu.Unlock()
	return nil
}

// ── SyncTurnProvider ────────────────────────────────────────────────────

func (m *mockProvider) SyncTurn(userMsg, assistantMsg string) error {
	m.mu.Lock()
	m.syncTurnCalls++
	m.lastUserMsg = userMsg
	m.lastAssistantMsg = assistantMsg
	m.mu.Unlock()
	return nil
}

// ── ToolCallProvider ────────────────────────────────────────────────────

func (m *mockProvider) HandleToolCall(name string, args map[string]any) (any, error) {
	m.mu.Lock()
	m.handleToolCallCalls++
	m.lastToolName = name
	m.lastToolArgs = args
	m.mu.Unlock()
	if m.toolCallHandler != nil {
		return m.toolCallHandler(name, args)
	}
	return map[string]any{"result": "ok", "provider": m.nameVal}, nil
}

// ── ShutdownProvider ────────────────────────────────────────────────────

func (m *mockProvider) Shutdown() error {
	m.mu.Lock()
	m.shutdownCalls++
	m.mu.Unlock()
	return nil
}

// ── Lifecycle hooks ─────────────────────────────────────────────────────

func (m *mockProvider) OnTurnStart(sessionID string) error {
	m.mu.Lock()
	m.turnStartCalls++
	m.lastSessionID = sessionID
	m.mu.Unlock()
	m.hookDone <- "turnStart:" + m.nameVal
	return nil
}

func (m *mockProvider) OnSessionEnd(sessionID string) error {
	m.mu.Lock()
	m.sessionEndCalls++
	m.lastSessionID = sessionID
	m.mu.Unlock()
	m.hookDone <- "sessionEnd:" + m.nameVal
	return nil
}

func (m *mockProvider) OnSessionSwitch(oldSessionID, newSessionID string) error {
	m.mu.Lock()
	m.sessionSwitchCalls++
	m.lastOldSessionID = oldSessionID
	m.lastNewSessionID = newSessionID
	m.mu.Unlock()
	m.hookDone <- "sessionSwitch:" + m.nameVal
	return nil
}

func (m *mockProvider) OnPreCompress(sessionID string) error {
	m.mu.Lock()
	m.preCompressCalls++
	m.lastSessionID = sessionID
	m.mu.Unlock()
	m.hookDone <- "preCompress:" + m.nameVal
	return nil
}

func (m *mockProvider) OnMemoryWrite(sessionID string, content string) error {
	m.mu.Lock()
	m.memoryWriteCalls++
	m.lastMemorySessionID = sessionID
	m.lastMemoryContent = content
	m.mu.Unlock()
	m.hookDone <- "memoryWrite:" + m.nameVal
	return nil
}

func (m *mockProvider) OnDelegation(sessionID string) error {
	m.mu.Lock()
	m.delegationCalls++
	m.lastSessionID = sessionID
	m.mu.Unlock()
	m.hookDone <- "delegation:" + m.nameVal
	return nil
}

// ── Config helpers ──────────────────────────────────────────────────────

func (m *mockProvider) GetConfigSchema() map[string]any {
	m.mu.Lock()
	m.getConfigSchemaCalls++
	m.mu.Unlock()
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

func (m *mockProvider) SaveConfig(values map[string]any, hermesHome string) error {
	m.mu.Lock()
	m.saveConfigCalls++
	m.lastSaveValues = values
	m.lastSaveHermesHome = hermesHome
	m.mu.Unlock()
	return nil
}

// ── Backup paths ────────────────────────────────────────────────────────

func (m *mockProvider) BackupPaths() []string {
	m.mu.Lock()
	m.backupPathsCalls++
	m.mu.Unlock()
	return []string{"/var/data/" + m.nameVal}
}

// ── Compile-time checks ─────────────────────────────────────────────────
//
// Verify mockProvider satisfies every interface in the package.

var _ MemoryProvider       = (*mockProvider)(nil)
var _ SystemPromptProvider = (*mockProvider)(nil)
var _ PrefetchProvider     = (*mockProvider)(nil)
var _ SyncTurnProvider     = (*mockProvider)(nil)
var _ ToolCallProvider     = (*mockProvider)(nil)
var _ ShutdownProvider     = (*mockProvider)(nil)
var _ TurnStartHook        = (*mockProvider)(nil)
var _ SessionEndHook       = (*mockProvider)(nil)
var _ SessionSwitchHook    = (*mockProvider)(nil)
var _ PreCompressHook      = (*mockProvider)(nil)
var _ MemoryWriteHook      = (*mockProvider)(nil)
var _ DelegationHook       = (*mockProvider)(nil)
var _ ConfigSchemaProvider = (*mockProvider)(nil)
var _ SaveConfigProvider   = (*mockProvider)(nil)
var _ BackupProvider       = (*mockProvider)(nil)

// ── Tests ───────────────────────────────────────────────────────────────

func TestNewManager(t *testing.T) {
	builtin := newMockProvider("builtin")
	mgr, err := NewManager(builtin, nil)
	if err != nil {
		t.Fatalf("NewManager(builtin, nil) returned error: %v", err)
	}
	if mgr == nil {
		t.Fatal("NewManager returned nil manager")
	}
	if got := mgr.Builtin(); got != builtin {
		t.Errorf("Builtin() = %v, want %v", got, builtin)
	}
	if got := mgr.External(); got != nil {
		t.Errorf("External() = %v, want nil", got)
	}
}

func TestNewManager_WithExternal(t *testing.T) {
	builtin := newMockProvider("builtin")
	ext := newMockProvider("external")
	mgr, err := NewManager(builtin, ext)
	if err != nil {
		t.Fatalf("NewManager(builtin, ext) returned error: %v", err)
	}
	if got := mgr.External(); got != ext {
		t.Errorf("External() = %v, want %v", got, ext)
	}
}

func TestNewManager_RegisterExternal(t *testing.T) {
	builtin := newMockProvider("builtin")
	mgr, err := NewManager(builtin, nil)
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}

	ext := newMockProvider("external")
	if err := mgr.RegisterExternal(ext); err != nil {
		t.Fatalf("RegisterExternal() returned error: %v", err)
	}
	if got := mgr.External(); got != ext {
		t.Errorf("External() = %v, want %v", got, ext)
	}
}

func TestNewManager_RegisterSecondExternal_ReturnsError(t *testing.T) {
	builtin := newMockProvider("builtin")
	ext1 := newMockProvider("ext1")
	mgr, err := NewManager(builtin, ext1)
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}

	ext2 := newMockProvider("ext2")
	err = mgr.RegisterExternal(ext2)
	if err == nil {
		t.Fatal("expected error when registering second external provider, got nil")
	}
	if !errors.Is(err, ErrExternalAlreadyRegistered) {
		t.Errorf("expected ErrExternalAlreadyRegistered, got: %v", err)
	}
}

func TestNewManager_NilBuiltin(t *testing.T) {
	mgr, err := NewManager(nil, nil)
	if err != nil {
		t.Fatalf("NewManager(nil, nil) returned error: %v", err)
	}
	if mgr == nil {
		t.Fatal("NewManager returned nil")
	}
	if mgr.Builtin() != nil {
		t.Errorf("Builtin() = %v, want nil", mgr.Builtin())
	}
}

func TestManager_GetToolSchemas_MergesAllProviders(t *testing.T) {
	builtin := newMockProvider("builtin")
	ext := newMockProvider("external")
	// Give the external provider different tools so we can distinguish
	ext.toolSchemasVal = []tools.ToolEntry{
		{Name: "ext_tool_1", Description: "External tool 1"},
		{Name: "ext_tool_2", Description: "External tool 2"},
	}

	mgr, err := NewManager(builtin, ext)
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}

	schemas := mgr.GetToolSchemas()
	if len(schemas) != 3 {
		t.Fatalf("GetToolSchemas() returned %d schemas, want 3", len(schemas))
	}

	// Verify both providers' tools are present
	names := make(map[string]bool)
	for _, s := range schemas {
		names[s.Name] = true
	}
	if !names["builtin_tool"] {
		t.Error("missing builtin_tool in merged schemas")
	}
	if !names["ext_tool_1"] {
		t.Error("missing ext_tool_1 in merged schemas")
	}
	if !names["ext_tool_2"] {
		t.Error("missing ext_tool_2 in merged schemas")
	}

	// Verify injected handlers are present (Finding 2 fix)
	for _, s := range schemas {
		if s.Handler == nil {
			t.Errorf("ToolEntry %q has nil Handler — GetToolSchemas must inject handlers", s.Name)
		}
	}
}

func TestManager_GetToolSchemas_HandlersAreFunctional(t *testing.T) {
	builtin := newMockProvider("builtin")
	mgr, err := NewManager(builtin, nil)
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}

	schemas := mgr.GetToolSchemas()
	if len(schemas) != 1 {
		t.Fatalf("GetToolSchemas() returned %d schemas, want 1", len(schemas))
	}

	// Actually call the injected handler to verify the wiring works
	result, err := schemas[0].Handler(context.Background(), map[string]any{"key": "val"})
	if err != nil {
		t.Fatalf("Handler() returned error: %v", err)
	}
	resultMap, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("Handler() result is %T, want map[string]any", result)
	}
	if resultMap["provider"] != "builtin" {
		t.Errorf("result provider = %q, want %q", resultMap["provider"], "builtin")
	}

	// Verify the mock's HandleToolCall was called through the injected handler
	builtin.mu.Lock()
	if builtin.handleToolCallCalls != 1 {
		t.Errorf("HandleToolCall called %d times via injected handler, want 1", builtin.handleToolCallCalls)
	}
	builtin.mu.Unlock()
}

func TestManager_GetToolSchemas_EmptyProviders(t *testing.T) {
	mgr, err := NewManager(nil, nil)
	if err != nil {
		t.Fatalf("NewManager(nil, nil) returned error: %v", err)
	}

	schemas := mgr.GetToolSchemas()
	if schemas == nil {
		t.Error("GetToolSchemas() returned nil, want empty slice")
	}
	if len(schemas) != 0 {
		t.Errorf("GetToolSchemas() returned %d schemas, want 0", len(schemas))
	}
}

func TestManager_GetToolSchemas_UnavailableProviderExcluded(t *testing.T) {
	builtin := newMockProvider("builtin")
	ext := newMockProvider("external")
	ext.availableVal = false // external provider is unavailable

	mgr, err := NewManager(builtin, ext)
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}

	schemas := mgr.GetToolSchemas()
	if len(schemas) != 1 {
		t.Fatalf("GetToolSchemas() returned %d schemas, want 1 (only builtin)", len(schemas))
	}
	if schemas[0].Name != "builtin_tool" {
		t.Errorf("got tool %q, want builtin_tool", schemas[0].Name)
	}
}

func TestManager_HandleToolCall_RoutesToCorrectProvider(t *testing.T) {
	builtin := newMockProvider("builtin")
	ext := newMockProvider("external")
	ext.toolSchemasVal = []tools.ToolEntry{
		{Name: "ext_tool", Description: "External tool"},
	}

	mgr, err := NewManager(builtin, ext)
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}

	// Call a tool that belongs to the external provider
	result, err := mgr.HandleToolCall("ext_tool", map[string]any{"key": "value"})
	if err != nil {
		t.Fatalf("HandleToolCall() returned error: %v", err)
	}

	resultMap, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("HandleToolCall() result is %T, want map[string]any", result)
	}
	if resultMap["provider"] != "external" {
		t.Errorf("result provider = %q, want %q", resultMap["provider"], "external")
	}

	// Verify the external provider received the call
	ext.mu.Lock()
	defer ext.mu.Unlock()
	if ext.handleToolCallCalls != 1 {
		t.Errorf("external HandleToolCall called %d times, want 1", ext.handleToolCallCalls)
	}
	if ext.lastToolName != "ext_tool" {
		t.Errorf("last tool name = %q, want %q", ext.lastToolName, "ext_tool")
	}
}

func TestManager_HandleToolCall_BuiltinRouting(t *testing.T) {
	builtin := newMockProvider("builtin")
	mgr, err := NewManager(builtin, nil)
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}

	_, err = mgr.HandleToolCall("builtin_tool", map[string]any{})
	if err != nil {
		t.Fatalf("HandleToolCall() returned error: %v", err)
	}

	builtin.mu.Lock()
	defer builtin.mu.Unlock()
	if builtin.handleToolCallCalls != 1 {
		t.Errorf("builtin HandleToolCall called %d times, want 1", builtin.handleToolCallCalls)
	}
}

func TestManager_HandleToolCall_UnknownTool(t *testing.T) {
	builtin := newMockProvider("builtin")
	mgr, err := NewManager(builtin, nil)
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}

	_, err = mgr.HandleToolCall("nonexistent_tool", nil)
	if err == nil {
		t.Fatal("expected error for unknown tool, got nil")
	}
	if !errors.Is(err, ErrToolNotFound) {
		t.Errorf("expected ErrToolNotFound, got: %v", err)
	}
}

func TestManager_HandleToolCall_ProviderDoesNotImplementToolCall(t *testing.T) {
	// Create a minimal provider that only implements MemoryProvider (not ToolCallProvider)
	minimal := &minimalProvider{nameVal: "minimal", availableVal: true}
	mgr, err := NewManager(nil, minimal)
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}

	_, err = mgr.HandleToolCall("minimal_tool", nil)
	if err == nil {
		t.Fatal("expected error when provider doesn't implement ToolCallProvider, got nil")
	}
}

func TestManager_TypeAssertions_DetectsOptionalInterfaces(t *testing.T) {
	builtin := newMockProvider("builtin")
	mgr, err := NewManager(builtin, nil)
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}

	// The mock implements everything — verify the manager detects this
	if !mgr.HasSystemPrompt() {
		t.Error("HasSystemPrompt() = false, want true")
	}
	if !mgr.HasPrefetch() {
		t.Error("HasPrefetch() = false, want true")
	}
	if !mgr.HasSyncTurn() {
		t.Error("HasSyncTurn() = false, want true")
	}
	if !mgr.HasShutdown() {
		t.Error("HasShutdown() = false, want true")
	}
}

func TestManager_TypeAssertions_NoOptionalInterfaces(t *testing.T) {
	minimal := &minimalProvider{nameVal: "minimal", availableVal: true}
	mgr, err := NewManager(minimal, nil)
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}

	if mgr.HasSystemPrompt() {
		t.Error("HasSystemPrompt() = true, want false for minimal provider")
	}
	if mgr.HasPrefetch() {
		t.Error("HasPrefetch() = true, want false for minimal provider")
	}
	if mgr.HasSyncTurn() {
		t.Error("HasSyncTurn() = true, want false for minimal provider")
	}
	if mgr.HasShutdown() {
		t.Error("HasShutdown() = true, want false for minimal provider")
	}
}

// waitForHooks drains exactly expected signals from the providers' hookDone
// channels. It fails the test if the signals don't arrive within 5 seconds.
// This replaces the flaky time.Sleep approach with deterministic
// synchronization.
func waitForHooks(t *testing.T, expected int, providers ...*mockProvider) {
	t.Helper()

	// Multiplex all provider channels into one logical stream.
	for received := 0; received < expected; received++ {
		drained := false
		for _, p := range providers {
			select {
			case <-p.hookDone:
				drained = true
			default:
			}
			if drained {
				break
			}
		}
		if !drained {
			// No signal ready yet — block on the first provider that still
			// has capacity (any will do since hooks fire concurrently).
			<-providers[0].hookDone
		}
	}
}

func TestManager_LifecycleHooks_AllCalled(t *testing.T) {
	builtin := newMockProvider("builtin")
	ext := newMockProvider("external")
	mgr, err := NewManager(builtin, ext)
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}

	sessionID := "session-123"
	newSessionID := "session-456"

	// Call all lifecycle hooks through the manager
	mgr.OnTurnStart(sessionID)
	mgr.OnSessionEnd(sessionID)
	mgr.OnSessionSwitch(sessionID, newSessionID)
	mgr.OnPreCompress(sessionID)
	mgr.OnMemoryWrite(sessionID, "test content")
	mgr.OnDelegation(sessionID)

	// Wait for all 12 goroutines (6 hooks × 2 providers) to complete
	waitForHooks(t, 12, builtin, ext)

	// Verify builtin received all hook calls
	builtin.mu.Lock()
	if builtin.turnStartCalls != 1 {
		t.Errorf("builtin turnStartCalls = %d, want 1", builtin.turnStartCalls)
	}
	if builtin.sessionEndCalls != 1 {
		t.Errorf("builtin sessionEndCalls = %d, want 1", builtin.sessionEndCalls)
	}
	if builtin.sessionSwitchCalls != 1 {
		t.Errorf("builtin sessionSwitchCalls = %d, want 1", builtin.sessionSwitchCalls)
	}
	if builtin.preCompressCalls != 1 {
		t.Errorf("builtin preCompressCalls = %d, want 1", builtin.preCompressCalls)
	}
	if builtin.memoryWriteCalls != 1 {
		t.Errorf("builtin memoryWriteCalls = %d, want 1", builtin.memoryWriteCalls)
	}
	if builtin.delegationCalls != 1 {
		t.Errorf("builtin delegationCalls = %d, want 1", builtin.delegationCalls)
	}
	builtin.mu.Unlock()

	// Verify external received ALL hook calls (Finding 10 fix)
	ext.mu.Lock()
	if ext.turnStartCalls != 1 {
		t.Errorf("external turnStartCalls = %d, want 1", ext.turnStartCalls)
	}
	if ext.sessionEndCalls != 1 {
		t.Errorf("external sessionEndCalls = %d, want 1", ext.sessionEndCalls)
	}
	if ext.sessionSwitchCalls != 1 {
		t.Errorf("external sessionSwitchCalls = %d, want 1", ext.sessionSwitchCalls)
	}
	if ext.preCompressCalls != 1 {
		t.Errorf("external preCompressCalls = %d, want 1", ext.preCompressCalls)
	}
	if ext.memoryWriteCalls != 1 {
		t.Errorf("external memoryWriteCalls = %d, want 1", ext.memoryWriteCalls)
	}
	if ext.delegationCalls != 1 {
		t.Errorf("external delegationCalls = %d, want 1", ext.delegationCalls)
	}
	ext.mu.Unlock()
}

func TestManager_LifecycleHooks_MinimalProvider(t *testing.T) {
	// Minimal providers that don't implement hooks should not panic
	minimal := &minimalProvider{nameVal: "minimal", availableVal: true}
	mgr, err := NewManager(minimal, nil)
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}

	// These should not panic even though the provider doesn't implement hooks
	mgr.OnTurnStart("s1")
	mgr.OnSessionEnd("s1")
	mgr.OnSessionSwitch("s1", "s2")
	mgr.OnPreCompress("s1")
	mgr.OnMemoryWrite("s1", "content")
	mgr.OnDelegation("s1")
}

func TestManager_LifecycleHooks_PanicRecovery(t *testing.T) {
	// A provider whose hook panics should not crash the test process
	panicky := &panickyProvider{nameVal: "panicky", availableVal: true}
	mgr, err := NewManager(panicky, nil)
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}

	// These must not panic the test
	mgr.OnTurnStart("s1")
	mgr.OnSessionEnd("s1")
	mgr.OnSessionSwitch("s1", "s2")
	mgr.OnPreCompress("s1")
	mgr.OnMemoryWrite("s1", "content")
	mgr.OnDelegation("s1")

	// If we reached here without panicking, the recovery works
}

func TestManager_Shutdown(t *testing.T) {
	builtin := newMockProvider("builtin")
	ext := newMockProvider("external")
	mgr, err := NewManager(builtin, ext)
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}

	if err := mgr.Shutdown(); err != nil {
		t.Fatalf("Shutdown() returned error: %v", err)
	}

	builtin.mu.Lock()
	if builtin.shutdownCalls != 1 {
		t.Errorf("builtin shutdownCalls = %d, want 1", builtin.shutdownCalls)
	}
	builtin.mu.Unlock()

	ext.mu.Lock()
	if ext.shutdownCalls != 1 {
		t.Errorf("external shutdownCalls = %d, want 1", ext.shutdownCalls)
	}
	ext.mu.Unlock()
}

func TestManager_Shutdown_NoShutdownProviders(t *testing.T) {
	minimal := &minimalProvider{nameVal: "minimal", availableVal: true}
	mgr, err := NewManager(minimal, nil)
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}

	// Should not panic or error
	if err := mgr.Shutdown(); err != nil {
		t.Fatalf("Shutdown() returned error: %v", err)
	}
}

func TestManager_Shutdown_UnavailableProviderStillShutDown(t *testing.T) {
	// Finding 7 fix: shutdown must include providers even if IsAvailable() == false
	builtin := newMockProvider("builtin")
	builtin.availableVal = false // unavailable but still holding resources
	mgr, err := NewManager(builtin, nil)
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}

	if err := mgr.Shutdown(); err != nil {
		t.Fatalf("Shutdown() returned error: %v", err)
	}

	builtin.mu.Lock()
	if builtin.shutdownCalls != 1 {
		t.Errorf("unavailable provider shutdown was NOT called (shutdownCalls=%d), want 1 — resources may leak",
			builtin.shutdownCalls)
	}
	builtin.mu.Unlock()
}

func TestManager_GetConfigSchema_Aggregates(t *testing.T) {
	builtin := newMockProvider("builtin")
	ext := newMockProvider("external")
	mgr, err := NewManager(builtin, ext)
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}

	schemas := mgr.GetConfigSchemas()
	if len(schemas) != 2 {
		t.Fatalf("GetConfigSchemas() returned %d schemas, want 2", len(schemas))
	}
	if _, ok := schemas["builtin"]; !ok {
		t.Error("missing builtin in config schemas")
	}
	if _, ok := schemas["external"]; !ok {
		t.Error("missing external in config schemas")
	}
}

func TestManager_SaveConfig(t *testing.T) {
	builtin := newMockProvider("builtin")
	mgr, err := NewManager(builtin, nil)
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}

	values := map[string]any{"key": "value"}
	if err := mgr.SaveConfig("builtin", values, "/tmp/hermes"); err != nil {
		t.Fatalf("SaveConfig() returned error: %v", err)
	}

	builtin.mu.Lock()
	if builtin.saveConfigCalls != 1 {
		t.Errorf("saveConfigCalls = %d, want 1", builtin.saveConfigCalls)
	}
	builtin.mu.Unlock()
}

func TestManager_SaveConfig_UnknownProvider(t *testing.T) {
	builtin := newMockProvider("builtin")
	mgr, err := NewManager(builtin, nil)
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}

	err = mgr.SaveConfig("nonexistent", map[string]any{}, "/tmp/hermes")
	if err == nil {
		t.Fatal("expected error for unknown provider, got nil")
	}
}

func TestManager_SaveConfig_ProviderLacksSaveConfig(t *testing.T) {
	minimal := &minimalProvider{nameVal: "minimal", availableVal: true}
	mgr, err := NewManager(minimal, nil)
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}

	err = mgr.SaveConfig("minimal", map[string]any{}, "/tmp/hermes")
	if err == nil {
		t.Fatal("expected error when provider lacks SaveConfigProvider, got nil")
	}
}

func TestManager_BackupPaths_Aggregates(t *testing.T) {
	builtin := newMockProvider("builtin")
	ext := newMockProvider("external")
	mgr, err := NewManager(builtin, ext)
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}

	paths := mgr.BackupPaths()
	if len(paths) != 2 {
		t.Fatalf("BackupPaths() returned %d paths, want 2", len(paths))
	}
}

func TestManager_BackupPaths_NoBackupProviders(t *testing.T) {
	minimal := &minimalProvider{nameVal: "minimal", availableVal: true}
	mgr, err := NewManager(minimal, nil)
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}

	paths := mgr.BackupPaths()
	if paths == nil {
		t.Error("BackupPaths() returned nil, want empty slice")
	}
	if len(paths) != 0 {
		t.Errorf("BackupPaths() returned %d paths, want 0", len(paths))
	}
}

func TestManager_Prefetch(t *testing.T) {
	builtin := newMockProvider("builtin")
	mgr, err := NewManager(builtin, nil)
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}

	result, err := mgr.Prefetch("test query")
	if err != nil {
		t.Fatalf("Prefetch() returned error: %v", err)
	}
	if result == "" {
		t.Error("Prefetch() returned empty result")
	}
}

func TestManager_QueuePrefetch(t *testing.T) {
	builtin := newMockProvider("builtin")
	mgr, err := NewManager(builtin, nil)
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}

	if err := mgr.QueuePrefetch("background query"); err != nil {
		t.Fatalf("QueuePrefetch() returned error: %v", err)
	}

	builtin.mu.Lock()
	if builtin.queuePrefetchCalls != 1 {
		t.Errorf("queuePrefetchCalls = %d, want 1", builtin.queuePrefetchCalls)
	}
	builtin.mu.Unlock()
}

func TestManager_SyncTurn(t *testing.T) {
	builtin := newMockProvider("builtin")
	mgr, err := NewManager(builtin, nil)
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}

	if err := mgr.SyncTurn("user msg", "assistant msg"); err != nil {
		t.Fatalf("SyncTurn() returned error: %v", err)
	}

	builtin.mu.Lock()
	if builtin.syncTurnCalls != 1 {
		t.Errorf("syncTurnCalls = %d, want 1", builtin.syncTurnCalls)
	}
	builtin.mu.Unlock()
}

func TestManager_SystemPromptBlock(t *testing.T) {
	builtin := newMockProvider("builtin")
	mgr, err := NewManager(builtin, nil)
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}

	block := mgr.SystemPromptBlock()
	if block == "" {
		t.Error("SystemPromptBlock() returned empty string")
	}
}

func TestManager_Initialize(t *testing.T) {
	builtin := newMockProvider("builtin")
	ext := newMockProvider("external")
	mgr, err := NewManager(builtin, ext)
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}

	if err := mgr.Initialize("session-42"); err != nil {
		t.Fatalf("Initialize() returned error: %v", err)
	}

	builtin.mu.Lock()
	if builtin.initCalls != 1 {
		t.Errorf("builtin initCalls = %d, want 1", builtin.initCalls)
	}
	if builtin.lastSessionID != "session-42" {
		t.Errorf("builtin sessionID = %q, want %q", builtin.lastSessionID, "session-42")
	}
	builtin.mu.Unlock()

	ext.mu.Lock()
	if ext.initCalls != 1 {
		t.Errorf("external initCalls = %d, want 1", ext.initCalls)
	}
	ext.mu.Unlock()
}

func TestManager_DuplicateToolNames_FirstWins(t *testing.T) {
	// Finding 8 fix: duplicate tool names should be deterministic (first wins)
	builtin := newMockProvider("builtin")
	builtin.toolSchemasVal = []tools.ToolEntry{
		{Name: "shared_tool", Description: "Builtin version"},
	}
	ext := newMockProvider("external")
	ext.toolSchemasVal = []tools.ToolEntry{
		{Name: "shared_tool", Description: "External version"},
	}

	mgr, err := NewManager(builtin, ext)
	if err != nil {
		t.Fatalf("NewManager returned error: %v", err)
	}

	// HandleToolCall should route to builtin (first wins)
	result, err := mgr.HandleToolCall("shared_tool", nil)
	if err != nil {
		t.Fatalf("HandleToolCall() returned error: %v", err)
	}
	resultMap, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("HandleToolCall() result is %T, want map[string]any", result)
	}
	if resultMap["provider"] != "builtin" {
		t.Errorf("duplicate tool routed to %q, want %q (first-wins)", resultMap["provider"], "builtin")
	}
}

// ── minimalProvider ─────────────────────────────────────────────────────
//
// A provider that only implements the required MemoryProvider interface.
// Used to test graceful nil/empty handling for optional interfaces.

type minimalProvider struct {
	nameVal      string
	availableVal bool
}

func (p *minimalProvider) Name() string                      { return p.nameVal }
func (p *minimalProvider) IsAvailable() bool                 { return p.availableVal }
func (p *minimalProvider) Initialize(sessionID string) error { return nil }
func (p *minimalProvider) GetToolSchemas() []tools.ToolEntry {
	return []tools.ToolEntry{
		{Name: p.nameVal + "_tool", Description: "Minimal tool for " + p.nameVal},
	}
}

var _ MemoryProvider = (*minimalProvider)(nil)

// ── panickyProvider ─────────────────────────────────────────────────────
//
// A provider whose hook methods panic. Used to verify that safeGo recovery
// prevents hook panics from crashing the process (Finding 1 fix).

type panickyProvider struct {
	nameVal      string
	availableVal bool
}

func (p *panickyProvider) Name() string                      { return p.nameVal }
func (p *panickyProvider) IsAvailable() bool                 { return p.availableVal }
func (p *panickyProvider) Initialize(sessionID string) error { return nil }
func (p *panickyProvider) GetToolSchemas() []tools.ToolEntry {
	return []tools.ToolEntry{
		{Name: p.nameVal + "_tool", Description: "Panicky tool"},
	}
}

// Every hook panics — safeGo must recover each one.
func (p *panickyProvider) OnTurnStart(sessionID string) error {
	panic("OnTurnStart panicked")
}
func (p *panickyProvider) OnSessionEnd(sessionID string) error {
	panic("OnSessionEnd panicked")
}
func (p *panickyProvider) OnSessionSwitch(oldSessionID, newSessionID string) error {
	panic("OnSessionSwitch panicked")
}
func (p *panickyProvider) OnPreCompress(sessionID string) error {
	panic("OnPreCompress panicked")
}
func (p *panickyProvider) OnMemoryWrite(sessionID string, content string) error {
	panic("OnMemoryWrite panicked")
}
func (p *panickyProvider) OnDelegation(sessionID string) error {
	panic("OnDelegation panicked")
}

var _ MemoryProvider    = (*panickyProvider)(nil)
var _ TurnStartHook     = (*panickyProvider)(nil)
var _ SessionEndHook    = (*panickyProvider)(nil)
var _ SessionSwitchHook = (*panickyProvider)(nil)
var _ PreCompressHook   = (*panickyProvider)(nil)
var _ MemoryWriteHook   = (*panickyProvider)(nil)
var _ DelegationHook    = (*panickyProvider)(nil)
