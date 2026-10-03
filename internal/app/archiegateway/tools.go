package archiegateway

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/samcharles93/archie-core/internal/agentexec/modelloop"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/plugin"
	"github.com/samcharles93/archie-core/internal/skill"
	"github.com/samcharles93/archie-core/internal/tools/mcp"
	"github.com/samcharles93/archie-core/internal/tools/minimax"
	toolprovider "github.com/samcharles93/archie-core/internal/tools/provider"
	builtintoolprovider "github.com/samcharles93/archie-core/internal/tools/provider/builtin"
	mcptoolprovider "github.com/samcharles93/archie-core/internal/tools/provider/mcp"
	"github.com/samcharles93/archie-core/internal/tools/sendfile"
	"github.com/samcharles93/archie-core/internal/tools/webfetch"
)

func (b *server) registerTools() error {
	cfg, log := b.cfgHolder.Get(), b.log
	b.providerRegistry = toolprovider.NewRegistry(b.toolReg)
	b.mcpMu.Lock()
	b.mcpApplied = make(map[string]appliedMCPServer, len(cfg.Tools.MCPServers))
	b.mcpMu.Unlock()
	if workspace := cfg.Chat.Workspace; workspace != "" {
		unrestricted := cfg.Chat.UnrestrictedFilesystem
		if err := b.providerRegistry.Register(builtintoolprovider.New(workspace, unrestricted)); err != nil {
			log.Error("workspace tool provider registration failed", "err", err)
			return err
		}

		log.Info("workspace tools enabled",
			"workspace", workspace, "unrestricted_filesystem", unrestricted)
	} else {
		log.Info("workspace tools disabled (chat.workspace is unset)")
	}
	for _, srv := range cfg.Tools.MCPServers {
		provider, err := b.buildMCPProvider(srv)
		if err != nil {
			log.Warn("mcp tool provider skipped", "name", srv.Name, "err", err)
			continue
		}

		if err := b.providerRegistry.RegisterOptional(provider); err != nil {
			log.Warn("mcp tool provider skipped", "name", srv.Name, "err", err)
			continue
		}
		b.mcpMu.Lock()
		b.mcpApplied[strings.TrimSpace(srv.Name)] = appliedMCPServer{server: srv, id: provider.Manifest().ID}
		b.mcpMu.Unlock()
	}
	if err := b.capabilityHost.Register(b.providerRegistry); err != nil {
		log.Error("tool-provider capability registration failed", "err", err)
		return err
	}
	return nil
}

func (b *server) registerStandaloneTools() {
	cfg, log := b.cfg, b.log

	if catalog, err := skill.CatalogRoots(skill.DefaultRoots(cfg.WorkDir, cfg.SkillsDir)...); err != nil {
		log.Warn("skill catalog load failed", "err", err)
	} else if entry := skill.ActivateTool(cfg.WorkDir, catalog); entry != nil {
		if err := b.toolReg.Register(*entry); err != nil {
			log.Warn("skill_activate registration failed", "err", err)
		} else {
			log.Info("skill catalog registered", "skills", len(catalog))
		}
	}

	if spillDir := cfg.Tools.Policy.SpillDir; spillDir != "" {
		if err := toolLimits(cfg).EnsureSpillDir(); err != nil {
			log.Warn("tool spill directory unavailable; large results will be truncated instead", "err", err)
		} else if ws := cfg.Chat.Workspace; ws != "" && !cfg.Chat.UnrestrictedFilesystem && !isWithin(ws, spillDir) {
			log.Warn("tool spill directory is outside chat.workspace; the model cannot read back what is spilled there",
				"spill_dir", spillDir, "workspace", ws)
		}
	}

	b.registerWebFetchTool(cfg)

	if entry := sendfile.Tool(cfg.Chat.Workspace); entry != nil {
		if err := b.toolReg.Register(*entry); err != nil {
			log.Warn("send_file registration failed", "err", err)
		} else {
			log.Info("file sending enabled", "workspace", cfg.Chat.Workspace)
		}
	} else {
		log.Info("file sending disabled (chat.workspace is unset)")
	}

	b.registerMinimaxTool(cfg, log)
}

func (b *server) registerWebFetchTool(cfg config.Config) {
	log := b.log
	entry := webfetch.Tool(webfetch.Config{
		Enabled:              cfg.Tools.WebFetch.IsEnabled(),
		Timeout:              cfg.Tools.WebFetch.Timeout.Std(),
		MaxBytes:             cfg.Tools.WebFetch.MaxBytes,
		AllowPrivateNetworks: cfg.Tools.WebFetch.AllowPrivateNetworks,
	})
	if entry == nil {
		log.Info("web fetch disabled")
		return
	}
	if err := b.toolReg.Register(*entry); err != nil {
		log.Warn("web_fetch registration failed", "err", err)
		return
	}
	log.Info("web fetch enabled",
		"allow_private_networks", cfg.Tools.WebFetch.AllowPrivateNetworks)
}

