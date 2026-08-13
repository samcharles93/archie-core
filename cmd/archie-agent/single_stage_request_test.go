package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/eventbus"
	"github.com/samcharles93/archie-core/internal/tools"
	toolprovider "github.com/samcharles93/archie-core/internal/tools/provider"
)

type singleStageMessageStub struct {
	data         []byte
	subject      string
	replyAddress string
	replyErr     error
}

func (m *singleStageMessageStub) Data() []byte { return m.data }

func (m *singleStageMessageStub) Subject() string { return m.subject }

func (m *singleStageMessageStub) ReplyAddress() (string, error) {
	return m.replyAddress, m.replyErr
}

func (m *singleStageMessageStub) Ack() error { return nil }

func (m *singleStageMessageStub) Nak() error { return nil }

type singleStageBusStub struct {
	respondErr      error
	respondCalls    int
	replyAddress    string
	responsePayload []byte
}

func (*singleStageBusStub) Fetch(context.Context) (eventbus.Message, error) {
	panic("unexpected Fetch call")
}

func (b *singleStageBusStub) Respond(_ context.Context, replyAddress string, payload []byte) error {
	b.respondCalls++
	b.replyAddress = replyAddress
	b.responsePayload = append([]byte(nil), payload...)
	return b.respondErr
}

func singleStageLogger() (*slog.Logger, *bytes.Buffer) {
	var output bytes.Buffer
	return slog.New(slog.NewTextHandler(&output, nil)), &output
}

func TestHandleRejectsInvalidStageRequest(t *testing.T) {
	msg := &singleStageMessageStub{data: []byte("not json"), subject: "archie.agent.invalid"}
	bus := &singleStageBusStub{}

	err := handle(t.Context(), msg, bus, slog.New(slog.DiscardHandler))
	if err == nil || !strings.HasPrefix(err.Error(), "decode request: ") {
		t.Fatalf("handle() error = %v, want decode request error", err)
	}
	if bus.respondCalls != 0 {
		t.Fatalf("Respond calls = %d, want 0", bus.respondCalls)
	}
}

func TestHandleRejectsStageRequestWithoutReplyAddress(t *testing.T) {
	replyErr := errors.New("reply address unavailable")
	msg := &singleStageMessageStub{
		data:     []byte(`{"providers":{"test":{"class":"openai"}}}`),
		subject:  "archie.agent.no-reply",
		replyErr: replyErr,
	}
	bus := &singleStageBusStub{}

	err := handle(t.Context(), msg, bus, slog.New(slog.DiscardHandler))
	if err == nil || err.Error() != "stage request on archie.agent.no-reply: reply address unavailable" {
		t.Fatalf("handle() error = %v, want wrapped reply-address error", err)
	}
	if !errors.Is(err, replyErr) {
		t.Fatalf("errors.Is(error, replyErr) = false; error = %v", err)
	}
	if bus.respondCalls != 0 {
		t.Fatalf("Respond calls = %d, want 0", bus.respondCalls)
	}
}

func TestHandleReturnsResponsePublicationFailure(t *testing.T) {
	respondErr := errors.New("response unavailable")
	msg := &singleStageMessageStub{
		data:         []byte(`{"providers":{"test":{"class":"openai"}}}`),
		subject:      "archie.agent.respond-error",
		replyAddress: "_INBOX.respond-error",
	}
	bus := &singleStageBusStub{respondErr: respondErr}
	log, output := singleStageLogger()

	err := handle(t.Context(), msg, bus, log)
	if err == nil || err.Error() != "publish response: response unavailable" {
		t.Fatalf("handle() error = %v, want wrapped response publication error", err)
	}
	if !errors.Is(err, respondErr) {
		t.Fatalf("errors.Is(error, respondErr) = false; error = %v", err)
	}
	if bus.respondCalls != 1 || bus.replyAddress != msg.replyAddress || len(bus.responsePayload) == 0 {
		t.Fatalf("Respond = (%d calls, %q, %q), want (1, %q, non-empty)", bus.respondCalls, bus.replyAddress, bus.responsePayload, msg.replyAddress)
	}
	if strings.Contains(output.String(), "stage complete") {
		t.Fatalf("log output %q unexpectedly contains completion message", output.String())
	}
}

