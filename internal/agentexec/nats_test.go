package agentexec

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	natssrv "github.com/nats-io/nats-server/v2/test"
	natsio "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	arnats "github.com/samcharles93/archie-core/internal/nats"
)

// ── helpers ──────────────────────────────────────────────────────────

type mockRunner struct{}

func (m *mockRunner) Run(_ context.Context, _ string, req Request) (Result, error) {
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

// ── regression: Gap 1 — per-task agent ──────────────────────────────

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
			Budget: Budget{MaxSteps: 1, MaxTokens: 10},
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
		t.Error("single-stage mode set TaskCompleted=true — should be false")
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
			Budget: Budget{MaxSteps: 1, MaxTokens: 10},
		},
		Stages: []Request{
			{
				Version: ProtocolVersion, TaskID: 2, Attempt: 1,
				Stage: "plan", Model: "test/model", Mission: "multi-plan",
				Budget: Budget{MaxSteps: 1, MaxTokens: 10},
			},
			{
				Version: ProtocolVersion, TaskID: 2, Attempt: 1,
				Stage: "build", Model: "test/model", Mission: "multi-build",
				Budget: Budget{MaxSteps: 1, MaxTokens: 10},
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

// ── regression: Gap 3 — response/system channel split ───────────────

func TestHandleMessageRoutesSystemMessagesSeparately(t *testing.T) {
	// Gap 3: no response/system channel split.
	// PRD section 2: system messages go to archie.agent.<id>.system and
	// are never forwarded to humans. Response messages go to
	// archie.agent.<id>.response for daemon review before human delivery.
	// Currently HandleMessage always returns Channel="response" — there
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
			Budget:  Budget{MaxSteps: 1, MaxTokens: 10},
		},
		Providers: map[string]Provider{"test": {Class: "openai", APIKeyEnv: "FAKE"}},
	}

	// Normal stage request — should route to response channel.
	resp, err := HandleMessage(context.Background(), msg, slog.New(slog.DiscardHandler), mockFactory)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Channel != "response" {
		t.Errorf("normal stage request: Channel = %q, want \"response\"", resp.Channel)
	}

	// System message (health check, log dump request) — should route to
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
	respSubj := arnats.SubjectForAgentResponse(42)
	sysSubj := arnats.SubjectForAgentSystem(42)
	if respSubj == sysSubj || respSubj == "" || sysSubj == "" {
		t.Error("Gap 3: subject functions broken — verify SubjectForAgentResponse " +
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

func startHandler(t *testing.T, nc *natsio.Conn) {
	t.Helper()
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := js.CreateOrUpdateStream(context.Background(), jetstream.StreamConfig{
		Name:      "ARCHIE_TASKS",
		Subjects:  []string{"archie.task.>", "archie.agent.>"},
		Storage:   jetstream.FileStorage,
		Retention: jetstream.WorkQueuePolicy,
	})
	if err != nil {
		t.Fatal(err)
	}
	cons, err := stream.CreateOrUpdateConsumer(context.Background(), jetstream.ConsumerConfig{
		Name:          "test-agent",
		FilterSubject: arnats.SubjectAgentWildcard,
		AckPolicy:     jetstream.AckExplicitPolicy,
		MaxDeliver:    1,
	})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			batch, err := cons.Fetch(1, jetstream.FetchMaxWait(5*time.Second))
			if err != nil {
				return
			}
			for msg := range batch.Messages() {
				if msg == nil {
					continue
				}
				var req AgentRequestMessage
				json.Unmarshal(msg.Data(), &req)
				replyTo := msg.Headers().Get(arnats.ReplyHeader)
				resp := AgentResponseEnvelope{
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
				}
				data, _ := json.Marshal(resp)
				nc.Publish(replyTo, data)
				msg.Ack()
			}
		}
	}()
}

func TestNATSRunnerRoundTrip(t *testing.T) {
	srv := startEmbeddedNATS(t)
	url := srv.ClientURL()

	ctx := context.Background()
	client, err := arnats.Connect(ctx, url, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	startHandler(t, client.Conn())

	runner := &NATSRunner{
		Nats:      client,
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
		Budget:  Budget{MaxSteps: 5, MaxTokens: 100},
	}

	result, err := runner.Run(ctx, "/tmp/test-workspace", req)
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
