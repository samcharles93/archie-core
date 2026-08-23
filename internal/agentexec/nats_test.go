package agentexec

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/nats-io/nats-server/v2/server"
	natssrv "github.com/nats-io/nats-server/v2/test"

	"github.com/samcharles93/archie-core/internal/domain/workintake"
	"github.com/samcharles93/archie-core/internal/eventbus"
	arnats "github.com/samcharles93/archie-core/internal/infrastructure/eventbus/nats"
)

// ── helpers ──────────────────────────────────────────────────────────

type mockRunner struct{}

func (m *mockRunner) Run(_ context.Context, _ string, req Request, _ ToolCallReporter) (Result, error) {
	return Result{
		Version:    ProtocolVersion,
		TaskID:     req.TaskID,
		Attempt:    req.Attempt,
		Stage:      req.Stage,
		Status:     StatusPassed,
		Summary:    "mock:" + req.Mission,
		TokensUsed: 10,
		Iterations: 1,
	}, nil
}

var mockFactory = func(providers map[string]Provider, log *slog.Logger) Runner {
	return &mockRunner{}
}

// ── regression: Gap 1  --  per-task agent ──────────────────────────────

func TestHandleMessageReadsWorkflowField(t *testing.T) {
	// Gap 1: archie-agent is per-stage, not per-task.
	// PRD section 1: when the daemon sends a task-level message with
	// Workflow="implement" and Stages populated, the agent must run all
	// stages as a batch. Single-stage mode (Request only) is the
	// backward-compatible fallback.

	ws := t.TempDir()
	providers := map[string]Provider{"test": {Class: "openai", APIKeyEnv: "FAKE"}}

	// Single-stage mode: uses Request, no Workflow, no Stages.
	singleMsg := AgentRequestMessage{
		TaskID:    1,
		Attempt:   1,
		Stage:     "plan",
		Workspace: ws,
		Request: Request{
			Version: ProtocolVersion, TaskID: 1, Attempt: 1,
			Stage: "plan", Model: "test/model", Mission: "single",
			Budget: Budget{MaxSteps: 1},
		},
		Providers: providers,
	}
	mockFactory := func(providers map[string]Provider, log *slog.Logger) Runner {
		return &mockRunner{}
	}
	singleResp, err := HandleMessage(context.Background(), singleMsg, slog.New(slog.DiscardHandler), mockFactory)
	if err != nil {
		t.Fatal(err)
	}
	if singleResp.TaskCompleted {
		t.Error("single-stage mode set TaskCompleted=true  --  should be false")
	}

	// Per-task mode: Workflow set, Stages populated with two stages.
	multiMsg := AgentRequestMessage{
		TaskID:    2,
		Attempt:   1,
		Stage:     "plan",
		Workflow:  "implement",
		Workspace: ws,
		Request: Request{
			Version: ProtocolVersion, TaskID: 2, Attempt: 1,
			Stage: "plan", Model: "test/model", Mission: "multi-1",
			Budget: Budget{MaxSteps: 1},
		},
		Stages: []Request{
			{
				Version: ProtocolVersion, TaskID: 2, Attempt: 1,
				Stage: "plan", Model: "test/model", Mission: "multi-plan",
				Budget: Budget{MaxSteps: 1},
			},
			{
				Version: ProtocolVersion, TaskID: 2, Attempt: 1,
				Stage: "build", Model: "test/model", Mission: "multi-build",
				Budget: Budget{MaxSteps: 1},
			},
		},
		Providers: providers,
	}
	multiResp, err := HandleMessage(context.Background(), multiMsg, slog.New(slog.DiscardHandler), mockFactory)
	if err != nil {
		t.Fatal(err)
	}
	if !multiResp.TaskCompleted {
		t.Error("Gap 1: per-task mode did not set TaskCompleted=true. " +
			"The agent must signal task completion when all stages in a " +
			"workflow batch have been processed.")
	}
	// Batch results must aggregate from both stages.
	if multiResp.Result.TokensUsed == singleResp.Result.TokensUsed &&
		multiResp.Result.Iterations == singleResp.Result.Iterations {
		t.Error("Gap 1: per-task mode produced same token/iteration counts " +
			"as single-stage mode. Multi-stage batch must accumulate " +
			"results from all stages. See PRD section 1.")
	}
}

// ── regression: Gap 3  --  response/system channel split ───────────────

