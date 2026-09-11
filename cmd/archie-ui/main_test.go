// Package main's smoke test runs the real archie-ui binary against a State
// Store and a Gateway reached over loopback gRPC, and drives it through HTTP
// the way an operator's browser does.
//
// The in-process suite (internal/app/archieui) proves the composition; this
// proves the process: flags and configuration file, listener bind, dashboard
// token, SPA assets, streamed chat, an operator task action crossing to the
// Gateway, the degradations the PRD ratified, dependency loss while running,
// and a clean SIGTERM. See docs/prds/ui-service-boundary.md, migration
// sequence step 6.

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"google.golang.org/grpc"

	"github.com/samcharles93/archie-core/internal/domain/health"
	"github.com/samcharles93/archie-core/internal/domain/messaging"
	"github.com/samcharles93/archie-core/internal/infrastructure/gatewayrpc"
	"github.com/samcharles93/archie-core/internal/infrastructure/staterpc"
	"github.com/samcharles93/archie-core/internal/store"
	"github.com/samcharles93/archie-core/internal/taskstate"
	"github.com/samcharles93/archie-core/internal/webui"
)

const dashboardToken = "smoke-token"

// fakeGateway stands in for the archie-gateway process. The dashboard only
// needs the Gateway to answer, stream, and accept an operator action, so the
// rest satisfy messaging.ChatContract without pretending to do more.
type fakeGateway struct {
	mu      sync.Mutex
	actions []string
}

func (f *fakeGateway) Snapshot(context.Context) (messaging.ChatSnapshot, error) {
	return messaging.ChatSnapshot{
		Sessions: []messaging.SessionContext{{
			SessionID: "s1",
			Source:    messaging.SessionSource{Platform: "web", ChannelID: "dash"},
		}},
		Models:                []string{"test/model"},
		ActiveModel:           "test/model",
		ActivePersonas:        map[string]string{},
		CancellationAvailable: true,
	}, nil
}

func (f *fakeGateway) GetSession(context.Context, string) (messaging.SessionContext, bool, error) {
	return messaging.SessionContext{}, false, nil
}

func (f *fakeGateway) RecentMessages(context.Context, string, int) ([]messaging.Message, error) {
	return nil, nil
}

func (f *fakeGateway) RecentTurns(context.Context, string, int) ([]messaging.TurnRecord, error) {
	return nil, nil
}

func (f *fakeGateway) Route(context.Context, messaging.Inbound) (messaging.ChatReply, error) {
	return messaging.ChatReply{Text: "routed", SessionID: "s1"}, nil
}

// Stream emits the contract's documented order: started, a delta, then done.
func (f *fakeGateway) Stream(ctx context.Context, in messaging.Inbound) (<-chan messaging.ChatEvent, error) {
	events := make(chan messaging.ChatEvent, 3)
	go func() {
		defer close(events)
		for _, event := range []messaging.ChatEvent{
			{Kind: "started", SessionID: "s1"},
			{Kind: "delta", Text: "hello " + in.Message.Text, SessionID: "s1"},
			{Kind: "done", SessionID: "s1"},
		} {
			select {
			case events <- event:
			case <-ctx.Done():
				return
			}
		}
	}()
	return events, nil
}

func (f *fakeGateway) Cancel(context.Context, string) (messaging.ChatCancellation, error) {
	return messaging.ChatCancellation{Cancelled: true}, nil
}

func (f *fakeGateway) SetPersona(context.Context, string, string) (bool, error) { return false, nil }

func (f *fakeGateway) ApplyTaskAction(context.Context, string, int64, taskstate.Action) (messaging.TaskActionResult, error) {
	return messaging.TaskActionResult{}, nil
}

func (f *fakeGateway) ApplyOperatorTaskAction(_ context.Context, id int64, action taskstate.Action) (messaging.TaskActionResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.actions = append(f.actions, fmt.Sprintf("%d:%s", id, action))
	return messaging.TaskActionResult{TaskID: id, Action: string(action)}, nil
}

func (f *fakeGateway) applied() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.actions...)
}

