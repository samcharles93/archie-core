package agentworker

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/tools"
)

type singleStageRequestDependencies struct {
	handleMessage  func(context.Context, agentexec.AgentRequestMessage, *slog.Logger, agentexec.RunnerFactory) (*agentexec.AgentResponseEnvelope, error)
	startProviders func(context.Context, []config.MCPServer, *slog.Logger) (*mcpProviderSet, error)
	newRunner      func(map[string]agentexec.Provider, *slog.Logger, ...*tools.Registry) agentexec.Runner
}

func productionSingleStageDependencies() singleStageRequestDependencies {
	return singleStageRequestDependencies{
		handleMessage:  agentexec.HandleMessage,
		startProviders: startMCPProviders,
		newRunner:      newSingleStageRunner,
	}
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
	message agentexec.StageMessage,
	log *slog.Logger,
	dependencies singleStageRequestDependencies,
) error {
	request, err := message.Request()
	if err != nil {
		return err
	}

	attributes := []any{
		"task", request.TaskID,
		"attempt", request.Attempt,
		"stage", request.Stage,
		"workflow", request.Workflow,
	}
	attributes = append(attributes, message.LogAttributes()...)
	log.Info("processing stage", attributes...)

	startProviders := dependencies.startProviders
	if startProviders == nil {
		startProviders = startMCPProviders
	}
	newRunner := dependencies.newRunner
	if newRunner == nil {
		newRunner = newSingleStageRunner
	}

	mcpSet, mcpErr := startProviders(ctx, request.MCPServers, log)
	if mcpSet != nil {
		defer mcpSet.cleanup(ctx, log)
	}
	if mcpErr != nil {
		log.Warn("mcp providers had errors, continuing with available tools", "err", mcpErr)
	}

	factory := func(providers map[string]agentexec.Provider, log *slog.Logger) agentexec.Runner {
		var registries []*tools.Registry
		if mcpSet != nil {
			registries = append(registries, mcpSet.registry)
		}
		return newRunner(providers, log, registries...)
	}

	response, err := dependencies.handleMessage(ctx, request, log, factory)
	if err != nil {
		return fmt.Errorf("handle: %w", err)
	}
	if err := message.Respond(ctx, response); err != nil {
		return err
	}

	log.Info("stage complete",
		"task", request.TaskID,
		"stage", request.Stage,
		"status", response.Result.Status,
	)
	return nil
}
