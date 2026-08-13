// Command archie-agent is a long-running NATS-connected worker. It answers
// two independent request shapes that serve different deployment modes:
//
//  1. Single-stage requests on archie.agent.> over the ARCHIE_TASKS
//     JetStream stream  --  used when agent.mode is "nats" without
//     container sandboxing. One stage is dispatched per message, and the
//     agent replies on the caller's inbox.
//
//  2. Full-task handoffs on archie.taskrun.> (core NATS, queue-grouped)
//     --  used with Docker container sandboxing. The agent runs
//     workflow.Route/workflow.Run itself, proxying Store/Forge/worktree
//     operations back to archied over NATS instead of holding those
//     credentials directly.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/domain/workintake"
	arnats "github.com/samcharles93/archie-core/internal/infrastructure/eventbus/nats"
	"github.com/samcharles93/archie-core/internal/storage"
)

const (
	consumerName = "archie-agent"
	pollTimeout  = 5 * time.Second

	// ackWait exceeds the longest stage runtime: a stage still working must
	// not have its message redelivered to another worker.
	ackWait = 30 * time.Minute
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

	// Before anything can run a gate: the worktree is a host bind mount owned
	// by another UID, and git refuses to work in it until it is marked safe.
	markWorktreeSafe(ctx, storage.WorktreeMountDir, runGit, log)

	// One bus client owns the stream definition. archied declares the same
	// stream with the same subjects; this process previously declared its
	// own copy, free to disagree about retention, dedup window and TTLs.
	bus, err := arnats.Connect(ctx, arnats.Config{
		URL:           natsURL,
		Token:         natsToken,
		Subjects:      []string{workintake.SubjectTaskWildcard, agentexec.SubjectAgentWildcard},
		ConsumerName:  *consumer,
		FilterSubject: agentexec.SubjectAgentWildcard,
		PollTimeout:   pollTimeout,
		AckWait:       ackWait,
	}, log)
	if err != nil {
		log.Error("nats connect failed", "err", err)
		return 1
	}
	defer bus.Close()

	// Full-task handoffs use core NATS request/reply with a queue group,
	// which the JetStream contract does not express.
	nc, err := bus.CoreConn()
	if err != nil {
		log.Error("nats connection unavailable", "err", err)
		return 1
	}

	// A dedicated per-task subscription (below) means this container was
	// spawned for one task, which is also when its stderr is otherwise
	// unrecoverable: container.Pool creates it with AutoRemove: true. Once
	// nc exists, ship this process's own logs to the daemon over
	// SubjectForSystem(taskID) so they survive the container's exit. Shared
	// queue-group mode serves many unrelated tasks per process, so there is
	// no single task subject to ship to -- it keeps the stderr-only logger.
	//
	// This only covers logging done through the `log` variable from here on.
	// bus (arnats.Client) was constructed above with the pre-wrap logger and
	// keeps it for its own lifetime, so its own wire-activity Debug lines
	// (e.g. "published" on every bus.Respond) stay stderr-only regardless.
	// That's a real, known gap, not an oversight: it's the bus's internal
	// chatter about NATS itself, not the agent's own reasoning or tool
	// output, and reordering construction to close it would mean connecting
	// to NATS before knowing whether NATS is even needed for this call.
	taskID, hasTaskID := bootTaskID(storage.WorktreeMountDir, log)
	if hasTaskID {
		log = slog.New(agentexec.NewSystemLogHandler(log.Handler(), nc, taskID))
		log.Info("system log publisher attached", "task", taskID)
	}

	// Subscribe to task run messages.
	taskRunSub, err := subscribeTaskRuns(ctx, nc, log, taskID, hasTaskID)
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
	return runMainLoop(ctx, bus, log)
}

func natsConnectionSettings(flagURL string, getenv func(string) string) (url, token string) {
	url = flagURL
	if url == "" {
		url = getenv("NATS_URL")
	}
	return url, getenv("NATS_TOKEN")
}
