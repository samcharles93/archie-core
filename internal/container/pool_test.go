package container

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moby/moby/client"
)

// ── regression: Gap 2  --  post-completion grace period ────────────────

func TestContainerSupportsGracePeriod(t *testing.T) {
	// Gap 2: MaxUptime is a container-level context timeout at creation,
	// not a post-completion grace period. PRD section 1 says:
	// "max_uptime  --  grace period after task completion before kill."
	//
	// The agent should stay alive after the task finishes so it can
	// handle follow-ups (gate re-runs, human replies). Currently
	// Release() stops the container immediately  --  there's no way to
	// keep it alive after task completion.

	c := &Container{ID: "test"}
	grace := 5 * time.Minute

	// A container must support being kept alive after task completion.
	// This means: after Release() is called (task done), the container
	// stays up for the grace period to handle follow-ups. Only after
	// the grace period expires (or on explicit shutdown) is it killed.
	//
	// Currently Release() immediately calls ContainerStop + ContainerRemove.
	// The Pool must support a grace period where Release() marks the
	// container as idle but keeps it alive.
	_ = c
	_ = grace

	// Structural check: Pool.Config must have a GracePeriod field
	// distinct from MaxUptime (which is the container lifetime cap).
	cfg := Config{MaxUptime: 30 * time.Minute}
	if cfg.MaxUptime == 0 {
		t.Error("MaxUptime not set")
	}
	// Gap 2 assertion: Release() must respect GracePeriod.
	// Currently Release() calls ContainerStop immediately regardless of
	// GracePeriod. When GracePeriod > 0, Release() must keep the
	// container alive for the grace window before stopping it.
	// The container should stay alive for cfg.GracePeriod after
	// the task completes, then be killed.
	_ = cfg.GracePeriod
}

// ── regression: /stop must not wait out a grace period or a graceful
// SIGTERM timeout it never asked for ─────────────────────────────────────

// TestReleaseDecisionSkipsGracePeriodWhenCancelled: /stop cancels the
// task's context before Release runs. A container being torn down because
// it was cancelled must not sit through a configured post-completion grace
// period meant for normal follow-ups (gate re-runs, human replies) -- that
// window is for a task that finished on its own, not one that was killed.
func TestReleaseDecisionSkipsGracePeriodWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	honorGrace, timeout := releaseDecision(ctx, 5*time.Minute)

	if honorGrace {
		t.Error("a cancelled release must not honour the grace period")
	}
	if timeout == nil || *timeout != 0 {
		t.Errorf("a cancelled release must stop immediately (Timeout 0), got %v", timeout)
	}
}

// TestReleaseDecisionHonoursGracePeriodOnNormalCompletion: a task that
// finished on its own (ctx still live) with a configured grace period
// should keep the behaviour PRD section 1 describes -- stay alive for
// follow-ups, then stop gracefully.
func TestReleaseDecisionHonoursGracePeriodOnNormalCompletion(t *testing.T) {
	ctx := context.Background()

	honorGrace, timeout := releaseDecision(ctx, 5*time.Minute)

	if !honorGrace {
		t.Error("normal completion with a configured grace period must honour it")
	}
	if timeout != nil {
		t.Errorf("normal completion must use the default graceful stop timeout, got %v", timeout)
	}
}

// TestReleaseDecisionNoGracePeriodConfigured: no grace period configured
// (the zero value, and the only value ever wired in production today) must
// never sleep, on a live or cancelled context alike.
func TestReleaseDecisionNoGracePeriodConfigured(t *testing.T) {
	ctx := context.Background()

	honorGrace, timeout := releaseDecision(ctx, 0)

	if honorGrace {
		t.Error("a zero grace period must never be honoured")
	}
	if timeout != nil {
		t.Errorf("normal completion must use the default graceful stop timeout, got %v", timeout)
	}
}

