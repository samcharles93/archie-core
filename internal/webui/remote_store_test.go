package webui

// Remote-contract coverage for the dashboard data paths. The production UI
// process (archie-ui, and the daemon's webui) serves task, capture, mapping
// and binding routes against a *staterpc.Client — the remote read contract —
// not a concrete *store.Store. Every other webui test feeds a local store
// directly, so none of them prove the handlers work once the data crosses the
// gRPC wire. These do: they stand up a real SQLite store behind
// staterpc.RegisterServer, dial it, and assert the handlers serve correctly.

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"google.golang.org/grpc"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/infrastructure/staterpc"
	"github.com/samcharles93/archie-core/internal/store"
)

// newRemoteTestServer serves a real SQLite store behind a gRPC listener and
// returns a webui.Server whose store fields are all the remote *staterpc.Client.
// This is the composition the UI Service uses; it exercises the full
// round-trip (handler -> narrow interface -> client -> wire -> server -> store)
// rather than a fake.
func newRemoteTestServer(t *testing.T) *Server {
	t.Helper()
	st, err := store.Open(t.Context(), t.TempDir()+"/remote.db")
	if err != nil {
		t.Fatalf("open temp store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := grpc.NewServer()
	// The same concrete *store.Store satisfies every interface the server's
	// per-surface accessors need; leaving one nil makes ListMappings/ListBindings
	// fail closed with err*Unavailable, which is the real composition's wiring.
	staterpc.RegisterServer(server, staterpc.Deps{
		Tasks:              st,
		Captures:           st,
		Mappings:           st,
		Bindings:           st,
		BindingDispatcher:  st,
		BindingTaskCreator: st,
		Log:                slog.New(slog.DiscardHandler),
	})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	client, closeConn, err := staterpc.Dial(listener.Addr().String(), "")
	if err != nil {
		t.Fatalf("dial state store: %v", err)
	}
	t.Cleanup(closeConn)

	return &Server{
		Cfg:               config.NewHolder(config.Config{}),
		Store:             client,
		Captures:          client,
		Mappings:          client,
		Bindings:          client,
		BindingDispatcher: client,
		Log:               slog.New(slog.DiscardHandler),
		CaptureMaxEvents:  10,
		CaptureRetention:  1000,
	}
}

func TestRemoteEndpointFamiliesServeOverReadContracts(t *testing.T) {
	srv := newRemoteTestServer(t)
	ctx := context.Background()

	t.Run("summary", func(t *testing.T) {
		rr := httptest.NewRecorder()
		srv.handleSummary(rr, httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/summary", nil))
		if rr.Code != http.StatusOK {
			t.Fatalf("GET /api/summary = %d, body %s", rr.Code, rr.Body.String())
		}
	})

	t.Run("tasks_and_timeline", func(t *testing.T) {
		if _, err := srv.Store.EnqueueIssue(ctx, "acme", "widget", 1, "remote task", "", "", ""); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
		task, err := srv.Store.ClaimNext(ctx)
		if err != nil || task == nil {
			t.Fatalf("claim (%v, %v)", task, err)
		}
		if err := srv.Store.Transition(ctx, task.ID, workflow.StatusRunning, workflow.StatusWaitingHuman, "needs info"); err != nil {
			t.Fatalf("transition: %v", err)
		}

		rr := httptest.NewRecorder()
		srv.handleTasks(rr, httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/tasks", nil))
		if rr.Code != http.StatusOK {
			t.Fatalf("GET /api/tasks = %d, body %s", rr.Code, rr.Body.String())
		}
		if !strings.Contains(rr.Body.String(), "remote task") {
			t.Fatalf("task not in response: %s", rr.Body.String())
		}

		// The {id} route reads the task's events over the wire. handleTask
		// reads r.PathValue("id"), which only the mux populates, so this goes
		// through Server.Handler() rather than a direct handler call — the
		// realistic path and the one that exercises routing too.
		handler := srv.Handler()
		rr2 := httptest.NewRecorder()
		handler.ServeHTTP(rr2, httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/tasks/"+strconv.FormatInt(task.ID, 10), nil))
		if rr2.Code != http.StatusOK {
			t.Fatalf("GET /api/tasks/{id} = %d, body %s", rr2.Code, rr2.Body.String())
		}
	})

	t.Run("mappings", func(t *testing.T) {
		rr := httptest.NewRecorder()
		srv.handleMappingsList(rr, httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/mappings", nil))
		if rr.Code != http.StatusOK {
			t.Fatalf("GET /api/mappings = %d, body %s", rr.Code, rr.Body.String())
		}
	})

	t.Run("bindings", func(t *testing.T) {
		rr := httptest.NewRecorder()
		srv.handleBindingsList(rr, httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/bindings", nil))
		if rr.Code != http.StatusOK {
			t.Fatalf("GET /api/bindings = %d, body %s", rr.Code, rr.Body.String())
		}
	})
}

// TestRemoteLogAndHealthDegrade covers the two paths with no State Store
// contract: logs (in-process feed, nil in a remote composition) report
// disabled, and health consults the local registry (nil here) rather than a
// concrete store. Both are deliberate degradation, not gaps to fill by
// inventing a contract.
func TestRemoteLogAndHealthDegrade(t *testing.T) {
	srv := newRemoteTestServer(t)
	ctx := context.Background()

	t.Run("logs_disabled", func(t *testing.T) {
		rr := httptest.NewRecorder()
		srv.handleLogs(rr, httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/logs", nil))
		if rr.Code != http.StatusOK {
			t.Fatalf("GET /api/logs = %d, body %s", rr.Code, rr.Body.String())
		}
		if !strings.Contains(rr.Body.String(), "disabled") {
			t.Fatalf("expected logs to report disabled, got %s", rr.Body.String())
		}
	})

	t.Run("health_without_registry", func(t *testing.T) {
		rr := httptest.NewRecorder()
		srv.handleHealthDetailed(rr, httptest.NewRequestWithContext(ctx, http.MethodGet, "/health/detailed", nil))
		if rr.Code != http.StatusOK && rr.Code != http.StatusServiceUnavailable {
			t.Fatalf("GET /health/detailed = %d, body %s", rr.Code, rr.Body.String())
		}
	})
}
