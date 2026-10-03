// Package container manages Docker containers running archie-agent.
// A Pool acquires and releases containers per task, handling image pull,
// creation, startup, health check, and teardown. The daemon hands each task to
// its container as one core-NATS request.
package container

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/registry"
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
// before the container starts.
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

// WriteTaskJSON writes the task payload to <workspace>/.git/task.json, where
// it is never committed.
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

	// live, when set, returns the pool's current settings. A production pool
	// is given one that reads the running config, so a container-runtime-
	// policies change reaches the next acquire without a restart; a test pool
	// leaves it nil and cfg is the whole truth.
	live func() Config

	mu     sync.Mutex
	active int
	// pulled records the images already made available, so a profile's image
	// is pulled once rather than on every task. Guarded by mu.
	pulled map[string]bool
	// teardowns holds the one-shot teardown state for each live container,
	// keyed by ID. See containerTeardown.
	teardowns map[string]*containerTeardown
}

// current returns the per-acquire settings from the live source when set,
// else the construction Config.
func (p *Pool) current() Config {
	if p.live == nil {
		return p.cfg
	}
	cfg := p.cfg
	live := p.live()
	cfg.Image = live.Image
	cfg.MaxConcurrency = live.MaxConcurrency
	cfg.MaxUptime = live.MaxUptime
	cfg.PullPolicy = live.PullPolicy
	cfg.Network = live.Network
	return cfg
}

// Config is the subset of daemon container configuration the pool needs.
type Config struct {
	Image          string
	MaxConcurrency int
	// MaxUptime is the total container lifetime cap from creation. When
	// exceeded, the container is killed regardless of task state.
	MaxUptime time.Duration
	// GracePeriod is the idle time after task completion before the container
	// is killed. The agent stays alive to handle follow-ups (gate re-runs,
	// human replies) during this window.
	GracePeriod time.Duration
	PullPolicy  string
	// RegistryAuth is the resolved value of [containers].registry_auth: either
	// empty (anonymous pulls -- the behaviour for a deployment with no private
	// registry) or a Docker registry.AuthConfig JSON document. pullImage
	// encodes it into the X-Registry-Auth header the Engine API expects.
	RegistryAuth string
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

// NewPool connects to Docker, optionally pulls the image and removes orphaned
// containers. A set cfg.DockerClient is reused and not closed. live, when
// non-nil, supplies Image, MaxConcurrency, MaxUptime, PullPolicy and Network
// on every acquire.
func NewPool(ctx context.Context, cfg Config, live func() Config, log *slog.Logger) (*Pool, error) {
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
		live:        live,
		log:         log,
		ownCli:      cfg.DockerClient == nil,
		network:     network,
		hostGateway: hostGateway,
	}

	// Pull image if needed. The configured image is recorded as pulled, so an
	// acquire that names it skips a second pull even though the live settings
	// may have changed the configured image since.
	if cfg.PullPolicy == "always" || cfg.PullPolicy == "missing" {
		if err := p.pullImage(ctx, cfg.Image); err != nil {
			if err := cli.Close(); err != nil {
				log.Warn("docker client close failed during pull error", "err", err)
			}
			return nil, err
		}
		p.markPulled(cfg.Image)
	}

	// Recover orphaned containers from a previous daemon crash.
	p.recoverOrphans(ctx)

	return p, nil
}