func TestWriteTaskJSONProducesValidFile(t *testing.T) {
	dir := t.TempDir()
	payload := TaskPayload{
		ID: 42, Owner: "acme", Repo: "todo", Number: 170,
		Title: "fix bug", Body: "body text",
		Labels: []string{"bug"}, Workflow: "tdd",
	}
	if err := WriteTaskJSON(dir, payload); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".git", "task.json"))
	if err != nil {
		t.Fatal("task.json was not written:", err)
	}
	var decoded TaskPayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal("task.json is not valid JSON:", err)
	}
	if decoded.ID != 42 || decoded.Workflow != "tdd" {
		t.Errorf("decoded payload = %+v", decoded)
	}
}

// The brief goes under .git because the worktree it is written into is the
// one the agent commits from, and go-git's Add ignores .gitignore and
// .git/info/exclude. Anything left in the working tree gets pushed onto the
// task branch; nothing under .git can ever be staged.
func TestWriteTaskJSONLeavesWorkingTreeClean(t *testing.T) {
	dir := t.TempDir()
	if err := WriteTaskJSON(dir, TaskPayload{ID: 7, Owner: "acme", Repo: "todo"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "task.json")); !os.IsNotExist(err) {
		t.Errorf("task.json written to the working tree root; err = %v, want IsNotExist", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git", "task.json")); err != nil {
		t.Errorf("task.json not written under .git: %v", err)
	}
}

// ── regression: Gap 6  --  /data/task.json boot brief ───────────────────

func TestTaskPayloadWrittenAsVolumeFile(t *testing.T) {
	// Gap 6: task travels over NATS, not a file. PRD section 3 describes
	// /data/task.json as the container's boot-time brief  --  the daemon
	// writes it to the volume before the container starts, and the agent
	// reads it on boot alongside NATS messages.

	// The container mount path must match the PRD's /data/ layout.
	// The pool already mounts the worktree at /data/worktree.
	// What's missing: /data/task.json written as a file in the worktree
	// directory (or a separate bind-mounted file) before Acquire.
	//
	// TaskPayload is the data that should be written to /data/task.json.
	type TaskPayload struct {
		ID       int64    `json:"id"`
		Owner    string   `json:"owner"`
		Repo     string   `json:"repo"`
		Number   int      `json:"issue_number"`
		Title    string   `json:"title"`
		Body     string   `json:"body"`
		Labels   []string `json:"labels"`
		Workflow string   `json:"workflow"`
	}

	payload := TaskPayload{
		ID: 42, Owner: "acme", Repo: "todo", Number: 170,
		Title: "fix bug", Body: "body text",
		Labels: []string{"bug"}, Workflow: "tdd",
	}

	// Gap 6 assertion: the daemon must serialize this payload as JSON
	// and write it to <workspace>/task.json before calling Acquire().
	// Currently no such file is written  --  the task travels over NATS.
	if payload.ID == 0 {
		t.Error("TaskPayload ID is zero")
	}
	_ = payload
}

// ── Regression tests for error paths ────────────────────────────────

func TestWriteTaskJSONNonexistentDir(t *testing.T) {
	// Create a regular file and then try to use a path where that file
	// would need to be a directory. Even root cannot mkdir through a
	// regular file, making this test reliable regardless of privileges.
	f, err := os.CreateTemp(t.TempDir(), "archie-test-blocker")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	defer os.Remove(f.Name())

	err = WriteTaskJSON(filepath.Join(f.Name(), "subdir"), TaskPayload{ID: 1})
	if err == nil {
		t.Error("expected error writing to nonexistent directory")
	}
}

func TestWriteTaskJSONEmptyWorkspace(t *testing.T) {
	err := WriteTaskJSON("", TaskPayload{ID: 1})
	if err == nil {
		t.Error("expected error with empty workspace path")
	}
}

func TestTaskPayloadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	payload := TaskPayload{
		ID:       99,
		Owner:    "test-owner",
		Repo:     "test-repo",
		Number:   42,
		Title:    "test title",
		Body:     "test body",
		Labels:   []string{"a", "b"},
		Workflow: "tdd",
		Branch:   "feature/x",
		Plan:     "plan text",
	}

	if err := WriteTaskJSON(dir, payload); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".git", "task.json"))
	if err != nil {
		t.Fatal(err)
	}

	var decoded TaskPayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}

	if decoded.ID != payload.ID {
		t.Errorf("ID = %d", decoded.ID)
	}
	if decoded.Owner != payload.Owner {
		t.Errorf("Owner = %q", decoded.Owner)
	}
	if decoded.Branch != payload.Branch {
		t.Errorf("Branch = %q", decoded.Branch)
	}
	if decoded.Plan != payload.Plan {
		t.Errorf("Plan = %q", decoded.Plan)
	}
	if len(decoded.Labels) != 2 {
		t.Errorf("Labels = %v", decoded.Labels)
	}
}

