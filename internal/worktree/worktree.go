// Package worktree owns archied's git operations: fresh clone per task,
// branch, commit as the bot identity, push, diff stats, cleanup. The
// daemon performs these deterministically  --  the model's shell tool never
// drives git.
//
// Implemented with go-git rather than by shelling out to the git binary.
// Two consequences are worth knowing:
//
//   - The forge token never leaves the process. The previous
//     implementation wrote a GIT_ASKPASS helper script and exported the
//     token into every child environment; authentication is now an
//     in-process http.BasicAuth value.
//   - There is no per-repo object cache. go-git can clone with shared
//     alternates but has no equivalent of `--dissociate`, so a cached
//     bare repo would remain a live dependency of every worktree built
//     from it and expiring one would corrupt running tasks. Each task
//     gets an independent full clone instead.
package worktree

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/go-git/go-git/v6"
	gitconfig "github.com/go-git/go-git/v6/config"
	"github.com/go-git/go-git/v6/plumbing"
	gitclient "github.com/go-git/go-git/v6/plumbing/client"
	"github.com/go-git/go-git/v6/plumbing/filemode"
	"github.com/go-git/go-git/v6/plumbing/object"
	githttp "github.com/go-git/go-git/v6/plumbing/transport/http"
)

// preparedSentinel marks a worktree as fully cloned, branched and
// configured. Its presence is what makes Prepare idempotent: the daemon
// may prepare a worktree before the task container is acquired.
//
// It lives inside .git deliberately. The previous implementation kept it
// in the working tree and hid it via .git/info/exclude, which worked
// because `git add -A` honours that file -- go-git's Add does NOT, so the
// sentinel was committed and pushed onto every task branch. Nothing under
// .git can ever be tracked, so placing it here removes the failure mode
// rather than papering over it.
const preparedSentinel = ".git/archie-prepared"

type Manager struct {
	// WorkDir is the root under which task worktrees are created.
	WorkDir string
	// Token authenticates clone and push over HTTPS. It is held in memory
	// and passed to go-git directly, never written to .git/config, an
	// askpass helper, or a child process environment.
	Token    string
	BotUser  string
	BotEmail string
	// BaseURL overrides the forge host. Set from config [forge].host.
	// Empty falls back to https://github.com.
	BaseURL string
}

// Dir is the worktree path for a task.
func (m *Manager) Dir(owner, repo string, issue int) string {
	ownerKey := base64.RawURLEncoding.EncodeToString([]byte(owner))
	repoKey := base64.RawURLEncoding.EncodeToString([]byte(repo))
	return filepath.Join(m.WorkDir, ownerKey, repoKey, fmt.Sprintf("issue-%d", issue))
}

// ValidCoordinates reports whether owner, repo, and issue can safely identify
// a task worktree without being interpreted as filesystem navigation.
func ValidCoordinates(owner, repo string, issue int) bool {
	return owner != "" && repo != "" && issue > 0 &&
		filepath.Base(owner) == owner && filepath.Base(repo) == repo &&
		owner != "." && owner != ".." && repo != "." && repo != ".."
}

// auth builds the HTTP credential for clone, fetch and push.
//
// Forges accept a personal access token as the password with any
// non-empty username, so the bot's own name is used. A nil return means
// no token was configured and go-git attempts an anonymous request,
// which is correct for a public read.
func (m *Manager) auth() []gitclient.Option {
	if m.Token == "" {
		return nil
	}
	username := m.BotUser
	if username == "" {
		// Any non-empty username is accepted alongside a token; this one
		// makes the origin obvious in a forge's audit log.
		username = "archie"
	}
	return []gitclient.Option{
		gitclient.WithHTTPAuth(&githttp.BasicAuth{Username: username, Password: m.Token}),
	}
}

// signature is the author and committer identity for bot commits.
func (m *Manager) signature() *object.Signature {
	return &object.Signature{Name: m.BotUser, Email: m.BotEmail, When: time.Now()}
}

