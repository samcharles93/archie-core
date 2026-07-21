// Command archie-agent is a long-running NATS-connected worker that executes
// autonomous agent stages. It subscribes to archie.agent.> on the ARCHIE_TASKS
// JetStream stream, runs the agent loop for each received request, and publishes
// the result back to the daemon's reply inbox.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/samcharles93/archie-core/internal/agentexec"
	arnats "github.com/samcharles93/archie-core/internal/nats"
)

const (
	streamName   = "ARCHIE_TASKS"
	consumerName = "archie-agent"
	dedupWindow  = 2 * time.Minute
	pollTimeout  = 5 * time.Second
	ackWait      = 30 * time.Minute
)

func main() {
	os.Exit(run())
}

func run() int {
	natsURL := flag.String("nats-url", "", "NATS server URL (required)")
	consumer := flag.String("consumer", consumerName, "JetStream consumer name")
	flag.Parse()

	if *natsURL == "" {
		fmt.Fprintln(os.Stderr, "error: -nats-url is required")
		flag.Usage()
		return 1
	}

	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Connect to NATS.
	nc, err := nats.Connect(*natsURL)
	if err != nil {
		log.Error("nats connect failed", "err", err)
		return 1
	}
	defer nc.Close()
	log.Info("nats connected", "url", *natsURL)

	// JetStream setup.
	js, err := jetstream.New(nc)
	if err != nil {
		log.Error("jetstream init failed", "err", err)
		return 1
	}

	stream, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:      streamName,
		Subjects:  []string{"archie.task.>", "archie.agent.>"},
		Storage:   jetstream.FileStorage,
		Retention: jetstream.WorkQueuePolicy,
		Duplicates: dedupWindow,
	})
	if err != nil {
		log.Error("stream setup failed", "err", err)
		return 1
	}

	cons, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Name:             *consumer,
		Durable:          *consumer,
		FilterSubject:    arnats.SubjectAgentWildcard,
		AckPolicy:        jetstream.AckExplicitPolicy,
		MaxDeliver:       3,
		AckWait:          ackWait,
		InactiveThreshold: 24 * time.Hour,
	})
	if err != nil {
		log.Error("consumer setup failed", "err", err)
		return 1
	}

	log.Info("archie-agent ready", "nats", *natsURL, "consumer", *consumer)

	// Main loop: fetch, run, reply, ack.
	for {
		if ctx.Err() != nil {
			log.Info("archie-agent shutting down")
			return 0
		}

		batch, err := cons.Fetch(1, jetstream.FetchMaxWait(pollTimeout))
		if err != nil {
			if ctx.Err() != nil {
				return 0
			}
			log.Error("fetch failed", "err", err)
			time.Sleep(1 * time.Second)
			continue
		}

		var msg jetstream.Msg
		for m := range batch.Messages() {
			if m != nil {
				msg = m
				break
			}
		}
		if err := batch.Error(); err != nil {
			log.Error("fetch batch error", "err", err)
			continue
		}
		if msg == nil {
			continue
		}

		// Process the message.
		if err := handle(ctx, msg, nc, log); err != nil {
			log.Error("handle failed", "err", err)
			msg.Nak()
			continue
		}
		msg.Ack()
	}
}

func handle(ctx context.Context, msg jetstream.Msg, nc *nats.Conn, log *slog.Logger) error {
	// Decode the request.
	var req agentexec.AgentRequestMessage
	if err := json.Unmarshal(msg.Data(), &req); err != nil {
		return fmt.Errorf("decode request: %w", err)
	}

	replyTo := msg.Headers().Get(arnats.ReplyHeader)
	if replyTo == "" {
		return fmt.Errorf("missing %s header", arnats.ReplyHeader)
	}

	log.Info("processing stage",
		"task", req.TaskID,
		"attempt", req.Attempt,
		"stage", req.Stage,
		"reply_to", replyTo,
	)

	// Build the agent runner and execute.
	llm := agentexec.NewRuntime(req.Providers)
	if llm == nil {
		return fmt.Errorf("no providers configured in request")
	}
	runner := agentexec.NewInProcessRunner(llm, log)
	result, runErr := runner.Run(ctx, req.Workspace, req.Request)

	// Build and publish the response.
	resp := agentexec.AgentResponseEnvelope{
		Version: req.Request.Version,
		Result:  result,
	}
	if runErr != nil {
		resp.Error = runErr.Error()
	}

	data, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("encode response: %w", err)
	}
	if err := nc.Publish(replyTo, data); err != nil {
		return fmt.Errorf("publish response: %w", err)
	}

	log.Info("stage complete",
		"task", req.TaskID,
		"stage", req.Stage,
		"status", result.Status,
	)
	return nil
}
