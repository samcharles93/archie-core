// Package main smoke-tests the standalone archie-state-store process by
// building the real binary, running it against a minimal config on the
// loopback interface, and driving it through the State Store gRPC contract
// exactly the way a consumer (archied, archie-gateway, archie-agent) does.
//
// This is the .4.7 end-to-end verification for
// docs/prds/state-store-contract.md (rev. 2c): it proves the process owns the
// single archie.db task SQLite, serves the combined task/event/capture
// surface over one gRPC service, preserves error-sentinel fidelity across the
// wire, honours a per-call deadline, survives a restart/recovery cycle, and
// is the sole owner of the SQLite file. See §5 (binary layout), §6 (handoff),
// §7 (error semantics), §11 (remote adapter) and §12 step 7/8 (single owner).

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/samcharles93/archie-core/internal/domain/binding"
	"github.com/samcharles93/archie-core/internal/domain/mapping"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/infrastructure/staterpc"
	"github.com/samcharles93/archie-core/internal/store"
)

// repoRoot walks up from the test's source directory to find the module root
// (where go.mod lives). The test runs in the package directory
// (cmd/archie-state-store), so the module root is above it; walking keeps the
// result correct regardless of the harness working directory.
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

// buildBinary compiles ./cmd/archie-state-store into dir and returns the path.
// This is the real binary the smoke test exercises, not a test double -- the
// point of .4.7 is to verify the actual process.
func buildBinary(t *testing.T, dir string) string {
	t.Helper()
	bin := filepath.Join(dir, "archie-state-store")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/archie-state-store")
	cmd.Dir = repoRoot(t)
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build archie-state-store: %v\n%s", err, out)
	}
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("built binary not found: %v", err)
	}
	return bin
}

// writeMinimalConfig writes a config.toml that passes configuration.Validate
// while keeping the process minimal: the standalone binary reads its gRPC
// listen address from -listen, not from [services.state].target, so the config
// only needs the fields validation requires plus the db_path it owns. The
// forge token resolves from an env var, so no real credential is needed.
func writeMinimalConfig(t *testing.T, dir string) string {
	t.Helper()
	cfg := filepath.Join(dir, "config.toml")
	content := fmt.Sprintf(`bot_user = "archie-bot"
db_path = %q
[forge]
type = "github"
host = "https://github.example.com"
token = { engine = "env", key = "ARCHIE_GITHUB_TOKEN" }
[[repos]]
owner = "acme"
name = "widget"
`, filepath.Join(dir, "archie.db"))
	if err := os.WriteFile(cfg, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return cfg
}

// stateStoreProcess wraps a running archie-state-store subprocess plus the
// addresses it bound. addr is the gRPC listen address; readyAddr is the
// readiness HTTP surface. It is stopped gracefully on cleanup by SIGTERM, the
// signal the systemd unit ("Restart=on-failure") and the daemon use to drain
// the server.
type stateStoreProcess struct {
	cmd       *exec.Cmd
	addr      string
	readyAddr string
	log       *strings.Builder
}

// startStateStoreProcess starts bin with config, waiting for the gRPC listener
// to come up and the readiness surface to answer /healthz. It parses the
// binary's stderr log for the bound addresses because --listen 127.0.0.1:0
// lets the process choose ephemeral ports, and the real address (not the
// configured one) is what a consumer must dial.
func startStateStoreProcess(t *testing.T, bin, cfg string) *stateStoreProcess {
	t.Helper()
	cmd := exec.Command(bin, "-config", cfg, "-listen", "127.0.0.1:0", "-ready-addr", "127.0.0.1:0")
	cmd.Env = append(os.Environ(), "ARCHIE_GITHUB_TOKEN=test-token")
	var log strings.Builder
	cmd.Stderr = &log
	cmd.Stdout = &log
	if err := cmd.Start(); err != nil {
		t.Fatalf("start state store: %v", err)
	}
	p := &stateStoreProcess{cmd: cmd, log: &log}
	t.Cleanup(func() { p.stop(t) })

	// Recover the ephemeral ports from the JSON log lines the binary emits.
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if p.parseAddrs() {
			break
		}
		if cmd.ProcessState != nil {
			t.Fatalf("state store exited early:\n%s", log.String())
		}
		time.Sleep(25 * time.Millisecond)
	}
	if p.addr == "" || p.readyAddr == "" {
		t.Fatalf("did not recover bound addresses from log:\n%s", log.String())
	}

	// The readiness surface is the liveness signal the runbook uses; poll it
	// until it answers (the gRPC listener may come up a tick ahead of it).
	deadline = time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if resp, err := http.Get("http://" + p.readyAddr + "/healthz"); err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return p
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("readiness /healthz never became healthy:\n%s", log.String())
	return nil
}