func TestHandleMessageRoutesSystemMessagesSeparately(t *testing.T) {
	// Gap 3: no response/system channel split.
	// PRD section 2: system messages go to archie.agent.<id>.system and
	// are never forwarded to humans. Response messages go to
	// archie.agent.<id>.response for daemon review before human delivery.
	// Currently HandleMessage always returns Channel="response"  --  there
	// is no code path that produces a system-channel response.

	msg := AgentRequestMessage{
		TaskID:    1,
		Attempt:   1,
		Stage:     "plan",
		Workspace: t.TempDir(),
		Request: Request{
			Version: ProtocolVersion,
			TaskID:  1,
			Attempt: 1,
			Stage:   "plan",
			Model:   "test/model",
			Mission: "test",
			Budget:  Budget{MaxSteps: 1},
		},
		Providers: map[string]Provider{"test": {Class: "openai", APIKeyEnv: "FAKE"}},
	}

	// Normal stage request  --  should route to response channel.
	resp, err := HandleMessage(context.Background(), msg, slog.New(slog.DiscardHandler), mockFactory)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Channel != "response" {
		t.Errorf("normal stage request: Channel = %q, want \"response\"", resp.Channel)
	}

	// System message (health check, log dump request)  --  should route to
	// system channel. Currently HandleMessage always returns "response".
	// The daemon sets Channel on the request to indicate message type.
	msg.Channel = "system"
	sysResp, err := HandleMessage(context.Background(), msg, slog.New(slog.DiscardHandler), mockFactory)
	if err != nil {
		t.Fatal(err)
	}
	if sysResp.Channel != "system" {
		t.Error("Gap 3: HandleMessage ignores the input Channel field. " +
			"When the daemon sends a system message (Channel=\"system\"), " +
			"the response must be routed to archie.agent.<id>.system, " +
			"not to the reply inbox. Currently all responses go to the " +
			"same inbox regardless of channel. See PRD section 2.")
	}

	// Verify the subject functions exist and are distinct.
	respSubj := SubjectForResponse(42)
	sysSubj := SubjectForSystem(42)
	if respSubj == sysSubj || respSubj == "" || sysSubj == "" {
		t.Error("Gap 3: subject functions broken  --  verify SubjectForAgentResponse " +
			"and SubjectForAgentSystem return distinct, non-empty subjects.")
	}
}

// ── existing tests ───────────────────────────────────────────────────

func startEmbeddedNATS(t *testing.T) *server.Server {
	t.Helper()
	srv := natssrv.RunRandClientPortServer()
	t.Cleanup(srv.Shutdown)
	if err := srv.EnableJetStream(&server.JetStreamConfig{StoreDir: t.TempDir()}); err != nil {
		t.Fatalf("enable jetstream: %v", err)
	}
	return srv
}

func startHandler(t *testing.T, client *arnats.Client) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	// Drain the client's own consumer rather than subscribing a second one:
	// the stream uses work-queue retention, so two consumers with
	// overlapping filter subjects conflict. This mirrors archie-agent.
	go func() {
		for ctx.Err() == nil {
			msg, err := client.Fetch(ctx)
			if err != nil {
				continue
			}
			if err := replyToStage(ctx, client, msg); err != nil {
				t.Errorf("stage handler: %v", err)
				_ = msg.Nak()
				continue
			}
			_ = msg.Ack()
		}
	}()
}

// replyToStage answers one stage request with a canned passing result.
func replyToStage(ctx context.Context, client *arnats.Client, msg eventbus.Message) error {
	var req AgentRequestMessage
	if err := json.Unmarshal(msg.Data(), &req); err != nil {
		return err
	}
	replyTo, err := msg.ReplyAddress()
	if err != nil {
		return err
	}
	data, err := json.Marshal(AgentResponseEnvelope{
		Version: req.Request.Version,
		Result: Result{
			Version:    ProtocolVersion,
			TaskID:     req.TaskID,
			Attempt:    req.Attempt,
			Stage:      req.Stage,
			Status:     StatusPassed,
			Summary:    "handler-reply",
			TokensUsed: 42,
		},
	})
	if err != nil {
		return err
	}
	return client.Respond(ctx, replyTo, data)
}

func TestNATSRunnerRoundTrip(t *testing.T) {
	srv := startEmbeddedNATS(t)
	url := srv.ClientURL()

	ctx := context.Background()
	client, err := arnats.Connect(ctx, arnats.Config{URL: url, Subjects: []string{workintake.SubjectTaskWildcard, SubjectAgentWildcard}, FilterSubject: SubjectAgentWildcard}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	startHandler(t, client)

	runner := &NATSRunner{
		Bus:       client,
		Providers: map[string]Provider{"openai": {Class: "openai", APIKeyEnv: "TEST_KEY"}},
		Log:       slog.New(slog.DiscardHandler),
	}

	req := Request{
		Version: ProtocolVersion,
		TaskID:  42,
		Attempt: 1,
		Stage:   "test",
		Model:   "openai/gpt",
		Mission: "test mission",
		Budget:  Budget{MaxSteps: 5},
	}

	result, err := runner.Run(ctx, "/tmp/test-workspace", req, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusPassed {
		t.Fatalf("expected passed, got %s", result.Status)
	}
	if result.Summary != "handler-reply" {
		t.Fatalf("expected 'handler-reply', got %q", result.Summary)
	}
	if result.TokensUsed != 42 {
		t.Fatalf("expected 42 tokens, got %d", result.TokensUsed)
	}
}
