package agentworker

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/tools"
	toolprovider "github.com/samcharles93/archie-core/internal/tools/provider"
)

func singleStageLogger() (*slog.Logger, *bytes.Buffer) {
	var output bytes.Buffer
	return slog.New(slog.NewTextHandler(&output, nil)), &output
}

func TestHandleSingleStageRequestReturnsTypedBoundaryAndExecutionFailures(t *testing.T) {
	requestErr := errors.New("decode request: invalid")
	handleErr := errors.New("agent execution failed")
	respondErr := errors.New("publish response: unavailable")
	tests := []struct {
		name       string
		message    *stageMessageStub
		handleErr  error
		wantErr    error
		wantHandle int
		wantReply  int
	}{
		{name: "typed boundary", message: &stageMessageStub{requestErr: requestErr}, wantErr: requestErr},
		{name: "execution", message: &stageMessageStub{request: agentexec.AgentRequestMessage{TaskID: 17}}, handleErr: handleErr, wantErr: handleErr, wantHandle: 1},
		{name: "response", message: &stageMessageStub{request: agentexec.AgentRequestMessage{TaskID: 23}, respondErr: respondErr}, wantErr: respondErr, wantHandle: 1, wantReply: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handleCalls := 0
			dependencies := singleStageRequestDependencies{
				handleMessage: func(context.Context, agentexec.AgentRequestMessage, *slog.Logger, agentexec.RunnerFactory) (*agentexec.AgentResponseEnvelope, error) {
					handleCalls++
					return &agentexec.AgentResponseEnvelope{Result: agentexec.Result{Status: agentexec.StatusPassed}}, test.handleErr
				},
			}
			err := handleSingleStageRequest(t.Context(), test.message, slog.New(slog.DiscardHandler), dependencies)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("error = %v, want %v", err, test.wantErr)
			}
			if handleCalls != test.wantHandle || test.message.respondCalls != test.wantReply {
				t.Fatalf("handle/respond calls = (%d,%d), want (%d,%d)", handleCalls, test.message.respondCalls, test.wantHandle, test.wantReply)
			}
		})
	}
}

func TestHandleSingleStageRequestAttachesAndCleansUpMCPProviders(t *testing.T) {
	message := &stageMessageStub{request: agentexec.AgentRequestMessage{
		TaskID:     29,
		Stage:      "implement",
		MCPServers: []config.MCPServer{{Name: "available"}},
	}}
	registry := tools.NewRegistry()
	if err := registry.Register(testTool("available")); err != nil {
		t.Fatal(err)
	}
	provider := &fakeMCPEngine{id: "available"}
	set := &mcpProviderSet{providers: []toolprovider.Engine{provider}, registry: registry}
	startCalls := 0
	factoryCalls := 0
	dependencies := singleStageRequestDependencies{
		startProviders: func(_ context.Context, servers []config.MCPServer, _ *slog.Logger) (*mcpProviderSet, error) {
			startCalls++
			if len(servers) != 1 || servers[0].Name != "available" {
				t.Fatalf("MCP servers = %#v", servers)
			}
			return set, nil
		},
		newRunner: func(_ map[string]agentexec.Provider, _ *slog.Logger, registries ...*tools.Registry) agentexec.Runner {
			factoryCalls++
			if len(registries) != 1 || registries[0] != registry {
				t.Fatalf("runner registries = %#v", registries)
			}
			return panicRunner{t: t}
		},
		handleMessage: func(_ context.Context, request agentexec.AgentRequestMessage, _ *slog.Logger, factory agentexec.RunnerFactory) (*agentexec.AgentResponseEnvelope, error) {
			if runner := factory(request.Providers, slog.New(slog.DiscardHandler)); runner == nil {
				t.Fatal("runner factory returned nil")
			}
			return &agentexec.AgentResponseEnvelope{Result: agentexec.Result{Status: agentexec.StatusPassed}}, nil
		},
	}
	if err := handleSingleStageRequest(t.Context(), message, slog.New(slog.DiscardHandler), dependencies); err != nil {
		t.Fatal(err)
	}
	if startCalls != 1 || factoryCalls != 1 || provider.stops != 1 || message.respondCalls != 1 {
		t.Fatalf("start/factory/stops/respond = (%d,%d,%d,%d), want all 1", startCalls, factoryCalls, provider.stops, message.respondCalls)
	}
}

func TestHandleSingleStageRequestPreservesLifecycleLogs(t *testing.T) {
	message := &stageMessageStub{request: agentexec.AgentRequestMessage{
		TaskID: 31, Attempt: 4, Stage: "test", Workflow: "tdd",
		MCPServers: []config.MCPServer{{Name: "broken", Transport: "stdio"}},
	}}
	dependencies := productionSingleStageDependencies()
	dependencies.handleMessage = func(context.Context, agentexec.AgentRequestMessage, *slog.Logger, agentexec.RunnerFactory) (*agentexec.AgentResponseEnvelope, error) {
		return &agentexec.AgentResponseEnvelope{Result: agentexec.Result{Status: agentexec.StatusPassed}}, nil
	}
	log, output := singleStageLogger()
	if err := handleSingleStageRequest(t.Context(), message, log, dependencies); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"processing stage", "task=31", "attempt=4", "stage=test", "workflow=tdd", "reply_to=_INBOX.test",
		"mcp providers had errors, continuing with available tools", "stage complete", "status=passed",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("log output %q does not contain %q", output.String(), expected)
		}
	}
}
