// Package container manages Docker containers running archie-agent.
// A Pool acquires and releases containers per task, handling image pull,
// creation, startup, health check, and teardown. The daemon hands each task to
// its container as one core-NATS request.
package container

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/samcharles93/archie-core/internal/storage"
)

// Container wraps a running Docker container.
type Container struct {
	ID     string
	exited <-chan struct{}
}

// Exited closes when the container stops running for any reason: the
// max-uptime reaper, an OOM kill, a crash or Release. A Container the pool did
// not start returns nil, which never closes.
func (c *Container) Exited() <-chan struct{} { return c.exited }

// TaskPayload is the boot-time brief written to /data/worktree/.git/task.json
// before the container starts, per PRD section 3.
type TaskPayload struct {
	ID       int64    `json:"id"`
	Owner    string   `json:"owner"`
	Repo     string   `json:"repo"`
	Number   int      `json:"issue_number"`
	Title    string   `json:"title"`
	Body     string   `json:"body"`
	Labels   []string `json:"labels"`
	Workflow string   `json:"workflow"`
	Branch   string   `json:"branch,omitempty"`
	Plan     string   `json:"plan,omitempty"`
	// Inputs are the workflow inputs a binding assigned, as structured data.
	Inputs map[string]any `json:"inputs,omitempty"`
}

