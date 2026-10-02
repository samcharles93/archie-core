package archied

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/samcharles93/archie-core/internal/app/controlplane"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/plugin"
	"github.com/samcharles93/archie-core/internal/secret"
	"github.com/samcharles93/archie-core/internal/tools"
	toolprovider "github.com/samcharles93/archie-core/internal/tools/provider"
	"github.com/samcharles93/archie-core/internal/tools/webfetch"
)

// fakeMCPEngine stands in for a configured MCP server so the live reconciliation
// can be driven without spawning a process. It records each lifecycle call on
// the shared recorder, keyed by configured server name.
type fakeMCPEngine struct {
	name     string
	tools    []tools.ToolEntry
	rec      *mcpEngineRecorder
	startErr error
}

func (e *fakeMCPEngine) Manifest() plugin.Manifest {
	return plugin.Manifest{
		ID:           "mcp." + e.name,
		Name:         e.name,
		Version:      "1.0.0",
		APIVersion:   plugin.HostAPIVersion,
		Capabilities: []plugin.CapabilityKind{"tools"},
	}
}

func (e *fakeMCPEngine) Start(context.Context) error {
	e.rec.recordStart(e.name)
	return e.startErr
}

func (e *fakeMCPEngine) Stop(context.Context) error {
	e.rec.recordStop(e.name)
	return nil
}

func (e *fakeMCPEngine) Discover(context.Context) ([]tools.ToolEntry, error) {
	return append([]tools.ToolEntry(nil), e.tools...), nil
}

// mcpEngineRecorder records what the live reconciliation did to each server and
// hands the test a factory boot.mcpProvider can use.
type mcpEngineRecorder struct {
	mu       sync.Mutex
	builds   map[string]int
	starts   map[string]int
	stops    map[string]int
	failNext map[string]error
	seen     map[string]config.MCPServer
}

func newMCPEngineRecorder() *mcpEngineRecorder {
	return &mcpEngineRecorder{
		builds:   map[string]int{},
		starts:   map[string]int{},
		stops:    map[string]int{},
		failNext: map[string]error{},
		seen:     map[string]config.MCPServer{},
	}
}

func (r *mcpEngineRecorder) factory() func(config.MCPServer) (toolprovider.Engine, error) {
	return func(srv config.MCPServer) (toolprovider.Engine, error) {
		name := strings.TrimSpace(srv.Name)
		r.mu.Lock()
		r.builds[name]++
		r.seen[name] = srv
		err := r.failNext[name]
		delete(r.failNext, name)
		r.mu.Unlock()
		return &fakeMCPEngine{
			name:     name,
			rec:      r,
			startErr: err,
			tools: []tools.ToolEntry{{
				Name:    "mcp." + name + ".tool",
				Toolset: "mcp",
				Handler: func(context.Context, map[string]any) (any, error) { return "ok", nil },
			}},
		}, nil
	}
}

func (r *mcpEngineRecorder) recordStart(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.starts[name]++
}

func (r *mcpEngineRecorder) recordStop(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stops[name]++
}

func (r *mcpEngineRecorder) failOnNextStart(name string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failNext[name] = err
}

func (r *mcpEngineRecorder) count(counts map[string]int, name string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return counts[name]
}

// toolSettingsDocument renders a stored tool-settings document carrying the
// given MCP servers and an enabled web_fetch.
func toolSettingsDocument(servers ...map[string]any) map[string]any {
	return map[string]any{
		"mcp_servers": servers,
		"policy":      map[string]any{},
		"web_fetch":   map[string]any{},
		"minimax":     map[string]any{},
	}
}

func mcpServerDoc(name string, parallel bool) map[string]any {
	return map[string]any{
		"name":                name,
		"transport":           "stdio",
		"command":             "run-server",
		"parallel_tool_calls": parallel,
	}
}

// newMCPReconcileBoot builds a boot whose provider registry is running, so the
// live reconciliation acts on a real registry and a real tool index.
func newMCPReconcileBoot(t *testing.T, base config.Config, stub *resourceWatchStub) (*boot, *applyStatusRecorder, *mcpEngineRecorder) {
	t.Helper()
	b, status := newLiveApplyBoot(t, base)
	b.controlPlane = controlplane.NewRPCClient(stub)
	b.secrets = secret.NewRegistry()
	b.toolReg = tools.NewRegistry()
	b.providerRegistry = toolprovider.NewRegistry(b.toolReg)
	if err := b.providerRegistry.Start(t.Context()); err != nil {
		t.Fatalf("start provider registry: %v", err)
	}
	b.mcpApplied = map[string]appliedMCPServer{}
	rec := newMCPEngineRecorder()
	b.mcpProvider = rec.factory()
	return b, status, rec
}

