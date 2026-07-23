// Command archied is the archie orchestrator daemon: it watches GitHub
// for issues labelled for archie, works each one in an isolated
// worktree through its routed workflow, and opens pull requests for
// human review.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/moby/moby/client"
	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/container"
	"github.com/samcharles93/archie-core/internal/daemon"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/forge"
	"github.com/samcharles93/archie-core/internal/nats"

	"github.com/samcharles93/archie-core/internal/plugin"
	"github.com/samcharles93/archie-core/internal/plugin/pluginextract"
	"github.com/samcharles93/archie-core/internal/storage"
	"github.com/samcharles93/archie-core/internal/store"
	"github.com/samcharles93/archie-core/internal/webui"
	"github.com/samcharles93/archie-core/internal/workflow/skillbuild"
	"github.com/samcharles93/archie-core/internal/workflow/wfeval"
	"github.com/samcharles93/archie-core/internal/worktree"
)

func main() {
	os.Exit(run())
}

func run() int {
	defaultCfg := filepath.Join(configHome(), "archie", "config.toml")
	cfgPath := flag.String("config", defaultCfg, "path to config.toml")
	once := flag.Bool("once", false, "run a single poll+process cycle and exit (systemd timer / testing)")
	requeue := flag.Int64("requeue", 0, "requeue a parked/waiting task by id (keeps its workflow), then exit unless -once is also set")
	flag.Parse()

	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	token := os.Getenv(cfg.Forge.TokenEnv)
	if token == "" {
		fmt.Fprintln(os.Stderr, cfg.Forge.TokenEnv+" is required")
		return 1
	}
	forgeClient, err := forge.New(cfg.Forge.Type, token, cfg.Forge.Host, log)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Error("open store", "err", err)
		return 1
	}
	defer func() {
		if err := st.Close(); err != nil {
			log.Error("close store", "err", err)
		}
	}()

	if *requeue > 0 {
		if err := st.Requeue(context.Background(), *requeue, "manual", ""); err != nil {
			log.Error("requeue failed", "task", *requeue, "err", err)
			return 1
		}
		log.Info("task requeued", "task", *requeue)
		if !*once {
			return 0
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Observability: every event is logged to SQLite (stamped with its
	// row id) and then fanned out to live dashboard connections.
	bus := events.NewBus()
	defer bus.Close()
	web := &webui.Server{Store: st, Log: log}
	sink := bus.Subscribe(256)
	go func() {
		for e := range sink.C {
			id, err := st.InsertEvent(context.Background(), e)
			if err != nil {
				log.Error("event sink insert failed", "err", err)
				continue
			}
			e.ID = id
			web.Broadcast(e)
		}
	}()
	if l := cfg.Web.Listen; l != "" && l != "off" {
		go func() {
			if err := web.Run(ctx, l); err != nil {
				log.Error("web ui failed", "err", err)
			}
		}()
	}

	// NATS client (optional — SQLite flow unchanged when [nats] is absent).
	var natsClient *nats.Client
	if cfg.NATS.URL != "" {
		natsToken, err := configuredNATSToken(cfg.NATS, os.Getenv)
		if err != nil {
			log.Error("nats credentials", "err", err)
			return 1
		}
		natsClient, err = nats.Connect(ctx, cfg.NATS.URL, natsToken, log)
		if err != nil {
			log.Error("nats connect failed", "err", err)
			return 1
		}
		defer natsClient.Close()
		log.Info("nats connected", "url", cfg.NATS.URL)
	}

	// Container pool (optional — no containers when [containers] is absent).
	var containerPool *container.Pool
	var storeBackend storage.Backend
	if cfg.Containers.Enabled {
		// Single Docker client shared between pool and storage backend.
		dockerCli, err := client.New(client.FromEnv)
		if err != nil {
			log.Error("docker client failed", "err", err)
			return 1
		}
		defer dockerCli.Close()

		containerPool, err = container.NewPool(ctx, container.Config{
			Image:          cfg.Containers.Image,
			MaxConcurrency: cfg.Containers.MaxConcurrency,
			MaxUptime:      cfg.Containers.MaxUptime.Std(),
			PullPolicy:     cfg.Containers.PullPolicy,
			DockerClient:   dockerCli,
		}, cfg.NATS.URL, log)
		if err != nil {
			log.Error("container pool failed", "err", err)
			return 1
		}
		defer containerPool.Close()

		storeBackend = storage.NewDockerBackend(dockerCli)
	}

	providers := executionProviders(cfg)
	llm := agentexec.NewRuntime(providers)
	var agentRunner agentexec.Runner
	if llm != nil {
		switch cfg.Agent.Mode {
		case "subprocess":
			agentRunner = &agentexec.SubprocessRunner{
				Command:       cfg.Agent.Command,
				Environ:       os.Environ(),
				AdditionalEnv: cfg.Agent.Env,
				Diagnostics:   os.Stderr,
				Providers:     providers,
			}
		case "inprocess":
			agentRunner = agentexec.NewInProcessRunner(llm, log)
		case "nats":
			if natsClient == nil {
				log.Error("agent.mode is nats but [nats] is not configured")
				return 1
			}
			agentRunner = &agentexec.NATSRunner{
				Nats:      natsClient,
				Providers: providers,
				Log:       log,
			}
		}
	}
	// Build the workflow registry from the skill catalog. Plugin-defined
	// workflows override built-ins of the same name; built-ins fill gaps.
	skillsBase := cfg.SkillsDir
	if skillsBase == "" {
		skillsBase = cfg.WorkDir
	}
	registry, err := skillbuild.BuildRegistry(skillsBase)
	if err != nil {
		log.Error("skill registry build failed", "err", err)
		return 1
	}
	log.Info("workflow registry built", "workflows", len(registry))

	// Load daemon plugins from the configured plugin directory (Layer 2).
	// Failed plugins are skipped — the daemon starts with the remaining set.
	var pluginReg *plugin.Registry
	if cfg.PluginDir != "" {
		plugins, err := plugin.LoadDir(cfg.PluginDir, pluginextract.Symbols)
		if err != nil {
			log.Error("plugin load failed", "dir", cfg.PluginDir, "err", err)
			return 1
		}
		pluginReg = &plugin.Registry{}
		for _, p := range plugins {
			pluginReg.Register(p)
			name, version := safePluginInfo(p)
			log.Info("daemon plugin loaded", "name", name, "version", version)
		}
	}

	d := &daemon.Daemon{
		Cfg:   cfg,
		Store: st,
		Bus:   bus,
		Forge: forgeClient,
		Trees: &worktree.Manager{
			WorkDir:  cfg.WorkDir,
			Token:    token,
			BotUser:  cfg.BotUser,
			BotEmail: cfg.BotEmail,
			BaseURL:  cfg.Forge.Host,
		},
		Runtime:        llm,
		Agent:          agentRunner,
		Workflows:      registry,
		PluginRegistry: pluginReg,
		Storage:        storeBackend,
		Log:            log,
		CustomStages:   wfeval.Discover,
		Nats:           natsClient,
		ContainerPool:  containerPool,
	}

	if err := d.Startup(ctx); err != nil {
		log.Error("startup", "err", err)
		return 1
	}

	if *once {
		d.Cycle(ctx)
		return 0
	}
	log.Info("archied running", "repos", len(cfg.Repos), "poll", cfg.PollInterval.Std().String(), "label", cfg.Label)
	if err := d.Run(ctx); err != nil && ctx.Err() == nil {
		log.Error("daemon exited", "err", err)
		return 1
	}
	return 0
}

func executionProviders(cfg config.Config) map[string]agentexec.Provider {
	providers := make(map[string]agentexec.Provider, len(cfg.Providers))
	for name, p := range cfg.Providers {
		providers[name] = agentexec.Provider{Class: p.Class, APIKeyEnv: p.APIKeyEnv, BaseURL: p.BaseURL}
	}
	return providers
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
