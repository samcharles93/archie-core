package archied

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"

	"github.com/samcharles93/archie-core/internal/app/servicekit"
	"github.com/samcharles93/archie-core/internal/domain/health"
)

// healthSurface is archied's own liveness endpoint, served on
// [health].listen for the lifetime of the process.
//
// Either way it is the wrong thing to ask "did archied come back up?" -- which
// is exactly what the update watchdog asks after restarting the daemon, and
// answers with a rollback when nothing replies
//
// serving gates the liveness answer: the listener binds early in boot, so
// the port is answering before the daemon is, and a 200 before the daemon
// reached its run loop would tell the watchdog a half-started release came
// up fine.
type healthSurface struct {
	serving  atomic.Bool
	registry func() *health.Registry
}

// markServing tolerates a nil surface so a boot path that serves no health
// endpoint (the Gateway and State Store entry points) cannot panic here.
func (h *healthSurface) markServing() {
	if h == nil {
		return
	}
	h.serving.Store(true)
}

func (h *healthSurface) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", h.handleLiveness)
	mux.HandleFunc("GET /health", h.handleLiveness)
	mux.HandleFunc("GET /health/detailed", h.handleDetailed)
	return mux
}

func (h *healthSurface) handleLiveness(w http.ResponseWriter, _ *http.Request) {
	if !h.serving.Load() {
		http.Error(w, "starting", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// handleDetailed reports the daemon's readiness probes, the same registry
// the dashboard serves. A nil registry (probes not wired yet, or at all)
// answers 503 rather than fabricating a report, so a deployment never looks
// healthy by accident.
func (h *healthSurface) handleDetailed(w http.ResponseWriter, r *http.Request) {
	var registry *health.Registry
	if h.registry != nil {
		registry = h.registry()
	}
	if registry == nil {
		http.Error(w, "readiness probes are unavailable", http.StatusServiceUnavailable)
		return
	}
	report := registry.Run(r.Context())
	w.Header().Set("Content-Type", "application/json")
	if report.Status != health.StatusOK {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	_ = json.NewEncoder(w).Encode(report)
}

// startHealth binds the daemon's liveness surface on [health].listen. A
// failure to bind is fatal: a daemon nothing can probe cannot have its own
// updates verified, and the watchdog would roll back every release it
// installs.
func (b *boot) startHealth(ctx context.Context) error {
	b.health = &healthSurface{registry: func() *health.Registry { return b.healthRegistry }}
	addr := b.cfg.Health.Listen
	if err := b.serveHealth(ctx, addr, b.health.handler(), "daemon health"); err != nil {
		b.log.Error("health listener failed", "addr", addr, "err", err)
		return err
	}
	return nil
}

func (b *boot) serveHealth(ctx context.Context, addr string, mux http.Handler, label string) error {
	stop, err := servicekit.ServeHealth(ctx, addr, mux, label, b.log)
	if err != nil {
		return err
	}
	b.addCleanup(stop)
	return nil
}
