// Ported from tau: internal/indexing/codesearch_test.go @ 4893818d0c76 (2026-07-13).
package indexing

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestBuildIndexAndCandidates(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "a.txt", "alpha needle omega\n")
	writeTestFile(t, root, "b.txt", "nothing here\n")
	indexPath := filepath.Join(t.TempDir(), "workspace.csearch")

	if err := BuildIndex(context.Background(), root, indexPath); err != nil {
		t.Fatalf("BuildIndex() error = %v", err)
	}
	files, err := IndexCandidates(indexPath, "needle", false, false)
	if err != nil {
		t.Fatalf("IndexCandidates() error = %v", err)
	}
	if len(files) != 1 || files[0] != filepath.Join(root, "a.txt") {
		t.Fatalf("candidates = %v", files)
	}
}

func TestWorkspaceFilesRespectsGitIgnore(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init", "-q")
	writeTestFile(t, root, ".gitignore", "ignored.txt\n")
	writeTestFile(t, root, "tracked.txt", "tracked\n")
	writeTestFile(t, root, "untracked.txt", "untracked\n")
	writeTestFile(t, root, "ignored.txt", "ignored\n")
	runGit(t, root, "add", ".gitignore", "tracked.txt")

	files, err := WorkspaceFiles(context.Background(), root)
	if err != nil {
		t.Fatalf("WorkspaceFiles() error = %v", err)
	}
	if slices.Contains(files, filepath.Join(root, "ignored.txt")) {
		t.Fatalf("ignored file returned: %v", files)
	}
	for _, name := range []string{"tracked.txt", "untracked.txt"} {
		if !slices.Contains(files, filepath.Join(root, name)) {
			t.Fatalf("%s missing from %v", name, files)
		}
	}
}

func TestWorkspaceFilesRejectsTrackedSymlinkOutsideRoot(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init", "-q")
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "link.txt")

	files, err := WorkspaceFiles(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	_, accepted := confinedWorkspaceFile(root, link)
	if slices.Contains(files, link) || accepted {
		t.Fatalf("outside symlink accepted: %v", files)
	}
}

func TestConfinedWorkspaceFileRejectsIntermediateSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writeTestFile(t, outside, "secret.txt", "secret")
	if err := os.Symlink(outside, filepath.Join(root, "linkdir")); err != nil {
		t.Fatal(err)
	}
	if path, ok := confinedWorkspaceFile(root, filepath.Join(root, "linkdir", "secret.txt")); ok {
		t.Fatalf("intermediate symlink escape accepted as %s", path)
	}
}

func TestManagerCandidatesIncludeFilesChangedSinceSnapshot(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init", "-q")
	oldPath := filepath.Join(root, "old.txt")
	newPath := filepath.Join(root, "new.txt")
	writeTestFile(t, root, "old.txt", "old\n")
	runGit(t, root, "add", "old.txt")
	indexedAt := time.Now().UTC().Add(-time.Second)
	writeTestFile(t, root, "new.txt", "new\n")
	staleTime := indexedAt.Add(-time.Hour)
	if err := os.Chtimes(newPath, staleTime, staleTime); err != nil {
		t.Fatal(err)
	}
	indexPath := filepath.Join(t.TempDir(), "workspace.csearch")
	writeTestFile(t, filepath.Dir(indexPath), filepath.Base(indexPath), "placeholder")
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(t.TempDir(), "indexes.db")
	m := &Manager{root: root, indexPath: indexPath, dbPath: dbPath, runner: fakeRunner{files: []string{oldPath, outside}}}
	if err := m.ensureSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := m.setState(context.Background(), "ready", indexedAt, ""); err != nil {
		t.Fatal(err)
	}

	files, ok := m.Candidates(context.Background(), "new", false, false)
	if !ok || !slices.Contains(files, oldPath) || !slices.Contains(files, newPath) {
		t.Fatalf("Candidates() = %v, %v", files, ok)
	}
	if slices.Contains(files, outside) {
		t.Fatalf("outside candidate accepted: %v", files)
	}
}

