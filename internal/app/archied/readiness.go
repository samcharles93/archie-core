// readiness.go wires the operator readiness probes (internal/domain/health)
// to their real subsystem dependencies and hands the assembled registry to the
// dashboard's /health/detailed surface. It is the composition-root bridge:
// each probe gets the narrowest real input it needs, never the daemon.
package archied

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	channelruntime "github.com/samcharles93/archie-core/internal/channels"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/health"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration"
	"github.com/samcharles93/archie-core/internal/infrastructure/readiness"
	"github.com/samcharles93/archie-core/internal/webui"
)

// setupReadinessProbes assembles the readiness probes and publishes the
// registry to the dashboard. It must be called after setupObservability (so
// b.web.Cfg and b.web.Channels are set) and after the chat wiring (so
// b.web.Chat exposes the model manager and session store). See
// docs/prds/status-health-surface.md and the Operations: readiness probes
// epic.
func (b *boot) setupReadinessProbes() {
	cfg := b.cfg
	probes := []health.Probe{
		readiness.NewStoreProbe(b.st),
		readiness.NewConfigProbe(func() config.Config { return b.web.Cfg.Get() }, configuration.Validate),
		readiness.NewDiskProbe(diskProbePath(cfg)),
		readiness.NewModelProbe(b.chatModels.ActiveModel, b.chatModels.Models, modelReachProbe(cfg, b.chatModels.ActiveModel)),
		readiness.NewGatewayProbe(
			func() []readiness.ChannelState { return channelStates(b.web.Channels) },
			func(ctx context.Context) int { return sessionCount(ctx, b.web.Chat) },
		),
	}
	b.web.Health = health.NewRegistry(probes...)
}

// channelStates projects a channel lifecycle snapshot into the readiness
// probe's view, so the probe does not depend on the channels package's
// concrete type.
func channelStates(m *channelruntime.Manager) []readiness.ChannelState {
	if m == nil {
		return nil
	}
	snapshot := m.Snapshot()
	states := make([]readiness.ChannelState, 0, len(snapshot))
	for _, s := range snapshot {
		states = append(states, readiness.ChannelState{
			ID:         s.ID,
			Configured: s.Configured,
			State:      string(s.State),
		})
	}
	return states
}

// sessionCount returns the number of gateway sessions. An unwired chat
// surface returns 0 rather than reporting a false failure.
func sessionCount(ctx context.Context, chat *webui.ChatService) int {
	if chat == nil || chat.Sessions == nil {
		return 0
	}
	sessions, err := chat.Sessions.List(ctx)
	if err != nil {
		return 0
	}
	return len(sessions)
}

// diskProbePath resolves the filesystem the readiness disk probe inspects:
// the store database directory first (the data that must fit on disk), then
// the working directory, then the process cwd.
func diskProbePath(cfg config.Config) string {
	switch {
	case cfg.DBPath != "":
		return filepath.Dir(cfg.DBPath)
	case cfg.WorkDir != "":
		return cfg.WorkDir
	default:
		return "."
	}
}

// modelReachProbe returns a live reachability check for the active model. It
// resolves the active model's provider and, when the provider has a custom
// base URL, performs a short-timeout HTTP request to it: any response
// (including 401/404) proves the endpoint is reachable, while a network error
// or an unconfigured provider reports unreachable. A provider using its SDK
// default endpoint is assumed reachable, because no custom endpoint exists to
// probe.
func modelReachProbe(cfg config.Config, active func() string) func(context.Context) error {
	return func(ctx context.Context) error {
		model := active()
		provider, _, _ := strings.Cut(model, "/")
		if provider == "" {
			return fmt.Errorf("active model %q has no provider", model)
		}
		p, ok := cfg.Providers[provider]
		if !ok {
			return fmt.Errorf("provider %q not configured", provider)
		}
		if p.BaseURL == "" {
			return nil
		}
		reqCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, p.BaseURL, nil)
		if err != nil {
			return err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		_ = resp.Body.Close()
		return nil
	}
}
