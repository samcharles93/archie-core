// Package container manages Docker containers running archie-agent.
// A Pool acquires and releases containers per task, handling image pull,
// creation, startup, health check, and teardown. The daemon's existing
// NATSRunner handles agent communication unchanged.
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
	ID string
}

// TaskPayload is the boot-time brief written to /data/task.json before
// the container starts, per PRD section 3.
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
}

// WriteTaskJSON writes the task payload to <workspace>/task.json.
func WriteTaskJSON(workspace string, payload TaskPayload) error {
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		return fmt.Errorf("task.json dir: %w", err)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(workspace, "task.json"), data, 0o644)
}

// Pool manages a set of Docker containers running archie-agent.
type Pool struct {
	cli     *client.Client
	ownCli  bool // true when the pool created the client (owns Close)
	cfg     Config
	log     *slog.Logger
	natsURL string
	// network is the user-defined Docker network spawned containers join,
	// detected from the daemon's own container so they can resolve
	// sibling services (nats, etc.) by compose service name. Empty when
	// the daemon isn't running in a container on such a network, or when
	// detection fails  --  Docker then falls back to its default bridge.
	network string

	mu     sync.Mutex
	active int
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
}

// NewPool connects to the Docker daemon, optionally pulls the image,
// and cleans up orphaned containers from a previous daemon crash.
//
// If cfg.DockerClient is set, it is reused (caller owns Close).
// Otherwise a new client is created via client.FromEnv (pool owns Close).
func NewPool(ctx context.Context, cfg Config, natsURL string, log *slog.Logger) (*Pool, error) {
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

	p := &Pool{
		cli:     cli,
		cfg:     cfg,
		log:     log,
		natsURL: natsURL,
		ownCli:  cfg.DockerClient == nil,
		network: network,
	}

	// Pull image if needed.
	if cfg.PullPolicy == "always" || cfg.PullPolicy == "missing" {
		if err := p.pullImage(ctx); err != nil {
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

// Acquire creates and starts a container with the given mounts and
// environment variables. Mounts are provided by the caller (typically
// from a storage.Backend). If MaxUptime is set, the container is
// created with a deadline  --  Docker kills it when the time elapses.
func (p *Pool) Acquire(ctx context.Context, mounts []storage.Mount, env []string) (*Container, error) {
	p.mu.Lock()
	if p.cfg.MaxConcurrency > 0 && p.active >= p.cfg.MaxConcurrency {
		p.mu.Unlock()
		return nil, fmt.Errorf("max concurrency %d reached", p.cfg.MaxConcurrency)
	}
	p.active++
	p.mu.Unlock()

	// Enforce MaxUptime via container-level timeout.
	if p.cfg.MaxUptime > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.cfg.MaxUptime)
		defer cancel()
	}

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
			Image: p.cfg.Image,
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

	p.log.Info("container started", "id", resp.ID[:12], "name", name)
	return &Container{ID: resp.ID}, nil
}

// Release stops and removes a container after a task completes. If
// GracePeriod is configured, the container stays alive for that duration
// before being killed  --  the agent can handle follow-ups (gate re-runs,
// human replies) during this window. PRD section 1.
func (p *Pool) Release(ctx context.Context, c *Container) {
	if c == nil {
		return
	}
	// Honour the post-completion grace period.
	if p.cfg.GracePeriod > 0 {
		p.log.Info("container keeping alive for grace period", "id", c.ID[:12], "grace", p.cfg.GracePeriod)
		time.Sleep(p.cfg.GracePeriod)
	}

	stopCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if _, err := p.cli.ContainerStop(stopCtx, c.ID, client.ContainerStopOptions{}); err != nil {
		p.log.Warn("container stop failed", "id", c.ID[:12], "err", err)
	}
	if _, err := p.cli.ContainerRemove(context.WithoutCancel(ctx), c.ID, client.ContainerRemoveOptions{Force: true}); err != nil {
		p.log.Warn("container remove failed", "id", c.ID[:12], "err", err)
	}

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

// pullImage pulls the configured image. On "missing" policy, skips if
// the image already exists locally.
func (p *Pool) pullImage(ctx context.Context) error {
	ref := p.cfg.Image

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
