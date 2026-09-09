package archieui

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"google.golang.org/grpc"

	"github.com/samcharles93/archie-core/internal/domain/health"
	"github.com/samcharles93/archie-core/internal/domain/messaging"
	"github.com/samcharles93/archie-core/internal/gateway"
	"github.com/samcharles93/archie-core/internal/infrastructure/gatewayrpc"
	"github.com/samcharles93/archie-core/internal/infrastructure/staterpc"
	"github.com/samcharles93/archie-core/internal/store"
	"github.com/samcharles93/archie-core/internal/taskstate"
)

// fakeChat is a minimal gateway.ChatContract standing in for the standalone
// archie-gateway process. Only the read path the dashboard exercises here
// returns data; the rest satisfy the interface.
type fakeChat struct{ sessions []gateway.SessionContext }

func (f *fakeChat) Snapshot(context.Context) (gateway.ChatSnapshot, error) {
	return gateway.ChatSnapshot{
		Sessions:       f.sessions,
		Models:         []string{"test/model"},
		ActiveModel:    "test/model",
		ActivePersonas: map[string]string{},
	}, nil
}

func (f *fakeChat) GetSession(context.Context, string) (gateway.SessionContext, bool, error) {
	return gateway.SessionContext{}, false, nil
}

func (f *fakeChat) RecentMessages(context.Context, string, int) ([]messaging.Message, error) {
	return nil, nil
}

func (f *fakeChat) RecentTurns(context.Context, string, int) ([]gateway.TurnRecord, error) {
	return nil, nil
}

func (f *fakeChat) Route(context.Context, gateway.Inbound) (gateway.ChatReply, error) {
	return gateway.ChatReply{}, nil
}

func (f *fakeChat) Stream(context.Context, gateway.Inbound) (<-chan gateway.ChatEvent, error) {
	ch := make(chan gateway.ChatEvent)
	close(ch)
	return ch, nil
}

func (f *fakeChat) Cancel(context.Context, string) (gateway.ChatCancellation, error) {
	return gateway.ChatCancellation{}, nil
}

func (f *fakeChat) SetPersona(context.Context, string, string) (bool, error) { return false, nil }

func (f *fakeChat) ApplyTaskAction(context.Context, string, int64, taskstate.Action) (gateway.TaskActionResult, error) {
	return gateway.TaskActionResult{}, nil
}

func (f *fakeChat) ApplyOperatorTaskAction(context.Context, int64, taskstate.Action) (gateway.TaskActionResult, error) {
	return gateway.TaskActionResult{}, nil
}

// serveGRPC runs register against a fresh loopback listener and returns the
// dial target plus a stop func. Mirrors the harness in
// internal/app/archied/state_store_test.go's TestServeStateStoreServesContract.
func serveGRPC(t *testing.T, register func(grpc.ServiceRegistrar)) (target string, stop func()) {
	t.Helper()
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := grpc.NewServer()
	register(server)
	go func() { _ = server.Serve(listener) }()
	return listener.Addr().String(), server.Stop
}

