// Command archied is the archie orchestrator daemon: it watches GitHub
// for issues labelled for archie, works each one in an isolated
// worktree through its routed workflow, and opens pull requests for
// human review.
package archied

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/moby/moby/client"
	natsio "github.com/nats-io/nats.go"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/app/servicekit"
	"github.com/samcharles93/archie-core/internal/buildinfo"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/container"
	"github.com/samcharles93/archie-core/internal/daemon"
	"github.com/samcharles93/archie-core/internal/domain/applystatus"
	"github.com/samcharles93/archie-core/internal/domain/presence"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/forge"
	"github.com/samcharles93/archie-core/internal/forgerpc"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration"
	"github.com/samcharles93/archie-core/internal/logging"
	"github.com/samcharles93/archie-core/internal/secret"
	"github.com/samcharles93/archie-core/internal/storage"
	"github.com/samcharles93/archie-core/internal/worktree"
	"github.com/samcharles93/archie-core/internal/worktreerpc"
)

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
func resolveForge(ctx context.Context, cfg config.Forge, instance string, secrets *secret.Registry, extensions *forgeExtensions, log *slog.Logger) (forge.Forge, string) {
	if configuration.ForgeDisabled(cfg.Type) {
		return forge.NewNoop(log), ""
	}
	token, err := secrets.Resolve(cfg.Token)
	if err != nil || token == "" {
		log.Warn("forge disabled: token unavailable",
			"forge_type", cfg.Type,
			"engine", cfg.Token.Engine,
			"key", cfg.Token.Key,
			"err", err)
		return forge.NewNoop(log), ""
	}
	if client, handled := extensions.open(ctx, cfg.Type, instance, cfg.Host, token); handled {
		return client, token
	}
	log.Warn("forge disabled: no enabled forge extension of that name",
		"forge_type", cfg.Type, "instance", instance)
	return forge.NewNoop(log), ""
}

func manualRequeueTask(ctx context.Context, st storecontract.TaskStore, taskID int64) error {
	task, err := st.TaskByID(ctx, taskID)
	if err != nil {
		return err
	}
	if task == nil {
		return fmt.Errorf("task %d not found", taskID)
	}
	switch task.Status {
	case workflow.StatusParked, workflow.StatusWaitingHuman:
		return st.Requeue(ctx, taskID, task.Status, "")
	default:
		return fmt.Errorf("task %d has status %q; only parked or waiting_human tasks can be requeued", taskID, task.Status)
	}
}

type runArgs struct {
	cfgPath     string
	overlayPath string
	once        bool
	requeue     int64
}

func parseArgs() (runArgs, bool) {
	var args runArgs
	flag.StringVar(&args.cfgPath, "config", configuration.DefaultConfigPath(), "path to a TOML/YAML config file or configuration directory")
	flag.StringVar(&args.overlayPath, "config-overlay", "", "path to a TOML/YAML overlay file or configuration directory applied on top of -config")
	flag.BoolVar(&args.once, "once", false, "run a single poll+process cycle and exit (systemd timer / testing)")
	flag.Int64Var(&args.requeue, "requeue", 0, "requeue a parked/waiting task by id (keeps its workflow), then exit unless -once is also set")
	showVersion := flag.Bool("version", false, "print the gateway and runtime versions and exit")
	flag.Parse()

	// The versions were only reachable through the chat /version command,
	// which needs a running daemon and a configured channel. An update-check
	// adapter has to answer "what is installed?" from a shell, so expose the
	// same two values here. Machine-readable: one "name version" per line.
	if *showVersion {
		fmt.Printf("archied %s\narchie-agent %s\n", buildinfo.Version, buildinfo.Runtime)
		return args, true
	}
	return args, false
}

