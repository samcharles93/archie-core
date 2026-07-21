package storage

import (
	"context"
	"os"
	"sort"
	"testing"
)

// ── interface contract ───────────────────────────────────────────────

func TestBackendInterfaceIsSatisfied(t *testing.T) {
	// Compile-time check that DockerBackend satisfies Backend.
	var _ Backend = (*DockerBackend)(nil)
}

func TestTaskRefFields(t *testing.T) {
	ref := TaskRef{
		Owner:       "alice",
		Repo:        "demo",
		IssueNumber: 42,
		Ecosystem:   "go",
		WorktreeDir: "/tmp/worktree",
	}
	if ref.Owner != "alice" || ref.Repo != "demo" || ref.IssueNumber != 42 {
		t.Fatal("TaskRef fields not assignable")
	}
}

func TestMountStruct(t *testing.T) {
	m := Mount{
		Type:        MountTypeVolume,
		Source:      "archie-cache-go",
		Destination: "/data/cache/go",
	}
	if m.Type != MountTypeVolume || m.Source != "archie-cache-go" {
		t.Fatal("Mount fields not assignable")
	}
}

// ── mount list helpers ───────────────────────────────────────────────

func TestMountListForEcosystemGo(t *testing.T) {
	mounts := cacheMounts("go")
	names := mountDestinations(mounts)
	if !contains(names, "/data/cache/go") {
		t.Errorf("go ecosystem missing /data/cache/go in %v", names)
	}
}

func TestMountListForEcosystemNode(t *testing.T) {
	mounts := cacheMounts("node")
	names := mountDestinations(mounts)
	if !contains(names, "/data/cache/node") {
		t.Errorf("node ecosystem missing /data/cache/node in %v", names)
	}
	if !contains(names, "/data/cache/pnpm") {
		t.Errorf("node ecosystem missing /data/cache/pnpm in %v", names)
	}
}

func TestMountListForEcosystemPython(t *testing.T) {
	mounts := cacheMounts("python")
	names := mountDestinations(mounts)
	if !contains(names, "/data/cache/pip") {
		t.Errorf("python ecosystem missing /data/cache/pip in %v", names)
	}
}

func TestMountListForEcosystemRust(t *testing.T) {
	mounts := cacheMounts("rust")
	names := mountDestinations(mounts)
	if !contains(names, "/data/cache/cargo") {
		t.Errorf("rust ecosystem missing /data/cache/cargo in %v", names)
	}
}

func TestMountListUnknownEcosystemReturnsBaseOnly(t *testing.T) {
	mounts := cacheMounts("custom")
	if len(mounts) != 0 {
		t.Errorf("custom ecosystem returned %d mounts, want 0", len(mounts))
	}
}

func TestMountListIsSorted(t *testing.T) {
	// Mounts must be sorted by Destination for deterministic container config.
	mounts := cacheMounts("node")
	if !sort.SliceIsSorted(mounts, func(i, j int) bool {
		return mounts[i].Destination < mounts[j].Destination
	}) {
		t.Error("cacheMounts not sorted by Destination")
	}
}

func TestMountListNoDuplicates(t *testing.T) {
	// Each destination must appear at most once.
	mounts := cacheMounts("node")
	seen := map[string]bool{}
	for _, m := range mounts {
		if seen[m.Destination] {
			t.Errorf("duplicate mount destination %q", m.Destination)
		}
		seen[m.Destination] = true
	}
}

// ── Docker backend (unit tests, no Docker daemon required) ──────────

func TestDockerBackendSetupWorktreeBindMount(t *testing.T) {
	// Setup must always include the worktree bind mount at /data/worktree.
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	backend := &DockerBackend{} // nil client — Setup doesn't need it for bind mounts
	mounts, err := backend.Setup(context.Background(), TaskRef{
		WorktreeDir: dir,
		Ecosystem:   "go",
	})
	if err != nil {
		t.Fatal(err)
	}

	found := false
	for _, m := range mounts {
		if m.Destination == "/data/worktree" {
			found = true
			if m.Type != MountTypeBind {
				t.Errorf("worktree mount type = %q, want bind", m.Type)
			}
			if m.Source != dir {
				t.Errorf("worktree mount source = %q, want %q", m.Source, dir)
			}
		}
	}
	if !found {
		t.Error("Setup did not include /data/worktree bind mount")
	}
}