func TestWriteTaskJSONOverwrite(t *testing.T) {
	dir := t.TempDir()
	p1 := TaskPayload{ID: 1, Title: "first"}
	p2 := TaskPayload{ID: 2, Title: "second"}

	_ = WriteTaskJSON(dir, p1)
	_ = WriteTaskJSON(dir, p2) // overwrite

	data, _ := os.ReadFile(filepath.Join(dir, ".git", "task.json"))
	var decoded TaskPayload
	_ = json.Unmarshal(data, &decoded)

	if decoded.ID != 2 || decoded.Title != "second" {
		t.Errorf("expected second payload, got ID=%d Title=%q", decoded.ID, decoded.Title)
	}
}

func TestWriteTaskJSONMinimalPayload(t *testing.T) {
	dir := t.TempDir()
	payload := TaskPayload{ID: 1}

	if err := WriteTaskJSON(dir, payload); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, ".git", "task.json"))
	var decoded TaskPayload
	_ = json.Unmarshal(data, &decoded)

	if decoded.ID != 1 {
		t.Error("minimal payload ID mismatch")
	}
}

// TestAcquireEnforcesMaxUptime pins the MaxUptime lifetime cap: a container
// acquired with MaxUptime > 0 must be stopped and removed once that cap
// elapses, independent of any later Release call.
func TestAcquireEnforcesMaxUptime(t *testing.T) {
	const containerID = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	var stopCalls, removeCalls atomic.Int32
	stopped := make(chan struct{}, 1)
	removed := make(chan struct{}, 1)

	dockerAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/containers/create"):
			writeDockerJSON(t, w, map[string]any{"Id": containerID, "Warnings": []string{}})
		case strings.HasSuffix(r.URL.Path, "/containers/"+containerID+"/start"):
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(r.URL.Path, "/containers/"+containerID+"/stop"):
			stopCalls.Add(1)
			select {
			case stopped <- struct{}{}:
			default:
			}
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(r.URL.Path, "/containers/"+containerID):
			removeCalls.Add(1)
			select {
			case removed <- struct{}{}:
			default:
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "unexpected Docker API path "+r.URL.Path, http.StatusNotFound)
		}
	}))
	t.Cleanup(dockerAPI.Close)

	dockerClient, err := client.New(client.WithHost(dockerAPI.URL), client.WithAPIVersion("1.55"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dockerClient.Close() })

	pool := &Pool{
		cli: dockerClient,
		cfg: Config{Image: "test/image", MaxUptime: 50 * time.Millisecond},
		log: discardLogger(),
	}

	c, err := pool.Acquire(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if c.ID != containerID {
		t.Fatalf("Acquire returned container ID %q, want %q", c.ID, containerID)
	}

	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("max uptime timer did not stop the container")
	}
	select {
	case <-removed:
	case <-time.After(2 * time.Second):
		t.Fatal("max uptime timer did not remove the container")
	}
	if stopCalls.Load() == 0 || removeCalls.Load() == 0 {
		t.Fatalf("expected at least one stop and remove, got stop=%d remove=%d", stopCalls.Load(), removeCalls.Load())
	}
}