// repoRoot walks up from this test's source directory to the module root, so
// the build works regardless of the harness working directory.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine test source path")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above test source")
		}
		dir = parent
	}
}

func buildUIBinary(t *testing.T, dir string) string {
	t.Helper()
	bin := filepath.Join(dir, "archie-ui")
	cmd := exec.CommandContext(t.Context(), "go", "build", "-o", bin, "./cmd/archie-ui")
	cmd.Dir = repoRoot(t)
	cmd.Env = os.Environ()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build archie-ui: %v\n%s", err, out)
	}
	return bin
}

// syncBuffer is a concurrency-safe sink for the subprocess's stderr: the
// os/exec goroutine writes while the test reads it to recover the bound
// address and to report it on failure.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// serveGRPC runs register against a fresh loopback listener, returning the
// dial target and a stop func so a test can take the dependency away.
func serveGRPC(t *testing.T, register func(grpc.ServiceRegistrar)) (target string, stop func()) {
	t.Helper()
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := grpc.NewServer()
	register(server)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	return listener.Addr().String(), server.Stop
}

// writeUIConfig writes the config.toml an operator would keep: the service
// endpoints the UI dials live in it, so the process is started without
// -gateway-target or -state-target and has to read them itself. The rest is
// what configuration validation requires of any archie config file.
func writeUIConfig(t *testing.T, dir, stateTarget, gatewayTarget string) string {
	t.Helper()
	path := filepath.Join(dir, "config.toml")
	content := fmt.Sprintf(`bot_user = "archie-bot"
db_path = %q
[forge]
type = "github"
host = "https://github.example.com"
token = { engine = "env", key = "ARCHIE_GITHUB_TOKEN" }
[[repos]]
owner = "acme"
name = "widget"
[services.state]
target = %q
[services.gateway]
target = %q
`, filepath.Join(dir, "archie.db"), stateTarget, gatewayTarget)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write ui config: %v", err)
	}
	return path
}

// uiProcess is a running archie-ui, plus the address it actually bound.
type uiProcess struct {
	cmd  *exec.Cmd
	addr string
	log  *syncBuffer
}

func startUIProcess(t *testing.T, bin, cfg string) *uiProcess {
	t.Helper()
	// The process outlives t.Context() deliberately: that context is
	// cancelled just before cleanups run, which would SIGKILL archie-ui out
	// from under the graceful-shutdown check in stop. Cancelling this one is
	// registered first, so it runs last and only backstops a process that
	// ignored SIGTERM.
	ctx, cancel := context.WithCancel(context.WithoutCancel(t.Context()))
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, bin,
		"-config", cfg,
		"-listen", "127.0.0.1:0",
		"-token", dashboardToken,
	)
	cmd.Env = append(os.Environ(), "ARCHIE_GITHUB_TOKEN=test-token")
	var log syncBuffer
	cmd.Stderr = &log
	cmd.Stdout = &log
	if err := cmd.Start(); err != nil {
		t.Fatalf("start archie-ui: %v", err)
	}
	p := &uiProcess{cmd: cmd, log: &log}
	t.Cleanup(func() { p.stop(t) })

	// -listen 127.0.0.1:0 means the kernel picks the port, so the bound
	// address comes back out of the process's own startup log.
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) && p.addr == "" {
		if cmd.ProcessState != nil {
			t.Fatalf("archie-ui exited early:\n%s", log.String())
		}
		p.parseAddr()
		if p.addr == "" {
			time.Sleep(25 * time.Millisecond)
		}
	}
	if p.addr == "" {
		t.Fatalf("did not recover the bound address from the log:\n%s", log.String())
	}
	waitHealthy(t, p)
	return p
}

func (p *uiProcess) parseAddr() {
	scanner := bufio.NewScanner(strings.NewReader(p.log.String()))
	for scanner.Scan() {
		var record map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			continue
		}
		if msg, _ := record["msg"].(string); msg != "archie-ui running" {
			continue
		}
		if addr, ok := record["addr"].(string); ok && addr != "" {
			p.addr = addr
			return
		}
	}
}