// WriteTaskJSON writes the task payload to <workspace>/.git/task.json.
// The workspace directory must already exist.
//
// It goes under .git deliberately. The workspace is the task worktree the
// agent commits from, and worktree.CommitAll stages with go-git's All
// option, which does not honour .gitignore or .git/info/exclude -- a brief
// at the worktree root is swept into the agent's commit and pushed onto the
// task branch. Nothing under .git can ever be tracked, which is the same
// reasoning that moved the prepared sentinel there.
func WriteTaskJSON(workspace string, payload TaskPayload) error {
	if workspace == "" {
		return fmt.Errorf("task.json: empty workspace path")
	}
	if info, err := os.Stat(workspace); err != nil {
		return fmt.Errorf("task.json: workspace %s: %w", workspace, err)
	} else if !info.IsDir() {
		return fmt.Errorf("task.json: workspace %s is not a directory", workspace)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	gitDir := filepath.Join(workspace, ".git")
	if err := os.MkdirAll(gitDir, 0o700); err != nil {
		return fmt.Errorf("task.json: %w", err)
	}
	return os.WriteFile(filepath.Join(gitDir, "task.json"), data, 0o644)
}

// Pool manages a set of Docker containers running archie-agent.
type Pool struct {
	cli    *client.Client
	ownCli bool // true when the pool created the client (owns Close)
	cfg    Config
	log    *slog.Logger
	// network is the Docker network spawned containers join. External broker
	// deployments may leave it empty to use Docker's implicit default bridge;
	// embedded deployments resolve that default explicitly as "bridge" so its
	// host gateway can be inspected.
	network string
	// hostGateway is the local IPv4 gateway of network. It is populated when
	// embedded broker reachability is required, so native composition can bind
	// the listener to an address visible only to workers on this bridge.
	hostGateway string

	mu     sync.Mutex
	active int
	// pulled records the images already made available, so a profile's image
	// is pulled once rather than on every task. Guarded by mu.
	pulled map[string]bool
	// teardowns holds the one-shot teardown state for each live container,
	// keyed by ID. See containerTeardown.
	teardowns map[string]*containerTeardown
}

// Config is the subset of daemon container configuration the pool needs.
type Config struct {
	Image          string
	MaxConcurrency int
	// MaxUptime is the total container lifetime cap from creation. When
	// exceeded, the container is killed regardless of task state. PRD §4.
	MaxUptime time.Duration
	// GracePeriod is the idle time after task completion before the
	// container is killed. The agent stays alive to handle follow-ups
	// (gate re-runs, human replies) during this window. PRD §1.
	GracePeriod time.Duration
	PullPolicy  string
	// Network is the Docker network spawned agent containers join. Empty
	// falls back to selfNetwork's best-effort auto-detection.
	Network string
	// DockerClient is an optional pre-connected Docker client. When nil,
	// NewPool creates its own via client.New(client.FromEnv). Pass a
	// shared client to avoid multiple independent connections.
	DockerClient *client.Client
	// RequireHostGateway resolves and validates a local Docker bridge gateway.
	// Embedded NATS needs this address; external brokers do not.
	RequireHostGateway bool
}

// NewPool connects to the Docker daemon, optionally pulls the image,
// and cleans up orphaned containers from a previous daemon crash.
//
// If cfg.DockerClient is set, it is reused (caller owns Close).
// Otherwise a new client is created via client.FromEnv (pool owns Close).
func NewPool(ctx context.Context, cfg Config, log *slog.Logger) (*Pool, error) {
	cli := cfg.DockerClient
	if cli == nil {
		var err error
		cli, err = client.New(client.FromEnv)
		if err != nil {
			return nil, fmt.Errorf("docker client: %w", err)
		}
	}

	network := cfg.Network
	if network == "" {
		network = selfNetwork(ctx, cli, log)
	}
	if cfg.RequireHostGateway && network == "" {
		// A native daemon is not itself attached to a Docker network. Its
		// managed workers use Docker's default bridge unless configured
		// otherwise, so that is the bridge whose host gateway must be bound.
		network = "bridge"
	}
	hostGateway, err := resolveHostGateway(ctx, cli, network, cfg.RequireHostGateway)
	if err != nil {
		if cfg.DockerClient == nil {
			_ = cli.Close()
		}
		return nil, err
	}

	p := &Pool{
		cli:         cli,
		cfg:         cfg,
		log:         log,
		ownCli:      cfg.DockerClient == nil,
		network:     network,
		hostGateway: hostGateway,
	}

	// Pull image if needed.
	if cfg.PullPolicy == "always" || cfg.PullPolicy == "missing" {
		if err := p.pullImage(ctx, cfg.Image); err != nil {
			if err := cli.Close(); err != nil {
				log.Warn("docker client close failed during pull error", "err", err)
			}
			return nil, err
		}
	}

	// Recover orphaned containers from a previous daemon crash.
	p.recoverOrphans(ctx)

	return p, nil
}

// Acquire creates and starts a container from image (empty means the
// configured image) with the given mounts and environment variables. Mounts
// are provided by the caller (typically from a storage.Backend). If MaxUptime
// is set, the pool schedules a hard stop and remove of the container once
// that lifetime cap elapses, regardless of task state.
func (p *Pool) Acquire(ctx context.Context, image string, mounts []storage.Mount, env []string) (*Container, error) {
	if image == "" {
		image = p.cfg.Image
	}
	if err := p.ensureImage(ctx, image); err != nil {
		return nil, err
	}
	p.mu.Lock()
	if p.cfg.MaxConcurrency > 0 && p.active >= p.cfg.MaxConcurrency {
		p.mu.Unlock()
		return nil, fmt.Errorf("max concurrency %d reached", p.cfg.MaxConcurrency)
	}
	p.active++
	p.mu.Unlock()

	name := fmt.Sprintf("archie-agent-%d", time.Now().UnixNano())

	hostConfig := &container.HostConfig{
		Mounts:     storage.ConvertMounts(mounts),
		AutoRemove: true,
	}
	if p.network != "" {
		// Join the same user-defined network the daemon itself is on, so
		// the agent container can resolve sibling compose services (nats,
		// etc.) by name  --  otherwise Docker attaches it to the default
		// bridge network, where those hostnames don't resolve.
		hostConfig.NetworkMode = container.NetworkMode(p.network)
	}

	resp, err := p.cli.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name: name,
		Config: &container.Config{
			Image: image,
			Env:   env,
			Labels: map[string]string{
				"archie-daemon": "true",
			},
		},
		HostConfig: hostConfig,
	})
	if err != nil {
		p.mu.Lock()
		p.active--
		p.mu.Unlock()
		return nil, fmt.Errorf("container create: %w", err)
	}

	if _, err := p.cli.ContainerStart(ctx, resp.ID, client.ContainerStartOptions{}); err != nil {
		// Best-effort cleanup.
		if _, rmErr := p.cli.ContainerRemove(context.WithoutCancel(ctx), resp.ID, client.ContainerRemoveOptions{Force: true}); rmErr != nil {
			p.log.Warn("container remove after start failure", "id", resp.ID[:12], "err", rmErr)
		}
		p.mu.Lock()
		p.active--
		p.mu.Unlock()
		return nil, fmt.Errorf("container start: %w", err)
	}

	if p.cfg.MaxUptime > 0 {
		p.armMaxUptime(ctx, resp.ID)
	}

	p.log.Info("container started", "id", resp.ID[:12], "name", name)
	return &Container{ID: resp.ID, exited: p.watchExit(ctx, resp.ID)}, nil
}