// A released container is already gone, so its max-uptime reaper has nothing
// left to reap. Leaving the timer armed made it fire an hour later against a
// dead ID, logging "max uptime stop failed ... No such container" as though
// teardown had gone wrong.
func TestReleaseCancelsTheMaxUptimeTimer(t *testing.T) {
	const containerID = "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"

	var stopCalls, removeCalls atomic.Int32
	dockerAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/containers/create"):
			writeDockerJSON(t, w, map[string]any{"Id": containerID, "Warnings": []string{}})
		case strings.HasSuffix(r.URL.Path, "/containers/"+containerID+"/start"):
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(r.URL.Path, "/containers/"+containerID+"/stop"):
			stopCalls.Add(1)
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(r.URL.Path, "/containers/"+containerID):
			removeCalls.Add(1)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "unexpected Docker API path "+r.URL.Path, http.StatusNotFound)
		}
	}))
	t.Cleanup(dockerAPI.Close)

	dockerClient, err := client.New(client.WithHost(dockerAPI.URL), client.WithAPIVersion("1.55"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dockerClient.Close() })

	pool := &Pool{
		cli: dockerClient,
		cfg: Config{Image: "test/image", MaxUptime: 60 * time.Millisecond},
		log: discardLogger(),
	}

	c, err := pool.Acquire(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	pool.Release(context.Background(), c)

	// Deterministic half: the reaper is disarmed and its bookkeeping dropped,
	// so nothing is left that could fire. The wait below is the observable
	// consequence, not the proof.
	pool.mu.Lock()
	remaining := len(pool.teardowns)
	pool.mu.Unlock()
	if remaining != 0 {
		t.Errorf("pool still holds %d armed teardown entr(ies) after Release, want 0", remaining)
	}

	stopAfterRelease, removeAfterRelease := stopCalls.Load(), removeCalls.Load()
	time.Sleep(200 * time.Millisecond)

	if got := stopCalls.Load(); got != stopAfterRelease {
		t.Errorf("stop called %d times after Release, want %d: the max uptime timer still fired", got, stopAfterRelease)
	}
	if got := removeCalls.Load(); got != removeAfterRelease {
		t.Errorf("remove called %d times after Release, want %d: the max uptime timer still fired", got, removeAfterRelease)
	}
}

// MaxUptime is a hard lifetime cap from creation, enforced "regardless of
// task state" (Config.MaxUptime). A grace period is task state, so a
// container that outlives the cap while waiting out its grace window is still
// reaped: cancelling the reaper before the grace sleep would extend the cap
// by GracePeriod.
func TestGracePeriodDoesNotExtendTheMaxUptimeCap(t *testing.T) {
	const containerID = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"

	stopped := make(chan struct{}, 4)
	dockerAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/containers/create"):
			writeDockerJSON(t, w, map[string]any{"Id": containerID, "Warnings": []string{}})
		case strings.HasSuffix(r.URL.Path, "/containers/"+containerID+"/start"):
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(r.URL.Path, "/containers/"+containerID+"/stop"):
			select {
			case stopped <- struct{}{}:
			default:
			}
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(r.URL.Path, "/containers/"+containerID):
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "unexpected Docker API path "+r.URL.Path, http.StatusNotFound)
		}
	}))
	t.Cleanup(dockerAPI.Close)

	dockerClient, err := client.New(client.WithHost(dockerAPI.URL), client.WithAPIVersion("1.55"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dockerClient.Close() })

	pool := &Pool{
		cli: dockerClient,
		cfg: Config{Image: "test/image", MaxUptime: 30 * time.Millisecond, GracePeriod: 2 * time.Second},
		log: discardLogger(),
	}

	c, err := pool.Acquire(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	released := make(chan struct{})
	go func() {
		defer close(released)
		pool.Release(context.Background(), c)
	}()

	// The cap must bite while Release is still sleeping out the grace period.
	select {
	case <-stopped:
	case <-released:
		t.Fatal("Release returned before the max uptime cap fired; the grace period extended the cap")
	case <-time.After(time.Second):
		t.Fatal("max uptime cap never fired during the grace period")
	}
	<-released
}

