// Package worktree owns archied's git operations: fresh clone per task,
// branch, commit as the bot identity, push, diff stats, cleanup. The
// daemon performs these deterministically — the model's shell tool never
// drives git.
package worktree

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Manager struct {
	// WorkDir is the root under which task worktrees are created.
	WorkDir string
	// Token authenticates clone/push over HTTPS via an askpass helper,
	// keeping it out of .git/config and process argv.
	Token    string
	BotUser  string
	BotEmail string
	// BaseURL overrides the forge host. Set from config [forge].host.
	// Empty falls back to https://github.com.
	BaseURL string

	cacheMu sync.Mutex
}

// Dir is the worktree path for a task.
func (m *Manager) Dir(owner, repo string, issue int) string {
	return filepath.Join(m.WorkDir, owner+"-"+repo, fmt.Sprintf("issue-%d", issue))
}

// askpass writes (once) a helper script that emits the token, so git
// authenticates without the token appearing in argv or .git/config.
func (m *Manager) askpass() (string, error) {
	path := filepath.Join(m.WorkDir, ".git-askpass")
	script := "#!/bin/sh\necho \"$ARCHIE_GIT_TOKEN\"\n"
	if err := os.MkdirAll(m.WorkDir, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		return "", err
	}
	return path, nil
}

func (m *Manager) git(ctx context.Context, dir string, args ...string) (string, error) {
	helper, err := m.askpass()
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(
		os.Environ(),
		"GIT_ASKPASS="+helper,
		"ARCHIE_GIT_TOKEN="+m.Token,
		"GIT_TERMINAL_PROMPT=0",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, out)
	}
	return string(out), nil
}

// Prepare creates a fresh clone for the task and checks out its branch.
// Any leftover worktree from a prior attempt is removed first.
// If the worktree is already prepared (the daemon may have done this
// before container acquisition), it returns the existing directory.
func (m *Manager) Prepare(ctx context.Context, owner, repo, base string, issue int, title, body, labels string) (dir, branch string, err error) {
	return m.prepare(ctx, owner, repo, base, issue, title, body, labels, "")
}

// PreparePersistent creates an isolated task worktree using a per-repo bare
// Git cache as an object reference. The cache avoids repeated full network
// clones while --dissociate keeps each task worktree independent so expiry
// cannot break active or parked tasks.
func (m *Manager) PreparePersistent(ctx context.Context, owner, repo, base string, issue int, title, body, labels string, ttl time.Duration) (dir, branch string, err error) {
	m.cacheMu.Lock()
	defer m.cacheMu.Unlock()

	cache, err := m.prepareCache(ctx, owner, repo, ttl)
	if err != nil {
		return "", "", err
	}
	return m.prepare(ctx, owner, repo, base, issue, title, body, labels, cache)
}

func (m *Manager) prepare(ctx context.Context, owner, repo, base string, issue int, title, body, labels, cache string) (dir, branch string, err error) {
	dir = m.Dir(owner, repo, issue)
	branch = archieBranch(issue, title, body, labels)

	// Already prepared (daemon cloned before container acquire).
	// Pull latest to avoid working on stale code.
	if st, statErr := os.Stat(filepath.Join(dir, ".git")); statErr == nil && st.IsDir() {
		if _, err := m.git(ctx, dir, "fetch", "origin"); err != nil {
			return "", "", err
		}
		if _, err := m.git(ctx, dir, "checkout", branch); err == nil {
			if _, err := m.git(ctx, dir, "reset", "--hard", "origin/"+base); err != nil {
				return "", "", err
			}
		}
		return dir, branch, nil
	}
	if err := os.RemoveAll(dir); err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return "", "", err
	}
	url := m.cloneURL(owner, repo)
	cloneArgs := []string{"clone", "--branch", base}
	if cache != "" {
		cloneArgs = append(cloneArgs, "--reference-if-able", cache, "--dissociate")
	}
	cloneArgs = append(cloneArgs, url, dir)
	if _, err := m.git(ctx, filepath.Dir(dir), cloneArgs...); err != nil {
		return "", "", err
	}
	for _, args := range [][]string{
		{"config", "user.name", m.BotUser},
		{"config", "user.email", m.BotEmail},
		{"checkout", "-b", branch},
	} {
		if _, err := m.git(ctx, dir, args...); err != nil {
			return "", "", err
		}
	}
	return dir, branch, nil
}