func TestDockerBackendSetupIncludesCacheMounts(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	backend := &DockerBackend{}
	mounts, err := backend.Setup(context.Background(), TaskRef{
		WorktreeDir: dir,
		Ecosystem:   "node",
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"/data/cache/node", "/data/cache/pnpm"} {
		found := false
		for _, m := range mounts {
			if m.Destination == want {
				found = true
				if m.Type != MountTypeVolume {
					t.Errorf("%s mount type = %q, want volume", want, m.Type)
				}
			}
		}
		if !found {
			t.Errorf("Setup missing cache mount %s for node ecosystem", want)
		}
	}
}

func TestDockerBackendSetupWorktreeMountIsFirst(t *testing.T) {
	// Worktree bind mount must be the first entry — container create
	// depends on this order for overlay semantics.
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	backend := &DockerBackend{}
	mounts, err := backend.Setup(context.Background(), TaskRef{
		WorktreeDir: dir,
		Ecosystem:   "python",
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(mounts) == 0 {
		t.Fatal("Setup returned no mounts")
	}
	if mounts[0].Destination != "/data/worktree" {
		t.Errorf("first mount = %q, want /data/worktree", mounts[0].Destination)
	}
}

func TestDockerBackendTeardownSucceeds(t *testing.T) {
	// Teardown is a no-op for Docker backend — cache volumes survive.
	// Must not error.
	backend := &DockerBackend{}
	err := backend.Teardown(context.Background(), TaskRef{
		WorktreeDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal("Teardown should not error for Docker backend:", err)
	}
}

// ── adversarial ──────────────────────────────────────────────────────

func TestDockerBackendSetupEmptyWorktreeDir(t *testing.T) {
	// An empty (non-existent) worktree dir must still produce valid mounts.
	// The backend doesn't validate host paths — that's the pool's job.
	backend := &DockerBackend{}
	mounts, err := backend.Setup(context.Background(), TaskRef{
		WorktreeDir: "/nonexistent/worktree",
		Ecosystem:   "go",
	})
	if err != nil {
		t.Fatal("Setup errored on nonexistent worktree:", err)
	}
	found := false
	for _, m := range mounts {
		if m.Destination == "/data/worktree" {
			found = true
		}
	}
	if !found {
		t.Error("worktree mount missing for nonexistent path")
	}
}

func TestDockerBackendSetupEmptyEcosystem(t *testing.T) {
	// Empty ecosystem must not cause nil pointer or crash.
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	backend := &DockerBackend{}
	mounts, err := backend.Setup(context.Background(), TaskRef{
		WorktreeDir: dir,
		Ecosystem:   "",
	})
	if err != nil {
		t.Fatal("Setup errored on empty ecosystem:", err)
	}
	// Must at minimum have the worktree bind mount.
	if len(mounts) == 0 {
		t.Error("Setup returned no mounts for empty ecosystem")
	}
}

func TestDockerBackendSetupUnknownEcosystemSkipped(t *testing.T) {
	// Unknown ecosystem must not add cache mounts, but must not error.
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	backend := &DockerBackend{}
	mounts, err := backend.Setup(context.Background(), TaskRef{
		WorktreeDir: dir,
		Ecosystem:   "haskell", // not in the cache map
	})
	if err != nil {
		t.Fatal("Setup errored on unknown ecosystem:", err)
	}
	for _, m := range mounts {
		if m.Destination != "/data/worktree" {
			t.Errorf("unexpected mount %s for unknown ecosystem", m.Destination)
		}
	}
}

// ── persistent storage ────────────────────────────────────────────────

func TestDockerBackendSetupWithPersistentStorage(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	backend := &DockerBackend{}
	mounts, err := backend.Setup(context.Background(), TaskRef{
		WorktreeDir:       dir,
		Ecosystem:         "go",
		PersistentStorage: true,
		Owner:             "alice",
		Repo:              "demo",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Must include /data/repo volume mount.
	found := false
	for _, m := range mounts {
		if m.Destination == "/data/repo" {
			found = true
			if m.Type != MountTypeVolume {
				t.Errorf("/data/repo mount type = %q, want volume", m.Type)
			}
			if m.Source != "archie-repo-alice-demo" {
				t.Errorf("/data/repo volume name = %q, want archie-repo-alice-demo", m.Source)
			}
		}
	}
	if !found {
		t.Error("persistent storage enabled but /data/repo mount missing")
	}
}

func TestDockerBackendSetupWithoutPersistentStorage(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	backend := &DockerBackend{}
	mounts, err := backend.Setup(context.Background(), TaskRef{
		WorktreeDir:       dir,
		Ecosystem:         "go",
		PersistentStorage: false,
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, m := range mounts {
		if m.Destination == "/data/repo" {
			t.Error("/data/repo mount present but PersistentStorage is false")
		}
	}
}

func TestDockerBackendSetupPersistentStorageEmptyOwner(t *testing.T) {
	// With empty Owner/Repo, no per-repo volume should be created even
	// when PersistentStorage is true — we can't name the volume.
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	backend := &DockerBackend{}
	mounts, err := backend.Setup(context.Background(), TaskRef{
		WorktreeDir:       dir,
		Ecosystem:         "go",
		PersistentStorage: true,
		Owner:             "",
		Repo:              "",
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, m := range mounts {
		if m.Destination == "/data/repo" {
			t.Error("/data/repo mount present but Owner is empty")
		}
	}
}

func TestRepoVolumeName(t *testing.T) {
	name := repoVolumeName("myorg", "myrepo")
	if name != "archie-repo-myorg-myrepo" {
		t.Errorf("repoVolumeName = %q, want archie-repo-myorg-myrepo", name)
	}
}

// ── helpers ──────────────────────────────────────────────────────────

func mountDestinations(mounts []Mount) []string {
	var out []string
	for _, m := range mounts {
		out = append(out, m.Destination)
	}
	return out
}

func contains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}
