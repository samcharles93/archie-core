package agentgit

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestMarkWorktreeSafe(t *testing.T) {
	log := slog.New(slog.DiscardHandler)

	present := func(t *testing.T) string { t.Helper(); return t.TempDir() }
	absent := func(t *testing.T) string { t.Helper(); return t.TempDir() + "/never-mounted" }

	tests := []struct {
		name     string
		mountDir func(t *testing.T) string
		runErr   error
		want     bool
		wantRuns int
	}{
		{
			name:     "configures git when the worktree is mounted",
			mountDir: present,
			want:     true,
			wantRuns: 1,
		},
		{
			name:     "git failure is best-effort",
			mountDir: present,
			runErr:   errors.New("exit status 1"),
			want:     false,
			wantRuns: 1,
		},
		{
			name:     "git missing is best-effort",
			mountDir: present,
			runErr:   errors.New(`exec: "git": executable file not found in $PATH`),
			want:     false,
			wantRuns: 1,
		},
		{
			name:     "no mounted worktree leaves git config untouched",
			mountDir: absent,
			want:     false,
			wantRuns: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := tc.mountDir(t)
			var runs [][]string
			run := func(_ context.Context, args ...string) ([]byte, error) {
				runs = append(runs, args)
				return []byte("output"), tc.runErr
			}

			got := markSafe(context.Background(), dir, run, log)

			if got != tc.want {
				t.Fatalf("markSafe() = %v, want %v", got, tc.want)
			}
			if len(runs) != tc.wantRuns {
				t.Fatalf("git invoked %d times, want %d (%v)", len(runs), tc.wantRuns, runs)
			}
			if tc.wantRuns == 0 {
				return
			}
			wantArgs := []string{"config", "--global", "--add", "safe.directory", dir}
			if !reflect.DeepEqual(runs[0], wantArgs) {
				t.Fatalf("git args = %v, want %v", runs[0], wantArgs)
			}
		})
	}
}

// The guard markSafe applies is existence, not mount-ness: a directory that
// merely exists at mountDir -- a stale leftover from a prior run, never
// actually bind-mounted this time -- still satisfies os.Stat and gets
// configured. This is the documented, accepted behavior (see markSafe's
// doc comment): a real mount check would need platform-specific mountinfo
// parsing, and the resulting stale safe.directory entry is inert unless a
// git repository later exists at that exact path. This test pins the
// current contract so a future change to it is a deliberate decision, not
// a silent regression either way.
func TestMarkWorktreeSafeAcceptsAnExistingButUnmountedDirectory(t *testing.T) {
	dir := t.TempDir() // exists on disk; nothing bind-mounted it here

	var runs [][]string
	run := func(_ context.Context, args ...string) ([]byte, error) {
		runs = append(runs, args)
		return []byte("output"), nil
	}

	if got := markSafe(context.Background(), dir, run, slog.New(slog.DiscardHandler)); !got {
		t.Fatal("markSafe() = false for an existing (if unmounted) directory, want true per the documented existence-based guard")
	}
	if len(runs) != 1 {
		t.Fatalf("git invoked %d times, want 1", len(runs))
	}
}

// A mount path that is a file, not a directory, is not a worktree: git must
// not be reconfigured for it.
func TestMarkWorktreeSafeIgnoresNonDirectory(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "worktree")
	if err := os.WriteFile(file, []byte("not a worktree"), 0o600); err != nil {
		t.Fatal(err)
	}

	invoked := false
	run := func(_ context.Context, _ ...string) ([]byte, error) {
		invoked = true
		return nil, nil
	}

	if got := markSafe(context.Background(), file, run, slog.New(slog.DiscardHandler)); got {
		t.Fatal("markSafe() = true for a non-directory mount path, want false")
	}
	if invoked {
		t.Fatal("git was invoked for a non-directory mount path")
	}
}
