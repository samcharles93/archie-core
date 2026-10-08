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

// reconcileLoop re-reads stored channel settings every restamp interval
// until ctx ends.
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

// reconcileOnce applies changed channel settings and records the outcome in
// apply status.
func (s *Service) reconcileOnce(ctx context.Context) error {
	base, err := Resolve(s.currentConfig().Options, s.log)
	if err != nil {
		return err
	}
	layered, version, err := s.settingsSource.RuntimeChatConfig(ctx, config.ChatConfig{
		Telegram: base.Telegram, ShowToolCalls: base.ShowToolCalls,
	})
	if err != nil {
		return err
	}
	if version == 0 || version <= s.appliedVersion.Load() {
		return nil
	}
	next, err := resolveChatSecrets(base, layered, s.secrets)
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
func resolveChatSecrets(base ResolvedConfig, layered config.ChatConfig, secrets *secret.Registry) (ResolvedConfig, error) {
	next := base
	next.Telegram, next.ShowToolCalls = layered.Telegram, layered.ShowToolCalls

	token, err := resolveTelegramToken(layered.Telegram, secrets)
	if err != nil {
		return next, fmt.Errorf("resolve database telegram token: %w", err)
	}
	next.TelegramToken = token
	return next, nil
}

// applyChannelSettings applies the changed channel settings: channels whose
// enablement changed are started or stopped, channels whose transport settings
// changed are restarted, and the report is success only once every change is in
// effect.
func (s *Service) applyChannelSettings(next ResolvedConfig) (bool, error) {
	current := s.currentConfig()

	if next.ShowToolCalls != current.ShowToolCalls {
		s.applyShowToolCalls(next.ShowToolCalls)
	}

	applied := true
	outstanding := s.applyChannelEnablement(current, next)
	if len(outstanding) > 0 {
		applied = false
	}
	for _, id := range channelChanges(current, next) {
		if err := s.restartChannel(id, next); err != nil {
			outstanding = append(outstanding, err.Error())
			applied = false
		}
	}
	// A failed change leaves the running configuration in place, so the next
	// tick sees the same difference and retries it.
	if applied {
		s.setConfig(next)
	}
	if len(outstanding) == 0 {
		return applied, nil
	}
	return applied, errors.New(strings.Join(outstanding, "; "))
}

// applyChannelEnablement starts or stops the channels whose enablement the
// stored settings changed, and reports only once the requested state is in
// effect. A channel whose enablement did not change is left alone.
func (s *Service) applyChannelEnablement(current, next ResolvedConfig) []string {
	var outstanding []string
	switch {
	case current.TelegramToken != "" && next.TelegramToken == "":
		if err := s.disableChannel("telegram"); err != nil {
			outstanding = append(outstanding, err.Error())
		}
	case current.TelegramToken == "" && next.TelegramToken != "":
		if err := s.enableChannel("telegram", next); err != nil {
			outstanding = append(outstanding, err.Error())
		}
	}
	return outstanding
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

// channelChanges returns the composed channels whose transport settings differ
// between the running and the next configuration while remaining enabled. A
// channel absent from current (not composed) never appears, an unchanged
// channel is not restarted, and an enable or disable is applied as enablement
// rather than as a transport change.
func channelChanges(current, next ResolvedConfig) []string {
	var changed []string
	if current.TelegramToken != "" && next.TelegramToken != "" &&
		(current.TelegramToken != next.TelegramToken || !slices.Equal(current.Telegram.AllowedUserIDs, next.Telegram.AllowedUserIDs)) {
		changed = append(changed, "telegram")
	}
	return changed
}

func (s *Service) report(ctx context.Context, version int64, applyErr error) {
	if s.reporter == nil {
		return
	}
	s.reporter.Report(ctx, controlplanerpc.ChannelSettingsKind, version, applyErr)
}