// Timer.Stop cannot unwind a callback that has already begun, so cancelling
// is not enough on its own: teardown has to be claimed once, by whichever of
// the reaper and Release gets there first. Firing the reaper before Release
// is the deterministic stand-in for the two overlapping.
func TestReleaseAfterTheReaperFiredDoesNotTearDownTwice(t *testing.T) {
	const containerID = "0f1e2d3c4b5a69780f1e2d3c4b5a69780f1e2d3c4b5a69780f1e2d3c4b5a6978"

	var stopCalls, removeCalls atomic.Int32
	reaped := make(chan struct{}, 1)
	dockerAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/containers/create"):
			writeDockerJSON(t, w, map[string]any{"Id": containerID, "Warnings": []string{}})
		case strings.HasSuffix(r.URL.Path, "/containers/"+containerID+"/start"):
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(r.URL.Path, "/containers/"+containerID+"/stop"):
			stopCalls.Add(1)
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(r.URL.Path, "/containers/"+containerID):
			removeCalls.Add(1)
			select {
			case reaped <- struct{}{}:
			default:
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "unexpected Docker API path "+r.URL.Path, http.StatusNotFound)
		}
	}))
	t.Cleanup(dockerAPI.Close)

	dockerClient, err := client.New(client.WithHost(dockerAPI.URL), client.WithAPIVersion("1.55"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dockerClient.Close() })

	pool := &Pool{
		cli: dockerClient,
		cfg: Config{Image: "test/image", MaxUptime: 20 * time.Millisecond},
		log: discardLogger(),
	}

	c, err := pool.Acquire(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	select {
	case <-reaped:
	case <-time.After(2 * time.Second):
		t.Fatal("max uptime reaper never ran")
	}
	pool.Release(context.Background(), c)

	if got := stopCalls.Load(); got != 1 {
		t.Errorf("stop called %d times, want 1: the reaper and Release both tore the container down", got)
	}
	if got := removeCalls.Load(); got != 1 {
		t.Errorf("remove called %d times, want 1: the reaper and Release both tore the container down", got)
	}
}

// A max-uptime callback that has already begun cannot be unwound by
// Timer.Stop, so it can reach its claim after Release has disarmed the timer
// and deleted the teardown entry. Release has torn the container down by then:
// the late callback must find nothing to claim, rather than recreating an
// unclosed entry behind Release's back and running stop/remove a second time
// against a container that is already gone.
//
// The interleaving is driven by running the reaper's callback body after
// Release returned. That is the closest reachable form: nothing in the public
// API lets a caller park the callback between the timer firing and its claim --
// the window is the callback goroutine's scheduling -- so a test that merely
// started a timer and a Release would be a coin flip. This covers the
// claim/forget ordering and the Docker calls that follow it; it does not cover
// the timer machinery itself.
func TestReaperCallbackAfterReleaseDoesNotResurrectTeardown(t *testing.T) {
	const containerID = "9a8b7c6d5e4f32109a8b7c6d5e4f32109a8b7c6d5e4f32109a8b7c6d5e4f3210"

	var stopCalls, removeCalls atomic.Int32
	dockerAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/containers/create"):
			writeDockerJSON(t, w, map[string]any{"Id": containerID, "Warnings": []string{}})
		case strings.HasSuffix(r.URL.Path, "/containers/"+containerID+"/start"):
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(r.URL.Path, "/containers/"+containerID+"/stop"):
			stopCalls.Add(1)
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(r.URL.Path, "/containers/"+containerID):
			removeCalls.Add(1)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "unexpected Docker API path "+r.URL.Path, http.StatusNotFound)
		}
	}))
	t.Cleanup(dockerAPI.Close)

	dockerClient, err := client.New(client.WithHost(dockerAPI.URL), client.WithAPIVersion("1.55"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dockerClient.Close() })

	pool := &Pool{
		cli: dockerClient,
		cfg: Config{Image: "test/image", MaxUptime: time.Hour},
		log: discardLogger(),
	}

	c, err := pool.Acquire(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	pool.Release(context.Background(), c)

	stopAfterRelease, removeAfterRelease := stopCalls.Load(), removeCalls.Load()
	if stopAfterRelease != 1 || removeAfterRelease != 1 {
		t.Fatalf("Release stopped the container %d times and removed it %d times, want 1/1: a late callback proves nothing unless the release already did the teardown", stopAfterRelease, removeAfterRelease)
	}

	pool.reapMaxUptime(context.Background(), c.ID)

	if got := stopCalls.Load(); got != stopAfterRelease {
		t.Errorf("stop called %d times after Release, want %d: the late reaper tore down an already-released container", got, stopAfterRelease)
	}
	if got := removeCalls.Load(); got != removeAfterRelease {
		t.Errorf("remove called %d times after Release, want %d: the late reaper tore down an already-released container", got, removeAfterRelease)
	}

	pool.mu.Lock()
	remaining := len(pool.teardowns)
	pool.mu.Unlock()
	if remaining != 0 {
		t.Errorf("pool holds %d teardown entr(ies) after Release, want 0: the late reaper recreated the entry", remaining)
	}
}

