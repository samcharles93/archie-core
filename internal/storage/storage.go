// Package storage provides the mounts for agent containers.
//
//	/data/
//	  worktree/           bind mount of the host worktree
//	    .git/task.json    task brief
//	  repo/               optional per-repo persistent volume
//	  cache/
//	    go/ node/ pnpm/ deno/ bun/ pip/ cargo/   shared ecosystem caches
//	    mcp-npm/          npm cache for npx MCP servers, always mounted
//
// Cache volumes are shared across tasks and never deleted.
package storage

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

// AppendJSONLine appends one complete JSON value to a JSONL file. The value is
// marshalled before opening the file so a marshal failure cannot leave a
// partial record behind.
func AppendJSONLine(path string, value any) error {
	record, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal JSONL record: %w", err)
	}
	record = append(record, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create JSONL directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open JSONL file: %w", err)
	}
	if _, err := f.Write(record); err != nil {
		_ = f.Close()
		return fmt.Errorf("append JSONL record: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close JSONL file: %w", err)
	}
	return nil
}

// ReadJSONLines calls fn for each JSON line in path. A missing file is not
// an error; a bad line or an fn error stops the scan.
func ReadJSONLines[T any](path string, fn func(T) error) error {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open JSONL file: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		var record T
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			return fmt.Errorf("decode JSONL record: %w", err)
		}
		if err := fn(record); err != nil {
			return fmt.Errorf("handle JSONL record: %w", err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan JSONL file: %w", err)
	}
	return nil
}

// WorktreeMountDir is the fixed container path where a task's worktree is
// bind-mounted. archie-agent uses this path directly rather than the host
// path archied's worktree.Manager reports  --  the two processes see the same
// files at different paths.
const WorktreeMountDir = "/data/worktree"

// Persistent storage paths share the per-repository volume so session output,
// and project memory survive individual task containers.
const (
	PersistentMountDir = "/data/repo"
	SessionPath        = PersistentMountDir + "/session.jsonl"
	MemoryPath         = PersistentMountDir + "/memory.jsonl"
)

// MCPNPMCacheMountDir is where the npm cache for npx MCP servers is mounted
// on every task container.
const MCPNPMCacheMountDir = "/data/cache/mcp-npm"

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

// alwaysOnCacheVolumes are mounted on every task container.
var alwaysOnCacheVolumes = []cacheVolume{
	{Name: "archie-cache-mcp-npm", MountPath: MCPNPMCacheMountDir},
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

// cacheMounts returns the always-on mounts plus the ecosystem's, sorted by
// destination. Ecosystem is case-insensitive.
func cacheMounts(ecosystem string) []Mount {
	vols := append([]cacheVolume(nil), alwaysOnCacheVolumes...)
	vols = append(vols, cacheVolumesByEcosystem[strings.ToLower(ecosystem)]...)

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

// ensureVolume creates a Docker volume unless it exists. A nil client does
// nothing.
func (d *DockerBackend) ensureVolume(ctx context.Context, name string, labels map[string]string) error {
	if d.cli == nil {
		return nil // nil client for testing  --  volumes must be pre-created
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
		// duplicate volumes  --  treat that as success.
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

// Setup returns a task container's mounts: the worktree, the repo volume
// when PersistentStorage is set, then the caches.
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
			Destination: PersistentMountDir,
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