func (m *Manager) cacheDir(owner, repo string) string {
	key := url.PathEscape(owner) + "-" + url.PathEscape(repo) + ".git"
	return filepath.Join(m.WorkDir, ".repo-cache", key)
}

func (m *Manager) prepareCache(ctx context.Context, owner, repo string, ttl time.Duration) (string, error) {
	cache := m.cacheDir(owner, repo)
	if st, err := os.Stat(filepath.Join(cache, "HEAD")); err == nil && !st.IsDir() {
		if ttl > 0 {
			if cacheInfo, statErr := os.Stat(cache); statErr == nil && time.Since(cacheInfo.ModTime()) >= ttl {
				if err := os.RemoveAll(cache); err != nil {
					return "", fmt.Errorf("expire repo cache: %w", err)
				}
			}
		}
	}

	if st, err := os.Stat(filepath.Join(cache, "HEAD")); err == nil && !st.IsDir() {
		if _, err := m.git(ctx, cache, "fetch", "--prune", "origin"); err != nil {
			return "", fmt.Errorf("refresh repo cache: %w", err)
		}
		if err := os.Chtimes(cache, time.Now(), time.Now()); err != nil {
			return "", fmt.Errorf("touch repo cache: %w", err)
		}
		return cache, nil
	}

	if err := os.RemoveAll(cache); err != nil {
		return "", fmt.Errorf("reset repo cache: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(cache), 0o755); err != nil {
		return "", fmt.Errorf("create repo cache dir: %w", err)
	}
	if _, err := m.git(ctx, filepath.Dir(cache), "clone", "--mirror", m.cloneURL(owner, repo), cache); err != nil {
		return "", fmt.Errorf("create repo cache: %w", err)
	}
	if err := os.Chtimes(cache, time.Now(), time.Now()); err != nil {
		return "", fmt.Errorf("touch repo cache: %w", err)
	}
	return cache, nil
}

// CleanupExpiredCaches removes per-repo Git object caches not used within ttl.
// Task worktrees are dissociated clones and remain intact.
func (m *Manager) CleanupExpiredCaches(ttl time.Duration) (int, error) {
	if ttl <= 0 {
		return 0, nil
	}
	m.cacheMu.Lock()
	defer m.cacheMu.Unlock()

	root := filepath.Join(m.WorkDir, ".repo-cache")
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("list repo caches: %w", err)
	}
	var (
		removed int
		errs    []error
	)
	now := time.Now()
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(root, entry.Name())
		info, err := entry.Info()
		if err != nil {
			errs = append(errs, fmt.Errorf("stat repo cache %s: %w", entry.Name(), err))
			continue
		}
		if now.Sub(info.ModTime()) < ttl {
			continue
		}
		if err := os.RemoveAll(path); err != nil {
			errs = append(errs, fmt.Errorf("remove repo cache %s: %w", entry.Name(), err))
			continue
		}
		removed++
	}
	return removed, errors.Join(errs...)
}

// archieBranch builds a descriptive branch name. Uses conventional
// commit prefix (feat/fix/chore) from the issue title when possible,
// falls back to labels, then a plain "archie" prefix.
func archieBranch(issue int, title, body, labels string) string {
	prefix, rest := branchPrefix(title, body, labels), title
	// Strip conventional prefix from slug to avoid duplication.
	if p := strings.TrimSuffix(prefix, ""); p != "" {
		t := strings.ToLower(title)
		if strings.HasPrefix(t, p+":") {
			rest = strings.TrimSpace(title[len(p)+1:])
		}
	}
	slug := branchSlug(rest)
	if slug == "" {
		return fmt.Sprintf("%s/%d", prefix, issue)
	}
	return fmt.Sprintf("%s/%d-%s", prefix, issue, slug)
}

