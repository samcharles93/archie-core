package archiemessaging

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	configtemplate "github.com/samcharles93/archie-core"
	"github.com/samcharles93/archie-core/internal/buildinfo"
	"github.com/samcharles93/archie-core/internal/channels/telegram"
	"github.com/samcharles93/archie-core/internal/domain/messaging"
	"github.com/samcharles93/archie-core/internal/domain/presence"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/releaseannounce"
	"github.com/samcharles93/archie-core/internal/releaseupdate"
	"github.com/samcharles93/archie-core/internal/secret"
)

// configureTelegram wires the operator seams telegram.Gateway leaves to its
// composition root.
func configureTelegram(ctx context.Context, g *telegram.Gateway, cfg ResolvedConfig, d deps) {
	g.Version = gatewayVersionReporter(ctx, d.Chat, cfg.Options.DependencyTimeout)
	g.SetShowToolCalls(cfg.ShowToolCalls)
	g.Reload = telegramReloader(cfg.Options, d.Secrets, d.Log)
	g.RunningVersions = runningVersions(ctx, d.Presence, cfg.Options.DependencyTimeout)

	if d.Updates != nil {
		g.Updates = d.Updates
	}
	if cfg.WorkDir != "" {
		g.UpdateReportPath = identityStatePath(cfg.WorkDir, "update-report", cfg.BotUser)
		g.ReleaseAnnouncements = &releaseannounce.Announcer{
			StatePath: identityStatePath(cfg.WorkDir, "release-announcements", cfg.BotUser),
			Components: []releaseannounce.Component{
				{ID: "archie", Label: "ARCHIE", Version: buildinfo.Version, Changelog: configtemplate.Changelog},
			},
		}
	}
}

// runningVersions reports the daemon version archied's own presence record
// carries, so an update report is checked against what archied compiled in
// rather than this process's build. A down or unreachable daemon, or a
// missing store, leaves the daemon unverified. The agent stays unverified:
// only archied observes it.
func runningVersions(ctx context.Context, store storecontract.PresenceStore, timeout time.Duration) func() map[string]string {
	return func() map[string]string {
		if store == nil {
			return nil
		}
		if timeout <= 0 {
			timeout = 5 * time.Second
		}
		callCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		records, err := store.ListPresence(callCtx)
		if err != nil {
			return nil
		}
		for _, service := range presence.Mesh(records, time.Now()) {
			if service.Service == presence.Daemon && service.State != presence.StateDown {
				return map[string]string{releaseupdate.ComponentDaemon: service.Version}
			}
		}
		return nil
	}
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

// telegramReloader re-resolves the token and allowlist from this service's own
// configuration on /restart. A reload that cannot produce a token is refused so
// the running bot keeps the credential it is already authenticated with.
func telegramReloader(o Options, secrets *secret.Registry, log *slog.Logger) func(*telegram.Gateway) error {
	return func(g *telegram.Gateway) error {
		reloaded, err := Resolve(o, log)
		if err != nil {
			return fmt.Errorf("reload config: %w", err)
		}
		if err := reloaded.resolveTokens(secrets); err != nil {
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
