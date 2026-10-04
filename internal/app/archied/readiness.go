// readiness.go wires the operator readiness probes (internal/domain/health)
// to their real subsystem dependencies and hands the assembled registry to the
// dashboard's /health/detailed surface. It is the composition-root bridge:
// each probe gets the narrowest real input it needs, never the daemon.
package archied

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/health"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration"
	"github.com/samcharles93/archie-core/internal/infrastructure/readiness"
	"github.com/samcharles93/archie-core/internal/webui"
)

// setupReadinessProbes assembles the readiness probes over the daemon's own
// subsystems. It must be called after setupObservability (so the config
// Holder and channel manager exist) and after the chat wiring (so b.chat
// exposes the contract).
//
// The probes read the daemon's state directly rather than through a
// webui.Server: the daemon serves /health/detailed on its own listener and
// has served no dashboard since the UI process cutover.
func (b *boot) setupReadinessProbes() {
	cfg := b.cfg
	probes := []health.Probe{
		// The State Store client waits for its peer, so the probe carries its
		// own deadline to report an outage instead of waiting it out.
		readiness.NewContractProbe("state_db", time.Duration(cfg.Health.DependencyTimeout), func(ctx context.Context) error {
			_, err := b.stateStore.StatusCounts(ctx)
			return err
		}),
		readiness.NewConfigProbe(b.cfgHolder.Get, configuration.Validate),
		readiness.NewDiskProbeTargets(diskProbeTargets(cfg)),
		// The daemon no longer runs a chat channel, so it cannot observe one's
		// lifecycle; channel health is the Messaging Service's own probe. What
		// this process depends on is the Gateway answering, so that is what it
		// reports.
		readiness.NewContractProbe("gateway", time.Duration(cfg.Health.DependencyTimeout), func(ctx context.Context) error {
			return pingChat(ctx, b.chat)
		}),
	}
	if b.accessLive != nil {
		probes = append(probes, readiness.NewProblemProbe("access_policies", b.accessLive))
	}
	b.healthRegistry = health.NewRegistry(probes...)
}

// pingChat asks the Gateway for a snapshot. An unwired chat surface is
// reported as degraded rather than silently OK: a daemon with no Gateway
// serves no chat at all.
func pingChat(ctx context.Context, chat *webui.ChatService) error {
	if chat == nil || chat.Contract == nil {
		return errors.New("chat contract not wired")
	}
	if _, err := chat.Contract.Snapshot(ctx); err != nil {
		return err
	}
	return nil
}

// diskProbeTargets returns the filesystems whose capacity matters to the
// daemon. Root is always required; /var is useful on conventional Linux
// installations but optional so it does not make other environments fail.
func diskProbeTargets(cfg config.Config) []readiness.DiskTarget {
	targets := []readiness.DiskTarget{{Name: "root", Path: "/"}}
	if info, err := os.Stat("/var"); err == nil && info.IsDir() {
		targets = append(targets, readiness.DiskTarget{Name: "var", Path: "/var", Optional: true})
	}
	targets = append(targets, readiness.DiskTarget{Name: "data", Path: diskProbePath(cfg)})
	return targets
}

// diskProbePath is the daemon's data disk target: the state directory first,
// then the work directory, then cwd.
func diskProbePath(cfg config.Config) string {
	switch {
	case cfg.StateDir != "":
		return cfg.StateDir
	case cfg.WorkDir != "":
		return cfg.WorkDir
	default:
		return "."
	}
}
