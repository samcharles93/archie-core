package archieui

import (
	"log/slog"

	"github.com/samcharles93/archie-core/internal/domain/health"
	"github.com/samcharles93/archie-core/internal/gateway"
	"github.com/samcharles93/archie-core/internal/store"
	"github.com/samcharles93/archie-core/internal/webui"
)

// deps are the resolved inputs the dashboard is built from: two remote
// contracts, the process's own logger and options, and the readiness registry
// assembled over those same contracts.
type deps struct {
	Options Options
	Log     *slog.Logger
	Store   store.TaskStore
	Chat    gateway.ChatContract
	Health  *health.Registry
}

// compose builds the dashboard server for the UI process. It sets exactly the
// fields a contract already carries plus this process's own settings, and
// leaves every remaining field at its zero value on purpose: each one is a
// daemon-local runtime handle with no contract behind it, and the handlers
// already degrade to a documented 501/503/empty response rather than panicking
// (docs/prds/ui-service-boundary.md:130-131).
//
// What is deliberately absent, and which bead fills it:
//
//   - Cfg, LastReload, UpdateConfig, ConfigOverrides, ResetConfig,
//     UpdateRepoField: the configuration owner stays the daemon, reached
//     through a narrow admin contract. The shared holder is removed from the
//     daemon's own wiring by archie-core-8cda.5.4.
//   - LogFeed, TaskLogs, Events, Channels, ReloadChannel, Curators, Memory,
//     Workflows, WorkRequests, RunningVersions, Chat.Updates,
//     UpdateReportPath: no contract exists for these yet.
//     Defining one amends the owning service's contract first
//     (docs/prds/ui-service-boundary.md:205-207); the route migration is
//     archie-core-8cda.5.3.
//   - Captures, CaptureLimiter, BindingDispatcher: withheld for the duration
//     of the seam. The daemon's in-process dashboard still serves
//     POST /webhooks/capture/{source}; wiring it here too would give two
//     listeners the same intake authority, which the PRD forbids
//     (lines 30-33). Turned on at cutover, once the daemon's listener is gone.
func compose(d deps) *webui.Server {
	srv := &webui.Server{
		Store:                 d.Store,
		Log:                   d.Log,
		Token:                 d.Options.Token,
		TrustForwardedHeaders: d.Options.trustForwardedHeaders(),
		Health:                d.Health,
	}
	if d.Chat != nil {
		srv.Chat = &webui.ChatService{Contract: d.Chat}
	}
	return srv
}
