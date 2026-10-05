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

// States a service reads as in the mesh view.
const (
	StateUp       = "up"
	StateDegraded = "degraded"
	StateDown     = "down"
	// StateUnknown is a service whose record cannot be read because the
	// State Store holding it is unreachable.
	StateUnknown = "unknown"
)

// Service is one expected service as the mesh view shows it: its newest
// record, or a zero record when it never reported.
type Service struct {
	storecontract.Presence
	State string `json:"state"`
}

// Mesh reads every expected service from the stored records. A service with
// no record, or whose newest record is stale, is down rather than omitted.
func Mesh(records []storecontract.Presence, now time.Time) []Service {
	newest := map[string]storecontract.Presence{}
	for _, record := range records {
		if current, ok := newest[record.Service]; !ok || record.ReportedAt.After(current.ReportedAt) {
			newest[record.Service] = record
		}
	}
	services := make([]Service, 0, len(Services()))
	for _, name := range Services() {
		record, ok := newest[name]
		if !ok {
			record.Service = name
		}
		service := Service{Presence: record, State: state(record, ok, now)}
		if service.State == StateDown {
			// The last probe result describes a process that is gone.
			service.Detail = ""
		}
		services = append(services, service)
	}
	return services
}

// Unreachable is the mesh view when the State Store cannot be read: the store
// is down and every other service is unknown, since its record lives there.
func Unreachable() []Service {
	services := make([]Service, 0, len(Services()))
	for _, name := range Services() {
		service := Service{Service: name, State: StateUnknown}
		if name == StateStore {
			service.State, service.Detail = StateDown, "unreachable"
		}
		services = append(services, service)
	}
	return services
}

func state(record storecontract.Presence, reported bool, now time.Time) string {
	switch {
	case !reported || Stale(record.ReportedAt, now):
		return StateDown
	case !record.Ready:
		return StateDegraded
	default:
		return StateUp
	}
}
