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

func startEmbeddedNATS(t *testing.T) *server.Server {
	t.Helper()
	// RunRandClientPortServer doesn't enable JetStream. Enable it separately.
	srv := natssrv.RunRandClientPortServer()
	t.Cleanup(srv.Shutdown)
	if err := srv.EnableJetStream(&server.JetStreamConfig{StoreDir: t.TempDir()}); err != nil {
		t.Fatalf("enable jetstream: %v", err)
	}
	return srv
}

// handler subscribes to archie.agent.> and replies with a canned result.
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
			batch, err := cons.Fetch(1, jetstream.FetchMaxWait(5 * time.Second))
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

	// Start the handler that simulates archie-agent.
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