// Acquire creates and starts a container from image (empty means the
// configured one). With MaxUptime set, the container is stopped and removed
// once it elapses.
func (p *Pool) Acquire(ctx context.Context, image string, mounts []storage.Mount, env []string) (*Container, error) {
	// One snapshot for the whole acquire: the image, pull policy, network,
	// concurrency cap and max-uptime cap are all read once, so a settings
	// change landing mid-acquire cannot mix two versions.
	cfg := p.current()
	if image == "" {
		image = cfg.Image
	}
	if err := p.ensureImage(ctx, cfg, image); err != nil {
		return nil, err
	}
	if err := p.reserve(cfg.MaxConcurrency); err != nil {
		return nil, err
	}

	name := fmt.Sprintf("archie-agent-%d", time.Now().UnixNano())

	hostConfig := &container.HostConfig{
		Mounts:     storage.ConvertMounts(mounts),
		AutoRemove: true,
	}
	// The live network wins when it names one; otherwise the network resolved
	// at construction (the configured one, or self-detection) stays in force.
	network := cfg.Network
	if network == "" {
		network = p.network
	}
	if network != "" {
		// Join the same user-defined network the daemon itself is on, so
		// the agent container can resolve sibling compose services (nats,
		// etc.) by name  --  otherwise Docker attaches it to the default
		// bridge network, where those hostnames don't resolve.
		hostConfig.NetworkMode = container.NetworkMode(network)
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
		p.unreserve()
		return nil, fmt.Errorf("container create: %w", err)
	}

	if _, err := p.cli.ContainerStart(ctx, resp.ID, client.ContainerStartOptions{}); err != nil {
		// Best-effort cleanup.
		if _, rmErr := p.cli.ContainerRemove(context.WithoutCancel(ctx), resp.ID, client.ContainerRemoveOptions{Force: true}); rmErr != nil {
			p.log.Warn("container remove after start failure", "id", resp.ID[:12], "err", rmErr)
		}
		p.unreserve()
		return nil, fmt.Errorf("container start: %w", err)
	}
	return p.started(ctx, cfg.MaxUptime, resp.ID, name), nil
}

// AcquireKit starts a Kit task container under the same concurrency cap,
// lifetime cap and exit watch as Acquire. Release tears it down.
func (p *Pool) AcquireKit(ctx context.Context, s KitSpec) (*Container, error) {
	cfg := p.current()
	if err := p.ensureImage(ctx, cfg, s.Image); err != nil {
		return nil, err
	}
	if err := p.reserve(cfg.MaxConcurrency); err != nil {
		return nil, err
	}
	s.Labels = maps.Clone(s.Labels)
	if s.Labels == nil {
		s.Labels = map[string]string{}
	}
	s.Labels["archie-daemon"] = "true"
	id, err := StartKit(ctx, p.cli, s)
	if err != nil {
		p.unreserve()
		return nil, err
	}
	return p.started(ctx, cfg.MaxUptime, id, s.Name), nil
}

func (p *Pool) reserve(maxConcurrency int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if maxConcurrency > 0 && p.active >= maxConcurrency {
		return fmt.Errorf("max concurrency %d reached", maxConcurrency)
	}
	p.active++
	return nil
}

func (p *Pool) unreserve() {
	p.mu.Lock()
	p.active--
	p.mu.Unlock()
}

func (p *Pool) started(ctx context.Context, maxUptime time.Duration, id, name string) *Container {
	if maxUptime > 0 {
		p.armMaxUptime(ctx, maxUptime, id)
	}
	p.log.Info("container started", "id", id[:12], "name", name)
	return &Container{ID: id, exited: p.watchExit(ctx, id)}
}

// Client is the pool's Docker client, for callers that provision what a
// container needs around it.
func (p *Pool) Client() *client.Client { return p.cli }

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

// containerTeardown is a live container's max-uptime timer and whether its
// Docker teardown has been claimed.
type containerTeardown struct {
	timer  *time.Timer
	closed bool
}

