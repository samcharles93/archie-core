package archiemessaging

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/samcharles93/archie-core/internal/channels/telegram"
	"github.com/samcharles93/archie-core/internal/domain/messaging"
	"github.com/samcharles93/archie-core/internal/installtype"
	"github.com/samcharles93/archie-core/internal/releaseupdate"
)

// configureTelegram wires the operator seams telegram.Gateway leaves to its
// composition root. Which process may satisfy each one is settled in
// docs/architecture/migration-decisions.md ("Telegram operator surface after
// extraction"); notably RunningVersions, ReleaseAnnouncements and Dangerous
// stay nil deliberately, because nothing this process can observe would make
// them true.
func configureTelegram(ctx context.Context, g *telegram.Gateway, cfg ResolvedConfig, chat messaging.ChatContract, log *slog.Logger) {
	g.Version = gatewayVersionReporter(ctx, chat, cfg.Options.DependencyTimeout)
	g.SetShowToolCalls(cfg.ShowToolCalls)
	g.Reload = telegramReloader(cfg.Options, log)

	if updates := updateService(cfg); updates != nil {
		g.Updates = updates
	}
	if cfg.WorkDir != "" {
		g.UpdateReportPath = identityStatePath(cfg.WorkDir, "update-report", cfg.BotUser)
	}
}

// gatewayVersionReporter renders /version from the Gateway's own build block.
// It is read per call rather than captured at compose so an upgraded Gateway
// is reported without restarting this process, and bounded by the same
// dependency timeout the readiness probe uses so a hung Gateway answers the
// command instead of leaving the operator with no reply at all. The service
// context bounds it too, so a shutdown does not wait on an in-flight call.
func gatewayVersionReporter(ctx context.Context, chat messaging.ChatContract, timeout time.Duration) func() string {
	return func() string {
		callCtx := ctx
		if timeout > 0 {
			var cancel context.CancelFunc
			callCtx, cancel = context.WithTimeout(callCtx, timeout)
			defer cancel()
		}
		snapshot, err := chat.Snapshot(callCtx)
		if err != nil {
			return "Version information is unavailable: the Gateway could not be reached."
		}
		if snapshot.Version == "" {
			return "Version information is unavailable: the Gateway reported no build."
		}
		return snapshot.Version
	}
}

// updateService builds /update from this service's own [chat.telegram]
// commands, or nil when none is configured, which leaves the channel reporting
// the command as unconfigured.
//
// Enrich is deliberately unset: the daemon filled it from [nats], which this
// service must not decode, so the check command's own reported install type
// stands.
func updateService(cfg ResolvedConfig) *releaseupdate.Service {
	if len(cfg.Telegram.UpdateCheckCommand) == 0 {
		return nil
	}
	updates := &releaseupdate.Service{
		Catalog:     releaseupdate.CommandCatalog{Command: cfg.Telegram.UpdateCheckCommand},
		StatePath:   filepath.Join(cfg.WorkDir, "telegram-update-deferrals.json"),
		InstallType: installtype.Type(),
	}
	if len(cfg.Telegram.UpdateInstallCommand) != 0 {
		updates.Installer = releaseupdate.CommandInstaller{
			Command:   cfg.Telegram.UpdateInstallCommand,
			HealthURL: cfg.HealthURL,
		}
	}
	return updates
}

// telegramReloader re-resolves the token and allowlist from this service's own
// configuration on /restart. A reload that cannot produce a token is refused so
// the running bot keeps the credential it is already authenticated with.
func telegramReloader(o Options, log *slog.Logger) func(*telegram.Gateway) error {
	return func(g *telegram.Gateway) error {
		reloaded, err := Resolve(o, log)
		if err != nil {
			return fmt.Errorf("reload config: %w", err)
		}
		if reloaded.TelegramToken == "" {
			return fmt.Errorf("reload config: chat.telegram token is empty")
		}
		g.Token = reloaded.TelegramToken
		g.AllowedUserIDs = reloaded.Telegram.AllowedUserIDs
		g.SetShowToolCalls(reloaded.ShowToolCalls)
		log.Info("telegram config reloaded",
			"allowed_user_ids", len(g.AllowedUserIDs),
			"show_tool_calls", g.ShowToolCalls())
		return nil
	}
}

// identityStatePath names a per-identity state file under workDir. The identity
// is hashed so several identities sharing one work directory (see
// docs/architecture/identity.md) never collide. The scheme is the daemon's,
// unchanged: a different path would replay every release announcement the
// operator has already been shown.
func identityStatePath(workDir, kind, identity string) string {
	identityHash := sha256.Sum256([]byte(identity))
	return filepath.Join(workDir, fmt.Sprintf("%s-%x.json", kind, identityHash[:8]))
}