// Prepare creates a fresh clone for the task and checks out its branch.
// Any leftover worktree from a prior attempt is removed first. If the
// worktree is already prepared, it is refreshed and reused.
func (m *Manager) Prepare(
	ctx context.Context,
	owner, repo, base string,
	issue int,
	title, body, labels string,
) (dir, branch string, err error) {
	if !ValidCoordinates(owner, repo, issue) {
		return "", "", fmt.Errorf("invalid worktree coordinates")
	}
	dir = m.Dir(owner, repo, issue)
	branch = archieBranch(issue, title, body, labels)

	if _, statErr := os.Stat(filepath.Join(dir, preparedSentinel)); statErr == nil {
		if err := m.refresh(ctx, dir, base, branch); err != nil {
			return "", "", err
		}
		return dir, branch, nil
	}
	if migrated, migrateErr := m.migrateLegacy(owner, repo, issue, dir); migrateErr != nil {
		return "", "", migrateErr
	} else if migrated {
		if err := m.refresh(ctx, dir, base, branch); err != nil {
			return "", "", err
		}
		return dir, branch, nil
	}

	if err := os.RemoveAll(dir); err != nil {
		return "", "", fmt.Errorf("clear stale worktree: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return "", "", fmt.Errorf("create worktree parent: %w", err)
	}

	r, err := git.PlainCloneContext(ctx, dir, &git.CloneOptions{
		URL:           m.cloneURL(owner, repo),
		ClientOptions: m.auth(),
		ReferenceName: plumbing.NewBranchReferenceName(base),
	})
	if err != nil {
		return "", "", fmt.Errorf("clone %s/%s: %w", owner, repo, err)
	}
	if err := m.setIdentity(r); err != nil {
		return "", "", err
	}

	wt, err := r.Worktree()
	if err != nil {
		return "", "", fmt.Errorf("open worktree: %w", err)
	}
	if err := wt.Checkout(&git.CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName(branch),
		Create: true,
	}); err != nil {
		return "", "", fmt.Errorf("create branch %s: %w", branch, err)
	}

	if err := writeSentinel(dir); err != nil {
		return "", "", err
	}
	return dir, branch, nil
}

func (m *Manager) legacyDir(owner, repo string, issue int) string {
	return filepath.Join(m.WorkDir, owner+"-"+repo, fmt.Sprintf("issue-%d", issue))
}

// migrateLegacy preserves worktrees created before repository coordinates
// were encoded as separate path components. The old flattened key was
// ambiguous, so migration is allowed only when the clone's origin exactly
// matches the requested repository; an uncertain directory is left untouched.
func (m *Manager) migrateLegacy(owner, repo string, issue int, dir string) (bool, error) {
	legacy := m.legacyDir(owner, repo, issue)
	if _, err := os.Stat(filepath.Join(legacy, preparedSentinel)); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("inspect legacy worktree: %w", err)
	}
	if !repositoryUsesURL(legacy, m.cloneURL(owner, repo)) {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return false, fmt.Errorf("create migrated worktree parent: %w", err)
	}
	if err := os.Rename(legacy, dir); err != nil {
		return false, fmt.Errorf("migrate legacy worktree: %w", err)
	}
	_ = os.Remove(filepath.Dir(legacy))
	return true, nil
}

func repositoryUsesURL(dir, want string) bool {
	r, err := git.PlainOpen(dir)
	if err != nil {
		return false
	}
	cfg, err := r.Config()
	if err != nil {
		return false
	}
	remote, ok := cfg.Remotes[git.DefaultRemoteName]
	if !ok {
		return false
	}
	return slices.Contains(remote.URLs, want)
}