// applyToolSettings stores the document and runs the live apply the watch
// would run, the seam the daemon meets a stored tool-settings change through.
func applyToolSettings(t *testing.T, b *boot, stub *resourceWatchStub, version int64, document map[string]any) {
	t.Helper()
	if stub.values == nil {
		stub.values = map[string]any{}
	}
	stub.values[controlplane.ToolSettingsKind] = document
	if stub.storedVersion == nil {
		stub.storedVersion = map[string]int64{}
	}
	stub.storedVersion[controlplane.ToolSettingsKind] = version
	b.applyRuntimeResourceUpdate(t.Context(), controlplane.ToolSettingsKind, controlplane.AppliedResource{Version: version})
}

func TestLiveToolSettingsConnectsAnAddedServer(t *testing.T) {
	stub := &resourceWatchStub{values: databaseOwnedResources(), version: 10, storedVersion: map[string]int64{}, opens: map[string]int{}}
	b, _, rec := newMCPReconcileBoot(t, fileConfig(), stub)

	applyToolSettings(t, b, stub, 11, toolSettingsDocument(mcpServerDoc("alpha", false)))

	if got := rec.count(rec.starts, "alpha"); got != 1 {
		t.Errorf("alpha start count = %d, want 1", got)
	}
	if _, ok := b.toolReg.Get("mcp.alpha.tool"); !ok {
		t.Error("the added server's tool was not indexed")
	}
	if !b.providerRegistry.Has("mcp.alpha") {
		t.Error("the added server is not registered")
	}
	servers := b.cfgHolder.Get().Tools.MCPServers
	if len(servers) != 1 || servers[0].Name != "alpha" {
		t.Errorf("running config servers = %+v, want the added alpha", servers)
	}
}

func TestLiveToolSettingsLeavesAnUnchangedServerUntouched(t *testing.T) {
	stub := &resourceWatchStub{values: databaseOwnedResources(), version: 10, storedVersion: map[string]int64{}, opens: map[string]int{}}
	b, _, rec := newMCPReconcileBoot(t, fileConfig(), stub)

	applyToolSettings(t, b, stub, 11, toolSettingsDocument(mcpServerDoc("alpha", false)))
	applyToolSettings(t, b, stub, 12, toolSettingsDocument(mcpServerDoc("alpha", false), mcpServerDoc("beta", false)))

	if got := rec.count(rec.builds, "alpha"); got != 1 {
		t.Errorf("alpha was rebuilt on an unchanged update: builds = %d, want 1", got)
	}
	if got := rec.count(rec.starts, "alpha"); got != 1 {
		t.Errorf("alpha was reconnected on an unchanged update: starts = %d, want 1", got)
	}
	if got := rec.count(rec.stops, "alpha"); got != 0 {
		t.Errorf("alpha was stopped on an unchanged update: stops = %d, want 0", got)
	}
	if got := rec.count(rec.starts, "beta"); got != 1 {
		t.Errorf("beta start count = %d, want 1", got)
	}
}

func TestLiveToolSettingsReconnectsAChangedServer(t *testing.T) {
	stub := &resourceWatchStub{values: databaseOwnedResources(), version: 10, storedVersion: map[string]int64{}, opens: map[string]int{}}
	b, _, rec := newMCPReconcileBoot(t, fileConfig(), stub)

	applyToolSettings(t, b, stub, 11, toolSettingsDocument(mcpServerDoc("alpha", false)))
	applyToolSettings(t, b, stub, 12, toolSettingsDocument(mcpServerDoc("alpha", true)))

	if got := rec.count(rec.stops, "alpha"); got != 1 {
		t.Errorf("the changed server was not disconnected: stops = %d, want 1", got)
	}
	if got := rec.count(rec.starts, "alpha"); got != 2 {
		t.Errorf("the changed server was not reconnected: starts = %d, want 2", got)
	}
	if _, ok := b.toolReg.Get("mcp.alpha.tool"); !ok {
		t.Error("the reconnected server's tool left the index")
	}
}

func TestLiveToolSettingsDisconnectsARemovedServer(t *testing.T) {
	stub := &resourceWatchStub{values: databaseOwnedResources(), version: 10, storedVersion: map[string]int64{}, opens: map[string]int{}}
	b, _, rec := newMCPReconcileBoot(t, fileConfig(), stub)

	applyToolSettings(t, b, stub, 11, toolSettingsDocument(mcpServerDoc("alpha", false), mcpServerDoc("beta", false)))
	applyToolSettings(t, b, stub, 12, toolSettingsDocument(mcpServerDoc("beta", false)))

	if got := rec.count(rec.stops, "alpha"); got != 1 {
		t.Errorf("the removed server was not disconnected: stops = %d, want 1", got)
	}
	if b.providerRegistry.Has("mcp.alpha") {
		t.Error("the removed server stayed registered")
	}
	if _, ok := b.toolReg.Get("mcp.alpha.tool"); ok {
		t.Error("the removed server's tool stayed indexed")
	}
	if got := rec.count(rec.stops, "beta"); got != 0 {
		t.Errorf("the surviving server was disturbed: stops = %d, want 0", got)
	}
}

