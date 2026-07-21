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

	"github.com/samcharles93/ai-sdk/runtime"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/daemon"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/forge"
	"github.com/samcharles93/archie-core/internal/store"
	"github.com/samcharles93/archie-core/internal/webui"
	"github.com/samcharles93/archie-core/internal/workflow"
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
	forgeClient, err := forge.New(token, cfg.Forge.Host, log)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Error("open store", "err", err)
		return 1
	}
	defer st.Close()

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

	llm := llmRuntime(cfg)
	var agentRunner agentexec.Runner
	if llm != nil {
		agentRunner = agentexec.NewInProcessRunner(llm, log)
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
		Runtime: llm,
		Agent:   agentRunner,
		Workflows: workflow.Registry{
			"bootstrap":   workflow.Bootstrap(),
			"implement":   workflow.Implement(),
			"tdd":         workflow.TDD(),
			"feasibility": workflow.Feasibility(),
			"default":     workflow.Implement(),
		},
		Log: log,
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

// llmRuntime builds the ai-sdk runtime from the [providers] config.
// Nil when no providers are configured — agent workflows then park with
// a clear error while deterministic workflows keep working.
func llmRuntime(cfg config.Config) *runtime.Runtime {
	if len(cfg.Providers) == 0 {
		return nil
	}
	runtime.RegisterBuiltinClasses()
	providers := make(map[string]runtime.ProviderConfig, len(cfg.Providers))
	for name, p := range cfg.Providers {
		pc := runtime.ProviderConfig{ID: name, Class: p.Class, BaseURL: p.BaseURL}
		if p.APIKeyEnv != "" {
			pc.Auth = runtime.AuthConfig{APIKeyEnv: p.APIKeyEnv}
		} else {
			pc.Auth = runtime.AuthConfig{Type: runtime.AuthTypeNone}
		}
		providers[name] = pc
	}
	return runtime.NewRuntime(runtime.Config{Providers: providers})
}

func configHome() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return x
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config")
}