func Run() int { //nolint:cyclop,funlen // the composition root's setup sequence is deliberately flat and sequential
	args, exit := parseArgs()
	if exit {
		return 0
	}

	if recovering, err := resumeUpdate(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	} else if recovering {
		return 0
	}

	root, cancelRoot := context.WithCancel(context.Background())
	ctx, stop := signal.NotifyContext(root, os.Interrupt, syscall.SIGTERM)
	defer stop()

	b := newBootstrap()
	b.processName = applystatus.Daemon
	// Arm the shutdown watchdog before any cleanup is registered so its
	// disarm runs last in the LIFO cleanup chain: it force-exits the process
	// as a backstop if graceful shutdown hangs past the drain-plus-grace
	// leash, and stands down once every subsystem has shut down cleanly.
	b.startShutdownWatchdog(ctx)
	// The drain monitor cancels this root to trigger a graceful shutdown, which
	// propagates through ctx exactly as an operator SIGTERM does.
	b.shutdown = cancelRoot
	if err := b.loadConfig(ctx, args.cfgPath, args.overlayPath); err != nil {
		return 1
	}
	defer b.cleanup()

	// Before any subsystem: the port answers 503 for the whole of boot, so a
	// watchdog polling a restarted daemon sees "not yet" rather than a
	// refused connection it cannot tell from a crash.
	if err := b.startHealth(ctx); err != nil {
		return 1
	}

	if err := b.openStateStage(ctx); err != nil {
		return 1
	}
	if err := b.loadRuntimeConfig(ctx); err != nil {
		b.log.Error("runtime settings unavailable", "err", err)
		return 1
	}
	b.openForge(ctx)
	if err := b.startLiveSettings(ctx); err != nil {
		b.log.Error("live settings unavailable", "err", err)
		return 1
	}
	go b.applyStatus.Run(ctx)
	if exit, err := b.handleRequeue(ctx, args.requeue, args.once); err != nil {
		return 1
	} else if exit {
		return 0
	}
	b.loadCatalog(ctx)
	b.seedSoul(args.cfgPath)
	b.setupObservability(ctx)

	if err := b.setupBackends(ctx); err != nil {
		return 1
	}

	if err := b.setupGatewayClient(); err != nil {
		return 1
	}
	if ps, ok := b.stateStore.(storecontract.PresenceStore); ok {
		go presence.Run(ctx, b.processName, servicekit.Build(), ps, b.healthRegistry, b.log)
	}
	// The daemon publishes the configuration view, which carries the catalog's
	// model limits.
	b.startModelCatalogRefresh(ctx)
	if err := b.buildTreesAndIdentities(ctx); err != nil {
		return 1
	}
	if err := b.loadWorkflows(); err != nil {
		return 1
	}

	if err := b.registerNATSRPC(); err != nil {
		return 1
	}
	if err := b.setupScheduling(); err != nil {
		return 1
	}

	b.buildDaemon()
	b.wireConfigSurfaces(ctx, args.cfgPath, args.overlayPath)

	if err := b.startServices(ctx); err != nil {
		return 1
	}
	return exitCode(b.runLoop(ctx, args.once))
}

// wireConfigSurfaces attaches the dashboard's configuration read path and
// the reload plumbing behind it. The write handlers (PATCH /api/config, the
// per-repository field update, per-row reset) are descoped for the UI
// process: the daemon still owns the validate-persist-
// publish policy, but no HTTP route reaches it -- editing is config.toml
// plus reload -- so only the snapshot publication and the override list the
// renderer needs stay wired.
func (b *boot) wireConfigSurfaces(ctx context.Context, cfgPath, overlayPath string) {
	b.wireConfigPublishing(ctx, cfgPath, overlayPath)
	// Publish once at boot. Every later change goes through publishConfig,
	// which republishes; without this the UI process would render nothing
	// until the first reload or dashboard edit.
	b.publishConfigSnapshot(ctx)
}

// persistEvents drains the bus until it closes, giving each event an ID from
// the store. The store is the only fan-out point: the UI process reads the same
// table through its event pump, so the daemon's job is persistence, not
// delivery.
//
// The insert deliberately outlives ctx: this loop ends when the bus closes,
// which is part of shutdown, and the last events of a run are exactly the
// ones an operator wants to read afterwards.
func persistEvents(ctx context.Context, sink *events.Sub, st storecontract.TaskStore, log *slog.Logger) {
	ctx = context.WithoutCancel(ctx)
	for e := range sink.C {
		if e.ID != 0 {
			continue
		}
		if _, err := st.InsertEvent(ctx, e); err != nil {
			log.Error("event sink insert failed", "err", err)
		}
	}
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
	if err != nil || port < 0 {
		return host, defaultPort
	}
	return host, port
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
		// The NATS callback has no context of its own; the write is an append to
		// an already-open sink, so there is nothing to cancel.
		taskLogs.Write(context.Background(), taskID, entry)
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
// choke point that inserts into the events table and fans out over
// SSE -- treats them exactly like every other daemon-observed event. Mirrors
// subscribeSystemLogs's demux-by-taskID shape.
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
	if err := nc.Flush(); err != nil {
		if cleanupErr := sub.Unsubscribe(); cleanupErr != nil {
			log.Warn("task event subscription cleanup failed after flush failure", "err", cleanupErr)
		}
		return nil, fmt.Errorf("flush task event subscription: %w", err)
	}
	return func() {
		if err := sub.Unsubscribe(); err != nil {
			log.Warn("task event unsubscribe failed", "err", err)
		}
	}, nil
}

