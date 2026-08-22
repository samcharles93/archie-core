// Command archied is the archie orchestrator daemon: it watches GitHub
// for issues labelled for archie, works each one in an isolated
// worktree through its routed workflow, and opens pull requests for
// human review.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"maps"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"

	"github.com/moby/moby/client"
	natsio "github.com/nats-io/nats.go"
	"github.com/samcharles93/ai-sdk/chat"
	"github.com/samcharles93/ai-sdk/core"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/container"
	"github.com/samcharles93/archie-core/internal/daemon"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/forge"
	"github.com/samcharles93/archie-core/internal/forgerpc"
	"github.com/samcharles93/archie-core/internal/gateway"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration/overlay"
	"github.com/samcharles93/archie-core/internal/logging"
	"github.com/samcharles93/archie-core/internal/memory/builtin"
	"github.com/samcharles93/archie-core/internal/plugin"
	"github.com/samcharles93/archie-core/internal/secret"
	"github.com/samcharles93/archie-core/internal/storage"
	"github.com/samcharles93/archie-core/internal/store"
	"github.com/samcharles93/archie-core/internal/storerpc"
	"github.com/samcharles93/archie-core/internal/tools"
	"github.com/samcharles93/archie-core/internal/tools/mcp"
	toolprovider "github.com/samcharles93/archie-core/internal/tools/provider"
	mcptoolprovider "github.com/samcharles93/archie-core/internal/tools/provider/mcp"
	"github.com/samcharles93/archie-core/internal/webui"
	"github.com/samcharles93/archie-core/internal/worktree"
	"github.com/samcharles93/archie-core/internal/worktreerpc"
)

func main() {
	// The codesearch helper is dispatched before the daemon's flags are
	// parsed: it is this same binary re-invoked as a short-lived child by
	// internal/indexing, and it must not touch config, the store or NATS.
	if args := os.Args[1:]; isCodesearchHelperArgs(args) {
		os.Exit(runCodesearchHelper(args[1:], os.Stdout))
	}
	os.Exit(run())
}

const (
	// defaultChatMaxSteps bounds the model/tool round-trips in one chat
	// turn when [config.ChatConfig.MaxSteps] is unset.
	defaultChatMaxSteps          = 100
	packagedGatewayChangelogPath = "/usr/share/archie/CHANGELOG.archied.md"
	packagedRuntimeChangelogPath = "/usr/share/archie/CHANGELOG.archie.md"
)

// Component versions are injected from their independent release tags.
// Components without a release tag remain "dev" and never generate upgrade
// notifications.
var (
	gatewayVersion = "dev"
	runtimeVersion = "dev"
)

// chatGenerateOptions builds one chat turn's request.
func chatGenerateOptions(
	ctx context.Context,
	messages []chat.Message,
	registry *tools.Registry,
	maxSteps int,
	limits agentexec.ToolLimits,
	extra []tools.ToolEntry,
) (core.GenerateOptions, error) {
	toolOpts := limits.Options()
	if approval := gateway.ApprovalFromContext(ctx); approval != nil {
		toolOpts.Approval = approval
	}
	toolSet, err := agentexec.BuildToolSet(registry, toolOpts)
	if err != nil {
		return core.GenerateOptions{}, err
	}
	extraSet, err := agentexec.BuildToolSetFrom(extra, toolOpts)
	if err != nil {
		return core.GenerateOptions{}, err
	}
	maps.Copy(toolSet, extraSet)
	if maxSteps <= 0 {
		maxSteps = defaultChatMaxSteps
	}
	return core.GenerateOptions{
		Messages: messages,
		Tools:    toolSet,
		MaxSteps: maxSteps,
	}, nil
}