func TestManagerRefreshBuildsAtomicallyAndRecordsGeneration(t *testing.T) {
	root := t.TempDir()
	dir := t.TempDir()
	m := &Manager{
		root: root, indexPath: filepath.Join(dir, "workspace.csearch"),
		dbPath: filepath.Join(dir, "indexes.db"), runner: writingRunner{},
	}
	if err := m.ensureSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	m.refreshAsync(ctx)

	// Poll for the async build to complete. Use a deadline longer than SQLite's
	// busy timeout (5 s) so a contended CI runner doesn't trigger a false timeout.
	deadline := time.Now().Add(8 * time.Second)
	var installedPath string
	for {
		state, stateErr := m.state(context.Background())
		if stateErr == nil && !state.IndexedAt.IsZero() {
			if _, err := os.Stat(state.IndexPath); err == nil {
				installedPath = state.IndexPath
				break
			}
		}
		// If the goroutine errored, fail fast with the last error.
		if stateErr == nil && state.IndexedAt.IsZero() {
			// Check if status indicates an error.
			db, dbErr := openIndexDB(m.dbPath)
			if dbErr == nil {
				var status, lastErr string
				_ = db.QueryRowContext(context.Background(),
					`SELECT status, last_error FROM workspace_indexes WHERE root = ?`, m.root).Scan(&status, &lastErr)
				_ = db.Close()
				if status == "error" {
					t.Fatalf("build failed: %s", lastErr)
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("index was not atomically installed")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if installedPath == m.indexPath {
		t.Fatalf("build did not install a generation-specific sidecar: %s", installedPath)
	}
	db, err := openIndexDB(m.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var status string
	var generation int
	if err := db.QueryRowContext(context.Background(), `SELECT status, generation FROM workspace_indexes WHERE root = ?`, root).Scan(&status, &generation); err != nil {
		t.Fatal(err)
	}
	if status != "ready" || generation != 1 {
		t.Fatalf("state = %q generation %d", status, generation)
	}
}

func TestStaleBuilderCannotCompleteNewerLease(t *testing.T) {
	dir := t.TempDir()
	m := &Manager{root: t.TempDir(), indexPath: filepath.Join(dir, "workspace.csearch"), dbPath: filepath.Join(dir, "indexes.db")}
	if err := m.ensureSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	oldStart := time.Now().UTC().Add(-2 * buildTimeout)
	claimed, err := m.claimBuild(context.Background(), oldStart, "build-a")
	if err != nil || !claimed {
		t.Fatalf("first claim = %v, %v", claimed, err)
	}
	newStart := time.Now().UTC()
	claimed, err = m.claimBuild(context.Background(), newStart, "build-b")
	if err != nil || !claimed {
		t.Fatalf("replacement claim = %v, %v", claimed, err)
	}
	if m.finishBuild(context.Background(), "build-a", "ready", oldStart, filepath.Join(dir, "a"), "") {
		t.Fatal("stale builder completed replacement lease")
	}
	pathB := filepath.Join(dir, "b")
	if !m.finishBuild(context.Background(), "build-b", "ready", newStart, pathB, "") {
		t.Fatal("current builder could not complete lease")
	}
	state, err := m.state(context.Background())
	if err != nil || state.IndexPath != pathB {
		t.Fatalf("state = %#v, %v", state, err)
	}
}

type fakeRunner struct {
	files []string
}

type writingRunner struct{}

func (writingRunner) Build(_ context.Context, _, target string) error {
	return os.WriteFile(target, []byte("index"), 0o600)
}

func (writingRunner) Candidates(context.Context, string, string, bool, bool) ([]string, error) {
	return nil, nil
}

func (f fakeRunner) Build(context.Context, string, string) error { return nil }
func (f fakeRunner) Candidates(context.Context, string, string, bool, bool) ([]string, error) {
	return slices.Clone(f.files), nil
}

func writeTestFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}
