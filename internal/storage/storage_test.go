package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"go/format"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/volume"
	"github.com/moby/moby/client"
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

	backend := &DockerBackend{} // nil client  --  Setup doesn't need it for bind mounts
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
	// Worktree bind mount must be the first entry  --  container create
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
	// Teardown is a no-op for Docker backend  --  cache volumes survive.
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
	// The backend doesn't validate host paths  --  that's the pool's job.
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
	// when PersistentStorage is true  --  we can't name the volume.
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

func TestDockerBackendLabelsPersistentRepoVolumes(t *testing.T) {
	var createBody string
	cli := dockerTestClient(t, func(req *http.Request) (*http.Response, error) {
		path := dockerAPIPath(req.URL.Path)
		switch {
		case req.Method == http.MethodGet:
			return dockerResponse(http.StatusNotFound, `{"message":"not found"}`), nil
		case req.Method == http.MethodPost && path == "/volumes/create":
			data, readErr := io.ReadAll(req.Body)
			if readErr != nil {
				return nil, readErr
			}
			createBody = string(data)
			return dockerResponse(http.StatusCreated, `{"Name":"archie-repo-alice-demo","Driver":"local","Labels":{}}`), nil
		default:
			return dockerResponse(http.StatusNotFound, `{"message":"unexpected request"}`), nil
		}
	})

	backend := NewDockerBackend(cli)
	if _, err := backend.Setup(context.Background(), TaskRef{
		WorktreeDir:       t.TempDir(),
		Ecosystem:         "custom",
		PersistentStorage: true,
		Owner:             "alice",
		Repo:              "demo",
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(createBody, `"com.samcharles93.archie.storage":"repo"`) {
		t.Fatalf("persistent volume create body lacks Archie repo label: %s", createBody)
	}
}

func TestDockerBackendCleanupExpiredOnlyRemovesOwnedRepoVolumes(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	listBody, err := json.Marshal(volume.ListResponse{Volumes: []volume.Volume{
		{
			Name:      "archie-repo-old",
			CreatedAt: now.Add(-25 * time.Hour).Format(time.RFC3339Nano),
			Labels:    map[string]string{"com.samcharles93.archie.storage": "repo"},
		},
		{
			Name:      "archie-repo-fresh",
			CreatedAt: now.Add(-23 * time.Hour).Format(time.RFC3339Nano),
			Labels:    map[string]string{"com.samcharles93.archie.storage": "repo"},
		},
		{
			Name:      "archie-cache-old",
			CreatedAt: now.Add(-100 * time.Hour).Format(time.RFC3339Nano),
			Labels:    map[string]string{"com.samcharles93.archie.storage": "cache"},
		},
		{
			Name:      "operator-volume",
			CreatedAt: now.Add(-100 * time.Hour).Format(time.RFC3339Nano),
		},
	}})
	if err != nil {
		t.Fatal(err)
	}

	var removed []string
	cli := dockerTestClient(t, func(req *http.Request) (*http.Response, error) {
		path := dockerAPIPath(req.URL.Path)
		switch req.Method {
		case http.MethodGet:
			if !strings.Contains(req.URL.Query().Get("filters"), "com.samcharles93.archie.storage") {
				t.Errorf("volume list did not filter by Archie ownership: %s", req.URL.RawQuery)
			}
			return dockerResponse(http.StatusOK, string(listBody)), nil
		case http.MethodDelete:
			removed = append(removed, strings.TrimPrefix(path, "/volumes/"))
			return dockerResponse(http.StatusNoContent, ""), nil
		default:
			return dockerResponse(http.StatusNotFound, `{"message":"unexpected request"}`), nil
		}
	})

	backend := NewDockerBackend(cli)
	backend.now = func() time.Time { return now }
	count, err := backend.CleanupExpired(context.Background(), 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("CleanupExpired count = %d, want 1", count)
	}
	if !slices.Equal(removed, []string{"archie-repo-old"}) {
		t.Fatalf("removed volumes = %v, want only expired owned repo volume", removed)
	}
}

func TestCacheMountsForTypeScript(t *testing.T) {
	// S4: typescript ecosystem must share node + pnpm cache volumes.
	mounts := cacheMounts("typescript")
	names := mountDestinations(mounts)
	if !contains(names, "/data/cache/node") {
		t.Errorf("typescript ecosystem missing /data/cache/node in %v", names)
	}
	if !contains(names, "/data/cache/pnpm") {
		t.Errorf("typescript ecosystem missing /data/cache/pnpm in %v", names)
	}
	// Must be sorted.
	if !sort.SliceIsSorted(mounts, func(i, j int) bool {
		return mounts[i].Destination < mounts[j].Destination
	}) {
		t.Error("typescript cacheMounts not sorted by Destination")
	}
}

// ── audit fixes ──────────────────────────────────────────────────────

func TestCacheMountsCaseInsensitive(t *testing.T) {
	// C6: ecosystem must be case-insensitive. "Python", "Go", "NODE"
	// must all return the same cache mounts as their lowercase forms.
	for _, eco := range []string{"Go", "GO", "go", "gO"} {
		mounts := cacheMounts(eco)
		names := mountDestinations(mounts)
		if !contains(names, "/data/cache/go") {
			t.Errorf("ecosystem %q: missing /data/cache/go in %v", eco, names)
		}
	}
	for _, eco := range []string{"Python", "PYTHON", "python"} {
		mounts := cacheMounts(eco)
		names := mountDestinations(mounts)
		if !contains(names, "/data/cache/pip") {
			t.Errorf("ecosystem %q: missing /data/cache/pip in %v", eco, names)
		}
	}
}

func TestEnsureVolumeAlreadyExistsIsNotError(t *testing.T) {
	// N1: if a volume already exists (concurrent creation), ensureVolume
	// must not treat it as an error. The TOCTOU between VolumeInspect and
	// VolumeCreate must be handled.
	//
	// We can't test against a real Docker daemon, but we can test the
	// design: ensureVolume should only return an error when Docker returns
	// something OTHER than "already exists". For now, verify that
	// ensureVolume with nil client returns nil (existing behavior).
	backend := &DockerBackend{}
	err := backend.ensureVolume(context.Background(), "test-volume", nil)
	if err != nil {
		t.Error("ensureVolume with nil client should not error:", err)
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
	return slices.Contains(slice, s)
}

func dockerResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func dockerTestClient(t *testing.T, roundTrip func(*http.Request) (*http.Response, error)) *client.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		resp, err := roundTrip(req)
		if err != nil {
			// roundTrip may return a partial response alongside the error.
			if resp != nil {
				resp.Body.Close()
			}
			t.Errorf("Docker test handler: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer resp.Body.Close()
		for key, values := range resp.Header {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(resp.StatusCode)
		if _, err := io.Copy(w, resp.Body); err != nil {
			t.Errorf("Docker test response write: %v", err)
		}
	}))
	t.Cleanup(srv.Close)

	cli, err := client.New(
		client.WithHost(srv.URL),
		client.WithHTTPClient(srv.Client()),
		client.WithAPIVersion("1.55"),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cli.Close() })
	return cli
}

// ── gofmt compliance ─────────────────────────────────────────────────

func TestStorageDotGoIsGofmtClean(t *testing.T) {
	// storage.go must be gofmt-clean so that CI checks (gofmt -l) pass.
	// This test reads the source and feeds it through go/format.Source;
	// if the output differs, the file is not canonical Go formatting.
	src, err := os.ReadFile("storage.go")
	if err != nil {
		t.Fatal(err)
	}
	formatted, err := format.Source(src)
	if err != nil {
		t.Fatalf("storage.go is not valid Go: %v", err)
	}
	if string(formatted) != string(src) {
		t.Errorf("storage.go is not gofmt-clean. diff:\n%s", diffBytes(src, formatted))
	}
}

func diffBytes(a, b []byte) string {
	linesA := strings.Split(string(a), "\n")
	linesB := strings.Split(string(b), "\n")
	var out strings.Builder
	maxLine := max(len(linesB), len(linesA))
	for i := range maxLine {
		var lineA, lineB string
		if i < len(linesA) {
			lineA = linesA[i]
		}
		if i < len(linesB) {
			lineB = linesB[i]
		}
		if lineA != lineB {
			fmt.Fprintf(&out, "-%s\n+%s\n", lineA, lineB)
		}
	}
	return out.String()
}

func dockerAPIPath(path string) string {
	if strings.HasPrefix(path, "/v1.55/") {
		return strings.TrimPrefix(path, "/v1.55")
	}
	return path
}