// parseAddrs scans the captured log for the bound gRPC and readiness
// addresses. The binary logs "archie-state-store running" with the gRPC addr
// and "state store readiness listening" with the HTTP addr.
func (p *stateStoreProcess) parseAddrs() bool {
	if p.addr != "" && p.readyAddr != "" {
		return true
	}
	scanner := bufio.NewScanner(strings.NewReader(p.log.String()))
	for scanner.Scan() {
		line := scanner.Text()
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		if msg, _ := rec["msg"].(string); msg == "archie-state-store running" {
			if addr, ok := rec["addr"].(string); ok && addr != "" && p.addr == "" {
				p.addr = addr
			}
		}
		if msg, _ := rec["msg"].(string); msg == "state store readiness listening" {
			if addr, ok := rec["addr"].(string); ok && addr != "" && p.readyAddr == "" {
				p.readyAddr = strings.TrimPrefix(addr, "http://")
			}
		}
	}
	return p.addr != "" && p.readyAddr != ""
}

// stop drains the process with SIGTERM and waits for it to exit. This is the
// same graceful-stop path serveStateStore's signal handling implements.
func (p *stateStoreProcess) stop(t *testing.T) {
	t.Helper()
	if p.cmd.Process == nil {
		return
	}
	_ = p.cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan struct{})
	go func() { _ = p.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		_ = p.cmd.Process.Kill()
		<-done
	}
}

// contract is the combined consumer surface a daemon/gateway/agent mixes: the
// task lifecycle + events surface (store.TaskStore) and the capture surface.
// Mapping/binding/dispatch is exercised separately in
// TestStateStoreRealProcessRemoteSurfaces via the concrete Client, which
// asserts against each narrow interface at compile time.
type contract interface {
	store.TaskStore
	store.CaptureStore
}

