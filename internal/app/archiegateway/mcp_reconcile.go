package archiegateway

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/tools/minimax"
	toolprovider "github.com/samcharles93/archie-core/internal/tools/provider"
	"github.com/samcharles93/archie-core/internal/tools/webfetch"
)

// appliedMCPServer is one MCP server the running provider registry was built
// from: the configuration it was built with and the manifest id it registered
// under. A live tool-settings change diffs the stored server set against this
// record, so a server that did not change is left running and untouched
type appliedMCPServer struct {
	server config.MCPServer
	id     string
}

// buildMCPProvider builds the tool-provider engine for one configured MCP
// server: configuredMCPProvider with the running config's work directory and the
// Gateway's sampling handler. Boot registration and the live reconciliation
// both reach the engine through here, so the two cannot build one differently.
func (b *server) buildMCPProvider(srv config.MCPServer) (toolprovider.Engine, error) {
	return configuredMCPProvider(srv, b.cfgHolder.Get().WorkDir, b.mcpSamplingHandler())
}

// reconcileToolSettings applies a live tool-settings change to the components
// built once from that kind: the MCP provider set (diffed, so an unchanged
// server keeps running), the web_fetch and minimax tool entries (rebuilt,
// because the entry captured the config at construction) and the tool-output
// policy (whose spill directory is ensured here; the task snapshot reads the
// policy fresh per dispatch and toolLimits is read per call). A nil tool
// registry means this process built no tools, so only the config was layered.
func (b *server) reconcileToolSettings(ctx context.Context, cfg config.Config) error {
	if err := toolLimits(cfg).EnsureSpillDir(); err != nil {
		b.log.Warn("tool spill directory unavailable; large results will be truncated instead", "err", err)
	}
	var problems []error
	if err := b.reconcileMCPServers(ctx, cfg.Tools.MCPServers); err != nil {
		problems = append(problems, err)
	}
	if b.toolReg != nil {
		// Unregister first: the boot entry is being replaced, and a second
		// registration would collide on the tool name. Minimax resolves its
		// API key again as part of the rebuild, so a rotated credential
		// applies too.
		b.toolReg.Unregister(webfetch.ToolName, minimax.ToolName)
		b.registerWebFetchTool(cfg)
		b.registerMinimaxTool(cfg, b.log)
	}
	return errors.Join(problems...)
}

// reconcileMCPServers brings the running provider set in line with the MCP
// servers the layered configuration now names: a server that appeared
// connects, one that disappeared disconnects, one whose configuration changed
// reconnects, and an unchanged one is left running and untouched. It is
// idempotent, so it can run on every stored tool-settings version without
// disturbing the servers that did not change.
//
// A server whose engine cannot be built or started is reported and left out;
// the servers already running keep running, the degrade-and-skip rule the boot
// registration applies. A changed server whose new engine fails to start is
// rolled back to the old one by the registry, so a refused change does not
// leave the server down.
func (b *server) reconcileMCPServers(ctx context.Context, servers []config.MCPServer) error {
	if b.providerRegistry == nil {
		return nil
	}
	b.mcpMu.Lock()
	defer b.mcpMu.Unlock()

	desired := make(map[string]config.MCPServer, len(servers))
	for _, srv := range servers {
		if name := strings.TrimSpace(srv.Name); name != "" {
			desired[name] = srv
		}
	}
	state := b.mcpRegistryState()
	var problems []error
	problems = append(problems, b.disconnectAbsentMCPServers(ctx, desired)...)
	problems = append(problems, b.applyDesiredMCPServers(ctx, desired, state)...)
	return errors.Join(problems...)
}

// mcpRegistryState is the read side of the provider registry a diff needs: is
// the family running at all, which ids are registered, and which are live.
type mcpRegistryState struct {
	familyRunning bool
	registered    map[string]bool
	live          map[string]bool
}

func (b *server) mcpRegistryState() mcpRegistryState {
	registered := make(map[string]bool)
	for _, id := range b.providerRegistry.RegisteredIDs() {
		registered[id] = true
	}
	live := make(map[string]bool)
	for _, id := range b.providerRegistry.RunningIDs() {
		live[id] = true
	}
	return mcpRegistryState{
		familyRunning: b.providerRegistry.Running(),
		registered:    registered,
		live:          live,
	}
}

// disconnectAbsentMCPServers removes every applied server the stored document
// no longer names.
func (b *server) disconnectAbsentMCPServers(ctx context.Context, desired map[string]config.MCPServer) []error {
	var problems []error
	for name := range b.mcpApplied {
		if _, ok := desired[name]; ok {
			continue
		}
		applied := b.mcpApplied[name]
		if err := b.providerRegistry.Remove(ctx, applied.id); err != nil {
			problems = append(problems, fmt.Errorf("disconnect MCP server %q: %w", name, err))
			continue
		}
		delete(b.mcpApplied, name)
	}
	return problems
}

// applyDesiredMCPServers connects, reconnects or restarts the servers the
// stored document names, skipping the ones already running unchanged.
func (b *server) applyDesiredMCPServers(ctx context.Context, desired map[string]config.MCPServer, state mcpRegistryState) []error {
	names := make([]string, 0, len(desired))
	for name := range desired {
		names = append(names, name)
	}
	slices.Sort(names)

	var problems []error
	for _, name := range names {
		srv := desired[name]
		if b.mcpServerCurrent(name, srv, state) {
			continue
		}
		engine, err := b.buildMCPProvider(srv)
		if err != nil {
			problems = append(problems, fmt.Errorf("MCP server %q: %w", name, err))
			continue
		}
		if err := b.applyMCPProvider(ctx, engine, state); err != nil {
			problems = append(problems, fmt.Errorf("connect MCP server %q: %w", name, err))
			continue
		}
		b.mcpApplied[name] = appliedMCPServer{server: srv, id: engine.Manifest().ID}
	}
	return problems
}

// mcpServerCurrent reports whether the server is already running unchanged, so
// a reconciliation can leave it untouched. Present means running while the
// family runs, or still registered while it has not started; a
// registered-but-not-running server on a running family is a refused earlier
// start and is worth another attempt.
func (b *server) mcpServerCurrent(name string, srv config.MCPServer, state mcpRegistryState) bool {
	applied, ok := b.mcpApplied[name]
	if !ok || !reflect.DeepEqual(applied.server, srv) {
		return false
	}
	if !state.registered[applied.id] {
		return false
	}
	return !state.familyRunning || state.live[applied.id]
}

// applyMCPProvider replaces a registered provider and adds an absent one,
// whichever the registry needs for that id.
func (b *server) applyMCPProvider(ctx context.Context, engine toolprovider.Engine, state mcpRegistryState) error {
	if state.registered[engine.Manifest().ID] {
		return b.providerRegistry.Replace(ctx, engine)
	}
	return b.providerRegistry.Add(ctx, engine)
}
