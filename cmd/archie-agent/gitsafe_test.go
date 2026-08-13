package main

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

			got := markWorktreeSafe(context.Background(), dir, run, log)

			if got != tc.want {
				t.Fatalf("markWorktreeSafe() = %v, want %v", got, tc.want)
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

	if got := markWorktreeSafe(context.Background(), file, run, slog.New(slog.DiscardHandler)); got {
		t.Fatal("markWorktreeSafe() = true for a non-directory mount path, want false")
	}
	if invoked {
		t.Fatal("git was invoked for a non-directory mount path")
	}
}