// refresh brings an already-prepared worktree back in line with the
// remote: fetch, move onto the task branch, and reset it to the base.
func (m *Manager) refresh(ctx context.Context, dir, base, branch string) error {
	r, err := git.PlainOpen(dir)
	if err != nil {
		return fmt.Errorf("open prepared worktree: %w", err)
	}
	if err := r.FetchContext(ctx, &git.FetchOptions{
		RemoteName:    git.DefaultRemoteName,
		ClientOptions: m.auth(),
	}); err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
		return fmt.Errorf("fetch origin: %w", err)
	}

	wt, err := r.Worktree()
	if err != nil {
		return fmt.Errorf("open worktree: %w", err)
	}
	// Force discards whatever the working tree looks like right now: an
	// interrupted prior attempt (killed container, a stage that edits
	// files before ever reaching commit) can leave local modifications
	// that make go-git's default MergeReset refuse to reconcile them, even
	// onto a commit HEAD is already on. That refusal used to surface as
	// "branch already exists" from the fallback below, which mis-assumed
	// any checkout failure meant the branch didn't exist yet. Force is
	// safe here regardless of cause: the hard reset to base a few lines
	// down is about to discard the working tree anyway.
	ref := plumbing.NewBranchReferenceName(branch)
	if err := wt.Checkout(&git.CheckoutOptions{Branch: ref, Force: true}); err != nil {
		// The branch does not exist yet in this worktree.
		if err := wt.Checkout(&git.CheckoutOptions{Branch: ref, Create: true, Force: true}); err != nil {
			return fmt.Errorf("checkout branch %s: %w", branch, err)
		}
	}

	// Reset onto the freshly fetched base. This is a prepared clone, so a
	// missing base is configuration or repository drift and must fail closed.
	baseHash, err := resolveBase(r, base)
	if err != nil {
		return err
	}
	if err := wt.Reset(&git.ResetOptions{Commit: baseHash, Mode: git.HardReset}); err != nil {
		return fmt.Errorf("reset to %s: %w", remoteBase(base), err)
	}
	if err := cleanUntracked(r, dir); err != nil {
		return fmt.Errorf("clean abandoned worktree files: %w", err)
	}
	return nil
}

func cleanUntracked(r *git.Repository, dir string) error {
	head, err := r.Head()
	if err != nil {
		return fmt.Errorf("resolve HEAD: %w", err)
	}
	commit, err := r.CommitObject(head.Hash())
	if err != nil {
		return fmt.Errorf("load HEAD commit: %w", err)
	}
	tree, err := commit.Tree()
	if err != nil {
		return fmt.Errorf("load HEAD tree: %w", err)
	}
	files := make(map[string]struct{})
	dirs := make(map[string]struct{})
	opaqueDirs := make(map[string]struct{})
	if err := collectTrackedPaths(tree, "", files, dirs, opaqueDirs); err != nil {
		return err
	}
	return filepath.WalkDir(dir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == dir {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == ".git" {
			return filepath.SkipDir
		}
		if entry.IsDir() {
			if _, ok := opaqueDirs[rel]; ok {
				return filepath.SkipDir
			}
			if _, ok := dirs[rel]; ok {
				return nil
			}
			if err := os.RemoveAll(path); err != nil {
				return err
			}
			return filepath.SkipDir
		}
		if _, ok := files[rel]; ok {
			return nil
		}
		return os.Remove(path)
	})
}

func collectTrackedPaths(tree *object.Tree, prefix string, files, dirs, opaqueDirs map[string]struct{}) error {
	for _, entry := range tree.Entries {
		path := filepath.ToSlash(filepath.Join(prefix, entry.Name))
		switch entry.Mode {
		case filemode.Dir:
			dirs[path] = struct{}{}
			child, err := tree.Tree(entry.Name)
			if err != nil {
				return fmt.Errorf("load tracked directory %s: %w", path, err)
			}
			if err := collectTrackedPaths(child, path, files, dirs, opaqueDirs); err != nil {
				return err
			}
		case filemode.Submodule:
			opaqueDirs[path] = struct{}{}
		default:
			files[path] = struct{}{}
		}
	}
	return nil
}

// setIdentity records the bot identity in the clone's own config so that
// anything inspecting the worktree sees the same author go-git commits
// with.
func (m *Manager) setIdentity(r *git.Repository) error {
	cfg, err := r.Config()
	if err != nil {
		return fmt.Errorf("read repo config: %w", err)
	}
	cfg.User.Name = m.BotUser
	cfg.User.Email = m.BotEmail
	// Pin signing off for this repository.
	//
	// go-git honours the host's global git config, and it cannot sign:
	// it has no gpg fallback, only an ObjectSigner plugin. A developer
	// or CI image with commit.gpgSign=true set globally would therefore
	// make every bot commit fail with "cannot auto-sign commit". The
	// daemon's behaviour must not depend on the ambient git config of
	// whatever machine it happens to run on.
	cfg.Raw.Section("commit").SetOption("gpgsign", "false")
	if err := r.SetConfig(cfg); err != nil {
		return fmt.Errorf("write repo config: %w", err)
	}
	return nil
}