// armMaxUptime schedules a stop and remove after maxUptime, recording the
// entry and timer under p.mu.
func (p *Pool) armMaxUptime(ctx context.Context, maxUptime time.Duration, id string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	t := p.teardownLocked(id)
	t.timer = time.AfterFunc(maxUptime, func() {
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

// claimReaper reports whether the max-uptime callback may tear the container
// down. A missing entry is a loss.
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

// claimRelease reports whether Release may tear the container down. A
// missing entry is a win.
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

// forgetTeardown stops the timer and deletes the container's entry.
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

// releaseDecision reports whether Release honours the grace period and the
// stop timeout to use. A cancelled ctx skips the grace period and kills
// immediately.
func releaseDecision(ctx context.Context, grace time.Duration) (honorGrace bool, stopTimeout *int) {
	if ctx.Err() != nil {
		zero := 0
		return false, &zero
	}
	return grace > 0, nil
}

// Release stops and removes a container after a task completes. If GracePeriod
// is configured, the container stays alive for that duration before being
// killed -- the agent can handle follow-ups (gate re-runs, human replies)
// during this window.
func (p *Pool) Release(ctx context.Context, c *Container) {
	if c == nil {
		return
	}
	honorGrace, stopTimeout := releaseDecision(ctx, p.cfg.GracePeriod)
	if honorGrace {
		p.log.Info("container keeping alive for grace period", "id", c.ID[:12], "grace", p.cfg.GracePeriod)
		time.Sleep(p.cfg.GracePeriod)
	}

	// Claim after the grace period so MaxUptime still applies during it.
	if p.claimRelease(c.ID) {
		// Stop with a fresh deadline even if ctx is cancelled.
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

// EnsureImage pulls ref under the live pull policy unless the pool has
// already made it available. The configured image was pulled by NewPool and
// recorded, so a caller naming it skips a second pull.
func (p *Pool) EnsureImage(ctx context.Context, ref string) error {
	return p.ensureImage(ctx, p.current(), ref)
}

// ensureImage pulls ref under cfg's pull policy unless it is already present
// in the pool. It is the live read an acquire uses; EnsureImage is the
// exported form for callers that only name a ref.
func (p *Pool) ensureImage(ctx context.Context, cfg Config, ref string) error {
	if cfg.PullPolicy != "always" && cfg.PullPolicy != "missing" {
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
	p.markPulled(ref)
	return nil
}

// markPulled records that ref is available locally, so a later acquire skips
// a second pull.
func (p *Pool) markPulled(ref string) {
	p.mu.Lock()
	if p.pulled == nil {
		p.pulled = map[string]bool{}
	}
	p.pulled[ref] = true
	p.mu.Unlock()
}

// pullImage pulls ref under the live pull policy. On "missing" policy, skips
// if the image already exists locally.
func (p *Pool) pullImage(ctx context.Context, ref string) error {
	cfg := p.current()
	if cfg.PullPolicy == "missing" {
		_, err := p.cli.ImageInspect(ctx, ref)
		if err == nil {
			p.log.Info("image already present", "image", ref)
			return nil
		}
	}

	p.log.Info("pulling image", "image", ref)
	rc, err := p.cli.ImagePull(ctx, ref, client.ImagePullOptions{RegistryAuth: p.registryAuthHeader(cfg, ref)})
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

// registryAuthHeader returns the X-Registry-Auth value, or "". A credential
// that cannot be rendered logs a warning and pulls anonymously.
func (p *Pool) registryAuthHeader(cfg Config, ref string) string {
	if cfg.RegistryAuth == "" {
		return ""
	}
	encoded, err := encodeRegistryAuth(cfg.RegistryAuth)
	if err != nil {
		p.log.Warn("registry credential unusable; pulling without auth", "image", ref, "err", err)
		return ""
	}
	return encoded
}

// encodeRegistryAuth encodes credential, a registry.AuthConfig JSON document,
// as the base64url JSON the Engine API documents for
// [registry.AuthHeader]. It is the shape docker login's config.json stores,
// minus the surrounding "auths" map.
func encodeRegistryAuth(credential string) (string, error) {
	var auth registry.AuthConfig
	if err := json.Unmarshal([]byte(credential), &auth); err != nil {
		return "", fmt.Errorf("decode registry credential: %w", err)
	}
	if auth.Username == "" && auth.Password == "" && auth.IdentityToken == "" && auth.RegistryToken == "" {
		return "", errors.New("registry credential names no username, password, identity token or registry token")
	}
	payload, err := json.Marshal(auth)
	if err != nil {
		return "", fmt.Errorf("encode registry credential: %w", err)
	}
	return base64.URLEncoding.EncodeToString(payload), nil
}

// selfNetwork returns the user-defined network this process's container is
// on, or "".
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

// Cap is the concurrency cap this pool enforces, from the live
// containers.max_concurrency. Zero means unlimited (Acquire only enforces a
// cap when MaxConcurrency > 0), so a caller rendering "active/cap" must not
// treat zero as a cap of zero.
func (p *Pool) Cap() int { return p.current().MaxConcurrency }
