// Package storage defines the pluggable storage backend interface for agent
// container execution. The Docker backend (DockerBackend) is the MVP
// implementation — no NFS/SMB/S3 yet, just Docker volumes + bind mounts.
//
// /data/ volume layout (implemented):
//
//	/data/
//	  task.json          — at /data/worktree/task.json (WriteTaskJSON writes
//	                        to the worktree root, which is bind-mounted)
//	  worktree/          — bind mount of host worktree
//	  repo/              — per-repo persistent volume (optional, on request)
//	  cache/
//	    go/              — GOMODCACHE (shared across all tasks)
//	    node/            — npm cache (shared)
//	    pnpm/            — pnpm store (shared)
//	    deno/            — Deno module cache (shared)
//	    bun/             — Bun package cache (shared)
//	    pip/             — pip cache (shared)
//	    cargo/           — Rust cargo cache (shared)
//
// TODO(PRD §3): session.jsonl, memory.jsonl, plugins/ volume — these
// require agent-side implementation before the storage layer can mount
// them. session.jsonl needs per-stage structured output from the agent;
// memory.jsonl needs cross-session persistence; plugins/ needs daemon-
// staged bundled plugins.
//
// Cache volumes are named Docker volumes created once and shared across all
// tasks. They are never deleted — the daemon operator manages them.
package storage

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"
)

// ── types ──────────────────────────────────────────────────────────────

// TaskRef identifies a task for storage setup.
type TaskRef struct {
	Owner, Repo       string
	IssueNumber       int
	Ecosystem         string // "go", "node", "python", "rust", "custom"
	WorktreeDir       string // host path to the git worktree
	PersistentStorage bool   // if true, attach a per-repo Docker volume at /data/repo
}

// Mount describes a container mount point.
type Mount struct {
	Type        string // "bind" or "volume"
	Source      string // host path (bind) or volume name (volume)
	Destination string // container path
}

// Mount type constants.
const (
	MountTypeBind   = "bind"
	MountTypeVolume = "volume"
)

// WorktreeMountDir is the fixed container path where a task's worktree is
// bind-mounted. archie-agent uses this path directly rather than the host
// path archied's worktree.Manager reports — the two processes see the same
// files at different paths.
const WorktreeMountDir = "/data/worktree"

// Backend prepares container storage. Each container runtime backend
// (Docker, future containerd, etc.) implements this interface.
type Backend interface {
	// Setup returns the mount specs for a task container. Must include
	// at minimum a bind mount for the worktree at /data/worktree.
	Setup(ctx context.Context, task TaskRef) ([]Mount, error)
	// Teardown cleans up ephemeral storage. Cache volumes survive.
	Teardown(ctx context.Context, task TaskRef) error
	// CleanupExpired removes persistent per-repo storage older than ttl.
	// Shared ecosystem caches and storage not owned by Archie survive.
	CleanupExpired(ctx context.Context, ttl time.Duration) (int, error)
}

// ── cache mounts per ecosystem ─────────────────────────────────────────

// cacheVolume describes a named Docker volume that is shared across tasks.
type cacheVolume struct {
	Name      string // Docker volume name
	MountPath string // container destination path
}

// cacheVolumesByEcosystem maps ecosystem names to their shared cache volumes.
// Volumes are created once by the pool and never deleted.
var cacheVolumesByEcosystem = map[string][]cacheVolume{
	"go": {
		{Name: "archie-cache-go", MountPath: "/data/cache/go"},
	},
	"node": {
		{Name: "archie-cache-node", MountPath: "/data/cache/node"},
		{Name: "archie-cache-pnpm", MountPath: "/data/cache/pnpm"},
	},
	"typescript": {
		{Name: "archie-cache-node", MountPath: "/data/cache/node"},
		{Name: "archie-cache-pnpm", MountPath: "/data/cache/pnpm"},
	},
	"deno": {
		{Name: "archie-cache-deno", MountPath: "/data/cache/deno"},
	},
	"bun": {
		{Name: "archie-cache-bun", MountPath: "/data/cache/bun"},
	},
	"python": {
		{Name: "archie-cache-pip", MountPath: "/data/cache/pip"},
	},
	"rust": {
		{Name: "archie-cache-cargo", MountPath: "/data/cache/cargo"},
	},
}

// cacheMounts returns the cache mount specs for an ecosystem, sorted by
// destination path for deterministic container configuration.
// Ecosystem is case-insensitive — "Python" and "python" are equivalent.
func cacheMounts(ecosystem string) []Mount {
	vols, ok := cacheVolumesByEcosystem[strings.ToLower(ecosystem)]
	if !ok {
		return nil
	}
	mounts := make([]Mount, len(vols))
	for i, v := range vols {
		mounts[i] = Mount{
			Type:        MountTypeVolume,
			Source:      v.Name,
			Destination: v.MountPath,
		}
	}
	sort.Slice(mounts, func(i, j int) bool {
		return mounts[i].Destination < mounts[j].Destination
	})
	return mounts
}

// ── error types ────────────────────────────────────────────────────────

// ErrVolumeCreate is returned when a Docker volume cannot be created.
type ErrVolumeCreate struct {
	Volume string
	Cause  error
}

func (e *ErrVolumeCreate) Error() string {
	return "create volume " + e.Volume + ": " + e.Cause.Error()
}

func (e *ErrVolumeCreate) Unwrap() error { return e.Cause }

// ── Docker backend ─────────────────────────────────────────────────────

// DockerBackend implements Backend for Docker via the moby client.
type DockerBackend struct {
	cli *client.Client
	now func() time.Time
}

