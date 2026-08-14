package agentgit

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

// MarkSafe adds mountDir to git's global safe.directory list on a best-effort basis.
func MarkSafe(ctx context.Context, mountDir string, log *slog.Logger) bool {
	return markSafe(ctx, mountDir, runGit, log)
}

// markSafe adds mountDir to git's global safe.directory list.
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
// The guard is existence, not mount-ness: os.Stat confirms mountDir exists and
// is a directory, which is what stops archie-agent running directly on a host
// (shared queue-group mode, no container) from writing to the operator's own
// ~/.gitconfig for a path that was never created at all. It does not detect a
// stale directory that happens to exist at the same path without actually
// being a bind mount -- a real mount check would need platform-specific
// parsing (e.g. /proc/self/mountinfo on Linux) that this deliberately does
// without. The failure mode is inert: git only rejects operations in a
// directory that both exists at this path and is a real repository, and
// safe.directory entries for a stale non-repo path do nothing.
func markSafe(ctx context.Context, mountDir string, run gitRunner, log *slog.Logger) bool {
	info, err := os.Stat(mountDir)
	if err != nil || !info.IsDir() {
		log.Debug("git safe.directory skipped: mountDir does not exist", "dir", mountDir)
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