// isWithin reports whether target sits inside base.
func isWithin(base, target string) bool {
	absBase, err := filepath.Abs(base)
	if err != nil {
		return false
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absBase, absTarget)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// toolLimits reads the configured per-turn result limits.
func toolLimits(cfg config.Config) agentexec.ToolLimits {
	return agentexec.ToolLimits{
		MaxResultChars:  cfg.Tools.Policy.MaxResultChars,
		TurnBudgetChars: cfg.Tools.Policy.TurnBudgetChars,
		SpillDir:        cfg.Tools.Policy.SpillDir,
	}
}

// taskListOverRead multiplies the requested limit when reading rows that are
// filtered by identity afterwards.
const taskListOverRead = 5

// chatTaskListerAdapter gives the gateway a read view of one identity's tasks.
// gateway deliberately does not import internal/store, so the projection
// happens here, as it does for the writer and controller adapters below.
type chatTaskListerAdapter struct {
	tasks func(context.Context, int) ([]store.Task, error)
}

func (a chatTaskListerAdapter) ListChatTasks(ctx context.Context, identity string, limit int) ([]gateway.ChatTaskSummary, error) {
	// Over-read before filtering: Tasks applies its limit across the whole
	// table, so asking for exactly `limit` would return fewer than that for
	// this identity whenever another identity's work is more recent.
	rows, err := a.tasks(ctx, limit*taskListOverRead)
	if err != nil {
		return nil, err
	}
	out := make([]gateway.ChatTaskSummary, 0, limit)
	for _, task := range rows {
		if task.Identity != identity {
			continue
		}
		if len(out) >= limit {
			break
		}
		out = append(out, gateway.ChatTaskSummary{
			ID:       task.ID,
			Repo:     task.Owner + "/" + task.Repo,
			Title:    task.Title,
			Status:   task.Status,
			Workflow: task.Workflow,
			PRNumber: task.PRNumber,
		})
	}
	return out, nil
}

// npmCacheServerEnv returns environment variables that make an
// npx-launched MCP server reuse a persistent package cache across daemon
// restarts, instead of npm re-resolving and re-downloading the package
// from the registry every time the daemon starts. Rooted under the
// daemon's own work dir so it persists regardless of whether the daemon
// itself runs in an ephemeral container. See mcp.NpmCacheEnv for the
// command-matching and env var details shared with archie-agent's
// per-task equivalent (internal/app/agentworker/mcp_providers.go), which
// points at a mounted cache volume instead of a work-dir subdirectory.
//
// workDir is unconditionally defaulted before registerTools runs
// (internal/infrastructure/configuration's applyGeneralDefaults), but
// filepath.Join silently accepts an empty string and would then put the
// cache in the daemon's current working directory instead of its
// persistent data directory -- guard it explicitly rather than trust that
// invariant here too, the way memoryProvider already does nearby.
func npmCacheServerEnv(command, workDir string) []string {
	if workDir == "" {
		return nil
	}
	return mcp.NpmCacheEnv(command, filepath.Join(workDir, "mcp-npm-cache"))
}

func configuredMCPProvider(server config.MCPServer, workDir string) (toolprovider.Engine, error) {
	name := strings.TrimSpace(server.Name)
	if name == "" {
		return nil, fmt.Errorf("MCP server name is required")
	}
	transportType := strings.ToLower(strings.TrimSpace(server.Transport))
	if transportType == "" {
		transportType = "stdio"
	}

	switch transportType {
	case "stdio":
		command := strings.TrimSpace(server.Command)
		if command == "" {
			return nil, fmt.Errorf("MCP stdio server %q requires a command", name)
		}
		transport := mcp.NewStdioTransport(mcp.StdioTransportConfig{
			Command: command,
			Args:    append([]string(nil), server.Args...),
			Dir:     server.WorkDir,
			Env:     npmCacheServerEnv(command, workDir),
		})
		return mcptoolprovider.New(name, transport), nil

	case "http", "streamablehttp":
		url := strings.TrimSpace(server.URL)
		if url == "" {
			return nil, fmt.Errorf("MCP http server %q requires a url", name)
		}
		transport := mcp.NewHTTPTransport(mcp.HTTPTransportConfig{
			Endpoint: url,
			Headers:  server.Headers,
		})
		return mcptoolprovider.New(name, transport), nil

	case "sse":
		sseEndpoint := strings.TrimSpace(server.SSEEndpoint)
		if sseEndpoint == "" {
			return nil, fmt.Errorf("MCP sse server %q requires an sse_endpoint", name)
		}
		transport := mcp.NewSSETransport(mcp.SSETransportConfig{
			SSEEndpoint:     sseEndpoint,
			MessageEndpoint: strings.TrimSpace(server.MessageEndpoint),
			Headers:         server.Headers,
		})
		return mcptoolprovider.New(name, transport), nil

	default:
		return nil, fmt.Errorf("MCP transport %q is not supported", transportType)
	}
}

// resolveForge builds the forge client for one forge configuration, returning
// the resolved token alongside it for the worktree manager.
//
// A missing or unusable credential disables the forge; it does not stop the
// daemon. The forge is one feature among many, and killing the process denies
// the operator chat, the gateway and every other subsystem over a capability
// they may not use at all -- under a systemd unit with Restart=on-failure that
// becomes a crash loop where the error scrolls past unread. A malformed
// configuration is a different matter and still fails fast in validation, well
// before this point.
func resolveForge(cfg config.Forge, secrets *secret.Registry, log *slog.Logger) (forge.Forge, string) {
	if configuration.ForgeDisabled(cfg.Type) {
		return forge.NewNoop(log), ""
	}
	token, err := cfg.Token.Resolve(secrets)
	if err != nil || token == "" {
		log.Warn("forge disabled: token unavailable",
			"forge_type", cfg.Type,
			"engine", cfg.Token.Engine,
			"key", cfg.Token.Key,
			"err", err)
		return forge.NewNoop(log), ""
	}
	client, err := forge.New(cfg.Type, token, cfg.Host, log)
	if err != nil {
		log.Warn("forge disabled: client construction failed",
			"forge_type", cfg.Type, "err", err)
		return forge.NewNoop(log), ""
	}
	return client, token
}

func openProductionTaskStore(ctx context.Context, path string) (store.TaskStore, error) {
	return store.Open(ctx, path)
}

func taskDBPath(configuredPath string) string {
	return configuredPath + "-tasks.sqlite"
}

// configDBPath resolves the runtime config overlay file. It is a sibling
// of the task and conversation stores (same configured path, own suffix)
// so each file owns its user_version and its migrator with no
// contention; recovery from a broken overlay is rm this file plus the
// --no-config-overlay boot flag.
func configDBPath(configuredPath string) string {
	return configuredPath + "-config.sqlite"
}

func manualRequeueTask(ctx context.Context, st store.TaskStore, taskID int64) error {
	task, err := st.TaskByID(ctx, taskID)
	if err != nil {
		return err
	}
	if task == nil {
		return fmt.Errorf("task %d not found", taskID)
	}
	switch task.Status {
	case store.StatusParked, store.StatusWaitingHuman:
		return st.Requeue(ctx, taskID, task.Status, "")
	default:
		return fmt.Errorf("task %d has status %q; only parked or waiting_human tasks can be requeued", taskID, task.Status)
	}
}

func run() int {
	defaultCfg := filepath.Join(configHome(), "archie", "config.toml")
	cfgPath := flag.String("config", defaultCfg, "path to a TOML/YAML config file or configuration directory")
	overlayPath := flag.String("config-overlay", "", "path to a TOML/YAML overlay file or configuration directory applied on top of -config")
	noConfigOverlay := flag.Bool("no-config-overlay", false, "skip the runtime config overlay (recovery hatch for the DB overlay; the -config-overlay file overlay still applies)")
	once := flag.Bool("once", false, "run a single poll+process cycle and exit (systemd timer / testing)")
	requeue := flag.Int64("requeue", 0, "requeue a parked/waiting task by id (keeps its workflow), then exit unless -once is also set")
	showVersion := flag.Bool("version", false, "print the gateway and runtime versions and exit")
	flag.Parse()
	// The versions were only reachable through the chat /version command,
	// which needs a running daemon and a configured channel. An update-check
	// adapter has to answer "what is installed?" from a shell, so expose the
	// same two values here. Machine-readable: one "name version" per line.
	if *showVersion {
		fmt.Printf("archied %s\narchie-agent %s\n", gatewayVersion, runtimeVersion)
		return 0
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	b := newBootstrap()
	if err := b.loadConfig(ctx, *cfgPath, *overlayPath, *noConfigOverlay); err != nil {
		return 1
	}
	defer b.cleanup()

	if err := b.openStores(ctx); err != nil {
		return 1
	}
	if exit, err := b.handleRequeue(ctx, *requeue, *once); err != nil {
		return 1
	} else if exit {
		return 0
	}
	b.loadCatalog(ctx, *cfgPath)
	b.setupObservability()

	if err := b.setupBackends(ctx); err != nil {
		return 1
	}

	b.setupLLMAndChat(ctx)
	if !b.setupGateways(ctx, *cfgPath, *overlayPath) {
		return 1
	}

	if err := b.buildAgentAndWorkflows(ctx); err != nil {
		return 1
	}
	if err := b.loadPlugins(); err != nil {
		return 1
	}
	if err := b.buildTreesAndIdentities(ctx); err != nil {
		return 1
	}

	if err := b.registerNATSRPC(); err != nil {
		return 1
	}
	if err := b.setupMemory(); err != nil {
		return 1
	}
	b.setupCurators(ctx)

	b.setupGuardrails()
	if err := b.registerTools(); err != nil {
		return 1
	}
	b.registerStandaloneTools()
	b.buildDaemon()
	b.wireConfigPublishing(ctx, *cfgPath, *overlayPath)
	b.installUpdateConfigHandler()
	b.installConfigHandlers(*cfgPath, *overlayPath)

	if err := b.startServices(ctx); err != nil {
		return 1
	}
	return exitCode(b.runLoop(ctx, *once))
}

// bootConfigOverlay layers the runtime config overlay over the resolved
// file config. Every failure degrades rather than aborts -- the daemon
// boots on file config alone and the reason is recorded in bootOverlayErr,
// which /api/config surfaces as the reload status. The returned store is
// nil only when Open failed; a store that opened but could not be read
// stays open and non-nil so the dashboard PATCH path and reload wiring
// keep exactly the behaviour of the original inline block.
func bootConfigOverlay(
	ctx context.Context,
	loader *configuration.Loader,
	doc *configuration.Document,
	bootOverlayErr *atomic.Pointer[string],
	log *slog.Logger,
) (*overlay.Store, *configuration.Document) {
	store, err := overlay.Open(ctx, configDBPath(doc.Config.DBPath))
	if err != nil {
		recordBootOverlayError(bootOverlayErr, log, err,
			"config overlay unavailable; booting on file config alone",
			"path", configDBPath(doc.Config.DBPath))
		return nil, doc
	}
	overrides, err := store.Snapshot(ctx)
	if err != nil {
		recordBootOverlayError(bootOverlayErr, log, err,
			"config overlay unreadable; booting on file config alone")
		return store, doc
	}
	if len(overrides) == 0 {
		return store, doc
	}
	applied, err := loader.ApplyOverlay(doc, overrides)
	if err != nil {
		recordBootOverlayError(bootOverlayErr, log, err,
			"config overlay rejected by validation; booting on file config alone")
		return store, doc
	}
	return store, applied
}

// recordBootOverlayError stores the degrade reason for /api/config and
// logs it, so the operator sees both where they are looking.
func recordBootOverlayError(p *atomic.Pointer[string], log *slog.Logger, err error, msg string, args ...any) {
	reason := err.Error()
	p.Store(&reason)
	args = append(args, "err", err)
	log.Error(msg, args...)
}

func persistAndBroadcastEvents(sink *events.Sub, st store.TaskStore, web *webui.Server, log *slog.Logger) {
	for e := range sink.C {
		if e.ID == 0 {
			id, err := st.InsertEvent(context.Background(), e)
			if err != nil {
				log.Error("event sink insert failed", "err", err)
				continue
			}
			e.ID = id
		}
		web.Broadcast(e)
	}
}

// curatorEventSink adapts the in-process event bus to the curator family's
// narrow emission contract. Bus publish is non-blocking with bounded,
// dropping per-subscriber buffers, so curator activity can never
// backpressure the daemon or a chat turn.
type curatorEventSink struct {
	b *events.Bus
}

func (s curatorEventSink) Emit(kind, detail string, data map[string]any) {
	s.b.Publish(events.Event{Kind: kind, Detail: detail, Data: data})
}

type chatTaskWriterAdapter struct {
	enqueue func(
		ctx context.Context,
		owner, repo, title, body, workflow, identity string,
	) (*store.Task, error)
}

func (a chatTaskWriterAdapter) EnqueueChatTask(
	ctx context.Context,
	owner, repo, title, body, workflow, identity string,
) (int64, error) {
	task, err := a.enqueue(ctx, owner, repo, title, body, workflow, identity)
	if err != nil {
		return 0, err
	}
	if task == nil {
		return 0, fmt.Errorf("enqueue chat task returned no task")
	}
	return task.ID, nil
}

type chatTaskControllerAdapter struct {
	taskByID   func(context.Context, int64) (*store.Task, error)
	requeue    func(context.Context, int64, string, string) error
	transition func(context.Context, int64, string, string, string) error
}

func (a chatTaskControllerAdapter) ChatTaskStatus(ctx context.Context, taskID int64) (gateway.ChatTaskStatus, bool, error) {
	task, err := a.taskByID(ctx, taskID)
	if err != nil {
		return gateway.ChatTaskStatus{}, false, err
	}
	if task == nil {
		return gateway.ChatTaskStatus{}, false, nil
	}
	if !task.IsForgeBacked() {
		return gateway.ChatTaskStatus{Status: task.Status, Identity: task.Identity}, true, nil
	}
	return gateway.ChatTaskStatus{}, false, fmt.Errorf("task %d is not chat-originated", taskID)
}

func (a chatTaskControllerAdapter) ApproveChatTask(ctx context.Context, taskID int64) error {
	return a.requeue(ctx, taskID, store.StatusWaitingHuman, "implement")
}

func (a chatTaskControllerAdapter) CancelChatTask(ctx context.Context, taskID int64, reason string) error {
	task, err := a.taskByID(ctx, taskID)
	if err != nil {
		return err
	}
	if task == nil {
		return fmt.Errorf("task %d not found", taskID)
	}
	if task.IsForgeBacked() {
		return fmt.Errorf("task %d is not chat-originated", taskID)
	}
	// An operator declining work lands in the same state whichever surface
	// they used. This used to record StatusRejected while the dashboard
	// recorded StatusClosedWontDo, so the same decision showed up as two
	// different states -- and StatusRejected, which the PR reconciler uses
	// for "the pull request was closed without merging", stopped meaning one
	// thing.
	return a.transition(ctx, taskID, task.Status, store.StatusClosedWontDo, reason)
}

// chatTaskLogReaderAdapter gives the gateway a read view of a task's
// persisted log history without importing internal/logging or internal/store
// into the gateway package. Each identity's reader is scoped to its own
// tasks: the identity bound at construction is used for authorization, so a
// model cannot read another identity's task logs by passing a different
// identity through the tool input.
type chatTaskLogReaderAdapter struct {
	tasks    func(context.Context, int64) (*store.Task, error)
	taskLogs *logging.TaskRegistry
}

func (a chatTaskLogReaderAdapter) ReadChatTaskLogs(
	ctx context.Context, identity string, taskID int64, attempt int, q gateway.ChatTaskLogQuery,
) (gateway.ChatTaskLogResult, error) {
	task, err := a.tasks(ctx, taskID)
	if err != nil {
		return gateway.ChatTaskLogResult{}, err
	}
	if task == nil {
		return gateway.ChatTaskLogResult{}, fmt.Errorf("task %d not found", taskID)
	}
	// Match the filter chatTaskListerAdapter already applies: a model
	// bound to one identity must not read logs for another identity's
	// tasks, and tasks with no identity (forge-sourced) are not
	// readable through a chat tool at all — they belong to the daemon,
	// not a particular identity. "The empty string MUST NOT retain
	// special meaning" (docs/architecture/identity.md).
	if task.Identity != identity {
		return gateway.ChatTaskLogResult{}, fmt.Errorf("task %d belongs to %q, not %q", taskID, task.Identity, identity)
	}

	if attempt <= 0 {
		attempt = task.Attempt
	}
	path := a.taskLogs.Path(taskID, attempt)
	if path == "" {
		return gateway.ChatTaskLogResult{Attempt: attempt, Entries: []gateway.ChatTaskLogEntry{}}, nil
	}

	result, err := logging.Tail(path, logging.Query{
		Component: q.Component,
		Contains:  q.Contains,
		Limit:     q.Limit,
	})
	if err != nil {
		return gateway.ChatTaskLogResult{}, err
	}

	entries := make([]gateway.ChatTaskLogEntry, len(result.Entries))
	for i, e := range result.Entries {
		entries[i] = gateway.ChatTaskLogEntry{
			Time:    e.Time,
			Level:   e.Level,
			Message: e.Message,
			Fields:  e.Fields,
		}
	}
	return gateway.ChatTaskLogResult{
		Entries:   entries,
		Attempt:   attempt,
		Truncated: result.Truncated,
	}, nil
}

func chatTaskProfiles(cfg config.Config) ([]gateway.TaskProfile, string) {
	if len(cfg.Identities) > 0 {
		profiles := make([]gateway.TaskProfile, 0, len(cfg.Identities))
		for _, identity := range cfg.Identities {
			if len(identity.Repos) == 0 {
				continue
			}
			profiles = append(profiles, newChatTaskProfile(identity.Name, identity.Repos))
		}
		if len(profiles) == 0 {
			return nil, ""
		}
		return profiles, profiles[0].Identity
	}
	if len(cfg.Repos) == 0 {
		return nil, ""
	}
	return []gateway.TaskProfile{newChatTaskProfile("", cfg.Repos)}, ""
}

func newChatTaskProfile(identity string, repos []config.Repo) gateway.TaskProfile {
	allowed := make([]string, 0, len(repos))
	for _, repo := range repos {
		allowed = append(allowed, repo.Owner+"/"+repo.Name)
	}
	return gateway.TaskProfile{
		Identity:     identity,
		DefaultOwner: repos[0].Owner,
		DefaultRepo:  repos[0].Name,
		Repos:        allowed,
	}
}

func configureTaskCommands(
	router *gateway.Router,
	tasks gateway.TaskCreator,
	controller gateway.TaskController,
	identity string,
) {
	router.Tasks = tasks
	router.Controller = controller
	router.Identity = identity
}

// parseListenAddr splits "host:port" into components, using defaults
// when the input is empty or missing a part.
func parseListenAddr(addr, defaultHost string, defaultPort int) (string, int) {
	if addr == "" {
		return defaultHost, defaultPort
	}
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return defaultHost, defaultPort
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 {
		return host, defaultPort
	}
	return host, port
}

// sessionKey builds a deterministic session identifier from a gateway
// message's routing fields. Platform + channel + thread uniquely identify
// a conversation for session persistence and history retrieval.
func executionProviders(cfg config.Config) map[string]agentexec.Provider {
	return agentexec.ProvidersFromConfig(cfg.Providers)
}

// subscribeSystemLogs subscribes to every task's system log subject at once.
// A sandboxed container's own stderr disappears at AutoRemove; archie-agent
// ships it here instead (agentexec.SubjectForSystem), and this is where it
// lands: one wildcard subscription demuxed by taskID and routed through
// taskLogs, which is opened for a task's duration in Daemon.process, so a
// message for a task not currently open there -- late, duplicate, or from
// an attempt this instance never dispatched -- is expected and silently
// dropped rather than treated as an error.
func subscribeSystemLogs(nc *natsio.Conn, taskLogs *logging.TaskRegistry, log *slog.Logger) (unsubscribe func(), err error) {
	sub, err := nc.Subscribe(agentexec.SubjectSystemWildcard, func(msg *natsio.Msg) {
		taskID, ok := agentexec.TaskIDFromSystemSubject(msg.Subject)
		if !ok {
			log.Warn("system log message on unparseable subject", "subject", msg.Subject)
			return
		}
		var entry logging.Entry
		if err := json.Unmarshal(msg.Data, &entry); err != nil {
			log.Warn("system log message undecodable", "task", taskID, "err", err)
			return
		}
		taskLogs.Write(taskID, entry)
	})
	if err != nil {
		return nil, err
	}
	return func() {
		if err := sub.Unsubscribe(); err != nil {
			log.Warn("system log unsubscribe failed", "err", err)
		}
	}, nil
}

// subscribeAgentEvents subscribes to every task's events subject at once and
// republishes each decoded event on bus. An archie-agent worker's
// agentexec.ForwardTaskEvents ships a task's workflow events (stage
// progress, outcome, parking) here because that worker's own *events.Bus is
// in-process and invisible to the daemon; this is the other half of the
// bridge, landing them on bus so persistAndBroadcastEvents -- the single
// choke point that inserts into the SQLite events table and fans out over
// SSE -- treats them exactly like an in-process daemon-run workflow's own
// events. Mirrors subscribeSystemLogs's demux-by-taskID shape.
func subscribeAgentEvents(nc *natsio.Conn, bus *events.Bus, log *slog.Logger) (unsubscribe func(), err error) {
	sub, err := nc.Subscribe(agentexec.SubjectEventsWildcard, func(msg *natsio.Msg) {
		taskID, ok := agentexec.TaskIDFromEventsSubject(msg.Subject)
		if !ok {
			log.Warn("task event message on unparseable subject", "subject", msg.Subject)
			return
		}
		var e events.Event
		if err := json.Unmarshal(msg.Data, &e); err != nil {
			log.Warn("task event message undecodable", "task", taskID, "err", err)
			return
		}
		bus.Publish(e)
	})
	if err != nil {
		return nil, err
	}
	return func() {
		if err := sub.Unsubscribe(); err != nil {
			log.Warn("task event unsubscribe failed", "err", err)
		}
	}, nil
}

// registerTaskRPCServers subscribes the storerpc/forgerpc/worktreerpc
// handlers on nc so an archie-agent container (which holds no DB
// connection, forge token, or push credential) can proxy those operations
// back to archied. The returned func unsubscribes all of them.
//
// The store is shared across identities, so storerpc registers once on its
// root subjects. Forge and worktree are identity-scoped: the root servers
// answer the root (identity-less) subjects for single-identity deployments
// and root-owned tasks, and one server pair per identity answers that
// identity's scoped subjects so a container-mode task owned by a non-root
// identity has its RPC calls served by its own forge client and worktree
// manager.
func registerTaskRPCServers(nc *natsio.Conn, st store.TaskStore, forgeClient forge.Forge, trees *worktree.Manager, identities []*daemon.IdentityRunner, grants *worktreerpc.Grants, log *slog.Logger) (unsubscribe func(), err error) {
	unsubs := make([]func(), 0, 3+2*len(identities))
	unsubAll := func() {
		for _, u := range unsubs {
			u()
		}
	}

	storeServer := &storerpc.Server{Store: st, Log: log}
	u, err := storeServer.Register(nc)
	if err != nil {
		return nil, fmt.Errorf("register storerpc: %w", err)
	}
	unsubs = append(unsubs, u)

	registerForge := func(fg forge.Forge, identity string) error {
		srv := &forgerpc.Server{Forge: fg, Log: log.With("rpc_identity", identity)}
		u, err := srv.RegisterFor(nc, identity)
		if err != nil {
			return fmt.Errorf("register forgerpc%s: %w", identitySuffix(identity), err)
		}
		unsubs = append(unsubs, u)
		return nil
	}
	registerTrees := func(mgr *worktree.Manager, identity string) error {
		srv := &worktreerpc.Server{Trees: mgr, Grants: grants, Log: log.With("rpc_identity", identity)}
		u, err := srv.RegisterFor(nc, identity)
		if err != nil {
			return fmt.Errorf("register worktreerpc%s: %w", identitySuffix(identity), err)
		}
		unsubs = append(unsubs, u)
		return nil
	}

	// Root (identity-less) forge and worktree servers.
	if err := registerForge(forgeClient, ""); err != nil {
		unsubAll()
		return nil, err
	}
	if err := registerTrees(trees, ""); err != nil {
		unsubAll()
		return nil, err
	}

	// One forge/worktree server pair per identity, on identity-scoped
	// subjects.
	for _, id := range identities {
		if err := registerForge(id.Forge, id.Name); err != nil {
			unsubAll()
			return nil, err
		}
		if err := registerTrees(id.Trees, id.Name); err != nil {
			unsubAll()
			return nil, err
		}
	}

	return unsubAll, nil
}

// identitySuffix renders a log suffix for an identity-scoped registration,
// empty for the root set.
func identitySuffix(identity string) string {
	if identity == "" {
		return ""
	}
	return " (" + identity + ")"
}

func configuredNATSToken(cfg config.NATSConfig, getenv func(string) string) (string, error) {
	if cfg.TokenEnv == "" {
		return "", nil
	}
	token := getenv(cfg.TokenEnv)
	if token == "" {
		return "", fmt.Errorf("%s is required when nats.token_env is configured", cfg.TokenEnv)
	}
	return token, nil
}

func configHome() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return x
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config")
}

func releaseAnnouncementStatePath(workDir, identity string) string {
	identityHash := sha256.Sum256([]byte(identity))
	return filepath.Join(
		workDir,
		fmt.Sprintf("release-announcements-%x.json", identityHash[:8]),
	)
}

// updateReportPath is where the update watchdog leaves the phase-2 outcome
// of an update for this identity to relay on its next launch. Hashed the
// same way as releaseAnnouncementStatePath so multiple identities sharing
// one daemon (see docs/architecture/identity.md) never collide.
func updateReportPath(workDir, identity string) string {
	identityHash := sha256.Sum256([]byte(identity))
	return filepath.Join(
		workDir,
		fmt.Sprintf("update-report-%x.json", identityHash[:8]),
	)
}

// safePluginInfo calls Name() and Version() on a plugin, recovering from
// panics. A panicking plugin must not crash the daemon at startup.
func safePluginInfo(p plugin.Plugin) (name, version string) {
	defer func() {
		if r := recover(); r != nil {
			name = "(panic)"
			version = "(panic)"
		}
	}()
	return p.Name(), p.Version()
}

// webTokenFor returns the dashboard token for a listen address, or "" when
// none is needed.
//
// A loopback bind needs no token: local access already implies the agent's own
// authority, since its shell and edit tools run as this user. A reachable bind
// mints one and logs a URL to click, so an instance cannot be left exposed
// merely by omitting configuration.
func webTokenFor(listen, dbPath string, log *slog.Logger) string {
	if webui.IsLoopback(listen) {
		return ""
	}
	path := filepath.Join(filepath.Dir(dbPath), "web-token")
	tok, err := webui.LoadOrCreateToken(path)
	if err != nil {
		log.Error("web ui token", "err", err)
		return ""
	}
	log.Info("web ui token", "open", webui.DashboardURL(listen, tok), "stored", path)
	return tok
}

// memoryProvider builds the built-in memory provider, falling back to the
// default work directory when the configured one cannot be established.
//
// A bad path warns and degrades rather than stopping the daemon, in line with
// the forge and container paths: memory is one capability among many. The
// fallback is the location an unset work_dir would have produced, so an
// operator who copied a config from another host (a container path such as
// /var/lib/archie/work onto a laptop, say) lands somewhere predictable
// instead of somewhere only this function knows about.
//
// Returns the provider and the directory actually in use. A nil provider
// means the configuration is unusable in a way no fallback can fix.
func memoryProvider(workDir string, log *slog.Logger) (*builtin.Provider, string) {
	// An empty workDir would make filepath.Join produce the relative path
	// "memory", which MkdirAll creates in whatever directory the daemon
	// happens to be started from -- succeeding, and putting memory somewhere
	// nobody will look. Defaulting should have prevented an empty workDir;
	// treat it as unset rather than trusting it.
	var dir string
	var p *builtin.Provider
	if workDir == "" {
		log.Warn("no work directory configured, using the default for memory")
	} else {
		dir = filepath.Join(workDir, "memory")
		var err error
		p, err = builtin.New(builtin.Config{Dir: dir})
		if err != nil {
			log.Warn("memory directory rejected, falling back to the default", "dir", dir, "err", err)
			p = nil
		}
		if p != nil && p.IsAvailable() {
			return p, dir
		}
		if p != nil {
			log.Warn("memory directory unusable, falling back to the default",
				"dir", dir, "err", p.Err())
		}
	}

	fallback := filepath.Join(configuration.DefaultWorkDir(), "memory")
	if fallback == dir {
		// Already the default: there is nowhere else to try, so run degraded
		// and say what that costs rather than pretending memory works.
		log.Error("memory unavailable: the agent will start every conversation "+
			"with no recollection of earlier ones, and memory writes will fail",
			"dir", dir)
		return p, dir
	}

	fb, err := builtin.New(builtin.Config{Dir: fallback})
	if err != nil {
		log.Error("memory provider init failed at the default directory", "dir", fallback, "err", err)
		return p, dir
	}
	if !fb.IsAvailable() {
		log.Error("memory unavailable: neither the configured nor the default "+
			"directory is usable, so the agent will start every conversation "+
			"with no recollection of earlier ones",
			"configured", dir, "default", fallback, "err", fb.Err())
		return fb, fallback
	}
	log.Warn("using the default memory directory", "dir", fallback)
	return fb, fallback
}

// startContainers brings up the container pool and storage backend, returning
// nils when containers are disabled or unavailable.
//
// Failure here degrades rather than aborting, for the same reason resolveForge
// does: containers are one capability among many, and refusing to start denies
// the operator chat, the dashboard and every other subsystem over a feature
// they may not be exercising. Under Restart=on-failure it also turns a
// recoverable problem into a crash loop with the real error scrolling past.
func startContainers(
	ctx context.Context,
	cfg config.Config,
	log *slog.Logger,
) (*container.Pool, storage.Backend, func()) {
	noop := func() {}
	if !cfg.Containers.Enabled {
		return nil, nil, noop
	}

	// Single Docker client shared between pool and storage backend.
	dockerCli, err := client.New(client.FromEnv)
	if err != nil {
		log.Error("containers disabled: docker client unavailable", "err", err)
		return nil, nil, noop
	}
	closeDocker := func() {
		if err := dockerCli.Close(); err != nil {
			log.Warn("docker client close failed", "err", err)
		}
	}

	pool, err := container.NewPool(ctx, container.Config{
		Image:          cfg.Containers.Image,
		MaxConcurrency: cfg.Containers.MaxConcurrency,
		MaxUptime:      cfg.Containers.MaxUptime.Std(),
		PullPolicy:     cfg.Containers.PullPolicy,
		Network:        cfg.Containers.Network,
		DockerClient:   dockerCli,
	}, cfg.NATS.URL, log)
	if err != nil {
		// A missing image is recoverable by hand. The daemon sends no registry
		// credentials on pull (internal/container/pool.go), so a private
		// registry always needs the operator's CLI to fetch it first.
		log.Error("containers disabled: pool unavailable", "err", err,
			"hint", "run `docker compose pull agent` (or `build agent`) so the image is present locally")
		return nil, storage.NewDockerBackend(dockerCli), closeDocker
	}

	// contextcheck: Pool.Close takes no context by design -- it is a shutdown
	// path that must run to completion after ctx is already cancelled.
	//nolint:contextcheck
	return pool, storage.NewDockerBackend(dockerCli), func() {
		if err := pool.Close(); err != nil {
			log.Warn("container pool close failed", "err", err)
		}
		closeDocker()
	}
}