func waitHealthy(t *testing.T, p *uiProcess) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+p.addr+"/healthz", nil)
		if err != nil {
			t.Fatalf("build liveness request: %v", err)
		}
		if resp, err := http.DefaultClient.Do(req); err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("/healthz never answered:\n%s", p.log.String())
}

// stop ends the process the way a service manager does and reports a
// non-graceful exit, which is how a shutdown-ordering regression surfaces.
func (p *uiProcess) stop(t *testing.T) {
	t.Helper()
	if p.cmd.Process == nil || p.cmd.ProcessState != nil {
		return
	}
	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Errorf("SIGTERM archie-ui: %v", err)
		return
	}
	done := make(chan error, 1)
	go func() { done <- p.cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("archie-ui exited %v after SIGTERM, want a clean shutdown:\n%s", err, p.log.String())
		}
	case <-time.After(20 * time.Second):
		_ = p.cmd.Process.Kill()
		t.Errorf("archie-ui did not exit within 20s of SIGTERM:\n%s", p.log.String())
	}
}

// reply is one dashboard response, already read and closed. The assertions
// want the status and the bytes rather than a live body, and handing back a
// closed value keeps the close where the read is.
type reply struct {
	status int
	header http.Header
	body   []byte
}

func (r reply) cookies() []*http.Cookie {
	return (&http.Response{Header: r.header}).Cookies()
}

// dashboard is a browser-shaped client for the running process.
type dashboard struct {
	t      *testing.T
	base   string
	client *http.Client
}

// noRedirect returns a dashboard that reports a redirect rather than
// following it, so an exchange can be inspected instead of chased. Following
// it here would land on the clean URL without the cookie jar a browser has.
func (d dashboard) noRedirect() dashboard {
	d.client = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	return d
}

func (d dashboard) do(req *http.Request) reply {
	d.t.Helper()
	client := d.client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		d.t.Fatalf("%s %s: %v", req.Method, req.URL.Path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		d.t.Fatalf("read %s: %v", req.URL.Path, err)
	}
	return reply{status: resp.StatusCode, header: resp.Header, body: body}
}

func (d dashboard) request(method, path, body string, authorized bool) *http.Request {
	d.t.Helper()
	var reader io.Reader = http.NoBody
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(d.t.Context(), method, d.base+path, reader)
	if err != nil {
		d.t.Fatalf("build %s %s: %v", method, path, err)
	}
	if authorized {
		req.Header.Set("Authorization", "Bearer "+dashboardToken)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Archie-CSRF", "1")
	}
	return req
}

func (d dashboard) get(path string) reply {
	d.t.Helper()
	return d.do(d.request(http.MethodGet, path, "", true))
}

