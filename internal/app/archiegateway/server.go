package archiegateway

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	natsio "github.com/nats-io/nats.go"

	"github.com/samcharles93/ai-sdk/runtime"
	"github.com/samcharles93/archie-core/internal/app/controlplane"
	"github.com/samcharles93/archie-core/internal/app/servicekit"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/applystatus"
	"github.com/samcharles93/archie-core/internal/domain/curator"
	domainmemory "github.com/samcharles93/archie-core/internal/domain/memory"
	"github.com/samcharles93/archie-core/internal/domain/messaging"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/gateway"
	"github.com/samcharles93/archie-core/internal/infrastructure/controlplanerpc"
	"github.com/samcharles93/archie-core/internal/infrastructure/modelcatalog"
	"github.com/samcharles93/archie-core/internal/infrastructure/secretengine"
	"github.com/samcharles93/archie-core/internal/infrastructure/staterpc"
	"github.com/samcharles93/archie-core/internal/infrastructure/taskactions"
	"github.com/samcharles93/archie-core/internal/logging"
	"github.com/samcharles93/archie-core/internal/plugin"
	"github.com/samcharles93/archie-core/internal/ratelimit"
	"github.com/samcharles93/archie-core/internal/releaseupdate"
	"github.com/samcharles93/archie-core/internal/sdnotify"
	"github.com/samcharles93/archie-core/internal/secret"
	"github.com/samcharles93/archie-core/internal/tools"
	toolprovider "github.com/samcharles93/archie-core/internal/tools/provider"
)

// Options contains process inputs for the standalone Gateway.
type Options struct {
	Config  string
	Overlay string
	// Listen is the gRPC listen address. Empty falls back to
	// [services.gateway].listen.
	Listen string
	// Token is the bearer token a non-loopback listener requires. Empty falls
	// back to [services.gateway].target_token.
	Token string
}

// server is the Gateway process: the owner of conversations and of every
// model call.
type server struct {
	cfg       config.Config
	cfgHolder *config.Holder
	log       *slog.Logger
	taskLogs  *logging.TaskRegistry

	secrets          *secret.Registry
	stateStore       *staterpc.Client
	controlPlane     *controlplane.Client
	applyStatus      *applystatus.Reporter
	runtimeVersions  map[string]int64
	catalog          *servicekit.Catalog
	chatPool         *pgxpool.Pool
	chatSessionStore gateway.SessionStore
	taskActionsConn  *natsio.Conn

	bus              *events.Bus
	capabilityHost   *plugin.Host
	rateLimiter      *ratelimit.Limiter
	llm              atomic.Pointer[runtime.Runtime]
	transcriber      messaging.Transcriber
	toolReg          *tools.Registry
	providerRegistry *toolprovider.Registry
	mcpMu            sync.Mutex
	mcpApplied       map[string]appliedMCPServer
	chatModels       *chatModelManager
	personas         *gateway.PersonaRegistry
	memEngines       *domainmemory.Registry
	curatorRegistry  *curator.Registry
	curatorRuntime   *curator.Runtime
	providerOutcomes *providerOutcomeRecorder
	statusHealth     gateway.HealthSource
	updateService    *releaseupdate.Service

	chatTasks           gateway.TaskCreator
	chatController      *gateway.StoreTaskController
	defaultChatIdentity string

	cleanups []func()
}

// Run serves the Gateway's chat contract until ctx ends.
func Run(ctx context.Context, options Options) error {
	b := &server{log: slog.New(slog.NewJSONHandler(os.Stderr, nil))}
	defer b.cleanup()
	if err := b.loadConfig(options.Config, options.Overlay); err != nil {
		return err
	}
	if err := b.openState(ctx); err != nil {
		return err
	}
	b.loadCatalog(ctx, options.Config)
	nc, err := b.connectNATS(ctx)
	if err != nil {
		return err
	}
	contract, err := b.startGatewayRuntime(ctx, taskactions.Client{Conn: nc, Timeout: 30 * time.Second})
	if err != nil {
		return err
	}
	listen, err := servicekit.ResolveListen("gateway", options.Listen, b.cfg.Services.Get(config.ServiceNameGateway).Listen)
	if err != nil {
		return err
	}
	token := options.Token
	if token == "" {
		token = b.cfg.Services.ResolvedToken(config.ServiceNameGateway, b.secrets.Getenv)
	}
	//nolint:contextcheck // grpc.StreamServerInterceptor has no context.Context parameter; gatewayrpc.StreamServerInterceptor derives its context from stream.Context() instead
	opts, loopback, err := gatewayServerOpts(listen, token)
	if err != nil {
		return err
	}
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", listen)
	if err != nil {
		return fmt.Errorf("listen for gateway: %w", err)
	}
	return b.serveGatewayListener(ctx, listener, loopback, contract, opts)
}

