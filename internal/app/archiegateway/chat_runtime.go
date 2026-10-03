package archiegateway

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/agentrun"

	"github.com/samcharles93/ai-sdk/chat"
	"github.com/samcharles93/ai-sdk/core"
	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/app/servicekit"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/gateway"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres"
	"github.com/samcharles93/archie-core/internal/infrastructure/transcription"
	"github.com/samcharles93/archie-core/internal/ratelimit"
	"github.com/samcharles93/archie-core/internal/tools"
)

func (b *server) openChatSessions(ctx context.Context) error {
	pool, err := servicekit.OpenPool(ctx, b.cfg.DatabaseURL, "the conversation store")
	if err != nil {
		return fmt.Errorf("open conversation store: %w", err)
	}
	chatSessionStore := gateway.NewPostgresSessionStore(pool)
	b.chatPool = pool
	b.chatSessionStore = chatSessionStore
	b.addCleanup(func() {
		if err := chatSessionStore.Close(); err != nil {
			b.log.Error("close conversation store", "err", err)
		}
	})
	return nil
}

func (b *server) claimGatewayOwnership(ctx context.Context) error {
	ownership, err := postgres.AcquireOwnership(ctx, b.chatPool, postgres.OwnerGateway)
	if err != nil {
		return fmt.Errorf("claim gateway ownership: %w", err)
	}
	releaseCtx := context.WithoutCancel(ctx)
	b.addCleanup(func() {
		if err := ownership.Release(releaseCtx); err != nil {
			b.log.Error("release gateway ownership", "err", err)
		}
	})
	return nil
}

func (b *server) setupTranscriber(cfg config.Config, log *slog.Logger) {
	client, ok := transcription.New(cfg.Models, cfg.Providers, transcription.Options{
		ResolveSecret: b.secrets.Resolve,
	})
	if ok {
		b.transcriber = client
		log.Info("voice transcription enabled", "role", transcription.Role)
	} else if cfg.Models[transcription.Role] != "" {
		log.Warn("voice transcription configured but unavailable; capability disabled", "role", transcription.Role)
	}
}

func (b *server) startRateLimiter(ctx context.Context, cfg config.RateLimitConfig) {
	if !cfg.Enabled() {
		return
	}
	b.rateLimiter = ratelimit.New(cfg.Window, cfg.MaxRequests)
	limiter := b.rateLimiter
	go func() {
		ticker := time.NewTicker(rateLimiterEvictInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				limiter.EvictStale()
			}
		}
	}()
}

// rebuildChatModelRuntime re-derives the gateway chat runtime's provider set
// after a live change to provider-settings or model-role-assignments. The
// turn runner reads the runtime through server.chatLLM, so the swap reaches its
// next turn without rebuilding the runner, and the model manager re-derives
// the references it offers from the new role assignments and the catalog.
func (b *server) rebuildChatModelRuntime(cfg config.Config) {
	if b.chatModels == nil {
		return // this process serves no chat turns
	}
	b.setLLM(agentexec.NewRuntime(executionProviders(cfg)))
	b.chatModels.SetConfigured(cfg.Models)
	catalog, models := b.catalogState()
	b.chatModels.SetModelCatalog(catalog, models)
	b.log.Info("chat model runtime rebuilt", "providers", len(cfg.Providers), "models", len(cfg.Models))
}

// sessionKey builds a deterministic session identifier from a gateway
// message's routing fields. Platform + channel + thread uniquely identify
// a conversation for session persistence and history retrieval.
func executionProviders(cfg config.Config) map[string]agentrun.Provider {
	return agentexec.ProvidersFromConfig(cfg.Providers)
}

// chatGenerateOptions builds one chat turn's request.
func chatGenerateOptions(
	ctx context.Context,
	messages []chat.Message,
	registry *tools.Registry,
	maxSteps int,
	limits agentexec.ToolLimits,
	extra []tools.ToolEntry,
	contextWindow int,
) (core.GenerateOptions, error) {
	toolOpts := limits.Options()
	if approval := gateway.ApprovalFromContext(ctx); approval != nil {
		toolOpts.Approval = approval
	}

	// Progressive tool disclosure: compose the base registry with the per-turn
	// extras into a turn-local registry (extras are identity-bound and cannot
	// live in the process-wide registry), then let the ContextPressureGate
	// decide whether to serve the full arsenal directly or fall back to the
	// bridge tools. The bridge tools are never registered process-wide, so they
	// stay inside this composed, per-turn registry.
	composed, err := tools.ComposeForTurn(registry, extra)
	if err != nil {
		return core.GenerateOptions{}, err
	}
	gate := tools.NewContextPressureGate(contextWindow)
	gate.Evaluate(composed.All())
	toolSet, err := agentexec.BuildToolSetFrom(gate.FilterTools(composed), toolOpts)
	if err != nil {
		return core.GenerateOptions{}, err
	}

	if maxSteps <= 0 {
		maxSteps = defaultChatMaxSteps
	}
	return core.GenerateOptions{
		Messages: messages,
		Tools:    toolSet,
		MaxSteps: maxSteps,
	}, nil
}

const defaultChatMaxSteps = 100

const rateLimiterEvictInterval = time.Minute
