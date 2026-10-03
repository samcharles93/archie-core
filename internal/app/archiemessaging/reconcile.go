package archiemessaging

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/samcharles93/archie-core/internal/channels/telegram"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/infrastructure/controlplanerpc"
	"github.com/samcharles93/archie-core/internal/secret"
)

// chatSettingsSource reads the stored channel-settings resource and layers it
// over the file document. *controlplanerpc.Client satisfies it.
type chatSettingsSource interface {
	RuntimeChatConfig(ctx context.Context, base config.ChatConfig) (config.ChatConfig, int64, error)
}

// reconcileLoop re-reads the stored channel settings on the apply-status
// restamp interval until ctx ends. A poll rather than a stream: the restamp
// interval is the cadence every process already reports at, the messaging
// process has no other control-plane watch to share a reconnect ladder with,
// and a stored document that cannot be read must leave the running channels
// alone.
func (s *Service) reconcileLoop(ctx context.Context) {
	ticker := time.NewTicker(s.reconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.reconcileOnce(ctx); err != nil {
				s.log.Warn("channel settings reconcile failed", "err", err)
			}
		}
	}
}

// reconcileOnce re-reads the stored channel settings and, when the version
// moved, applies the change to the running channels and records the outcome in
// apply status.
//
// The file document is re-resolved first, exactly as the /restart path does, so
// the layering is the same projection boot used: the store overrides the file,
// and a field the store omits keeps the file's value.
func (s *Service) reconcileOnce(ctx context.Context) error {
	base, err := Resolve(s.currentConfig().Options, s.log)
	if err != nil {
		return err
	}
	layered, version, err := s.settingsSource.RuntimeChatConfig(ctx, config.ChatConfig{
		Telegram: base.Telegram, Email: base.Email, Webhook: base.Webhook,
		WebhookAddr: base.WebhookAddr, ShowToolCalls: base.ShowToolCalls,
	})
	if err != nil {
		return err
	}
	if version == 0 || version <= s.appliedVersion.Load() {
		return nil
	}
	next, err := resolveChatSecrets(base, layered)
	if err != nil {
		// A credential this process cannot resolve is a document rejection: the
		// running channels keep serving and the version stays reported as not
		// applied, so the operator sees the refusal rather than a false success.
		s.report(ctx, version, err)
		return err
	}
	applied, outstanding := s.applyChannelSettings(next)
	if applied {
		s.appliedVersion.Store(version)
	}
	s.report(ctx, version, outstanding)
	return nil
}

// resolveChatSecrets carries the layered chat settings onto the resolved
// projection, resolving the store's secret references. A channel whose token
// cannot be resolved stays off rather than composing with an empty credential
// (the startup rule, config.go resolveTelegramToken).
func resolveChatSecrets(base ResolvedConfig, layered config.ChatConfig) (ResolvedConfig, error) {
	next := base
	next.Telegram, next.Email, next.Webhook = layered.Telegram, layered.Email, layered.Webhook
	next.WebhookAddr, next.ShowToolCalls = layered.WebhookAddr, layered.ShowToolCalls

	secrets := secret.NewRegistry()
	token, err := resolveTelegramToken(layered.Telegram, secrets)
	if err != nil {
		return next, fmt.Errorf("resolve database telegram token: %w", err)
	}
	next.TelegramToken = token
	webhookSecret, err := resolveWebhookSecret(layered.Webhook, secrets)
	if err != nil {
		return next, fmt.Errorf("resolve database webhook secret: %w", err)
	}
	next.WebhookSecret = webhookSecret
	return next, nil
}

