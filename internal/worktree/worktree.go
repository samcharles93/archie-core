// Package worktree owns archied's git operations: fresh clone per task,
// branch, commit as the bot identity, push, diff stats, cleanup. The
// daemon performs these deterministically — the model's shell tool never
// drives git.
package worktree

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
func (m *Manager) Prepare(ctx context.Context, owner, repo, base string, issue int, title string) (dir, branch string, err error) {
	dir = m.Dir(owner, repo, issue)
	branch = archieBranch(issue, title)

	// Already prepared (daemon cloned before container acquire).
	if st, statErr := os.Stat(filepath.Join(dir, ".git")); statErr == nil && st.IsDir() {
		return dir, branch, nil
	}
	if err := os.RemoveAll(dir); err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return "", "", err
	}
	url := m.cloneURL(owner, repo)
	if _, err := m.git(ctx, filepath.Dir(dir), "clone", "--branch", base, url, dir); err != nil {
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

// archieBranch builds a descriptive branch name from issue number and
// a slug of the title. Example: "archie/62-clear-finished-button".
func archieBranch(issue int, title string) string {
	slug := branchSlug(title)
	if slug == "" {
		return fmt.Sprintf("archie/%d", issue)
	}
	return fmt.Sprintf("archie/%d-%s", issue, slug)
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