// branchPrefix derives a conventional-commit prefix from the issue
// labels, then falls back to "feat". Maps: bug→fix, feature→feat,
// enhancement→feat, docs→docs, chore→chore, test→test.
func branchPrefix(title, body, labels string) string {
	for l := range strings.SplitSeq(labels, ",") {
		switch strings.TrimSpace(strings.ToLower(l)) {
		case "bug":
			return "fix"
		case "feature", "enhancement":
			return "feat"
		case "docs":
			return "docs"
		case "chore":
			return "chore"
		case "test":
			return "test"
		case "refactor":
			return "refactor"
		}
	}
	return "feat"
}

// branchSlug converts a title to a kebab-case slug suitable for a git
// branch. Only keeps lowercase alphanumerics and hyphens, max 40 chars.
func branchSlug(title string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(title) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else if r == ' ' || r == '-' || r == '_' {
			b.WriteRune('-')
		}
		if b.Len() >= 40 {
			break
		}
	}
	slug := strings.Trim(b.String(), "-")
	// Collapse multiple hyphens.
	for strings.Contains(slug, "--") {
		slug = strings.ReplaceAll(slug, "--", "-")
	}
	return slug
}

func (m *Manager) cloneURL(owner, repo string) string {
	if m.BaseURL != "" {
		return fmt.Sprintf("%s/%s/%s.git", m.BaseURL, owner, repo)
	}
	return fmt.Sprintf("https://%s@github.com/%s/%s.git", m.BotUser, owner, repo)
}

// CommitAll stages everything and commits; returns false when the tree
// is clean (nothing to commit).
func (m *Manager) CommitAll(ctx context.Context, dir, message string) (bool, error) {
	if _, err := m.git(ctx, dir, "add", "-A"); err != nil {
		return false, err
	}
	status, err := m.git(ctx, dir, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(status) == "" {
		return false, nil
	}
	if _, err := m.git(ctx, dir, "commit", "-m", message); err != nil {
		return false, err
	}
	return true, nil
}

// Push publishes the branch.
func (m *Manager) Push(ctx context.Context, dir, branch string) error {
	_, err := m.git(ctx, dir, "push", "-u", "origin", branch)
	return err
}

// ChangedLines reports lines added+deleted vs the base branch — the
// input to the diff-size cap.
func (m *Manager) ChangedLines(ctx context.Context, dir, base string) (int, error) {
	out, err := m.git(ctx, dir, "diff", "--shortstat", "origin/"+base+"...HEAD")
	if err != nil {
		return 0, err
	}
	total := 0
	for f := range strings.FieldsSeq(out) {
		var n int
		if _, err := fmt.Sscanf(f, "%d", &n); err == nil {
			total += n
		}
	}
	// The first field is the file count; subtract it back out.
	if strings.TrimSpace(out) == "" {
		return 0, nil
	}
	var files int
	if _, err := fmt.Sscanf(strings.TrimSpace(out), "%d", &files); err != nil {
		return 0, fmt.Errorf("parse git diff shortstat %q: %w", strings.TrimSpace(out), err)
	}
	return total - files, nil
}

// Diff returns the unified diff of all committed changes against base.
func (m *Manager) Diff(ctx context.Context, dir, base string) (string, error) {
	return m.git(ctx, dir, "diff", "origin/"+base+"...HEAD")
}

// ChangedFiles lists the repo-relative paths changed against base.
func (m *Manager) ChangedFiles(ctx context.Context, dir, base string) ([]string, error) {
	out, err := m.git(ctx, dir, "diff", "--name-only", "origin/"+base+"...HEAD")
	if err != nil {
		return nil, err
	}
	var files []string
	for line := range strings.SplitSeq(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			files = append(files, line)
		}
	}
	return files, nil
}

// Cleanup removes a task's worktree (merged/rejected); parked worktrees
// are kept for post-mortems.
func (m *Manager) Cleanup(owner, repo string, issue int) error {
	return os.RemoveAll(m.Dir(owner, repo, issue))
}