// dial opens a gRPC connection to the state store process and wraps it in the
// remote contract adapter, exactly as the daemon/gateway/agent consumer does
// (internal/infrastructure/staterpc.Client).
func dial(t *testing.T, addr string) contract {
	t.Helper()
	conn, err := grpc.NewClient("passthrough:///"+addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return staterpc.NewClient(conn)
}

// capture builds a CapturedEvent without pulling the whole store field set
// into every call site.
func capture(source, body string) store.CapturedEvent {
	return store.CapturedEvent{Source: source, Body: body, Authenticated: true}
}

// TestStateStoreRealProcessSmoke exercises the actual archie-state-store
// binary end-to-end: the combined task/event/capture view through one consumer
// adapter, error-sentinel fidelity across the wire, the not-found-as-(nil,nil)
// convention, and single-owner SQLite (the binary owns the one archie.db file
// and the consumer dials gRPC without opening any store file).
func TestStateStoreRealProcessSmoke(t *testing.T) {
	dir := t.TempDir()
	bin := buildBinary(t, dir)
	cfg := writeMinimalConfig(t, dir)
	st := startStateStoreProcess(t, bin, cfg)
	cl := dial(t, st.addr)

	ctx := t.Context()

	// Combined task/event/capture view: a task, an event on that task, and an
	// unrelated capture written through one consumer, read back across all
	// three surfaces in a single pass (the dashboard/observability read path).
	task, err := cl.EnqueueChatTask(ctx, "acme", "widget", "verify state store", "body", "implement", "")
	if err != nil || task == nil {
		t.Fatalf("EnqueueChatTask = %+v, %v", task, err)
	}
	if err := cl.Transition(ctx, task.ID, task.Status, "running", "smoke started"); err != nil {
		t.Fatalf("Transition: %v", err)
	}
	if _, err := cl.InsertEvent(ctx, events.Event{Kind: "stage_finish", TaskID: task.ID, Data: map[string]any{"duration_ms": 12.0}}); err != nil {
		t.Fatalf("InsertEvent: %v", err)
	}
	if capID, err := cl.InsertCapture(ctx, capture("sentry", `{"id":1}`), 0, 0); err != nil || capID == 0 {
		t.Fatalf("InsertCapture = (%d, %v)", capID, err)
	}

	got, err := cl.TaskByID(ctx, task.ID)
	if err != nil || got == nil || got.ID != task.ID || got.Status != "running" {
		t.Fatalf("TaskByID after transition = %+v, %v", got, err)
	}
	if evs, err := cl.TaskEvents(ctx, task.ID); err != nil || len(evs) != 1 || evs[0].Kind != "stage_finish" {
		t.Fatalf("TaskEvents = %+v, %v", evs, err)
	}
	if caps, err := cl.ListCaptures(ctx, 10); err != nil || len(caps) != 1 || caps[0].Source != "sentry" {
		t.Fatalf("ListCaptures = %+v, %v", caps, err)
	}
	if counts, err := cl.StatusCounts(ctx); err != nil || counts["running"] != 1 {
		t.Fatalf("StatusCounts = %+v, %v", counts, err)
	}

	t.Run("error_sentinel_fidelity", func(t *testing.T) {
		// A stale transition must rehydrate to store.ErrStaleTransition across
		// the wire (§7). Because the binary is up and we dialed it, a sentinel
		// mismatch here would surface as a transport error, so a pass proves
		// the gRPC mapError/unmapError round-trip, not just the in-process
		// adapter.
		err := cl.Transition(ctx, task.ID, "queued", "merged", "")
		if !errors.Is(err, store.ErrStaleTransition) {
			t.Fatalf("stale Transition = %v, want store.ErrStaleTransition", err)
		}
	})

	t.Run("not_found_is_not_error", func(t *testing.T) {
		// Reading a missing task returns (nil, nil), never a NotFound gRPC
		// error (§7's found=false-not-error convention).
		missing, err := cl.TaskByID(ctx, task.ID+999999)
		if err != nil || missing != nil {
			t.Fatalf("TaskByID missing = (%+v, %v), want (nil, nil)", missing, err)
		}
	})

	t.Run("single_owner_sqlite", func(t *testing.T) {
		// The binary owns one task SQLite file (db_path + "-tasks.sqlite");
		// the consumer called only gRPC methods and never opened a store file.
		// The config anchor path itself must not be opened directly (no dual
		// store ownership, §12 step 8).
		taskFile := filepath.Join(dir, "archie.db-tasks.sqlite")
		if _, err := os.Stat(taskFile); err != nil {
			t.Fatalf("state store did not own %s: %v", taskFile, err)
		}
		if _, err := os.Stat(filepath.Join(dir, "archie.db")); err == nil {
			t.Fatalf("config anchor path was opened directly; dual store ownership violated §12 step 8")
		}
	})
}

// TestStateStoreRealProcessDeadline verifies the per-call deadline behaviour
// the state store contract requires (§6 StoreTimeout): a consumer context that
// is cancelled before the call completes must surface a deadline/cancel error
// rather than hang or silently succeed.
func TestStateStoreRealProcessDeadline(t *testing.T) {
	dir := t.TempDir()
	bin := buildBinary(t, dir)
	cfg := writeMinimalConfig(t, dir)
	st := startStateStoreProcess(t, bin, cfg)
	cl := dial(t, st.addr)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := cl.StatusCounts(ctx)
	if err == nil {
		t.Fatal("StatusCounts on a cancelled context returned nil; expected a deadline/cancel error")
	}
	if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("StatusCounts cancelled = %v, want context.Canceled or DeadlineExceeded", err)
	}
}