// TestUIServesDashboardAgainstRemoteContracts is the bead's headline claim:
// the composed UI process serves the whole dashboard with its data coming
// over the wire from a State Store and a Gateway in other processes, and
// every route with no contract behind it degrades exactly as
// docs/prds/ui-service-boundary.md:130-131 permits.
func TestUIServesDashboardAgainstRemoteContracts(t *testing.T) {
	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "tasks.sqlite"))
	if err != nil {
		t.Fatalf("open temp store: %v", err)
	}
	defer st.Close()
	if _, err := st.EnqueueChatTask(t.Context(), "acme", "widget", "remote summary", "body", "implement", ""); err != nil {
		t.Fatalf("seed task: %v", err)
	}

	stateTarget, stopState := serveGRPC(t, func(r grpc.ServiceRegistrar) {
		staterpc.RegisterServer(r, staterpc.Deps{Tasks: st, Log: slog.New(slog.DiscardHandler)})
	})
	defer stopState()
	chat := &fakeChat{sessions: []gateway.SessionContext{
		{SessionID: "s1", Source: gateway.SessionSource{Platform: "web", ChannelID: "dash"}},
	}}
	gatewayTarget, stopGateway := serveGRPC(t, func(r grpc.ServiceRegistrar) {
		gatewayrpc.RegisterServer(r, chat)
	})
	defer stopGateway()

	stateClient, closeState, err := staterpc.Dial(stateTarget, "")
	if err != nil {
		t.Fatalf("dial state store: %v", err)
	}
	defer closeState()
	chatClient, closeGateway, err := gatewayrpc.Dial(gatewayTarget, "")
	if err != nil {
		t.Fatalf("dial gateway: %v", err)
	}
	defer closeGateway()

	options := Options{Listen: "127.0.0.1:0", DependencyTimeout: defaultDependencyTimeout}
	srv := compose(deps{
		Options: options,
		Log:     slog.New(slog.DiscardHandler),
		Store:   stateClient,
		Chat:    chatClient,
		Health:  newReadinessRegistry(options, stateClient, chatClient),
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	get := func(path string) (int, []byte) {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.URL+path, nil)
		if err != nil {
			t.Fatalf("request %s: %v", path, err)
		}
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		return resp.StatusCode, body
	}

	// Liveness is unauthenticated (PRD lines 119-120).
	for _, path := range []string{"/healthz", "/health"} {
		if code, _ := get(path); code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, code)
		}
	}

	// The task board's data came over the State Store's gRPC listener.
	code, body := get("/api/summary")
	if code != http.StatusOK {
		t.Fatalf("GET /api/summary = %d (%s), want 200", code, body)
	}
	var summary struct {
		Statuses map[string]int `json:"statuses"`
	}
	if err := json.Unmarshal(body, &summary); err != nil {
		t.Fatalf("decode summary: %v", err)
	}
	total := 0
	for _, n := range summary.Statuses {
		total += n
	}
	if total != 1 {
		t.Fatalf("summary counted %d tasks, want the 1 seeded behind the gRPC listener (%s)", total, body)
	}

	// Chat reads come over the Gateway's gRPC listener.
	code, body = get("/api/chat/sessions")
	if code != http.StatusOK {
		t.Fatalf("GET /api/chat/sessions = %d (%s), want 200", code, body)
	}
	var sessions struct {
		Sessions []gateway.SessionContext `json:"sessions"`
	}
	if err := json.Unmarshal(body, &sessions); err != nil {
		t.Fatalf("decode sessions: %v", err)
	}
	if len(sessions.Sessions) != 1 || sessions.Sessions[0].SessionID != "s1" {
		t.Fatalf("chat sessions = %s, want the single web session from the remote contract", body)
	}

	// No shared config.Holder: the read is empty and there is no second writer.
	if code, body := get("/api/config"); code != http.StatusOK || string(body) != "{}\n" {
		t.Errorf("GET /api/config = %d %q, want 200 {}", code, body)
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPatch, ts.URL+"/api/config", http.NoBody)
	if err != nil {
		t.Fatalf("patch request: %v", err)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("PATCH /api/config: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("PATCH /api/config = %d, want 503: the daemon stays the configuration writer (PRD lines 95-96)", resp.StatusCode)
	}

	// Routes with no contract behind them degrade rather than panic.
	code, body = get("/api/logs")
	if code != http.StatusOK {
		t.Errorf("GET /api/logs = %d, want 200", code)
	}
	var logs struct {
		Disabled bool `json:"disabled"`
	}
	if err := json.Unmarshal(body, &logs); err != nil {
		t.Fatalf("decode logs: %v", err)
	}
	if !logs.Disabled {
		t.Errorf("GET /api/logs reported logging enabled; the UI process has no daemon log feed (%s)", body)
	}
	if code, _ := get("/api/version"); code != http.StatusNotImplemented {
		t.Errorf("GET /api/version = %d, want 501", code)
	}

	// Capture intake stays with the daemon until bead archie-core-8cda.5.4.
	capReq, err := http.NewRequestWithContext(t.Context(), http.MethodPost, ts.URL+"/webhooks/capture/demo", http.NoBody)
	if err != nil {
		t.Fatalf("capture request: %v", err)
	}
	capResp, err := ts.Client().Do(capReq)
	if err != nil {
		t.Fatalf("POST /webhooks/capture/demo: %v", err)
	}
	capResp.Body.Close()
	if capResp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("POST /webhooks/capture/demo = %d, want 503: two processes must not share webhook intake authority", capResp.StatusCode)
	}

	// Readiness aggregates the two remote dependencies, and drops when one dies.
	code, body = get("/health/detailed")
	if code != http.StatusOK {
		t.Fatalf("GET /health/detailed = %d (%s), want 200", code, body)
	}
	report := decodeReport(t, body)
	if report.Status != health.StatusOK {
		t.Fatalf("readiness = %s with both dependencies up, want ok (%s)", report.Status, body)
	}
	stopState()
	_, body = get("/health/detailed")
	report = decodeReport(t, body)
	if report.Status != health.StatusDegraded {
		t.Fatalf("readiness = %s with the State Store stopped, want degraded (%s)", report.Status, body)
	}
	if ready, ok := componentReady(report, "state_db"); !ok || ready {
		t.Fatalf("state_db component ready=%v present=%v with the State Store stopped (%s)", ready, ok, body)
	}
	if ready, ok := componentReady(report, "gateway"); !ok || !ready {
		t.Fatalf("gateway component ready=%v present=%v while the Gateway is still up (%s)", ready, ok, body)
	}
}

