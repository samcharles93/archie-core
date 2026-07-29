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
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/container"
	"github.com/samcharles93/archie-core/internal/domain/workintake"
	"github.com/samcharles93/archie-core/internal/eventbus"
	arnats "github.com/samcharles93/archie-core/internal/infrastructure/eventbus/nats"
	"github.com/samcharles93/archie-core/internal/storage"
	"github.com/samcharles93/archie-core/internal/taskrun"
	"github.com/samcharles93/archie-core/internal/tools"
	"github.com/samcharles93/archie-core/internal/tools/mcp"
	toolprovider "github.com/samcharles93/archie-core/internal/tools/provider"
	mcptoolprovider "github.com/samcharles93/archie-core/internal/tools/provider/mcp"
)

const (
	consumerName = "archie-agent"
	pollTimeout  = 5 * time.Second

	// ackWait exceeds the longest stage runtime: a stage still working must
	// not have its message redelivered to another worker.
	ackWait = 30 * time.Minute

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
	return runMainLoop(ctx, bus, log)
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

// stageBus is the messaging this loop needs: pull the next stage request and
// answer it on the requester's inbox.
type stageBus interface {
	Fetch(ctx context.Context) (eventbus.Message, error)
	Respond(ctx context.Context, replyAddress string, payload []byte) error
}

// runMainLoop runs the fetch-handle-ack loop until the context is cancelled.
func runMainLoop(ctx context.Context, bus stageBus, log *slog.Logger) int {
	for {
		if ctx.Err() != nil {
			log.Info("archie-agent shutting down")
			return 0
		}
		msg, err := bus.Fetch(ctx)
		switch {
		case errors.Is(err, eventbus.ErrNoMessage):
			continue
		case err != nil:
			if ctx.Err() != nil {
				return 0
			}
			log.Error("fetch failed", "err", err)
			time.Sleep(1 * time.Second)
			continue
		}
		if err := handle(ctx, msg, bus, log); err != nil {
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

// mcpProviderSet holds the resources needed to clean up MCP providers.
type mcpProviderSet struct {
	providers []toolprovider.Engine
	registry  *tools.Registry
}

// startMCPProviders creates MCP transports from config, starts them,
// discovers tools, and registers them into a local registry. Returns
// nil when there are no servers configured.
func startMCPProviders(ctx context.Context, servers []config.MCPServer, log *slog.Logger) (*mcpProviderSet, error) {
	if len(servers) == 0 {
		return nil, nil
	}
	reg := tools.NewRegistry()
	var providers []toolprovider.Engine
	var firstErr error

	for _, srv := range servers {
		name := strings.TrimSpace(srv.Name)
		if name == "" {
			log.Warn("mcp server skipped: empty name")
			continue
		}
		transportType := strings.ToLower(strings.TrimSpace(srv.Transport))
		if transportType == "" {
			transportType = "stdio"
		}

		var transport mcptoolprovider.LifecycleTransport
		switch transportType {
		case "stdio":
			command := strings.TrimSpace(srv.Command)
			if command == "" {
				err := fmt.Errorf("MCP stdio server %q requires a command", name)
				log.Warn("mcp server skipped", "name", name, "err", err)
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			transport = mcp.NewStdioTransport(mcp.StdioTransportConfig{
				Command: command,
				Args:    append([]string(nil), srv.Args...),
			})
		case "http", "streamablehttp":
			url := strings.TrimSpace(srv.URL)
			if url == "" {
				err := fmt.Errorf("MCP http server %q requires a url", name)
				log.Warn("mcp server skipped", "name", name, "err", err)
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			transport = mcp.NewHTTPTransport(mcp.HTTPTransportConfig{
				Endpoint: url,
				Headers:  srv.Headers,
			})
		case "sse":
			sseEndpoint := strings.TrimSpace(srv.SSEEndpoint)
			if sseEndpoint == "" {
				err := fmt.Errorf("MCP sse server %q requires an sse_endpoint", name)
				log.Warn("mcp server skipped", "name", name, "err", err)
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			transport = mcp.NewSSETransport(mcp.SSETransportConfig{
				SSEEndpoint:     sseEndpoint,
				MessageEndpoint: strings.TrimSpace(srv.MessageEndpoint),
				Headers:         srv.Headers,
			})
		default:
			log.Warn("mcp server skipped: unknown transport", "name", name, "transport", transportType)
			continue
		}

		provider := mcptoolprovider.New(name, transport)
		if err := provider.Start(ctx); err != nil {
			log.Warn("mcp provider start failed", "name", name, "err", err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}

		entries, err := provider.Discover(ctx)
		if err != nil {
			log.Warn("mcp tool discovery failed", "name", name, "err", err)
			// Stop the provider since we can't use it.
			_ = provider.Stop(ctx)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}

		for _, entry := range entries {
			if err := reg.Register(entry); err != nil {
				log.Warn("mcp tool registration failed", "tool", entry.Name, "err", err)
			}
		}
		providers = append(providers, provider)
		log.Info("mcp provider started", "name", name, "transport", transportType, "tools", len(entries))
	}

	if len(providers) == 0 {
		return nil, firstErr
	}
	return &mcpProviderSet{providers: providers, registry: reg}, nil
}

func (s *mcpProviderSet) cleanup(ctx context.Context, log *slog.Logger) {
	for _, p := range s.providers {
		if err := p.Stop(ctx); err != nil {
			log.Warn("mcp provider stop failed", "err", err)
		}
	}
}

func handle(ctx context.Context, msg eventbus.Message, bus stageBus, log *slog.Logger) error {
	var req agentexec.AgentRequestMessage
	if err := json.Unmarshal(msg.Data(), &req); err != nil {
		return fmt.Errorf("decode request: %w", err)
	}

	replyTo, err := msg.ReplyAddress()
	if err != nil {
		return fmt.Errorf("stage request on %s: %w", msg.Subject(), err)
	}

	log.Info("processing stage",
		"task", req.TaskID,
		"attempt", req.Attempt,
		"stage", req.Stage,
		"workflow", req.Workflow,
		"reply_to", replyTo,
	)

	// Start MCP providers and build a local tool registry.
	mcpSet, mcpErr := startMCPProviders(ctx, req.MCPServers, log)
	if mcpSet != nil {
		defer mcpSet.cleanup(ctx, log)
	}
	if mcpErr != nil {
		log.Warn("mcp providers had errors, continuing with available tools", "err", mcpErr)
	}

	// Build a runner factory that includes MCP-discovered tools.
	factory := func(providers map[string]agentexec.Provider, log *slog.Logger) agentexec.Runner {
		var registries []*tools.Registry
		if mcpSet != nil {
			registries = append(registries, mcpSet.registry)
		}
		return agentexec.NewInProcessRunner(agentexec.NewRuntime(providers), log, registries...)
	}

	resp, err := agentexec.HandleMessage(ctx, req, log, factory)
	if err != nil {
		return fmt.Errorf("handle: %w", err)
	}

	data, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("encode response: %w", err)
	}
	if err := bus.Respond(ctx, replyTo, data); err != nil {
		return fmt.Errorf("publish response: %w", err)
	}

	log.Info("stage complete",
		"task", req.TaskID,
		"stage", req.Stage,
		"status", resp.Result.Status,
	)
	return nil
}