// writeSentinel marks the worktree as fully prepared.
func writeSentinel(dir string) error {
	if err := os.WriteFile(filepath.Join(dir, preparedSentinel), nil, 0o644); err != nil {
		return fmt.Errorf("write prepared sentinel: %w", err)
	}
	return nil
}

// CommitAll stages everything and commits; returns false when the tree
// is clean (nothing to commit).
func (m *Manager) CommitAll(_ context.Context, dir, message string) (bool, error) {
	r, err := git.PlainOpen(dir)
	if err != nil {
		return false, fmt.Errorf("open worktree: %w", err)
	}
	wt, err := r.Worktree()
	if err != nil {
		return false, fmt.Errorf("open worktree: %w", err)
	}
	if err := wt.AddWithOptions(&git.AddOptions{All: true}); err != nil {
		return false, fmt.Errorf("stage changes: %w", err)
	}
	status, err := wt.Status()
	if err != nil {
		return false, fmt.Errorf("read status: %w", err)
	}
	if status.IsClean() {
		return false, nil
	}
	sig := m.signature()
	if _, err := wt.Commit(message, &git.CommitOptions{Author: sig, Committer: sig}); err != nil {
		return false, fmt.Errorf("commit: %w", err)
	}
	return true, nil
}

// Push publishes the branch and sets it to track origin.
func (m *Manager) Push(ctx context.Context, dir, branch string) error {
	r, err := git.PlainOpen(dir)
	if err != nil {
		return fmt.Errorf("open worktree: %w", err)
	}
	if m.Token == "" && remoteUsesHTTP(r) {
		return fmt.Errorf("push %s: no forge credential configured; "+
			"set [forge].token (or the identity's forge.token) so archied can authenticate", branch)
	}
	ref := plumbing.NewBranchReferenceName(branch)
	spec := gitconfig.RefSpec(fmt.Sprintf("%s:%s", ref, ref))
	if err := r.PushContext(ctx, &git.PushOptions{
		RemoteName:    git.DefaultRemoteName,
		RefSpecs:      []gitconfig.RefSpec{spec},
		ClientOptions: m.auth(),
	}); err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
		return fmt.Errorf("push %s: %w", branch, err)
	}
	// Record the upstream so the branch tracks origin, matching what
	// `git push -u` left behind previously.
	cfg, err := r.Config()
	if err != nil {
		return fmt.Errorf("read repo config: %w", err)
	}
	if cfg.Branches == nil {
		cfg.Branches = map[string]*gitconfig.Branch{}
	}
	cfg.Branches[branch] = &gitconfig.Branch{
		Name:   branch,
		Remote: git.DefaultRemoteName,
		Merge:  ref,
	}
	if err := r.SetConfig(cfg); err != nil {
		return fmt.Errorf("record branch upstream: %w", err)
	}
	return nil
}

// remoteUsesHTTP reports whether the repository's origin remote is
// configured over HTTP(S), the only transport that requires a token.
func remoteUsesHTTP(r *git.Repository) bool {
	cfg, err := r.Config()
	if err != nil {
		return false
	}
	remote, ok := cfg.Remotes[git.DefaultRemoteName]
	if !ok {
		return false
	}
	for _, u := range remote.URLs {
		if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
			return true
		}
	}
	return false
}