// registerTaskRPCServers subscribes the forgerpc/worktreerpc handlers on nc so
// an archie-agent container (which holds no forge token, or push credential)
// can proxy those operations back to archied. The returned func unsubscribes
// all of them. The agent's store calls are NOT proxied here: they go over gRPC
// to the State Store, so the legacy NATS storerpc registration is deleted.
//
// Forge and worktree are identity-scoped: the root servers answer the root
// (identity-less) subjects for single-identity deployments and root-owned
// tasks, and one server pair per identity answers that identity's scoped
// subjects so a container-mode task owned by a non-root identity has its RPC
// calls served by its own forge client and worktree manager.
func registerTaskRPCServers(nc *natsio.Conn, forgeClient forge.Forge, trees *worktree.Manager, identities []*daemon.IdentityRunner, runs worktreerpc.Runs, log *slog.Logger) (unsubscribe func(), err error) {
	unsubs := make([]func(), 0, 2+2*len(identities))
	unsubAll := func() {
		for _, u := range unsubs {
			u()
		}
	}

	registerForge := func(fg forge.Forge, identity string) error {
		srv := &forgerpc.Server{Forge: fg, Runs: runs, Log: log.With("rpc_identity", identity)}
		u, err := srv.RegisterFor(nc, identity)
		if err != nil {
			return fmt.Errorf("register forgerpc%s: %w", identitySuffix(identity), err)
		}
		unsubs = append(unsubs, u)
		return nil
	}
	registerTrees := func(mgr *worktree.Manager, identity string) error {
		srv := &worktreerpc.Server{Trees: mgr, Runs: runs, Log: log.With("rpc_identity", identity)}
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

// updateReportPath is where the update watchdog leaves the phase-2 outcome of
// an update for this identity to relay on its next launch. The identity is
// hashed so multiple identities sharing one daemon never collide.
func updateReportPath(workDir, identity string) string {
	identityHash := sha256.Sum256([]byte(identity))
	return filepath.Join(
		workDir,
		fmt.Sprintf("update-report-%x.json", identityHash[:8]),
	)
}

// startContainers brings up the mandatory autonomous-worker pool and storage
// backend, returning a degraded nil pool when Docker is unavailable.
//
// Failure here degrades rather than aborting: the native interactive agent and
// dashboard remain useful without Docker, while autonomous tasks park because
// there is deliberately no host execution fallback.
func startContainers(
	ctx context.Context,
	cfgHolder *config.Holder,
	secrets *secret.Registry,
	log *slog.Logger,
) (*container.Pool, storage.Backend, func()) {
	noop := func() {}
	cfg := cfgHolder.Get()
	// Single Docker client shared between pool and storage backend.
	dockerCli, err := client.New(client.FromEnv)
	if err != nil {
		log.Error("autonomous workflows unavailable: docker client unavailable", "err", err)
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
		RegistryAuth:   resolveRegistryAuth(cfg.Containers.RegistryAuth, secrets, log),
		Network:        cfg.Containers.Network,
		DockerClient:   dockerCli,
		// Only the embedded broker binds a discovered host gateway. The
		// standalone State Store uses its configured, container-reachable target.
		RequireHostGateway: cfg.NATS.Mode == config.NATSModeEmbedded,
	}, containerLiveSettings(cfgHolder), log)
	if err != nil {
		// A missing image is recoverable by hand.
		log.Error("autonomous workflows unavailable: container pool unavailable", "err", err,
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

// containerLiveSettings reads the per-acquire container settings from the
// running config. The pool calls it on every acquire, so a stored
// container-runtime-policies change reaches the next container without a
// restart. Only fields the resource document carries are taken live:
// RegistryAuth is file-owned and stays the boot-resolved value, DockerClient
// and RequireHostGateway are construction-time, and GracePeriod is not part
// of the document.
func containerLiveSettings(cfgHolder *config.Holder) func() container.Config {
	return func() container.Config {
		cfg := cfgHolder.Get()
		return container.Config{
			Image:          cfg.Containers.Image,
			MaxConcurrency: cfg.Containers.MaxConcurrency,
			MaxUptime:      cfg.Containers.MaxUptime.Std(),
			PullPolicy:     cfg.Containers.PullPolicy,
			Network:        cfg.Containers.Network,
		}
	}
}

// resolveRegistryAuth resolves [containers].registry_auth into the credential
// the container pool sends on pull. It returns "" -- an anonymous pull, the
// pool's behaviour before the field existed -- whenever the credential is not
// configured or cannot be resolved, because a missing registry credential must
// not stop autonomous work: the pool only needs it for a private registry. The
// warning is what makes the 401 that a private registry will still answer
// explicable after a typo or an unset secret. A ref that names only one half is
// a config error caught by configuration.Validate, not a missing credential.
func resolveRegistryAuth(ref secret.SecretRef, secrets *secret.Registry, log *slog.Logger) string {
	if ref == (secret.SecretRef{}) {
		return ""
	}
	if secrets == nil {
		log.Warn("registry credential unavailable; pulling without auth", "reason", "no secret registry", "engine", ref.Engine, "key", ref.Key)
		return ""
	}
	value, err := secrets.Resolve(ref)
	if err != nil {
		log.Warn("registry credential unavailable; pulling without auth", "err", err, "engine", ref.Engine, "key", ref.Key)
		return ""
	}
	if strings.TrimSpace(value) == "" {
		log.Warn("registry credential resolved empty; pulling without auth", "engine", ref.Engine, "key", ref.Key)
		return ""
	}
	return value
}