func (b *server) addCleanup(fn func()) { b.cleanups = append(b.cleanups, fn) }

func (b *server) cleanup() {
	for _, fn := range slices.Backward(b.cleanups) {
		fn()
	}
}

func (b *server) chatLLM() *runtime.Runtime  { return b.llm.Load() }
func (b *server) setLLM(rt *runtime.Runtime) { b.llm.Store(rt) }
func (b *server) announceReady()             { sdnotify.Ready(b.log) }

func (b *server) loadConfig(cfgPath, overlayPath string) error {
	_, doc, err := servicekit.Resolve(b.log, cfgPath, overlayPath)
	if err != nil {
		return err
	}
	b.cfg = doc.Config
	b.cfgHolder = config.NewHolder(b.cfg)
	logs := servicekit.Logging(b.cfg, "gateway")
	b.log, b.taskLogs = logs.Log, logs.TaskLogs
	b.addCleanup(func() { _ = logs.Closer.Close() })
	b.secrets = secret.NewRegistry()
	return nil
}

// openState claims the conversation store, dials the State Store, starts the
// enabled secret engines and layers the stored runtime settings.
func (b *server) openState(ctx context.Context) error {
	if err := b.openChatSessions(ctx); err != nil {
		return err
	}
	if err := b.claimGatewayOwnership(ctx); err != nil {
		return err
	}
	client, cleanup, err := servicekit.StateStoreClient(b.cfg.Services, b.secrets)
	if err != nil {
		return fmt.Errorf("state store client: %w", err)
	}
	b.addCleanup(cleanup)
	b.stateStore = client
	b.controlPlane = controlplane.NewRPCClient(client.ControlPlane())
	b.applyStatus = applystatus.New(applystatus.Gateway, client, b.log)
	source := secretengine.Source{Query: b.controlPlane, Packages: client}
	report := func(ctx context.Context, version int64, err error) {
		b.applyStatus.Report(ctx, controlplanerpc.ExtensionSettingsKind, version, err)
	}
	b.addCleanup(secretengine.Supervise(ctx, b.secrets, source, applystatus.Gateway, report, b.log).Close)
	cfg, versions, err := b.runtimeConfig(ctx, b.cfg)
	if err != nil {
		return fmt.Errorf("load runtime settings: %w", err)
	}
	b.cfg, b.runtimeVersions = cfg, versions
	b.cfgHolder.Set(cfg)
	go b.applyStatus.Run(ctx)
	return nil
}

func (b *server) runtimeConfig(ctx context.Context, base config.Config) (config.Config, map[string]int64, error) {
	return servicekit.RuntimeConfig(ctx, b.controlPlane, b.applyStatus, b.secrets, b.log, base, nil)
}

// connectNATS joins the broker task actions travel over. The daemon owns the
// embedded broker, so the Gateway waits for its endpoint rather than starting
// a second one.
func (b *server) connectNATS(ctx context.Context) (*natsio.Conn, error) {
	url, token := b.cfg.NATS.URL, ""
	if b.cfg.NATS.Mode == config.NATSModeExternal {
		var err error
		if token, err = servicekit.NATSToken(b.cfg.NATS, os.Getenv); err != nil {
			return nil, err
		}
	} else {
		endpoint, err := b.waitForNATSEndpoint(ctx)
		if err != nil {
			return nil, err
		}
		url, token = endpoint.URL, endpoint.Token
	}
	nc, err := natsio.Connect(url, natsio.Token(token), natsio.MaxReconnects(-1))
	if err != nil {
		return nil, fmt.Errorf("connect gateway task actions: %w", err)
	}
	b.addCleanup(nc.Close)
	b.taskActionsConn = nc
	return nc, nil
}