func decodeReport(t *testing.T, body []byte) health.Report {
	t.Helper()
	var report health.Report
	if err := json.Unmarshal(body, &report); err != nil {
		t.Fatalf("decode readiness report: %v (%s)", err, body)
	}
	return report
}

func componentReady(report health.Report, name string) (ready, found bool) {
	for _, c := range report.Components {
		if c.Name == name {
			return c.Ready, true
		}
	}
	return false, false
}

// TestUIRefusesNonLoopbackListenWithoutToken holds the PRD's security
// posture (lines 116-118): "non-loopback listeners require a non-empty token
// and fail closed when it is absent". Unlike the daemon, which mints a token
// silently (webTokenFor), the standalone process refuses to start; -token-file
// is the explicit opt-in to minting one.
func TestUIRefusesNonLoopbackListenWithoutToken(t *testing.T) {
	tests := []struct {
		name    string
		options Options
		wantErr bool
	}{
		{name: "loopback without token", options: Options{Listen: "127.0.0.1:8484"}},
		{name: "localhost without token", options: Options{Listen: "localhost:8484"}},
		{name: "non-loopback without token", options: Options{Listen: "0.0.0.0:8484"}, wantErr: true},
		{name: "routable without token", options: Options{Listen: "192.168.1.10:8484"}, wantErr: true},
		{name: "non-loopback with token", options: Options{Listen: "0.0.0.0:8484", Token: "s3cret"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			options := tt.options
			options.Gateway = ServiceTarget{Target: "127.0.0.1:8585"}
			options.State = ServiceTarget{Target: "127.0.0.1:9090"}
			_, err := Resolve(options, slog.New(slog.DiscardHandler))
			if tt.wantErr && err == nil {
				t.Fatalf("Resolve(%q, token=%q) succeeded; a non-loopback listener without a token must fail closed", options.Listen, options.Token)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("Resolve(%q, token=%q): %v", options.Listen, options.Token, err)
			}
		})
	}
}

// TestUIMintsTokenFromTokenFile is the explicit opt-in the fail-closed rule
// leaves the operator: -token-file names where the dashboard token lives, and
// a non-loopback listener then starts with the minted token rather than
// refusing.
func TestUIMintsTokenFromTokenFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "web-token")
	resolved, err := Resolve(Options{
		Listen:    "0.0.0.0:8484",
		TokenFile: path,
		Gateway:   ServiceTarget{Target: "127.0.0.1:8585"},
		State:     ServiceTarget{Target: "127.0.0.1:9090"},
	}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("Resolve with -token-file: %v", err)
	}
	if resolved.Token == "" {
		t.Fatal("Resolve did not mint a token from -token-file")
	}
	again, err := Resolve(Options{
		Listen:    "0.0.0.0:8484",
		TokenFile: path,
		Gateway:   ServiceTarget{Target: "127.0.0.1:8585"},
		State:     ServiceTarget{Target: "127.0.0.1:9090"},
	}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("Resolve reusing -token-file: %v", err)
	}
	if again.Token != resolved.Token {
		t.Fatalf("token file minted a second token %q, want the persisted %q", again.Token, resolved.Token)
	}
}
