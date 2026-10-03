// Package presence keeps one record per running service instance in the State
// Store, so the control plane can tell which services are up.
package presence

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"strings"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/applystatus"
	"github.com/samcharles93/archie-core/internal/domain/health"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
)

// Service names. Writer and reader share them so a record is never orphaned
// by a spelling.
const (
	Daemon     = applystatus.Daemon
	Gateway    = applystatus.Gateway
	Messaging  = applystatus.Messaging
	UI         = "archie-ui"
	StateStore = "archie-state-store"
)

// Services is every service expected to report.
func Services() []string { return []string{Daemon, Gateway, Messaging, UI, StateStore} }

// Stale reports whether a record's instance has stopped re-stamping it.
func Stale(reportedAt, now time.Time) bool { return applystatus.Stale(reportedAt, now) }

// Build is what the binary was compiled as.
type Build struct {
	Version     string
	InstallType string
}

// Run publishes this instance's record immediately and then on every restamp
// interval until ctx ends. A nil registry reports ready with no probes.
// Write failures are logged: the next tick retries.
func Run(ctx context.Context, service string, build Build, store storecontract.PresenceStore, registry *health.Registry, log *slog.Logger) {
	if store == nil {
		return
	}
	record := storecontract.Presence{
		Service: service, InstanceID: instanceID(), Version: build.Version,
		InstallType: build.InstallType, StartedAt: time.Now().UTC(),
	}
	ticker := time.NewTicker(applystatus.RestampInterval)
	defer ticker.Stop()
	for {
		record.Ready, record.Detail = summarise(ctx, registry)
		record.ReportedAt = time.Now().UTC()
		if err := store.PutPresence(ctx, record); err != nil && ctx.Err() == nil {
			log.Warn("presence not reported", "service", service, "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func summarise(ctx context.Context, registry *health.Registry) (bool, string) {
	if registry == nil {
		return true, ""
	}
	report := registry.Run(ctx)
	var degraded []string
	for _, c := range report.Components {
		if !c.Ready {
			degraded = append(degraded, c.Name)
		}
	}
	return report.Status == health.StatusOK, strings.Join(degraded, ", ")
}

func instanceID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