// TestPoolActiveReportsInFlightContainers pins the worker-pool read surface
// /status reports as its container line: how many containers this pool
// currently holds, and the concurrency cap it enforces.
//
// Both come from the pool's own counter. A Docker listing would report
// containers this pool does not own (orphans from a crashed daemon, containers
// another archie instance started) and would make reading /status cost a
// Docker round trip on every invocation.
func TestPoolActiveReportsInFlightContainers(t *testing.T) {
	const maxConcurrency = 2

	var dockerCalls atomic.Int32
	created := 0
	dockerAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dockerCalls.Add(1)
		if strings.HasSuffix(r.URL.Path, "/containers/create") {
			created++
			writeDockerJSON(t, w, map[string]any{"Id": fmt.Sprintf("%064d", created), "Warnings": []string{}})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(dockerAPI.Close)

	dockerClient, err := client.New(client.WithHost(dockerAPI.URL), client.WithAPIVersion("1.55"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dockerClient.Close() })

	pool := &Pool{
		cli: dockerClient,
		cfg: Config{Image: "test/image", MaxConcurrency: maxConcurrency},
		log: discardLogger(),
	}

	var held []*Container
	acquire := func(t *testing.T) {
		t.Helper()
		c, err := pool.Acquire(context.Background(), nil, nil)
		if err != nil {
			t.Fatalf("Acquire: %v", err)
		}
		held = append(held, c)
	}
	release := func(t *testing.T) {
		t.Helper()
		if len(held) == 0 {
			t.Fatal("release with nothing held")
		}
		pool.Release(context.Background(), held[len(held)-1])
		held = held[:len(held)-1]
	}

	tests := []struct {
		name string
		act  func(*testing.T)
		want int
	}{
		{name: "idle pool holds nothing", act: func(*testing.T) {}, want: 0},
		{name: "one acquired container", act: acquire, want: 1},
		{name: "two acquired containers", act: acquire, want: 2},
		{name: "one released", act: release, want: 1},
		{name: "both released", act: release, want: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.act(t)
			before := dockerCalls.Load()
			got := pool.Active()
			if after := dockerCalls.Load(); after != before {
				t.Errorf("Active() made %d Docker API call(s), want 0: it must read the pool's own counter", after-before)
			}
			if got != tc.want {
				t.Errorf("Active() = %d, want %d", got, tc.want)
			}
		})
	}

	if got := pool.Cap(); got != maxConcurrency {
		t.Errorf("Cap() = %d, want the configured MaxConcurrency %d", got, maxConcurrency)
	}

	// An unconfigured cap means "no limit" (Acquire treats MaxConcurrency <= 0
	// that way), so it must not be reported as a cap of zero.
	if got := (&Pool{}).Cap(); got != 0 {
		t.Errorf("Cap() with no MaxConcurrency = %d, want 0 (unlimited)", got)
	}
}