// watchExit returns a channel closed once Docker reports the container is no
// longer running. A failed wait says nothing about the container, so it never
// closes the channel: a Docker API error must not abort a live run.
func (p *Pool) watchExit(ctx context.Context, id string) <-chan struct{} {
	exited := make(chan struct{})
	wait := p.cli.ContainerWait(context.WithoutCancel(ctx), id, client.ContainerWaitOptions{Condition: container.WaitConditionNotRunning})
	go func() {
		select {
		case <-wait.Result:
			close(exited)
		case <-wait.Error:
		}
	}()
	return exited
}

// containerTeardown is the one-shot teardown state for a live container: its
// armed max-uptime reaper, and whether Docker stop/remove has been claimed.
//
// Both the reaper and Release tear a container down, and Timer.Stop cannot
// unwind a callback that has already begun, so cancelling the timer is not
// enough to keep them from overlapping. Claiming decides the winner instead:
// exactly one path talks to Docker, and the loser skips straight to its own
// bookkeeping.
type containerTeardown struct {
	timer  *time.Timer
	closed bool
}

// armMaxUptime schedules a hard stop and remove for a container once its
// lifetime cap elapses. The timer never touches p.active: Release (or Close)
// remains the only owner of the active slot.
//
// The entry and its timer are recorded in one critical section, so the
// callback cannot run ahead of the bookkeeping it needs: time.AfterFunc hands
// control to the callback as soon as the duration elapses, and the callback's
// first act is claimReaper, which takes p.mu. Holding p.mu across both the
// entry creation and the timer's creation is what makes that claim observe a
// container that is armed rather than one that is mid-arm -- a timer recorded
// after a callback had already fired (and a Release already forgotten the
// entry) left an unclosed entry behind, with a one-shot timer that would never
// fire again.
//
// The lock covers only bookkeeping: the callback runs on its own goroutine and
// nothing here waits on it, and every Docker call it makes happens after it
// has released p.mu.
func (p *Pool) armMaxUptime(ctx context.Context, id string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	t := p.teardownLocked(id)
	t.timer = time.AfterFunc(p.cfg.MaxUptime, func() {
		p.reapMaxUptime(ctx, id)
	})
}

// reapMaxUptime is the max-uptime callback body: it claims the container's
// Docker teardown and, if it won, stops and removes the container.
func (p *Pool) reapMaxUptime(ctx context.Context, id string) {
	if !p.claimReaper(id) {
		return
	}
	zero := 0
	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if _, err := p.cli.ContainerStop(stopCtx, id, client.ContainerStopOptions{Timeout: &zero}); err != nil {
		p.log.Warn("max uptime stop failed", "id", id[:12], "err", err)
	}
	if _, err := p.cli.ContainerRemove(context.WithoutCancel(ctx), id, client.ContainerRemoveOptions{Force: true}); err != nil {
		p.log.Warn("max uptime remove failed", "id", id[:12], "err", err)
	}
}

// claimReaper reports whether the max-uptime callback owns this container's
// Docker teardown. The first claim wins; every later one is told to stay out.
//
// A missing entry is a loss, never a fresh claim: only armMaxUptime creates
// entries, so no entry means MaxUptime was never armed or Release has already
// forgotten the container -- in both cases the reaper has nothing to tear
// down. Creating one here is exactly how a callback that was still running
// when Release forgot the entry could resurrect it.
func (p *Pool) claimReaper(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	t := p.teardowns[id]
	if t == nil || t.closed {
		return false
	}
	t.closed = true
	return true
}

// claimRelease reports whether Release owns this container's Docker teardown.
//
// Unlike the reaper, Release is the path that always exists: MaxUptime == 0
// arms no reaper at all, so the absence of an entry must still be a win here.
// The pool guarantees every acquired container is released exactly once, so by
// the time Release runs an entry can be missing only because a reaper was
// never armed -- an absent entry is never a live reaper in flight, since
// nothing but Release's forgetTeardown deletes one, and that runs after this
// claim.
func (p *Pool) claimRelease(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	t := p.teardowns[id]
	if t == nil {
		return true
	}
	if t.closed {
		return false
	}
	t.closed = true
	return true
}

// forgetTeardown disarms the reaper and drops the container's bookkeeping, so
// the map does not grow for the life of the pool. Only Release calls this, as
// the last step of a teardown it claimed, and deleting the entry is final: the
// reaper's claimReaper looks the entry up and never creates one, so a callback
// that was already running when the entry went away finds nothing to claim
// instead of racing a fresh entry back into existence.
func (p *Pool) forgetTeardown(id string) {
	p.mu.Lock()
	t := p.teardowns[id]
	delete(p.teardowns, id)
	p.mu.Unlock()
	if t != nil && t.timer != nil {
		t.timer.Stop()
	}
}

