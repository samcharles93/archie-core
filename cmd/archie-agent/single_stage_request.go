package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/eventbus"
	"github.com/samcharles93/archie-core/internal/tools"
)

type singleStageRequestDependencies struct {
	handleMessage  func(context.Context, agentexec.AgentRequestMessage, *slog.Logger, agentexec.RunnerFactory) (*agentexec.AgentResponseEnvelope, error)
	encodeResponse func(any) ([]byte, error)
	startProviders func(context.Context, []config.MCPServer, *slog.Logger) (*mcpProviderSet, error)
	newRunner      func(map[string]agentexec.Provider, *slog.Logger, ...*tools.Registry) agentexec.Runner
}

func handle(ctx context.Context, msg eventbus.Message, bus stageBus, log *slog.Logger) error {
	return handleSingleStageRequest(ctx, msg, bus, log, singleStageRequestDependencies{
		handleMessage:  agentexec.HandleMessage,
		encodeResponse: json.Marshal,
		startProviders: startMCPProviders,
		newRunner:      newSingleStageRunner,
	})
}

func newSingleStageRunner(
	providers map[string]agentexec.Provider,
	log *slog.Logger,
	registries ...*tools.Registry,
) agentexec.Runner {
	return agentexec.NewInProcessRunner(agentexec.NewRuntime(providers), log, registries...)
}

func handleSingleStageRequest(
	ctx context.Context,
	msg eventbus.Message,
	bus stageBus,
	log *slog.Logger,
	dependencies singleStageRequestDependencies,
) error {
	var req agentexec.AgentRequestMessage
	if err := json.Unmarshal(msg.Data(), &req); err != nil {
		return fmt.Errorf("decode request: %w", err)
	}

	replyTo, err := msg.ReplyAddress()
	if err != nil {
		return fmt.Errorf("stage request on %s: %w", msg.Subject(), err)
	}

	log.Info("processing stage",
		"task", req.TaskID,
		"attempt", req.Attempt,
		"stage", req.Stage,
		"workflow", req.Workflow,
		"reply_to", replyTo,
	)

	startProviders := dependencies.startProviders
	if startProviders == nil {
		startProviders = startMCPProviders
	}
	newRunner := dependencies.newRunner
	if newRunner == nil {
		newRunner = newSingleStageRunner
	}

	// Start MCP providers and build a local tool registry.
	mcpSet, mcpErr := startProviders(ctx, req.MCPServers, log)
	if mcpSet != nil {
		defer mcpSet.cleanup(ctx, log)
	}
	if mcpErr != nil {
		log.Warn("mcp providers had errors, continuing with available tools", "err", mcpErr)
	}

	// Build a runner factory that includes MCP-discovered tools.
	factory := func(providers map[string]agentexec.Provider, log *slog.Logger) agentexec.Runner {
		var registries []*tools.Registry
		if mcpSet != nil {
			registries = append(registries, mcpSet.registry)
		}
		return newRunner(providers, log, registries...)
	}

	resp, err := dependencies.handleMessage(ctx, req, log, factory)
	if err != nil {
		return fmt.Errorf("handle: %w", err)
	}

	data, err := dependencies.encodeResponse(resp)
	if err != nil {
		return fmt.Errorf("encode response: %w", err)
	}
	if err := bus.Respond(ctx, replyTo, data); err != nil {
		return fmt.Errorf("publish response: %w", err)
	}

	log.Info("stage complete",
		"task", req.TaskID,
		"stage", req.Stage,
		"status", resp.Result.Status,
	)
	return nil
}