func (b *server) registerMinimaxTool(cfg config.Config, log *slog.Logger) {
	if !cfg.Tools.Minimax.IsEnabled() {
		log.Info("minimax video generation disabled")
		return
	}

	apiKey, err := b.secrets.Resolve(cfg.Tools.Minimax.APIKey)
	if err != nil {
		log.Warn("minimax video generation enabled but the API key failed to resolve; tool not registered", "err", err)
		return
	}
	if apiKey == "" {
		log.Warn("minimax video generation enabled but no API key is configured; tool not registered")
		return
	}

	entry := minimax.Tool(minimax.Config{Enabled: true, APIKey: apiKey, BaseURL: cfg.Tools.Minimax.BaseURL})
	if entry == nil {
		return
	}
	if err := b.toolReg.Register(*entry); err != nil {
		log.Warn("generate_video registration failed", "err", err)
		return
	}
	log.Info("minimax video generation enabled")
}

// toolLimits reads the configured per-turn result limits.
func toolLimits(cfg config.Config) modelloop.ToolLimits {
	return modelloop.ToolLimits{
		MaxResultChars: cfg.Tools.Policy.MaxResultChars,
		SpillDir:       cfg.Tools.Policy.SpillDir,
	}
}

// isWithin reports whether target sits inside base.
func isWithin(base, target string) bool {
	absBase, err := filepath.Abs(base)
	if err != nil {
		return false
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absBase, absTarget)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func configuredMCPProvider(server config.MCPServer, workDir string, sampling mcp.SamplingHandler) (toolprovider.Engine, error) {
	name := strings.TrimSpace(server.Name)
	if name == "" {
		return nil, fmt.Errorf("MCP server name is required")
	}
	transportType := strings.ToLower(strings.TrimSpace(server.Transport))
	if transportType == "" {
		transportType = "stdio"
	}

	samplingOption := mcptoolprovider.WithSamplingHandler(sampling)

	switch transportType {
	case "stdio":
		command := strings.TrimSpace(server.Command)
		if command == "" {
			return nil, fmt.Errorf("MCP stdio server %q requires a command", name)
		}
		transport := mcp.NewStdioTransport(mcp.StdioTransportConfig{
			Command: command,
			Args:    append([]string(nil), server.Args...),
			Dir:     server.WorkDir,
			Env:     npmCacheServerEnv(command, workDir),
		})
		return mcptoolprovider.New(name, transport, server.ParallelToolCalls, samplingOption), nil

	case "http", "streamablehttp":
		url := strings.TrimSpace(server.URL)
		if url == "" {
			return nil, fmt.Errorf("MCP http server %q requires a url", name)
		}
		transport := mcp.NewHTTPTransport(mcp.HTTPTransportConfig{
			Endpoint: url,
			Headers:  server.Headers,
		})
		return mcptoolprovider.New(name, transport, server.ParallelToolCalls, samplingOption), nil

	case "sse":
		sseEndpoint := strings.TrimSpace(server.SSEEndpoint)
		if sseEndpoint == "" {
			return nil, fmt.Errorf("MCP sse server %q requires an sse_endpoint", name)
		}
		transport := mcp.NewSSETransport(mcp.SSETransportConfig{
			SSEEndpoint:     sseEndpoint,
			MessageEndpoint: strings.TrimSpace(server.MessageEndpoint),
			Headers:         server.Headers,
		})
		return mcptoolprovider.New(name, transport, server.ParallelToolCalls, samplingOption), nil

	default:
		return nil, fmt.Errorf("MCP transport %q is not supported", transportType)
	}
}

// npmCacheServerEnv returns environment variables that make an
// npx-launched MCP server reuse a persistent package cache across Gateway
// restarts, instead of npm re-resolving and re-downloading the package
// from the registry every time the Gateway starts. Rooted under the
// work dir so it persists regardless of whether the Gateway
// itself runs in an ephemeral container. See mcp.NpmCacheEnv for the
// command-matching and env var details shared with archie-agent's
// per-task equivalent (internal/app/agentworker/mcp_providers.go), which
// points at a mounted cache volume instead of a work-dir subdirectory.
//
// workDir is unconditionally defaulted before registerTools runs
// (internal/infrastructure/configuration's applyGeneralDefaults), but
// filepath.Join silently accepts an empty string and would then put the
// cache in the process's current working directory instead of its
// persistent data directory -- guard it explicitly rather than trust that
// invariant here too.
func npmCacheServerEnv(command, workDir string) []string {
	if workDir == "" {
		return nil
	}
	return mcp.NpmCacheEnv(command, filepath.Join(workDir, "mcp-npm-cache"))
}

func shutdownCapabilityHost(capabilityHost *plugin.Host, log *slog.Logger) func() {
	return func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := capabilityHost.Stop(stopCtx); err != nil {
			log.Error("capability host shutdown", "err", err)
		}
	}
}