// TestLiveToolSettingsKeepsTheRunningServerWhenAChangeCannotStart is the path
// where the change must NOT apply: a changed server whose replacement fails to
// start is rolled back to the one that was running, and the refusal reaches
// apply status rather than the kind silently reporting success.
func TestLiveToolSettingsKeepsTheRunningServerWhenAChangeCannotStart(t *testing.T) {
	stub := &resourceWatchStub{values: databaseOwnedResources(), version: 10, storedVersion: map[string]int64{}, opens: map[string]int{}}
	b, status, rec := newMCPReconcileBoot(t, fileConfig(), stub)

	applyToolSettings(t, b, stub, 11, toolSettingsDocument(mcpServerDoc("alpha", false)))
	rec.failOnNextStart("alpha", errors.New("handshake refused"))
	applyToolSettings(t, b, stub, 12, toolSettingsDocument(mcpServerDoc("alpha", true)))

	// Two stops: the running server is disconnected for the replacement, and
	// the replacement's own half-start is cleaned up when its handshake fails.
	if got := rec.count(rec.stops, "alpha"); got != 2 {
		t.Errorf("stops = %d, want 2 (the replaced server and the failed replacement's cleanup)", got)
	}
	if got := rec.count(rec.starts, "alpha"); got != 3 {
		t.Errorf("starts = %d, want 3 (initial, refused replacement, rollback restart)", got)
	}
	if _, ok := b.toolReg.Get("mcp.alpha.tool"); !ok {
		t.Error("the refused change left the running server's tool out of the index")
	}
	if !b.providerRegistry.Has("mcp.alpha") {
		t.Error("the refused change left the server unregistered")
	}
	last, ok := status.last(controlplane.ToolSettingsKind)
	if !ok {
		t.Fatal("the refused change reported no apply status")
	}
	if !strings.Contains(last.Error, "alpha") {
		t.Errorf("apply status error = %q, want it to name the refused server", last.Error)
	}
}

// TestLiveToolSettingsRebuildsTheWebFetchEntry: web_fetch captured its config
// at construction, so a live change rebuilds the entry rather than leaving the
// boot value in the model's tool list.
func TestLiveToolSettingsRebuildsTheWebFetchEntry(t *testing.T) {
	stub := &resourceWatchStub{values: databaseOwnedResources(), version: 10, storedVersion: map[string]int64{}, opens: map[string]int{}}
	b, _, _ := newMCPReconcileBoot(t, fileConfig(), stub)

	disabled := toolSettingsDocument()
	disabled["web_fetch"] = map[string]any{"enabled": false}
	applyToolSettings(t, b, stub, 11, disabled)
	if _, ok := b.toolReg.Get(webfetch.ToolName); ok {
		t.Error("web_fetch stayed registered after the stored document disabled it")
	}

	applyToolSettings(t, b, stub, 12, toolSettingsDocument())
	if _, ok := b.toolReg.Get(webfetch.ToolName); !ok {
		t.Error("web_fetch was not registered after the stored document re-enabled it")
	}
}

// TestLiveToolSettingsKeepsAnUnprojectedMCPHeader: the stored document cannot
// carry secret-bearing headers, so the layering restores them from the running
// base (controlplane.runtimeToolConfigFrom). The reconciliation must build the
// reconnected engine from that layered server, not from the document alone, or
// an authentication header disappears on the first live edit.
func TestLiveToolSettingsKeepsAnUnprojectedMCPHeader(t *testing.T) {
	base := fileConfig()
	base.Tools.MCPServers = []config.MCPServer{{
		Name:      "alpha",
		Transport: "http",
		URL:       "https://alpha.example/mcp",
		Headers:   map[string]string{"Authorization": "Bearer secret"},
	}}
	stub := &resourceWatchStub{values: databaseOwnedResources(), version: 10, storedVersion: map[string]int64{}, opens: map[string]int{}}
	b, _, rec := newMCPReconcileBoot(t, base, stub)

	changed := mcpServerDoc("alpha", true)
	changed["transport"] = "http"
	changed["url"] = "https://alpha.example/mcp"
	delete(changed, "command")
	applyToolSettings(t, b, stub, 11, toolSettingsDocument(changed))

	rec.mu.Lock()
	seen := rec.seen["alpha"]
	rec.mu.Unlock()
	if got := seen.Headers["Authorization"]; got != "Bearer secret" {
		t.Errorf("reconnected server headers = %v, want the unprojected Authorization header restored", seen.Headers)
	}
}
