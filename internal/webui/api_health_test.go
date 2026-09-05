package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/health"
)

type healthProbe struct {
	name  string
	check func(context.Context) health.Result
}

func (p healthProbe) Name() string                            { return p.name }
func (p healthProbe) Check(ctx context.Context) health.Result { return p.check(ctx) }

// TestHealthIsReachableWithoutAToken: a liveness probe must be answerable by
// local orchestration tooling (systemd, a watchdog, a container healthcheck)
// with no dashboard token, mirroring /healthz's precedent.
func TestHealthIsReachableWithoutAToken(t *testing.T) {
	srv := newTestServer(t)
	srv.Token = "dashboard-secret"
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %q", w.Code, w.Body.String())
	}
	if w.Body.String() != "ok" {
		t.Fatalf("body = %q, want ok", w.Body.String())
	}
}

// TestHealthDetailedRequiresToken: the detailed surface exposes real
// subsystem state (disk layout, provider reachability) so it must be gated by
// the dashboard token, unlike /health.
func TestHealthDetailedRequiresToken(t *testing.T) {
	srv := newTestServer(t)
	srv.Token = "dashboard-secret"
	srv.Health = health.NewRegistry(healthProbe{name: "state_db", check: func(context.Context) health.Result {
		return health.Result{Status: health.StatusOK}
	}})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/health/detailed", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestHealthDetailedReportsRollupAndComponents(t *testing.T) {
	srv := newTestServer(t)
	srv.Token = "dashboard-secret"
	srv.Health = health.NewRegistry(
		healthProbe{name: "state_db", check: func(context.Context) health.Result {
			return health.Result{Status: health.StatusOK}
		}},
		healthProbe{name: "disk", check: func(context.Context) health.Result {
			return health.Result{Status: health.StatusDegraded, Detail: "93% used"}
		}},
	)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/health/detailed", nil)
	req.Header.Set("Authorization", "Bearer dashboard-secret")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %q", w.Code, w.Body.String())
	}

	var report health.Report
	if err := json.Unmarshal(w.Body.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v (body %q)", err, w.Body.String())
	}
	if report.Status != health.StatusDegraded {
		t.Fatalf("status = %q, want degraded", report.Status)
	}
	if len(report.Components) != 2 {
		t.Fatalf("components = %d, want 2", len(report.Components))
	}
	if report.Components[0].Name != "state_db" || !report.Components[0].Ready {
		t.Fatalf("state_db component = %+v, want ready", report.Components[0])
	}
	if report.Components[1].Name != "disk" || report.Components[1].Ready || report.Components[1].Detail != "93% used" {
		t.Fatalf("disk component = %+v, want degraded with detail", report.Components[1])
	}
}

func TestHealthDetailedAllOK(t *testing.T) {
	srv := newTestServer(t)
	srv.Token = "dashboard-secret"
	srv.Health = health.NewRegistry(
		healthProbe{name: "state_db", check: func(context.Context) health.Result {
			return health.Result{Status: health.StatusOK}
		}},
		healthProbe{name: "config", check: func(context.Context) health.Result {
			return health.Result{Status: health.StatusOK}
		}},
	)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/health/detailed", nil)
	req.Header.Set("Authorization", "Bearer dashboard-secret")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	var report health.Report
	if err := json.Unmarshal(w.Body.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if report.Status != health.StatusOK {
		t.Fatalf("status = %q, want ok", report.Status)
	}
	for _, c := range report.Components {
		if !c.Ready {
			t.Fatalf("component %q not ready, got %+v", c.Name, c)
		}
	}
}

// TestHealthDetailedUnwiredAnswers503: a deployment that has not wired probes
// must not report a fabricated healthy state.
func TestHealthDetailedUnwiredAnswers503(t *testing.T) {
	srv := newTestServer(t)
	srv.Token = "dashboard-secret"
	srv.Health = nil

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/health/detailed", nil)
	req.Header.Set("Authorization", "Bearer dashboard-secret")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
}
