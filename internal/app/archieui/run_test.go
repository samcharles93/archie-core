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
	"strconv"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"

	"github.com/samcharles93/archie-core/internal/domain/health"
	"github.com/samcharles93/archie-core/internal/domain/messaging"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/gateway"
	"github.com/samcharles93/archie-core/internal/infrastructure/gatewayrpc"
	"github.com/samcharles93/archie-core/internal/infrastructure/staterpc"
	"github.com/samcharles93/archie-core/internal/store"
	"github.com/samcharles93/archie-core/internal/taskstate"
	"github.com/samcharles93/archie-core/internal/webui"
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
	seeded, err := st.EnqueueChatTask(t.Context(), "acme", "widget", "remote summary", "body", "implement", "")
	if err != nil {
		t.Fatalf("seed task: %v", err)
	}
	// The daemon publishes the configuration page's projection; this process
	// only renders it (archie-core-ymut). The forge host is published too: it
	// is what the run-detail reads resolve their repository and pull-request
	// links from, the same projection the task list uses.
	published, err := json.Marshal(webui.ConfigView{
		Identity: webui.IdentityView{BotUser: "archie", ForgeType: "github", ForgeHost: "https://forge.example"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PutConfigSnapshot(t.Context(), store.ConfigSnapshot{
		Schema: webui.ConfigViewSchema, Document: published,
	}); err != nil {
		t.Fatalf("publish config snapshot: %v", err)
	}

	// The other half of the configuration page is per-attempt provenance, and
	// the three run-detail reads are backed by the same State Store contract as
	// the rest of the task surface. The statements below are what other
	// processes write in production -- the daemon's dispatch capture, the
	// worker's change capture -- attributed to one attempt, so the assertions
	// further down read them back through the real client rather than a fake.
	seedAt := time.Now().UTC()
	for _, e := range []events.Event{
		{Kind: events.KindStageStart, TaskID: seeded.ID, Attempt: 1, Stage: "prepare", At: seedAt},
		{Kind: events.KindStageFinish, TaskID: seeded.ID, Attempt: 1, Stage: "prepare", At: seedAt.Add(time.Second), Data: map[string]any{"duration_ms": 1000}},
		{Kind: events.KindConfigCaptured, TaskID: seeded.ID, Attempt: 1, At: seedAt, Data: map[string]any{
			"schema": events.ConfigCapturedSchema, "document": map[string]any{"bot_user": "archie"},
		}},
		{Kind: events.KindChangesCaptured, TaskID: seeded.ID, Attempt: 1, Stage: "open-pr", At: seedAt, Data: map[string]any{
			"schema": events.ChangesCapturedSchema, "owner": "acme", "repo": "widget",
			"base": "main", "branch": "feat/1-widget", "head_sha": "head-sha", "base_sha": "base-sha",
			"pr_number": 7, "captured_after": "open-pr", "truncated": false,
			"files":  []any{map[string]any{"path": "internal/x.go", "old_path": "", "status": "modified", "additions": 12, "deletions": 3, "binary": false}},
			"totals": map[string]any{"files": 1, "additions": 12, "deletions": 3},
		}},
	} {
		if _, err := st.InsertEvent(t.Context(), e); err != nil {
			t.Fatalf("seed %s event: %v", e.Kind, err)
		}
	}

	stateTarget, stopState := serveGRPC(t, func(r grpc.ServiceRegistrar) {
		staterpc.RegisterServer(r, staterpc.Deps{Tasks: st, Captures: st, BindingDispatcher: st, ConfigSnapshots: st, Log: slog.New(slog.DiscardHandler)})
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

	// No shared config.Holder: the page renders the snapshot the daemon
	// published, and this process is not a second writer.
	code, body = get("/api/config")
	if code != http.StatusOK {
		t.Fatalf("GET /api/config = %d (%s), want 200", code, body)
	}
	var rendered webui.ConfigView
	if err := json.Unmarshal(body, &rendered); err != nil {
		t.Fatalf("decode config view: %v", err)
	}
	if rendered.Identity.BotUser != "archie" {
		t.Fatalf("config view = %s, want the published projection", body)
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
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("PATCH /api/config = %d, want 405: configuration is read here and written through the control plane", resp.StatusCode)
	}

	// The run-detail reads. These three routes have no fake behind them: the
	// rail, the capture and the raw event list all come back over the State
	// Store listener the UI dialled, so each assertion below is the real
	// client's answer about the attempt-attributed set seeded above.
	taskPath := "/api/tasks/" + strconv.FormatInt(seeded.ID, 10)
	code, body = get(taskPath + "/attempts")
	if code != http.StatusOK {
		t.Fatalf("GET /attempts = %d (%s), want 200", code, body)
	}
	var rail struct {
		Attempts []struct {
			Attempt int    `json:"attempt"`
			Status  string `json:"status"`
			Stages  []struct {
				Name   string `json:"name"`
				Status string `json:"status"`
			} `json:"stages"`
		} `json:"attempts"`
	}
	if err := json.Unmarshal(body, &rail); err != nil {
		t.Fatalf("decode attempts: %v (%s)", err, body)
	}
	if len(rail.Attempts) != 1 || rail.Attempts[0].Attempt != 1 || rail.Attempts[0].Status != "ok" {
		t.Fatalf("attempts = %s, want exactly the seeded attempt 1 recorded ok", body)
	}
	if len(rail.Attempts[0].Stages) != 1 || rail.Attempts[0].Stages[0].Name != "prepare" || rail.Attempts[0].Stages[0].Status != "ok" {
		t.Fatalf("stage rail = %s, want the attempt's own prepare stage", body)
	}

	code, body = get(taskPath + "/changes?attempt=1")
	if code != http.StatusOK {
		t.Fatalf("GET /changes = %d (%s), want 200", code, body)
	}
	var changes struct {
		Found    bool `json:"found"`
		Captures []struct {
			PRNumber int    `json:"pr_number"`
			PRURL    string `json:"pr_url"`
			Files    []struct {
				Path string `json:"path"`
			} `json:"files"`
		} `json:"captures"`
	}
	if err := json.Unmarshal(body, &changes); err != nil {
		t.Fatalf("decode changes: %v (%s)", err, body)
	}
	if !changes.Found || len(changes.Captures) != 1 {
		t.Fatalf("changes = %s, want the one capture recorded for the attempt", body)
	}
	if changes.Captures[0].PRNumber != 7 || changes.Captures[0].PRURL != "https://forge.example/acme/widget/pull/7" {
		t.Errorf("capture = pr %d %q, want #7 linked through the published forge projection (%s)",
			changes.Captures[0].PRNumber, changes.Captures[0].PRURL, body)
	}
	if len(changes.Captures[0].Files) != 1 || changes.Captures[0].Files[0].Path != "internal/x.go" {
		t.Errorf("capture files = %s, want the file the attempt changed", body)
	}

	code, body = get(taskPath + "/debug?attempt=1")
	if code != http.StatusOK {
		t.Fatalf("GET /debug = %d (%s), want 200", code, body)
	}
	var debug struct {
		Attempt int `json:"attempt"`
		Task    struct {
			ID int64 `json:"id"`
		} `json:"task"`
		Events []struct {
			Kind    string `json:"kind"`
			Attempt int    `json:"attempt"`
		} `json:"events"`
	}
	if err := json.Unmarshal(body, &debug); err != nil {
		t.Fatalf("decode debug: %v (%s)", err, body)
	}
	if debug.Task.ID != seeded.ID || debug.Attempt != 1 {
		t.Errorf("debug = task %d attempt %d, want the seeded task and attempt 1 (%s)", debug.Task.ID, debug.Attempt, body)
	}
	kinds := map[string]int{}
	for _, e := range debug.Events {
		kinds[e.Kind]++
		if e.Attempt != 1 {
			t.Errorf("debug event %s carries attempt %d, want 1: attribution must survive the store it was read from", e.Kind, e.Attempt)
		}
	}
	for _, kind := range []string{events.KindStageStart, events.KindStageFinish, events.KindConfigCaptured, events.KindChangesCaptured} {
		if kinds[kind] != 1 {
			t.Errorf("debug events = %s, want exactly one %s", body, kind)
		}
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

	// The capture POST is served by this process from the cutover change:
	// the receiver persists through the State Store contract, and the
	// daemon's binding-dispatch loop consumes what arrives from the same
	// store (archie-core-8cda.5.4). It bypasses the dashboard token like
	// /healthz -- capture accepts unauthenticated senders by design.
	capReq, err := http.NewRequestWithContext(t.Context(), http.MethodPost, ts.URL+"/webhooks/capture/demo", http.NoBody)
	if err != nil {
		t.Fatalf("capture request: %v", err)
	}
	capResp, err := ts.Client().Do(capReq)
	if err != nil {
		t.Fatalf("POST /webhooks/capture/demo: %v", err)
	}
	capResp.Body.Close()
	if capResp.StatusCode != http.StatusAccepted {
		t.Errorf("POST /webhooks/capture/demo = %d, want an accepted capture: this process is the only listener serving intake after the cutover", capResp.StatusCode)
	}
	code, body = get("/api/captures")
	if code != http.StatusOK || !strings.Contains(string(body), `"enabled":true`) {
		t.Errorf("GET /api/captures = %d (%s), want an enabled capture list over the State Store", code, body)
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
