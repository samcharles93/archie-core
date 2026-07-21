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

// ── regression tests for PRD gaps ────────────────────────────────────
// These must FAIL until the gaps are closed.

func TestAgentRequestMessageHasWorkflowField(t *testing.T) {
	// Gap 1: archie-agent is per-stage, not per-task.
	// PRD section 1 says the agent runs full multi-stage workflows.
	// AgentRequestMessage must carry the workflow name.
	msg := AgentRequestMessage{Workflow: "implement"}
	if msg.Workflow == "" {
		t.Error("Gap 1: AgentRequestMessage.Workflow is empty. " +
			"Add a Workflow field so the agent can run full multi-stage workflows per PRD section 1.")
	}
}

func TestAgentResponseEnvelopeHasChannelField(t *testing.T) {
	// Gap 3: no response/system channel split.
	// PRD section 2: archie.agent.<id>.response vs .system.
	// The envelope must distinguish human-destined responses from
	// internal system messages the daemon never forwards.
	env := AgentResponseEnvelope{Channel: "system"}
	if env.Channel == "" {
		t.Error("Gap 3: AgentResponseEnvelope has no Channel field. " +
			"Add a Channel field (\"response\"|\"system\") per PRD section 2.")
	}
}

func TestSubjectForAgentResponseExists(t *testing.T) {
	// Gap 3: subject functions for response/system channels.
	subj := arnats.SubjectForAgentResponse(42)
	if subj == "" {
		t.Error("Gap 3: SubjectForAgentResponse not implemented. " +
			"Add SubjectForAgentResponse(taskID) returning archie.agent.<id>.response per PRD section 2.")
	}
}

func TestSubjectForAgentSystemExists(t *testing.T) {
	subj := arnats.SubjectForAgentSystem(42)
	if subj == "" {
		t.Error("Gap 3: SubjectForAgentSystem not implemented. " +
			"Add SubjectForAgentSystem(taskID) returning archie.agent.<id>.system per PRD section 2.")
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
