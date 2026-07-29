// Command archie-agent is a long-running NATS-connected worker. It answers
// two request shapes: single-stage archie.agent.> requests over the
// ARCHIE_TASKS JetStream stream (the legacy per-stage protocol), and full
// task handoffs on archie.taskrun.> (core NATS, queue-grouped)  --  where it
// runs workflow.Route/workflow.Run itself, proxying Store/Forge/worktree
// operations back to archied over NATS instead of holding those
// credentials directly.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/container"
	arnats "github.com/samcharles93/archie-core/internal/nats"
	"github.com/samcharles93/archie-core/internal/storage"
	"github.com/samcharles93/archie-core/internal/taskrun"
)

const (
	streamName   = "ARCHIE_TASKS"
	consumerName = "archie-agent"
	dedupWindow  = 2 * time.Minute
	pollTimeout  = 5 * time.Second
	ackWait      = 30 * time.Minute

	// taskRunSubjectWildcard matches full-task handoffs (archie.taskrun.<id>).
	// A queue group means only one archie-agent instance answers each
	// message, whether deployed as a shared worker pool (today) or spawned
	// one-per-task (the sandboxed container target).
	taskRunSubjectWildcard = "archie.taskrun.>"
	taskRunQueueGroup      = "archie-taskrun-workers"
)

func main() {
	os.Exit(run())
}

func run() int {
	natsURLFlag := flag.String("nats-url", "", "NATS server URL (defaults to NATS_URL)")
	consumer := flag.String("consumer", consumerName, "JetStream consumer name")
	flag.Parse()

	natsURL, natsToken := natsConnectionSettings(*natsURLFlag, os.Getenv)
	if natsURL == "" {
		fmt.Fprintln(os.Stderr, "error: -nats-url or NATS_URL is required")
		flag.Usage()
		return 1
	}

	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Connect to NATS.
	var natsOptions []nats.Option
	if natsToken != "" {
		natsOptions = append(natsOptions, nats.Token(natsToken))
	}
	nc, err := nats.Connect(natsURL, natsOptions...)
	if err != nil {
		log.Error("nats connect failed", "err", err)
		return 1
	}
	defer nc.Close()
	log.Info("nats connected", "url", natsURL)

	// JetStream setup.
	_, cons, err := setupJetStream(ctx, nc, *consumer)
	if err != nil {
		log.Error("jetstream setup failed", "err", err)
		return 1
	}

	// Subscribe to task run messages.
	taskRunSub, err := subscribeTaskRuns(ctx, nc, log)
	if err != nil {
		log.Error("taskrun subscribe failed", "err", err)
		return 1
	}
	defer func() {
		if err := taskRunSub.Unsubscribe(); err != nil {
			log.Warn("task run unsubscribe failed", "err", err)
		}
	}()

	log.Info("archie-agent ready", "nats", natsURL, "consumer", *consumer)
	return runMainLoop(ctx, cons, nc, log)
}

func setupJetStream(ctx context.Context, nc *nats.Conn, consumerName string) (jetstream.JetStream, jetstream.Consumer, error) {
	js, err := jetstream.New(nc)
	if err != nil {
		return nil, nil, err
	}
	stream, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:       streamName,
		Subjects:   []string{"archie.task.>", "archie.agent.>"},
		Storage:    jetstream.FileStorage,
		Retention:  jetstream.WorkQueuePolicy,
		Duplicates: dedupWindow,
	})
	if err != nil {
		return nil, nil, err
	}
	cons, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Name:              consumerName,
		Durable:           consumerName,
		FilterSubject:     arnats.SubjectAgentWildcard,
		AckPolicy:         jetstream.AckExplicitPolicy,
		MaxDeliver:        3,
		AckWait:           ackWait,
		InactiveThreshold: 24 * time.Hour,
	})
	if err != nil {
		return nil, nil, err
	}
	return js, cons, nil
}

// subscribeTaskRuns prefers a dedicated per-task subscription over the
// shared queue group whenever this container was spawned for a specific
// task (task.json present at the worktree mount).
func subscribeTaskRuns(ctx context.Context, nc *nats.Conn, log *slog.Logger) (*nats.Subscription, error) {
	if taskID, ok := bootTaskID(storage.WorktreeMountDir, log); ok {
		log.Info("taskrun: dedicated per-task subscription", "task", taskID)
		return nc.Subscribe(taskrun.SubjectForTask(taskID), func(msg *nats.Msg) {
			handleTaskRun(ctx, msg, nc, log)
		})
	}
	log.Info("taskrun: shared queue-group subscription (no task.json found)")
	return nc.QueueSubscribe(taskRunSubjectWildcard, taskRunQueueGroup, func(msg *nats.Msg) {
		handleTaskRun(ctx, msg, nc, log)
	})
}

// bootTaskID reads the boot-time task.json brief the daemon writes into
// the worktree before container acquire (container.WriteTaskJSON) and
// returns its ID. Returns (0, false) when the file is absent, unparseable,
// or has no ID  --  any of which mean "shared pool mode, no dedicated task."
func bootTaskID(mountDir string, log *slog.Logger) (int64, bool) {
	data, err := os.ReadFile(filepath.Join(mountDir, "task.json"))
	if err != nil {
		return 0, false
	}
	var payload container.TaskPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		log.Warn("task.json present but unparseable  --  falling back to shared queue subscription", "err", err)
		return 0, false
	}
	if payload.ID <= 0 {
		return 0, false
	}
	return payload.ID, true
}

// runMainLoop runs the fetch-handle-ack loop until the context is cancelled.
func runMainLoop(ctx context.Context, cons jetstream.Consumer, nc *nats.Conn, log *slog.Logger) int {
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
		if err := handle(ctx, msg, nc, log); err != nil {
			log.Error("handle failed", "err", err)
			if err := msg.Nak(); err != nil {
				log.Warn("nak failed", "err", err)
			}
			continue
		}
		if err := msg.Ack(); err != nil {
			log.Warn("ack failed", "err", err)
		}
	}
}

func natsConnectionSettings(flagURL string, getenv func(string) string) (url, token string) {
	url = flagURL
	if url == "" {
		url = getenv("NATS_URL")
	}
	return url, getenv("NATS_TOKEN")
}

func handle(ctx context.Context, msg jetstream.Msg, nc *nats.Conn, log *slog.Logger) error {
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
		"workflow", req.Workflow,
		"reply_to", replyTo,
	)

	resp, err := agentexec.HandleMessage(ctx, req, log, agentexec.DefaultRunnerFactory)
	if err != nil {
		return fmt.Errorf("handle: %w", err)
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
		"status", resp.Result.Status,
	)
	return nil
}
