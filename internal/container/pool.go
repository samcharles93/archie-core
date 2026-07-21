// Package container manages Docker containers running archie-agent.
// A Pool acquires and releases containers per task, handling image pull,
// creation, startup, health check, and teardown. The daemon's existing
// NATSRunner handles agent communication unchanged.
package container

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"
)

// Container wraps a running Docker container.
type Container struct {
	ID string
}

// Pool manages a set of Docker containers running archie-agent.
type Pool struct {
	cli     *client.Client
	cfg     Config
	log     *slog.Logger
	natsURL string

	mu     sync.Mutex
	active int
}

// Config is the subset of daemon container configuration the pool needs.
type Config struct {
	Image          string
	MaxConcurrency int
	MaxUptime      time.Duration
	PullPolicy     string
}

// NewPool connects to the Docker daemon, optionally pulls the image,
// and cleans up orphaned containers from a previous daemon crash.
func NewPool(ctx context.Context, cfg Config, natsURL string, log *slog.Logger) (*Pool, error) {
	cli, err := client.New(client.FromEnv)
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}

	p := &Pool{
		cli:     cli,
		cfg:     cfg,
		log:     log,
		natsURL: natsURL,
	}

	// Pull image if needed.
	if cfg.PullPolicy == "always" || cfg.PullPolicy == "missing" {
		if err := p.pullImage(ctx); err != nil {
			cli.Close()
			return nil, err
		}
	}

	// Recover orphaned containers from a previous daemon crash.
	p.recoverOrphans(ctx)

	return p, nil
}

// Acquire creates and starts a container, mounting the worktree at
// /data/worktree, passing provider API keys and NATS URL as environment
// variables. If MaxUptime is set, the container is created with a
// deadline — Docker kills it when the time elapses.
func (p *Pool) Acquire(ctx context.Context, workspace string, env []string) (*Container, error) {
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

	resp, err := p.cli.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name: name,
		Config: &container.Config{
			Image: p.cfg.Image,
			Env:   env,
			Labels: map[string]string{
				"archie-daemon": "true",
			},
		},
		HostConfig: &container.HostConfig{
			Mounts: []mount.Mount{
				{
					Type:   mount.TypeBind,
					Source: workspace,
					Target: "/data/worktree",
				},
			},
			AutoRemove: true,
		},
	})
	if err != nil {
		p.mu.Lock()
		p.active--
		p.mu.Unlock()
		return nil, fmt.Errorf("container create: %w", err)
	}

	if _, err := p.cli.ContainerStart(ctx, resp.ID, client.ContainerStartOptions{}); err != nil {
		// Best-effort cleanup.
		p.cli.ContainerRemove(context.Background(), resp.ID, client.ContainerRemoveOptions{Force: true})
		p.mu.Lock()
		p.active--
		p.mu.Unlock()
		return nil, fmt.Errorf("container start: %w", err)
	}

	p.log.Info("container started", "id", resp.ID[:12], "name", name, "workspace", workspace)
	return &Container{ID: resp.ID}, nil
}

// Release stops and removes a container after a task completes.
func (p *Pool) Release(c *Container) {
	if c == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, err := p.cli.ContainerStop(ctx, c.ID, client.ContainerStopOptions{}); err != nil {
		p.log.Warn("container stop failed", "id", c.ID[:12], "err", err)
	}
	// AutoRemove is set, but force-remove as a safety net.
	p.cli.ContainerRemove(context.Background(), c.ID, client.ContainerRemoveOptions{Force: true})

	p.mu.Lock()
	p.active--
	p.mu.Unlock()
	p.log.Info("container released", "id", c.ID[:12])
}

// Close stops and removes all active containers, then closes the client.
func (p *Pool) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	list, err := p.cli.ContainerList(ctx, client.ContainerListOptions{
		All: true,
		Filters: client.Filters{}.Add("label", "archie-daemon=true"),
	})
	if err != nil {
		p.log.Warn("container list failed during close", "err", err)
	} else {
		for _, c := range list.Items {
			p.log.Info("cleaning up container", "id", c.ID[:12])
			p.cli.ContainerStop(ctx, c.ID, client.ContainerStopOptions{})
			p.cli.ContainerRemove(ctx, c.ID, client.ContainerRemoveOptions{Force: true})
		}
	}
	return p.cli.Close()
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
	defer rc.Close()
	io.Copy(io.Discard, rc)
	p.log.Info("image pulled", "image", ref)
	return nil
}

// recoverOrphans stops and removes containers left by a previous daemon.
func (p *Pool) recoverOrphans(ctx context.Context) {
	list, err := p.cli.ContainerList(ctx, client.ContainerListOptions{
		All: true,
		Filters: client.Filters{}.Add("label", "archie-daemon=true"),
	})
	if err != nil {
		return
	}
	for _, c := range list.Items {
		p.log.Info("recovering orphaned container", "id", c.ID[:12])
		p.cli.ContainerStop(ctx, c.ID, client.ContainerStopOptions{})
		p.cli.ContainerRemove(ctx, c.ID, client.ContainerRemoveOptions{Force: true})
	}
	if len(list.Items) > 0 {
		p.log.Info("orphan recovery complete", "count", len(list.Items))
	}
}
