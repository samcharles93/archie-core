package archiemessaging

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/samcharles93/archie-core/internal/buildinfo"
	"github.com/samcharles93/archie-core/internal/channels/telegram"
	"github.com/samcharles93/archie-core/internal/domain/messaging"
	"github.com/samcharles93/archie-core/internal/installtype"
	"github.com/samcharles93/archie-core/internal/releaseupdate"
)

// configureTelegram wires the operator seams telegram.Gateway leaves to its
// composition root. RunningVersions is supplied here from this process's own
// build stamp; see messagingRunningVersions.
func configureTelegram(ctx context.Context, g *telegram.Gateway, cfg ResolvedConfig, chat messaging.ChatContract, log *slog.Logger) {
	g.Version = gatewayVersionReporter(ctx, chat, cfg.Options.DependencyTimeout)
	g.SetShowToolCalls(cfg.ShowToolCalls)
	g.Reload = telegramReloader(cfg.Options, log)
	g.RunningVersions = messagingRunningVersions

	if updates := updateService(cfg); updates != nil {
		g.Updates = updates
	}
	if cfg.WorkDir != "" {
		g.UpdateReportPath = identityStatePath(cfg.WorkDir, "update-report", cfg.BotUser)
	}
}

// messagingRunningVersions reports this process's own build version for
// update verification.
func messagingRunningVersions() map[string]string {
	return map[string]string{releaseupdate.ComponentDaemon: buildinfo.Version}
}

// gatewayVersionReporter renders /version from the Gateway's build info,
// fetched per call with a timeout.
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

// updateService builds /update from [chat.telegram] commands, or nil when
// none are configured.
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
// is hashed so several identities sharing one work directory never collide. The
// scheme is the daemon's, unchanged: a different path would replay every
// release announcement the operator has already been shown.
func identityStatePath(workDir, kind, identity string) string {
	identityHash := sha256.Sum256([]byte(identity))
	return filepath.Join(workDir, fmt.Sprintf("%s-%x.json", kind, identityHash[:8]))
}