func (b *server) waitForNATSEndpoint(ctx context.Context) (servicekit.NATSEndpoint, error) {
	for {
		endpoint, err := servicekit.ReadNATSEndpoint(b.cfg.StateDir)
		if err == nil {
			return endpoint, nil
		}
		b.log.Warn("waiting for the daemon's NATS endpoint", "err", err)
		if !servicekit.WaitFor(ctx, 2*time.Second) {
			return servicekit.NATSEndpoint{}, ctx.Err()
		}
	}
}

func (b *server) catalogState() (modelcatalog.Snapshot, []string) { return b.catalog.State() }

// loadCatalog layers the model catalog under the running config. A catalog
// that cannot be read leaves the configured providers and models in effect.
func (b *server) loadCatalog(ctx context.Context, cfgPath string) {
	b.catalog = servicekit.NewCatalog(cfgPath)
	snapshot, err := b.catalog.Fetch(ctx, b.secrets.Getenv, b.cfg.Providers)
	if err != nil {
		b.log.Warn("model catalog unavailable; using configured providers and models", "err", err)
		return
	}
	models := servicekit.ApplyModelCatalog(&b.cfg, snapshot)
	b.cfgHolder.Set(b.cfg.Clone())
	b.catalog.Set(snapshot, models)
}

func (b *server) startModelCatalogRefresh(ctx context.Context) {
	servicekit.Every(ctx, servicekit.CatalogRefreshInterval, func() {
		snapshot, err := b.catalog.Fetch(ctx, b.secrets.Getenv, b.cfgHolder.Get().Providers)
		if err != nil {
			b.log.Warn("model catalog refresh failed; keeping the loaded catalog", "err", err)
			return
		}
		base := b.cfgHolder.Get().Clone()
		models := servicekit.ApplyModelCatalog(&base, snapshot)
		b.catalog.Set(snapshot, models)
		b.relayer(ctx, base, "")
	})
}

// gatewayRuntimeKinds are the kinds the Gateway applies live: the settings its
// chat runtime and tool providers are built from.
var gatewayRuntimeKinds = []string{
	controlplane.ProviderSettingsKind,
	controlplane.ModelRoleAssignmentsKind,
	controlplane.ToolSettingsKind,
}

func (b *server) startRuntimeWatches(ctx context.Context) error {
	for _, kind := range gatewayRuntimeKinds {
		updates, err := b.controlPlane.WatchResource(ctx, kind, b.runtimeVersions[kind])
		if err != nil {
			return fmt.Errorf("watch %s: %w", kind, err)
		}
		go servicekit.KeepWatch(ctx, b.log, kind, b.runtimeVersions[kind], updates,
			func(ctx context.Context, afterVersion int64) (<-chan controlplane.AppliedResource, error) {
				return b.controlPlane.WatchResource(ctx, kind, afterVersion)
			},
			servicekit.WaitFor,
			func(update controlplane.AppliedResource) int64 { return update.Version },
			func(update controlplane.AppliedResource) {
				if update.Err != nil {
					b.log.Error("runtime settings watch failed", "kind", kind, "err", update.Err)
					return
				}
				base := b.cfgHolder.Get()
				catalog, _ := b.catalogState()
				servicekit.ApplyModelCatalog(&base, catalog)
				b.relayer(ctx, base, kind)
			})
	}
	return nil
}

// relayer re-runs the runtime layering over base and applies the result to the
// chat runtime and, for tool settings, the tool providers. A refused document
// leaves the running settings in place.
func (b *server) relayer(ctx context.Context, base config.Config, kind string) {
	cfg, versions, err := b.runtimeConfig(ctx, base)
	if err != nil {
		b.log.Error("runtime settings update rejected; the running settings stay", "kind", kind, "err", err)
		return
	}
	b.cfgHolder.Set(cfg)
	b.rebuildChatModelRuntime(cfg)
	if kind == controlplane.ToolSettingsKind {
		if err := b.reconcileToolSettings(ctx, cfg); err != nil {
			b.applyStatus.Report(ctx, kind, versions[kind], err)
			b.log.Error("tool settings reconciled with errors; the running servers stay", "err", err)
		}
	}
}