func TestHandleSingleStageRequestReturnsHandlerFailure(t *testing.T) {
	handleErr := errors.New("agent execution failed")
	msg := &singleStageMessageStub{
		data:         []byte(`{"task_id":17,"attempt":2,"stage":"implement","workflow":"tdd"}`),
		subject:      "archie.agent.handler-error",
		replyAddress: "_INBOX.handler-error",
	}
	bus := &singleStageBusStub{}
	encodeCalled := false
	dependencies := singleStageRequestDependencies{
		handleMessage: func(_ context.Context, request agentexec.AgentRequestMessage, _ *slog.Logger, factory agentexec.RunnerFactory) (*agentexec.AgentResponseEnvelope, error) {
			if request.TaskID != 17 || request.Attempt != 2 || request.Stage != "implement" || request.Workflow != "tdd" {
				t.Fatalf("decoded request = %#v, want task/stage identity from payload", request)
			}
			if runner := factory(request.Providers, slog.New(slog.DiscardHandler)); runner == nil {
				t.Fatal("runner factory returned nil")
			}
			return nil, handleErr
		},
		encodeResponse: func(any) ([]byte, error) {
			encodeCalled = true
			return nil, nil
		},
	}

	err := handleSingleStageRequest(t.Context(), msg, bus, slog.New(slog.DiscardHandler), dependencies)
	if err == nil || err.Error() != "handle: agent execution failed" {
		t.Fatalf("handleSingleStageRequest() error = %v, want wrapped handler error", err)
	}
	if !errors.Is(err, handleErr) {
		t.Fatalf("errors.Is(error, handleErr) = false; error = %v", err)
	}
	if encodeCalled || bus.respondCalls != 0 {
		t.Fatalf("(encode called, Respond calls) = (%v, %d), want (false, 0)", encodeCalled, bus.respondCalls)
	}
}

func TestHandleSingleStageRequestReturnsResponseEncodingFailure(t *testing.T) {
	encodeErr := errors.New("response cannot be encoded")
	response := &agentexec.AgentResponseEnvelope{Result: agentexec.Result{Status: agentexec.StatusPassed}}
	msg := &singleStageMessageStub{
		data:         []byte(`{"task_id":23,"stage":"review"}`),
		subject:      "archie.agent.encode-error",
		replyAddress: "_INBOX.encode-error",
	}
	bus := &singleStageBusStub{}
	dependencies := singleStageRequestDependencies{
		handleMessage: func(context.Context, agentexec.AgentRequestMessage, *slog.Logger, agentexec.RunnerFactory) (*agentexec.AgentResponseEnvelope, error) {
			return response, nil
		},
		encodeResponse: func(value any) ([]byte, error) {
			if value != response {
				t.Fatalf("encoded response = %#v, want handler response %#v", value, response)
			}
			return nil, encodeErr
		},
	}

	err := handleSingleStageRequest(t.Context(), msg, bus, slog.New(slog.DiscardHandler), dependencies)
	if err == nil || err.Error() != "encode response: response cannot be encoded" {
		t.Fatalf("handleSingleStageRequest() error = %v, want wrapped encoding error", err)
	}
	if !errors.Is(err, encodeErr) {
		t.Fatalf("errors.Is(error, encodeErr) = false; error = %v", err)
	}
	if bus.respondCalls != 0 {
		t.Fatalf("Respond calls = %d, want 0", bus.respondCalls)
	}
}

