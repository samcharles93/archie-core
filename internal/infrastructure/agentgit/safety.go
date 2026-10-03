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

// markSafe adds mountDir to git's global safe.directory when it is an
// existing directory. Failures are logged. Returns true on success.
func markSafe(ctx context.Context, mountDir string, run gitRunner, log *slog.Logger) bool {
	info, err := os.Stat(mountDir)
	if err != nil || !info.IsDir() {
		log.Debug("git safe.directory skipped: mountDir does not exist", "dir", mountDir)
		return false
	}

	out, err := run(ctx, "config", "--global", "--add", "safe.directory", mountDir)
	if err != nil {
		log.Warn(
			"git safe.directory config failed  --  gates that shell out to git may fail",
			"dir", mountDir,
			"err", err,
			"output", strings.TrimSpace(string(out)),
		)
		return false
	}

	log.Info("git safe.directory configured", "dir", mountDir)
	return true
}
