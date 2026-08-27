package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/curator"
)

type stubCuratorEngine struct {
	name     string
	manifest curator.Manifest
	health   curator.Health
}

func (c *stubCuratorEngine) Name() string                          { return c.name }
func (c *stubCuratorEngine) Version() string                       { return "v1" }
func (c *stubCuratorEngine) Manifest() curator.Manifest            { return c.manifest }
func (c *stubCuratorEngine) Bind(curator.Registrar)                {}
func (c *stubCuratorEngine) Start(context.Context) error           { return nil }
func (c *stubCuratorEngine) Stop(context.Context) error            { return nil }
func (c *stubCuratorEngine) Health(context.Context) curator.Health { return c.health }
func (c *stubCuratorEngine) Check(context.Context) (bool, error)   { return false, nil }
func (c *stubCuratorEngine) Pass(context.Context, curator.PassInput) (curator.PassResult, error) {
	return curator.PassResult{}, nil
}

func TestHandleCuratorsNilRegistryReturnsEmpty(t *testing.T) {
	srv := newTestServer(t)

	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/curators", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body)
	}

	var response struct {
		Curators []CuratorView `json:"curators"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Curators) != 0 {
		t.Fatalf("curators = %#v, want empty", response.Curators)
	}
}

func TestHandleCuratorsReportsNamesHealthAndActivity(t *testing.T) {
	registry := curator.NewRegistry(curator.Registrar{})
	c := &stubCuratorEngine{
		name:     "skill-curator",
		manifest: curator.Manifest{Interval: time.Minute},
		health:   curator.Health{Status: curator.HealthHealthy, Message: "ok"},
	}
	if err := registry.Register(c); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	registry.RecordActivity("skill-curator", at, []curator.Action{
		{At: at, Type: "skill.updated", Detail: "rewrote foo", Reason: "stale content"},
	})

	srv := newTestServer(t)
	srv.Curators = registry

	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/curators", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body)
	}

	var response struct {
		Curators []CuratorView `json:"curators"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Curators) != 1 {
		t.Fatalf("curators = %#v, want 1", response.Curators)
	}
	got := response.Curators[0]
	if got.Name != "skill-curator" {
		t.Fatalf("Name = %q, want skill-curator", got.Name)
	}
	if got.Health.Status != string(curator.HealthHealthy) || got.Health.Message != "ok" {
		t.Fatalf("Health = %#v", got.Health)
	}
	if got.LastRunAt == nil || !got.LastRunAt.Equal(at) {
		t.Fatalf("LastRunAt = %v, want %v", got.LastRunAt, at)
	}
	if got.LastRunActions != 1 {
		t.Fatalf("LastRunActions = %d, want 1", got.LastRunActions)
	}
	if len(got.RecentActions) != 1 || got.RecentActions[0].Type != "skill.updated" || got.RecentActions[0].Reason != "stale content" {
		t.Fatalf("RecentActions = %#v", got.RecentActions)
	}
}

func TestHandleCuratorsReportsRegisteredCuratorWithNoActivityYet(t *testing.T) {
	registry := curator.NewRegistry(curator.Registrar{})
	c := &stubCuratorEngine{
		name:     "never-run",
		manifest: curator.Manifest{Interval: time.Minute},
		health:   curator.Health{Status: curator.HealthHealthy},
	}
	if err := registry.Register(c); err != nil {
		t.Fatal(err)
	}

	srv := newTestServer(t)
	srv.Curators = registry

	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/curators", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body)
	}

	var response struct {
		Curators []CuratorView `json:"curators"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Curators) != 1 {
		t.Fatalf("curators = %#v, want 1", response.Curators)
	}
	if got := response.Curators[0]; got.LastRunAt != nil || got.LastRunActions != 0 || len(got.RecentActions) != 0 {
		t.Fatalf("expected zero-value activity for a never-run curator, got %#v", got)
	}
	// Regression guard: time.Time's zero value is not "empty" to
	// encoding/json, so a naive time.Time field would serialize as
	// "0001-01-01T00:00:00Z" (a truthy string on the frontend) instead of
	// being omitted. Assert the raw JSON, not just the unmarshalled Go
	// value, so a future revert of the *time.Time fix is caught here.
	if body := w.Body.String(); strings.Contains(body, "0001-01-01") {
		t.Fatalf("response leaks zero-value time.Time: %s", body)
	}
}