// patch computes the diff of the task branch against base, from their
// merge base  --  the three-dot semantics of `git diff origin/base...HEAD`.
// Diffing against the remote tip instead would attribute every commit
// landed on base since the branch started to this task.
func (m *Manager) patch(ctx context.Context, dir, base string) (*object.Patch, error) {
	r, err := git.PlainOpen(dir)
	if err != nil {
		return nil, fmt.Errorf("open worktree: %w", err)
	}
	baseHash, err := resolveBase(r, base)
	if err != nil {
		return nil, err
	}
	head, err := r.Head()
	if err != nil {
		return nil, fmt.Errorf("resolve HEAD: %w", err)
	}
	baseCommit, err := r.CommitObject(baseHash)
	if err != nil {
		return nil, fmt.Errorf("load base commit: %w", err)
	}
	headCommit, err := r.CommitObject(head.Hash())
	if err != nil {
		return nil, fmt.Errorf("load head commit: %w", err)
	}
	bases, err := baseCommit.MergeBase(headCommit)
	if err != nil {
		return nil, fmt.Errorf("find merge base for %s...HEAD: %w", remoteBase(base), err)
	}
	if len(bases) == 0 {
		return nil, fmt.Errorf("find merge base for %s...HEAD: no common ancestor", remoteBase(base))
	}
	p, err := bases[0].PatchContext(ctx, headCommit)
	if err != nil {
		return nil, fmt.Errorf("diff %s...HEAD: %w", remoteBase(base), err)
	}
	return p, nil
}

// ChangedLines reports lines added+deleted vs the base branch  --  the
// input to the diff-size cap.
func (m *Manager) ChangedLines(ctx context.Context, dir, base string) (int, error) {
	p, err := m.patch(ctx, dir, base)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, stat := range p.Stats() {
		total += stat.Addition + stat.Deletion
	}
	return total, nil
}

// Diff returns the unified diff of all committed changes against base.
func (m *Manager) Diff(ctx context.Context, dir, base string) (string, error) {
	p, err := m.patch(ctx, dir, base)
	if err != nil {
		return "", err
	}
	return p.String(), nil
}

// ChangedFiles lists the repo-relative paths changed against base.
// A rename reports its destination; a deletion reports the removed path.
func (m *Manager) ChangedFiles(ctx context.Context, dir, base string) ([]string, error) {
	p, err := m.patch(ctx, dir, base)
	if err != nil {
		return nil, err
	}
	var (
		files []string
		seen  = map[string]struct{}{}
	)
	for _, fp := range p.FilePatches() {
		fromFile, toFile := fp.Files()
		name := ""
		switch {
		case toFile != nil:
			name = toFile.Path()
		case fromFile != nil:
			name = fromFile.Path()
		}
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		files = append(files, name)
	}
	return files, nil
}

// Cleanup removes a task's worktree (merged/rejected); parked worktrees
// are kept for post-mortems.
func (m *Manager) Cleanup(owner, repo string, issue int) error {
	if !ValidCoordinates(owner, repo, issue) {
		return fmt.Errorf("invalid worktree coordinates")
	}
	if err := os.RemoveAll(m.Dir(owner, repo, issue)); err != nil {
		return err
	}
	legacy := m.legacyDir(owner, repo, issue)
	if repositoryUsesURL(legacy, m.cloneURL(owner, repo)) {
		if err := os.RemoveAll(legacy); err != nil {
			return err
		}
		_ = os.Remove(filepath.Dir(legacy))
	}
	return nil
}

func remoteBase(base string) string {
	return "refs/remotes/" + git.DefaultRemoteName + "/" + base
}

// resolveBase finds the commit the task branch is compared against.
//
// The fully-qualified remote-tracking ref is tried first because that is
// what a clone produces and it cannot be shadowed. The bare "origin/<base>"
// revision is tried second so that resolution stays as permissive as
// `git diff origin/<base>...HEAD` was  --  git resolves that short form
// against refs/heads too, and repositories built that way exist.
func resolveBase(r *git.Repository, base string) (plumbing.Hash, error) {
	candidates := []plumbing.Revision{
		plumbing.Revision(remoteBase(base)),
		plumbing.Revision(git.DefaultRemoteName + "/" + base),
	}
	var err error
	for _, rev := range candidates {
		var hash *plumbing.Hash
		if hash, err = r.ResolveRevision(rev); err == nil {
			return *hash, nil
		}
	}
	return plumbing.ZeroHash, fmt.Errorf("resolve %s: %w", remoteBase(base), err)
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
		if b.Len() >= 60 {
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
	return fmt.Sprintf("https://github.com/%s/%s.git", owner, repo)
}
