package main

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"strings"
)

// gitRunner runs a git invocation and returns its combined output. Injected so
// the safe.directory configuration can be tested without a git binary.
type gitRunner func(ctx context.Context, args ...string) ([]byte, error)

// runGit is the production gitRunner.
func runGit(ctx context.Context, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, "git", args...).CombinedOutput()
}

// markWorktreeSafe adds mountDir to git's global safe.directory list.
//
// The daemon bind-mounts the task worktree into the container from the host,
// so the directory is owned by a UID the container user is not. Git refuses to
// operate in a repository it does not own ("detected dubious ownership"), and
// the Go toolchain shells out to git for VCS stamping during build/test/vet --
// so without this every gate fails before the agent does any real work.
//
// Best-effort by design: git may be absent from a minimal image, and an agent
// working a repo with no gate commands never needs it. A failure is logged and
// the agent continues. Returns true only when git accepted the configuration.
//
// The mount is checked first so that running archie-agent directly on a host
// (shared queue-group mode, no container) does not write to the operator's own
// ~/.gitconfig for a path that was never mounted.
func markWorktreeSafe(ctx context.Context, mountDir string, run gitRunner, log *slog.Logger) bool {
	info, err := os.Stat(mountDir)
	if err != nil || !info.IsDir() {
		log.Debug("git safe.directory skipped: no worktree mounted", "dir", mountDir)
		return false
	}

	out, err := run(ctx, "config", "--global", "--add", "safe.directory", mountDir)
	if err != nil {
		log.Warn("git safe.directory config failed  --  gates that shell out to git may fail",
			"dir", mountDir,
			"err", err,
			"output", strings.TrimSpace(string(out)),
		)
		return false
	}

	log.Info("git safe.directory configured", "dir", mountDir)
	return true
}
