package archied

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/health"
)

// TestHealthSurfaceLivenessWaitsForBoot: the update watchdog restarts archied
// and takes the first 200 as proof the new release came up. The listener
// binds early so the port is not simply refused during boot, which means an
// unconditional 200 would confirm a release that had not started yet.
func TestHealthSurfaceLivenessWaitsForBoot(t *testing.T) {
	surface := &healthSurface{}
	handler := surface.handler()

	for _, path := range []string{"/healthz", "/health"} {
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
		if res.Code != http.StatusServiceUnavailable {
			t.Errorf("GET %s while booting = %d, want 503", path, res.Code)
		}
	}

	surface.markServing()

	for _, path := range []string{"/healthz", "/health"} {
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
		if res.Code != http.StatusOK {
			t.Errorf("GET %s once serving = %d, want 200", path, res.Code)
		}
	}
}

// TestHealthSurfaceDetailedReportsProbes: /health/detailed is the readiness
// answer, and an unwired or degraded registry must never read as ready.
func TestHealthSurfaceDetailedReportsProbes(t *testing.T) {
	var registry *health.Registry
	surface := &healthSurface{registry: func() *health.Registry { return registry }}
	surface.markServing()
	handler := surface.handler()

	res := httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/health/detailed", nil))
	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("detailed with no probes = %d, want 503", res.Code)
	}

	registry = health.NewRegistry(failingProbe{})
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/health/detailed", nil))
	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("detailed with a failing probe = %d, want 503", res.Code)
	}

	registry = health.NewRegistry(passingProbe{})
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/health/detailed", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("detailed with a passing probe = %d, want 200", res.Code)
	}
}

type failingProbe struct{}

func (failingProbe) Name() string { return "test" }

func (failingProbe) Check(context.Context) health.Result {
	return health.Result{Status: health.StatusDegraded, Detail: "down"}
}

type passingProbe struct{}

func (passingProbe) Name() string { return "test" }

func (passingProbe) Check(context.Context) health.Result {
	return health.Result{Status: health.StatusOK}
}