// teardownLocked returns the container's teardown state, creating it on first
// use. armMaxUptime is its only caller: entries are created by the producer of
// the reaper, under the same lock that lets the callback claim them, and never
// by a path that might be racing Release. Callers must hold p.mu.
func (p *Pool) teardownLocked(id string) *containerTeardown {
	if p.teardowns == nil {
		p.teardowns = make(map[string]*containerTeardown)
	}
	t := p.teardowns[id]
	if t == nil {
		t = &containerTeardown{}
		p.teardowns[id] = t
	}
	return t
}

// releaseDecision reports how Release should tear a container down: whether
// to honour the configured post-completion grace period, and what Docker
// stop timeout to request (nil means the container's configured timeout or
// the engine default; a non-nil zero means stop immediately with SIGKILL,
// skipping the graceful SIGTERM wait).
//
// A cancelled ctx means the release is happening because the task was
// stopped, not because it finished -- /stop is an emergency brake, so
// neither the grace period (meant for a task that completed on its own and
// might get a follow-up) nor a graceful stop (meant to let a healthy
// process wind down) apply. A still-live ctx is a normal completion, so
// both keep their existing meaning.
func releaseDecision(ctx context.Context, grace time.Duration) (honorGrace bool, stopTimeout *int) {
	if ctx.Err() != nil {
		zero := 0
		return false, &zero
	}
	return grace > 0, nil
}

// Release stops and removes a container after a task completes. If
// GracePeriod is configured, the container stays alive for that duration
// before being killed  --  the agent can handle follow-ups (gate re-runs,
// human replies) during this window. PRD section 1.
func (p *Pool) Release(ctx context.Context, c *Container) {
	if c == nil {
		return
	}
	honorGrace, stopTimeout := releaseDecision(ctx, p.cfg.GracePeriod)
	if honorGrace {
		p.log.Info("container keeping alive for grace period", "id", c.ID[:12], "grace", p.cfg.GracePeriod)
		time.Sleep(p.cfg.GracePeriod)
	}

	// Claim only now. MaxUptime is a cap from creation enforced regardless of
	// task state, so the reaper must still be free to bite during the grace
	// period above; claiming before the sleep would quietly extend the cap by
	// GracePeriod. Losing the claim means the reaper already removed this
	// container, so there is nothing left to stop.
	if p.claimRelease(c.ID) {
		// Detach from ctx before stopping. Release runs on the way out of a
		// task, and the most important reason a task is on its way out is
		// that it was cancelled -- at which point ctx is already dead, the
		// graceful stop fails immediately, and the container is left to the
		// forced remove below. Teardown is cleanup, so it gets its own
		// deadline rather than inheriting the caller's cancellation.
		stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()

		if _, err := p.cli.ContainerStop(stopCtx, c.ID, client.ContainerStopOptions{Timeout: stopTimeout}); err != nil {
			p.log.Warn("container stop failed", "id", c.ID[:12], "err", err)
		}
		if _, err := p.cli.ContainerRemove(context.WithoutCancel(ctx), c.ID, client.ContainerRemoveOptions{Force: true}); err != nil {
			p.log.Warn("container remove failed", "id", c.ID[:12], "err", err)
		}
	}
	p.forgetTeardown(c.ID)

	p.mu.Lock()
	p.active--
	p.mu.Unlock()
	p.log.Info("container released", "id", c.ID[:12])
}

// Close stops and removes all active containers. If the pool created its
// own Docker client, it is closed. Shared clients are not closed.
func (p *Pool) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	list, err := p.cli.ContainerList(ctx, client.ContainerListOptions{
		All:     true,
		Filters: client.Filters{}.Add("label", "archie-daemon=true"),
	})
	if err != nil {
		p.log.Warn("container list failed during close", "err", err)
	} else {
		for _, c := range list.Items {
			p.log.Info("cleaning up container", "id", c.ID[:12])
			if _, err := p.cli.ContainerStop(ctx, c.ID, client.ContainerStopOptions{}); err != nil {
				p.log.Warn("container stop during close", "id", c.ID[:12], "err", err)
			}
			if _, err := p.cli.ContainerRemove(ctx, c.ID, client.ContainerRemoveOptions{Force: true}); err != nil {
				p.log.Warn("container remove during close", "id", c.ID[:12], "err", err)
			}
		}
	}
	if p.ownCli {
		return p.cli.Close()
	}
	return nil
}

// ── helpers ──────────────────────────────────────────────────────────