// applyChannelSettings applies a freshly resolved chat config to the running
// channels. A channel restarts only when its own transport settings changed;
// every other channel keeps serving. It reports whether every requested restart
// was carried out, so a rebuild that failed is retried rather than recorded as
// applied.
//
// Machine-level settings do not restart a channel. The chat session defaults
// (workspace, filesystem access, max steps, rate limit) are not part of the
// messaging projection at all, and show_tool_calls is handed to the running
// Telegram gateway in place, so both apply to turns that start after the change
// rather than interrupting one in flight.
//
// The composed set and the listen addresses are fixed for the life of the
// process. Enabling or disabling a channel and rebinding a listen address are
// process bindings; the change is refused and reported for a restart instead of
// applied.
func (s *Service) applyChannelSettings(next ResolvedConfig) (bool, error) {
	current := s.currentConfig()
	effective, outstanding := pinProcessBindings(current, next)

	if next.ShowToolCalls != current.ShowToolCalls {
		s.applyShowToolCalls(next.ShowToolCalls)
	}

	applied := true
	for _, id := range channelChanges(current, effective) {
		if err := s.restartChannel(id, effective); err != nil {
			outstanding = append(outstanding, err.Error())
			applied = false
		}
	}
	// A failed restart leaves the running configuration in place, so the next
	// tick sees the same change and retries it. A change that was carried out
	// (including a binding change that was refused and reported) is recorded, so
	// it is not applied again on every tick.
	if applied {
		s.setConfig(effective)
	}
	if len(outstanding) == 0 {
		return applied, nil
	}
	return applied, errors.New(strings.Join(outstanding, "; "))
}

// applyShowToolCalls updates the running Telegram gateways in place. Nothing
// else consumes the setting, and it is read per reply, so it needs no restart.
func (s *Service) applyShowToolCalls(show bool) {
	for _, c := range s.channels {
		if g, ok := c.current().(*telegram.Gateway); ok {
			g.SetShowToolCalls(show)
		}
	}
}

// pinProcessBindings holds the values a running process cannot change in place:
// the listen addresses it already bound, and the set of channels it composed.
// The returned configuration keeps the running value for each, and the returned
// problems name what the operator changed and when it takes effect.
func pinProcessBindings(current, next ResolvedConfig) (ResolvedConfig, []string) {
	effective := next
	var problems []string

	if (current.TelegramToken != "") != (next.TelegramToken != "") {
		problems = append(problems, "chat.telegram enabled/disabled; takes effect on restart")
		effective.Telegram, effective.TelegramToken = current.Telegram, current.TelegramToken
	}
	if (current.Email.ListenAddr != "") != (next.Email.ListenAddr != "") {
		problems = append(problems, "chat.email enabled/disabled; takes effect on restart")
		effective.Email = current.Email
	}
	if (current.WebhookAddr != "") != (next.WebhookAddr != "") {
		problems = append(problems, "chat.webhook enabled/disabled; takes effect on restart")
		effective.Webhook, effective.WebhookAddr, effective.WebhookSecret = current.Webhook, current.WebhookAddr, current.WebhookSecret
	}

	if current.Email.ListenAddr != "" && next.Email.ListenAddr != current.Email.ListenAddr {
		problems = append(problems, "chat.email.listen_addr changed; takes effect on restart")
		effective.Email.ListenAddr = current.Email.ListenAddr
	}
	if current.WebhookAddr != "" && next.WebhookAddr != current.WebhookAddr {
		problems = append(problems, "chat.webhook_addr changed; takes effect on restart")
		effective.WebhookAddr = current.WebhookAddr
	}
	return effective, problems
}

// channelChanges returns the composed channels whose transport settings differ
// between the running and the effective configuration. A channel absent from
// current (not composed) never appears, and an unchanged channel is not
// restarted.
func channelChanges(current, next ResolvedConfig) []string {
	var changed []string
	if current.TelegramToken != "" &&
		(current.TelegramToken != next.TelegramToken || !slices.Equal(current.Telegram.AllowedUserIDs, next.Telegram.AllowedUserIDs)) {
		changed = append(changed, "telegram")
	}
	if current.Email.ListenAddr != "" && current.Email.RelayAddr != next.Email.RelayAddr {
		changed = append(changed, "email")
	}
	if current.WebhookAddr != "" && (current.Webhook != next.Webhook || current.WebhookSecret != next.WebhookSecret) {
		changed = append(changed, "webhook")
	}
	return changed
}

func (s *Service) report(ctx context.Context, version int64, applyErr error) {
	if s.reporter == nil {
		return
	}
	s.reporter.Report(ctx, controlplanerpc.ChannelSettingsKind, version, applyErr)
}