const (
	storageLabel     = "com.samcharles93.archie.storage"
	storageKindRepo  = "repo"
	storageKindCache = "cache"
)

// NewDockerBackend creates a Docker storage backend.
func NewDockerBackend(cli *client.Client) *DockerBackend {
	return &DockerBackend{cli: cli}
}

// ensureVolume creates a Docker volume if it doesn't already exist.
// It is idempotent: concurrent callers racing to create the same volume
// will not see an error — "already exists" is treated as success.
//
// When d.cli is nil (unit tests), ensureVolume returns nil without
// creating a volume. The returned mounts will fail at container create
// time if Docker cannot find the volume. Production code always sets
// cli via NewDockerBackend.
func (d *DockerBackend) ensureVolume(ctx context.Context, name string, labels map[string]string) error {
	if d.cli == nil {
		return nil // nil client for testing — volumes must be pre-created
	}
	// VolumeInspectOptions{} is the empty struct form (requires moby v25+).
	// The go.mod pins a compatible version; zero value is always valid.
	_, err := d.cli.VolumeInspect(ctx, name, client.VolumeInspectOptions{})
	if err == nil {
		return nil // already exists
	}
	_, err = d.cli.VolumeCreate(ctx, client.VolumeCreateOptions{
		Name:   name,
		Driver: "local",
		Labels: labels,
	})
	if err != nil {
		// TOCTOU: a concurrent caller may have created the volume between
		// our inspect and create calls. Docker returns an error for
		// duplicate volumes — treat that as success.
		if strings.Contains(err.Error(), "already exists") || strings.Contains(err.Error(), "already in use") {
			return nil
		}
		return &ErrVolumeCreate{Volume: name, Cause: err}
	}
	return nil
}

// repoVolumeName returns the Docker volume name for a repo's persistent storage.
func repoVolumeName(owner, repo string) string {
	return fmt.Sprintf("archie-repo-%s-%s", owner, repo)
}

// Setup returns the mount list for a task container. Mounts are ordered:
// 1. /data/worktree bind mount (always first)
// 2. /data/repo volume mount (only when PersistentStorage is true)
// 3. /data/cache/<ecosystem> volume mounts (sorted by destination)
func (d *DockerBackend) Setup(ctx context.Context, task TaskRef) ([]Mount, error) {
	mounts := []Mount{
		{
			Type:        MountTypeBind,
			Source:      task.WorktreeDir,
			Destination: WorktreeMountDir,
		},
	}

	// Per-repo persistent volume.
	if task.PersistentStorage && task.Owner != "" && task.Repo != "" {
		volName := repoVolumeName(task.Owner, task.Repo)
		if err := d.ensureVolume(ctx, volName, map[string]string{storageLabel: storageKindRepo}); err != nil {
			return nil, err
		}
		mounts = append(mounts, Mount{
			Type:        MountTypeVolume,
			Source:      volName,
			Destination: "/data/repo",
		})
	}

	caches := cacheMounts(task.Ecosystem)
	for _, m := range caches {
		if err := d.ensureVolume(ctx, m.Source, map[string]string{storageLabel: storageKindCache}); err != nil {
			return nil, err
		}
	}
	mounts = append(mounts, caches...)

	return mounts, nil
}

// Teardown is a no-op for Docker backend. Cache volumes survive task
// completion so subsequent tasks benefit from warmed caches.
func (d *DockerBackend) Teardown(ctx context.Context, task TaskRef) error {
	return nil
}

// CleanupExpired removes Archie-owned per-repo volumes whose creation time is
// at least ttl old. Docker volume labels are immutable, so the TTL is a hard
// maximum age rather than an idle lease. Removal is non-forced: a volume still
// attached to a running container is retained and retried on the next sweep.
func (d *DockerBackend) CleanupExpired(ctx context.Context, ttl time.Duration) (int, error) {
	if d.cli == nil || ttl <= 0 {
		return 0, nil
	}
	result, err := d.cli.VolumeList(ctx, client.VolumeListOptions{
		Filters: client.Filters{}.Add("label", storageLabel+"="+storageKindRepo),
	})
	if err != nil {
		return 0, fmt.Errorf("list persistent volumes: %w", err)
	}

	now := time.Now()
	if d.now != nil {
		now = d.now()
	}
	var (
		removed int
		errs    []error
	)
	for _, vol := range result.Items {
		// Defend against Docker implementations that ignore label filters.
		if vol.Labels[storageLabel] != storageKindRepo {
			continue
		}
		created, err := time.Parse(time.RFC3339Nano, vol.CreatedAt)
		if err != nil {
			errs = append(errs, fmt.Errorf("persistent volume %s created_at %q: %w", vol.Name, vol.CreatedAt, err))
			continue
		}
		if now.Sub(created) < ttl {
			continue
		}
		if _, err := d.cli.VolumeRemove(ctx, vol.Name, client.VolumeRemoveOptions{}); err != nil {
			errs = append(errs, fmt.Errorf("remove persistent volume %s: %w", vol.Name, err))
			continue
		}
		removed++
	}
	return removed, errors.Join(errs...)
}

// ConvertMounts converts []Mount to moby mount.Mount for container creation.
func ConvertMounts(mounts []Mount) []mount.Mount {
	out := make([]mount.Mount, len(mounts))
	for i, m := range mounts {
		out[i] = mount.Mount{
			Type:   mount.Type(m.Type),
			Source: m.Source,
			Target: m.Destination,
		}
	}
	return out
}