// ensureImage pulls an image other than the configured one the first time a
// task asks for it, under the configured pull policy. The configured image
// was already pulled by NewPool.
func (p *Pool) ensureImage(ctx context.Context, ref string) error {
	if ref == p.cfg.Image || (p.cfg.PullPolicy != "always" && p.cfg.PullPolicy != "missing") {
		return nil
	}
	p.mu.Lock()
	done := p.pulled[ref]
	p.mu.Unlock()
	if done {
		return nil
	}
	if err := p.pullImage(ctx, ref); err != nil {
		return err
	}
	p.mu.Lock()
	if p.pulled == nil {
		p.pulled = map[string]bool{}
	}
	p.pulled[ref] = true
	p.mu.Unlock()
	return nil
}

// pullImage pulls ref. On "missing" policy, skips if the image already
// exists locally.
func (p *Pool) pullImage(ctx context.Context, ref string) error {
	if p.cfg.PullPolicy == "missing" {
		_, err := p.cli.ImageInspect(ctx, ref)
		if err == nil {
			p.log.Info("image already present", "image", ref)
			return nil
		}
	}

	p.log.Info("pulling image", "image", ref)
	rc, err := p.cli.ImagePull(ctx, ref, client.ImagePullOptions{})
	if err != nil {
		return fmt.Errorf("pull %s: %w", ref, err)
	}
	defer func() {
		if err := rc.Close(); err != nil {
			p.log.Warn("image pull reader close failed", "err", err)
		}
	}()
	if _, err := io.Copy(io.Discard, rc); err != nil {
		return fmt.Errorf("pull %s: discard: %w", ref, err)
	}
	p.log.Info("image pulled", "image", ref)
	return nil
}

// selfNetwork detects the user-defined Docker network the current
// process's own container is attached to, by inspecting the container
// named after our hostname (Docker sets a container's hostname to its
// short ID by default). Returns "" if we're not running in a container,
// the inspect fails, or we're only on the default bridge network  --  in
// all of those cases Acquire falls back to Docker's normal default.
func selfNetwork(ctx context.Context, cli *client.Client, log *slog.Logger) string {
	hostname, err := os.Hostname()
	if err != nil {
		log.Warn("container network auto-detect: os.Hostname failed, spawned containers will use the default bridge network and won't resolve sibling services by name — set containers.network explicitly", "err", err)
		return ""
	}
	self, err := cli.ContainerInspect(ctx, hostname, client.ContainerInspectOptions{})
	if err != nil {
		log.Warn("container network auto-detect: could not inspect self by hostname, spawned containers will use the default bridge network and won't resolve sibling services by name — set containers.network explicitly", "hostname", hostname, "err", err)
		return ""
	}
	if self.Container.NetworkSettings == nil {
		log.Warn("container network auto-detect: self container has no NetworkSettings, spawned containers will use the default bridge network — set containers.network explicitly", "hostname", hostname)
		return ""
	}
	for name := range self.Container.NetworkSettings.Networks {
		if name != "bridge" {
			log.Info("spawned containers will join daemon's network", "network", name)
			return name
		}
	}
	return ""
}

// recoverOrphans stops and removes containers left by a previous daemon.
func (p *Pool) recoverOrphans(ctx context.Context) {
	list, err := p.cli.ContainerList(ctx, client.ContainerListOptions{
		All:     true,
		Filters: client.Filters{}.Add("label", "archie-daemon=true"),
	})
	if err != nil {
		return
	}
	for _, c := range list.Items {
		p.log.Info("recovering orphaned container", "id", c.ID[:12])
		if _, err := p.cli.ContainerStop(ctx, c.ID, client.ContainerStopOptions{}); err != nil {
			p.log.Warn("container stop during orphan recovery", "id", c.ID[:12], "err", err)
		}
		if _, err := p.cli.ContainerRemove(ctx, c.ID, client.ContainerRemoveOptions{Force: true}); err != nil {
			p.log.Warn("container remove during orphan recovery", "id", c.ID[:12], "err", err)
		}
	}
	if len(list.Items) > 0 {
		p.log.Info("orphan recovery complete", "count", len(list.Items))
	}
}

// Active is the number of containers this pool currently holds: acquired but
// not yet released. It reads the pool's own counter rather than listing
// Docker containers, which would include containers this pool does not own
// (orphans from a crashed daemon, another instance's workers).
func (p *Pool) Active() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.active
}

// Cap is the concurrency cap this pool enforces, from the configured
// containers.max_concurrency. Zero means unlimited (Acquire only enforces a
// cap when MaxConcurrency > 0), so a caller rendering "active/cap" must not
// treat zero as a cap of zero.
func (p *Pool) Cap() int { return p.cfg.MaxConcurrency }