// seedStore fills a real task database and publishes the configuration
// projection, so everything the dashboard renders arrives over the wire from
// a store this process never opens.
func seedStore(t *testing.T, dir string) *store.Store {
	t.Helper()
	st, err := store.Open(t.Context(), filepath.Join(dir, "tasks.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	if _, err := st.EnqueueIssue(t.Context(), "acme", "widget", 7, "smoke task", "body", "", ""); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	tasks, err := st.Tasks(t.Context(), 10)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("seeded tasks = %d (%v), want 1", len(tasks), err)
	}
	seeded := tasks[0]
	seeded.PRNumber = 34
	if err := st.Update(t.Context(), &seeded); err != nil {
		t.Fatalf("record PR number: %v", err)
	}

	published, err := json.Marshal(webui.ConfigView{
		Identity: webui.IdentityView{
			BotUser:   "archie-bot",
			ForgeType: "github",
			ForgeHost: "https://github.example.com",
		},
		Repositories: []webui.RepoView{{Owner: "acme", Name: "widget", Base: "main"}},
		Chat: webui.ChatView{
			ShowToolCalls:     true,
			Operator:          "Sam",
			ChannelConfigured: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PutConfigSnapshot(t.Context(), store.ConfigSnapshot{
		Schema: webui.ConfigViewSchema, Document: published,
	}); err != nil {
		t.Fatalf("publish config snapshot: %v", err)
	}
	return st
}

func TestUIProcessServesTheDashboardAgainstLiveDependencies(t *testing.T) {
	dir := t.TempDir()
	st := seedStore(t, dir)
	gateway := &fakeGateway{}

	stateTarget, stopState := serveGRPC(t, func(r grpc.ServiceRegistrar) {
		staterpc.RegisterServer(r, staterpc.Deps{
			Tasks: st, Captures: st, BindingDispatcher: st, ConfigSnapshots: st,
			Log: slog.New(slog.DiscardHandler),
		})
	})
	gatewayTarget, _ := serveGRPC(t, func(r grpc.ServiceRegistrar) {
		gatewayrpc.RegisterServer(r, gateway)
	})

	ui := startUIProcess(t, buildUIBinary(t, dir), writeUIConfig(t, dir, stateTarget, gatewayTarget))
	d := dashboard{t: t, base: "http://" + ui.addr}

	t.Run("liveness stays unauthenticated", func(t *testing.T) {
		for _, path := range []string{"/healthz", "/health"} {
			if got := d.do(d.request(http.MethodGet, path, "", false)); got.status != http.StatusOK {
				t.Errorf("GET %s = %d without a token, want 200: probes must not need the dashboard token", path, got.status)
			}
		}
	})

	t.Run("an unauthenticated API request is refused", func(t *testing.T) {
		if got := d.do(d.request(http.MethodGet, "/api/tasks", "", false)); got.status != http.StatusUnauthorized {
			t.Errorf("GET /api/tasks = %d without a token, want 401", got.status)
		}
		wrong := d.request(http.MethodGet, "/api/tasks", "", false)
		wrong.Header.Set("Authorization", "Bearer not-the-token")
		if got := d.do(wrong); got.status != http.StatusUnauthorized {
			t.Errorf("GET /api/tasks = %d with a wrong token, want 401", got.status)
		}
	})

	t.Run("the token in the URL is exchanged for a cookie", func(t *testing.T) {
		got := d.noRedirect().do(d.request(http.MethodGet, "/?t="+dashboardToken, "", false))
		if got.status != http.StatusSeeOther {
			t.Fatalf("GET /?t=<token> = %d, want 303 onto the clean URL", got.status)
		}
		var carried bool
		for _, cookie := range got.cookies() {
			if cookie.Value == dashboardToken && cookie.HttpOnly {
				carried = true
			}
		}
		if !carried {
			t.Errorf("no HttpOnly cookie carried the token; it would stay in browser history (%v)", got.cookies())
		}
	})

	t.Run("task rows carry forge links from the published projection", func(t *testing.T) {
		got := d.get("/api/tasks")
		if got.status != http.StatusOK {
			t.Fatalf("GET /api/tasks = %d (%s), want 200", got.status, got.body)
		}
		var rows []struct {
			ID       int64  `json:"id"`
			RepoURL  string `json:"repo_url"`
			IssueURL string `json:"issue_url"`
			PRURL    string `json:"pr_url"`
		}
		if err := json.Unmarshal(got.body, &rows); err != nil {
			t.Fatalf("decode task rows: %v (%s)", err, got.body)
		}
		if len(rows) != 1 {
			t.Fatalf("task rows = %d, want the 1 seeded behind the State Store (%s)", len(rows), got.body)
		}
		const repoURL = "https://github.example.com/acme/widget"
		if rows[0].RepoURL != repoURL || rows[0].IssueURL != repoURL+"/issues/7" || rows[0].PRURL != repoURL+"/pull/34" {
			t.Errorf("forge links = %q, %q, %q; want them built from the daemon's published projection, since this process holds no configuration of its own",
				rows[0].RepoURL, rows[0].IssueURL, rows[0].PRURL)
		}
	})

	t.Run("the SPA and its assets are served", func(t *testing.T) {
		index := d.get("/")
		if index.status != http.StatusOK || !bytes.Contains(bytes.ToLower(index.body), []byte("<!doctype html")) {
			t.Errorf("GET / = %d serving %d bytes; want the embedded index.html", index.status, len(index.body))
		}
		for path, wantType := range map[string]string{
			"/assets/index.js":  "javascript",
			"/assets/index.css": "css",
		} {
			asset := d.get(path)
			if asset.status != http.StatusOK {
				t.Errorf("GET %s = %d, want the embedded asset", path, asset.status)
				continue
			}
			if contentType := asset.header.Get("Content-Type"); !strings.Contains(contentType, wantType) {
				t.Errorf("GET %s Content-Type = %q, want %s", path, contentType, wantType)
			}
			if len(asset.body) == 0 {
				t.Errorf("GET %s served an empty body", path)
			}
		}
	})

	t.Run("chat streams over the Gateway contract", func(t *testing.T) {
		req := d.request(http.MethodPost, "/api/chat/stream", `{"text":"there","channel_id":"dash"}`, true)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("POST /api/chat/stream: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("POST /api/chat/stream = %d, want 200", resp.StatusCode)
		}
		if contentType := resp.Header.Get("Content-Type"); !strings.Contains(contentType, "text/event-stream") {
			t.Fatalf("stream Content-Type = %q, want text/event-stream", contentType)
		}
		var kinds []string
		var text string
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			payload, ok := strings.CutPrefix(scanner.Text(), "data: ")
			if !ok {
				continue
			}
			var frame struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			if err := json.Unmarshal([]byte(payload), &frame); err != nil {
				t.Fatalf("decode stream frame %q: %v", payload, err)
			}
			kinds = append(kinds, frame.Type)
			if frame.Type == "delta" {
				text += frame.Text
			}
			if frame.Type == "done" {
				break
			}
		}
		if strings.Join(kinds, ",") != "started,delta,done" {
			t.Errorf("stream frames = %v, want started,delta,done in order", kinds)
		}
		if text != "hello there" {
			t.Errorf("streamed text = %q, want the Gateway's reply relayed verbatim", text)
		}
	})

	t.Run("an operator task action crosses to the Gateway", func(t *testing.T) {
		listed := d.get("/api/tasks")
		var rows []struct {
			ID int64 `json:"id"`
		}
		if err := json.Unmarshal(listed.body, &rows); err != nil || len(rows) != 1 {
			t.Fatalf("decode task rows: %v (%s)", err, listed.body)
		}
		path := fmt.Sprintf("/api/tasks/%d/action", rows[0].ID)
		action := string(taskstate.ActionCancel)
		if got := d.do(d.request(http.MethodPost, path, `{"action":"`+action+`"}`, true)); got.status != http.StatusOK {
			t.Fatalf("POST %s = %d (%s), want 200", path, got.status, got.body)
		}
		want := fmt.Sprintf("%d:%s", rows[0].ID, action)
		if applied := gateway.applied(); len(applied) != 1 || applied[0] != want {
			t.Errorf("Gateway saw %v, want exactly [%s]: the UI must not mutate task state itself", applied, want)
		}

		// The same action without the CSRF header is refused, so a
		// cross-site form post cannot drive the daemon.
		noCSRF := d.request(http.MethodPost, path, "", true)
		noCSRF.Body = io.NopCloser(strings.NewReader(`{"action":"` + action + `"}`))
		noCSRF.Header.Set("Content-Type", "application/json")
		if got := d.do(noCSRF); got.status != http.StatusForbidden {
			t.Errorf("POST %s without the CSRF header = %d, want 403", path, got.status)
		}
	})

	t.Run("capture intake is served without the dashboard token", func(t *testing.T) {
		accepted := d.do(d.request(http.MethodPost, "/webhooks/capture/demo", "", false))
		if accepted.status != http.StatusAccepted {
			t.Fatalf("POST /webhooks/capture/demo = %d (%s), want 202: this process is the only listener serving intake after the cutover", accepted.status, accepted.body)
		}
		listed := d.get("/api/captures")
		if listed.status != http.StatusOK || !strings.Contains(string(listed.body), `"enabled":true`) {
			t.Errorf("GET /api/captures = %d (%s), want the capture list over the State Store", listed.status, listed.body)
		}
	})

	t.Run("routes with no owner degrade explicitly", func(t *testing.T) {
		// The daemon's log feed is host-local and has no contract, so the
		// page reports itself off rather than erroring
		// (docs/prds/ui-service-boundary.md, route family table).
		logs := d.get("/api/logs")
		if logs.status != http.StatusOK || !strings.Contains(string(logs.body), `"disabled":true`) {
			t.Errorf("GET /api/logs = %d (%s), want an explicit disabled report", logs.status, logs.body)
		}
		if version := d.get("/api/version"); version.status != http.StatusNotImplemented {
			t.Errorf("GET /api/version = %d, want 501: host-local update reporting stays with the daemon", version.status)
		}

		// Configuration renders from the published snapshot, and this
		// process is not a second writer.
		rendered := d.get("/api/config")
		if rendered.status != http.StatusOK {
			t.Fatalf("GET /api/config = %d (%s), want 200", rendered.status, rendered.body)
		}
		var view webui.ConfigView
		if err := json.Unmarshal(rendered.body, &view); err != nil {
			t.Fatalf("decode config view: %v (%s)", err, rendered.body)
		}
		if view.Identity.BotUser != "archie-bot" || view.Editable {
			t.Errorf("config view = %s, want the published projection rendered read-only", rendered.body)
		}
		if got := d.do(d.request(http.MethodPatch, "/api/config", `{}`, true)); got.status != http.StatusServiceUnavailable {
			t.Errorf("PATCH /api/config = %d, want 503", got.status)
		}

		// The setup checklist reads the same projection. It reported
		// nothing here while it needed a live config.Holder this process
		// never receives (archie-core-ml30).
		setup := d.get("/api/setup")
		if setup.status != http.StatusOK {
			t.Fatalf("GET /api/setup = %d (%s), want 200", setup.status, setup.body)
		}
		var checklist struct {
			Steps []struct {
				Title string `json:"title"`
				Done  bool   `json:"done"`
			} `json:"steps"`
			Operator string `json:"operator"`
		}
		if err := json.Unmarshal(setup.body, &checklist); err != nil {
			t.Fatalf("decode setup checklist: %v (%s)", err, setup.body)
		}
		if len(checklist.Steps) == 0 {
			t.Errorf("setup checklist is empty; the published projection carries what it reads (%s)", setup.body)
		}
		if checklist.Operator != "Sam" {
			t.Errorf("setup operator = %q, want the published name", checklist.Operator)
		}
		for _, step := range checklist.Steps {
			if step.Title == "Connect a repository" && !step.Done {
				t.Errorf("checklist says no repository is connected; the projection publishes one (%s)", setup.body)
			}
		}
	})

	// Last: taking the State Store away is not reversible for this process.
	t.Run("losing a dependency degrades readiness without failing liveness", func(t *testing.T) {
		healthy := d.get("/health/detailed")
		if healthy.status != http.StatusOK {
			t.Fatalf("GET /health/detailed = %d (%s), want 200", healthy.status, healthy.body)
		}
		var report health.Report
		if err := json.Unmarshal(healthy.body, &report); err != nil {
			t.Fatalf("decode readiness: %v (%s)", err, healthy.body)
		}
		if report.Status != health.StatusOK {
			t.Fatalf("readiness = %s with both dependencies up, want ok (%s)", report.Status, healthy.body)
		}

		stopState()
		degraded := d.get("/health/detailed")
		if err := json.Unmarshal(degraded.body, &report); err != nil {
			t.Fatalf("decode readiness after outage: %v (%s)", err, degraded.body)
		}
		if report.Status == health.StatusOK {
			t.Errorf("readiness stayed ok with the State Store stopped (%s)", degraded.body)
		}
		if live := d.do(d.request(http.MethodGet, "/healthz", "", false)); live.status != http.StatusOK {
			t.Errorf("GET /healthz = %d during a dependency outage, want 200: liveness must not imply dependency readiness", live.status)
		}
	})
}