func TestHandleSingleStageRequestAttachesAndCleansUpMCPProviders(t *testing.T) {
	response := &agentexec.AgentResponseEnvelope{Result: agentexec.Result{Status: agentexec.StatusPassed}}
	msg := &singleStageMessageStub{
		data:         []byte(`{"task_id":29,"stage":"implement","mcp_servers":[{"name":"available"}]}`),
		subject:      "archie.agent.mcp-success",
		replyAddress: "_INBOX.mcp-success",
	}
	bus := &singleStageBusStub{}
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
				t.Fatalf("MCP servers = %#v, want available server", servers)
			}
			return set, nil
		},
		newRunner: func(_ map[string]agentexec.Provider, _ *slog.Logger, registries ...*tools.Registry) agentexec.Runner {
			factoryCalls++
			if len(registries) != 1 || registries[0] != registry {
				t.Fatalf("runner registries = %#v, want MCP registry", registries)
			}
			return panicRunner{t: t}
		},
		handleMessage: func(_ context.Context, request agentexec.AgentRequestMessage, _ *slog.Logger, factory agentexec.RunnerFactory) (*agentexec.AgentResponseEnvelope, error) {
			if runner := factory(request.Providers, slog.New(slog.DiscardHandler)); runner == nil {
				t.Fatal("runner factory returned nil")
			}
			return response, nil
		},
		encodeResponse: func(any) ([]byte, error) { return []byte(`{"result":{"status":"passed"}}`), nil },
	}

	if err := handleSingleStageRequest(t.Context(), msg, bus, slog.New(slog.DiscardHandler), dependencies); err != nil {
		t.Fatalf("handleSingleStageRequest() error = %v", err)
	}
	if startCalls != 1 || factoryCalls != 1 {
		t.Fatalf("(provider starts, runner factories) = (%d, %d), want (1, 1)", startCalls, factoryCalls)
	}
	if provider.stops != 1 {
		t.Fatalf("provider cleanup stops = %d, want 1", provider.stops)
	}
	if bus.respondCalls != 1 {
		t.Fatalf("Respond calls = %d, want 1", bus.respondCalls)
	}
}

func TestHandleSingleStageRequestLogsLifecycleAndPartialMCPWarning(t *testing.T) {
	response := &agentexec.AgentResponseEnvelope{Result: agentexec.Result{Status: agentexec.StatusPassed}}
	msg := &singleStageMessageStub{
		data:         []byte(`{"task_id":31,"attempt":4,"stage":"test","workflow":"tdd","mcp_servers":[{"name":"broken","transport":"stdio"}]}`),
		subject:      "archie.agent.success",
		replyAddress: "_INBOX.success",
	}
	bus := &singleStageBusStub{}
	encoded := []byte(`{"result":{"status":"passed"}}`)
	dependencies := singleStageRequestDependencies{
		handleMessage: func(context.Context, agentexec.AgentRequestMessage, *slog.Logger, agentexec.RunnerFactory) (*agentexec.AgentResponseEnvelope, error) {
			return response, nil
		},
		encodeResponse: func(value any) ([]byte, error) {
			if value != response {
				t.Fatalf("encoded response = %#v, want handler response %#v", value, response)
			}
			return encoded, nil
		},
	}
	log, output := singleStageLogger()

	if err := handleSingleStageRequest(t.Context(), msg, bus, log, dependencies); err != nil {
		t.Fatalf("handleSingleStageRequest() error = %v", err)
	}
	if bus.respondCalls != 1 || bus.replyAddress != msg.replyAddress || string(bus.responsePayload) != string(encoded) {
		t.Fatalf("Respond = (%d calls, %q, %q), want (1, %q, %q)", bus.respondCalls, bus.replyAddress, bus.responsePayload, msg.replyAddress, encoded)
	}
	for _, expected := range []string{
		"processing stage",
		"task=31",
		"attempt=4",
		"stage=test",
		"workflow=tdd",
		"reply_to=_INBOX.success",
		"mcp providers had errors, continuing with available tools",
		`err="MCP stdio server \"broken\" requires a command"`,
		"stage complete",
		"status=passed",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("log output %q does not contain %q", output.String(), expected)
		}
	}
}

func TestMainContainsNoSingleStageRequestHandler(t *testing.T) {
	data, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "func handle(") {
		t.Fatal("main.go still contains single-stage request orchestration")
	}
}