// TestStateStoreRealProcessRestartRecovery starts the process, writes a task,
// stops it (SIGTERM), restarts it on the same config/db_path, and verifies the
// task and its events were recovered from the on-disk SQLite -- the
// restart/recovery path the runbook relies on when archie-state-store is
// restarted as a service.
func TestStateStoreRealProcessRestartRecovery(t *testing.T) {
	dir := t.TempDir()
	bin := buildBinary(t, dir)
	cfg := writeMinimalConfig(t, dir)

	first := startStateStoreProcess(t, bin, cfg)
	s1 := dial(t, first.addr)
	task, err := s1.EnqueueChatTask(t.Context(), "acme", "widget", "survive restart", "body", "implement", "")
	if err != nil || task == nil {
		t.Fatalf("first EnqueueChatTask = %+v, %v", task, err)
	}
	if _, err := s1.InsertEvent(t.Context(), events.Event{Kind: "run_started", TaskID: task.ID}); err != nil {
		t.Fatalf("first InsertEvent: %v", err)
	}
	first.stop(t)

	// Restart on the same config; the SQLite file persists on disk.
	second := startStateStoreProcess(t, bin, cfg)
	s2 := dial(t, second.addr)

	got, err := s2.TaskByID(t.Context(), task.ID)
	if err != nil || got == nil || got.Title != "survive restart" {
		t.Fatalf("TaskByID after restart = %+v, %v", got, err)
	}
	if evs, err := s2.TaskEvents(t.Context(), task.ID); err != nil || len(evs) != 1 {
		t.Fatalf("TaskEvents after restart = %+v, %v", evs, err)
	}
	// Stale-running work reopened on a fresh process is recovered by
	// RecoverStale, which must return without error on the restarted store.
	if _, err := s2.RecoverStale(t.Context()); err != nil {
		t.Fatalf("RecoverStale after restart: %v", err)
	}
}

// TestStateStoreRealProcessRemoteSurfaces exercises the capture/mapping/
// binding surfaces the standalone process fronts (the daemon/webui surfaces
// swapped to the adapter in .4.4/.4.5), proving one process serves the full
// combined contract a consumer mixes.
func TestStateStoreRealProcessRemoteSurfaces(t *testing.T) {
	dir := t.TempDir()
	bin := buildBinary(t, dir)
	cfg := writeMinimalConfig(t, dir)
	st := startStateStoreProcess(t, bin, cfg)

	conn, err := grpc.NewClient("passthrough:///"+st.addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	cl := staterpc.NewClient(conn)

	ctx := t.Context()
	mappingID, err := cl.InsertMapping(ctx, mapping.Mapping{Name: "m1", Fields: []mapping.Field{{Name: "a", Path: "a", Type: mapping.TypeString}}})
	if err != nil || mappingID == 0 {
		t.Fatalf("InsertMapping = (%d, %v)", mappingID, err)
	}
	if got, err := cl.GetMapping(ctx, mappingID); err != nil || got == nil || got.Name != "m1" {
		t.Fatalf("GetMapping = %+v, %v", got, err)
	}
	bindingID, err := cl.InsertBinding(ctx, binding.Binding{
		Name: "b1", Matcher: binding.Matcher{Source: "sentry"}, MappingID: mappingID,
		Workflow: "implement", Secret: "0123456789abcdef0123456789abcdef",
	})
	if err != nil || bindingID == 0 {
		t.Fatalf("InsertBinding = (%d, %v)", bindingID, err)
	}
	// A binding starts as draft and must be edited (draft -> pending_approval)
	// then approved (pending_approval -> armed) before it is armed for a
	// source. Drive the state machine so ArmedBindingsForSource returns it.
	if err := cl.UpdateBinding(ctx, binding.Binding{
		ID: bindingID, Name: "b1", Matcher: binding.Matcher{Source: "sentry"},
		MappingID: mappingID, Workflow: "implement", Owner: "acme", Repo: "widget",
		Secret: "0123456789abcdef0123456789abcdef",
	}); err != nil {
		t.Fatalf("UpdateBinding: %v", err)
	}
	if err := cl.ApproveBinding(ctx, bindingID); err != nil {
		t.Fatalf("ApproveBinding: %v", err)
	}
	if armed, err := cl.ArmedBindingsForSource(ctx, "sentry"); err != nil || len(armed) != 1 || armed[0].ID != bindingID {
		t.Fatalf("ArmedBindingsForSource = %+v, %v", armed, err)
	}
	// Remote surfaces share error-sentinel fidelity on the wire too.
	missing := mapping.Mapping{ID: mappingID + 999999, Name: "x", Fields: []mapping.Field{{Name: "a", Path: "a", Type: mapping.TypeString}}}
	if err := cl.UpdateMapping(ctx, missing); !errors.Is(err, store.ErrMappingNotFound) {
		t.Fatalf("UpdateMapping missing = %v, want ErrMappingNotFound", err)
	}
}
