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
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/go-git/go-billy/v6"
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
	// Log optionally receives diagnostics and non-fatal warnings from
	// worktree operations.
	Log *slog.Logger
}

func (m *Manager) logger() *slog.Logger {
	if m != nil && m.Log != nil {
		return m.Log
	}
	return slog.Default()
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
	branch = archieBranch(issue, title, labels)

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

	// Reset onto the freshly fetched base. This is a prepared clone, so a
	// missing base is configuration or repository drift and must fail closed
	// -- and it is resolved before the first mutation, so a failure here
	// leaves the worktree as it was rather than half-refreshed.
	baseHash, err := resolveBase(r, base)
	if err != nil {
		return err
	}
	return resetOnto(r, dir, branch, baseHash, remoteBase(base))
}

// Resume re-syncs an already-prepared worktree onto its branch's remote tip
// without resetting to base, so the remediate workflow can continue work on
// the PR branch the implement run already pushed. It is refresh's complement:
// refresh resets to origin/<base> for a fresh run; Resume resets to
// origin/<branch> so the committed PR work survives.
func (m *Manager) Resume(ctx context.Context, dir, branch string) error {
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

	tip, err := r.ResolveRevision(plumbing.Revision(remoteBase(branch)))
	if err != nil {
		return fmt.Errorf("resolve remote tip of %s: %w", branch, err)
	}
	return resetOnto(r, dir, branch, *tip, remoteBase(branch))
}

// resetOnto discards the worktree's current contents and points branch at
// commit, leaving a clean tree that matches commit.
//
// The ORDER here is the substance of the function, and it is deliberately the
// opposite of the checkout-then-reset-then-clean it replaced. A previous
// attempt -- an interrupted stage, a killed container, or a build run inside
// the root sandbox -- can leave a path whose type contradicts the tree being
// reset onto as well as untracked output beside it. go-git's reset cannot
// write a file where a directory sits, nor create a directory through a file;
// it failed with `openat <path>: is a directory` / `not a directory` before it
// could apply anything, and the Create-fallback that used to follow reported
// that as a branch that already existed. Clearing the worktree against the
// TARGET tree first removes exactly those conflicts -- an on-disk directory the
// tree declares as a file is untracked, and so is a file the tree declares as a
// directory -- so the reset that follows has nothing left to collide with.
func resetOnto(r *git.Repository, dir, branch string, commit plumbing.Hash, label string) error {
	target, err := commitTree(r, commit)
	if err != nil {
		return err
	}
	if err := cleanUntracked(dir, target); err != nil {
		return fmt.Errorf("clear abandoned worktree files: %w", err)
	}

	wt, err := r.Worktree()
	if err != nil {
		return fmt.Errorf("open worktree: %w", err)
	}
	if err := checkOutBranch(r, wt, branch, commit); err != nil {
		return err
	}
	if err := wt.Reset(&git.ResetOptions{Commit: commit, Mode: git.HardReset}); err != nil {
		return fmt.Errorf("reset to %s: %w", label, err)
	}
	return nil
}

// checkOutBranch switches the worktree to branch, creating it at commit when it
// does not exist yet.
//
// Whether the branch exists is resolved from the reference store rather than
// inferred from a failed checkout. A checkout fails for reasons that have
// nothing to do with a missing branch -- most often a path in the working tree
// the tree cannot be written over -- and treating any such failure as "the
// branch does not exist yet" produced `a branch named "refs/heads/<branch>"
// already exists`: it named a branch that was never the problem and hid the
// real cause from the park reason an operator reads to decide what to do.
//
// Creating from an explicit commit rather than from HEAD matters too: a branch
// removed while HEAD still pointed at it (a renamed branch, a half-finished
// refresh) made go-git's create path read an unresolvable HEAD and fail with
// "reference not found" instead of creating the branch.
func checkOutBranch(r *git.Repository, wt *git.Worktree, branch string, commit plumbing.Hash) error {
	ref := plumbing.NewBranchReferenceName(branch)
	opts := &git.CheckoutOptions{Branch: ref, Force: true}
	switch _, err := r.Reference(ref, false); {
	case err == nil:
	case errors.Is(err, plumbing.ErrReferenceNotFound):
		opts.Create = true
		opts.Hash = commit
	default:
		return fmt.Errorf("resolve branch %s: %w", branch, err)
	}
	if err := wt.Checkout(opts); err != nil {
		return fmt.Errorf("checkout branch %s: %w", branch, err)
	}
	return nil
}

// commitTree loads the tree of one commit, for the caller that has to know
// which paths the worktree is about to be reset onto.
func commitTree(r *git.Repository, commit plumbing.Hash) (*object.Tree, error) {
	c, err := r.CommitObject(commit)
	if err != nil {
		return nil, fmt.Errorf("load commit %s: %w", commit, err)
	}
	tree, err := c.Tree()
	if err != nil {
		return nil, fmt.Errorf("load tree of %s: %w", commit, err)
	}
	return tree, nil
}

// cleanUntracked removes every path under dir that tree does not contain:
// untracked files, whole directories of build output, and any path whose type
// on disk contradicts what the tree declares.
//
// It is given the tree the worktree is being reset ONTO, not HEAD: the point is
// to leave nothing behind that the incoming tree cannot be written over, and
// on a retry HEAD is one of the things being replaced.
func cleanUntracked(dir string, tree *object.Tree) error {
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
			// SkipDir on a non-directory skips the rest of the *parent*
			// directory -- at the worktree root, the whole walk. .git is a
			// file, not a directory, in a linked worktree, so returning
			// SkipDir there would abandon cleaning entirely. Either shape
			// is left untouched; only the directory shape needs descent
			// suppressed.
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			if _, ok := opaqueDirs[rel]; ok {
				return filepath.SkipDir
			}
			if _, ok := dirs[rel]; ok {
				return nil
			}
			// A directory the tree does not declare as one -- including a
			// directory sitting on a path the tree declares as a FILE -- is
			// output, not tracked content. It has to go before the reset can
			// write the file this path is supposed to hold.
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
func (m *Manager) CommitAll(ctx context.Context, dir, message string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	r, err := git.PlainOpen(dir)
	if err != nil {
		return false, fmt.Errorf("open worktree: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	wt, err := r.Worktree()
	if err != nil {
		return false, fmt.Errorf("open worktree: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := wt.AddWithOptions(&git.AddOptions{All: true}); err != nil {
		return false, fmt.Errorf("stage changes: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	status, err := wt.Status()
	if err != nil {
		return false, fmt.Errorf("read status: %w", err)
	}
	if status.IsClean() {
		return false, nil
	}
	if err := ctx.Err(); err != nil {
		return false, err
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
	// `git push -u` left behind previously. A post-publication metadata
	// failure must not cause push to report an error.
	if err := m.recordUpstream(r, branch, ref); err != nil {
		m.logger().Warn("record branch upstream", "branch", branch, "err", err)
	}
	return nil
}

func (m *Manager) recordUpstream(r *git.Repository, branch string, ref plumbing.ReferenceName) error {
	cfg, err := r.Config()
	if err != nil {
		return fmt.Errorf("read repo config: %w", err)
	}
	// SetConfig may succeed for root even when the file is marked read-only.
	// Respect the repository's mode bits so callers see the same failure that
	// an unprivileged process would get.
	if fsStorer, ok := r.Storer.(interface{ Filesystem() billy.Filesystem }); ok {
		if info, statErr := fsStorer.Filesystem().Stat("config"); statErr == nil && info.Mode().Perm()&0o222 == 0 {
			return fmt.Errorf("write repo config: config is read-only")
		}
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
		return fmt.Errorf("write repo config: %w", err)
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

// Snapshot exports HEAD's tracked files into a fresh, empty destDir with no
// .git directory: file contents only, no commit history, branch name, or
// reflog. Used to build the adversarial reviewer's isolated workspace
// (docs/prds/adversarial-self-review.md section 1) -- stripping .git is
// what makes the implementer's reasoning (commit messages, branch name,
// history) structurally unreachable rather than merely undisclosed.
func (m *Manager) Snapshot(ctx context.Context, dir, destDir string) error {
	r, err := git.PlainOpen(dir)
	if err != nil {
		return fmt.Errorf("open worktree: %w", err)
	}
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

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("create snapshot directory: %w", err)
	}

	files := tree.Files()
	defer files.Close()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		file, iterErr := files.Next()
		if errors.Is(iterErr, io.EOF) {
			break
		}
		if iterErr != nil {
			return fmt.Errorf("walk HEAD tree: %w", iterErr)
		}
		if err := writeSnapshotFile(destDir, file); err != nil {
			return err
		}
	}
	return nil
}

// writeSnapshotFile writes one tracked file's blob content to destDir,
// preserving its relative path and creating parent directories as needed.
func writeSnapshotFile(destDir string, file *object.File) error {
	target := filepath.Join(destDir, filepath.FromSlash(file.Name))
	if rel, relErr := filepath.Rel(destDir, target); relErr != nil || strings.HasPrefix(rel, "..") {
		return fmt.Errorf("snapshot: tracked path %q escapes destination", file.Name)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("create snapshot subdirectory for %s: %w", file.Name, err)
	}
	contents, err := file.Contents()
	if err != nil {
		return fmt.Errorf("read tracked file %s: %w", file.Name, err)
	}
	if err := os.WriteFile(target, []byte(contents), 0o644); err != nil {
		return fmt.Errorf("write snapshot file %s: %w", file.Name, err)
	}
	return nil
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

// CheckoutPR materialises an existing pull request's head commit in a fresh
// full clone so the caller can Diff it against its base and Snapshot it for
// operator-triggered review. The head must already be pushed to the same
// repository (archie's own PRs always are); a cross-repo or deleted head is
// refused rather than half-reviewed. The returned cleanup removes the clone.
func (m *Manager) CheckoutPR(ctx context.Context, owner, repo, headRef, baseRef string) (dir string, cleanup func(), err error) {
	if m.WorkDir != "" {
		if err := os.MkdirAll(m.WorkDir, 0o755); err != nil {
			return "", nil, fmt.Errorf("create worktree root directory: %w", err)
		}
	}
	dir, err = os.MkdirTemp(m.WorkDir, "pr-review-*")
	if err != nil {
		return "", nil, fmt.Errorf("create PR review directory: %w", err)
	}
	cleanup = func() { _ = os.RemoveAll(dir) }

	r, err := git.PlainCloneContext(ctx, dir, &git.CloneOptions{
		URL:           m.cloneURL(owner, repo),
		ClientOptions: m.auth(),
	})
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("clone %s/%s for review: %w", owner, repo, err)
	}

	headHash, err := r.ResolveRevision(plumbing.Revision(remoteBase(headRef)))
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("resolve head branch %q: %w (the PR head must already be pushed to this repository)", headRef, err)
	}
	wt, err := r.Worktree()
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("open review worktree: %w", err)
	}
	if err := wt.Checkout(&git.CheckoutOptions{Hash: *headHash}); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("checkout head %q: %w", headRef, err)
	}
	// Diff resolves origin/<baseRef>; a full clone fetches every branch, so
	// it exists. Fail closed now rather than mid-diff with a confusing error.
	if _, err := resolveBase(r, baseRef); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("resolve base branch %q: %w", baseRef, err)
	}
	return dir, cleanup, nil
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
// commit prefix (feat/fix/chore/etc.) from the issue title when present,
// falls back to labels, then defaults to "feat".
func archieBranch(issue int, title, labels string) string {
	prefix := branchPrefix(title, labels)
	_, rest := parseTitlePrefix(title)
	slug := branchSlug(rest)
	if slug == "" {
		return fmt.Sprintf("%s/%d", prefix, issue)
	}
	return fmt.Sprintf("%s/%d-%s", prefix, issue, slug)
}

// branchPrefix derives a conventional-commit prefix from the issue title
// when present, falls back to labels, and defaults to "feat".
// Supported label mappings: bug→fix, feature/enhancement→feat, docs→docs,
// chore→chore, test→test, refactor→refactor.
func branchPrefix(title, labels string) string {
	if prefix, _ := parseTitlePrefix(title); prefix != "" {
		return prefix
	}
	return labelPrefix(labels)
}

func parseTitlePrefix(title string) (prefix, rest string) {
	before, after, ok := strings.Cut(title, ":")
	if !ok {
		return "", title
	}
	lead := strings.TrimSpace(strings.ToLower(before))
	if open := strings.Index(lead, "("); open != -1 && strings.HasSuffix(lead, ")") {
		lead = lead[:open]
	}
	switch lead {
	case "fix", "feat", "chore", "docs", "test", "refactor", "perf", "build", "ci", "style", "revert":
		return lead, strings.TrimSpace(after)
	case "bug":
		return "fix", strings.TrimSpace(after)
	case "feature", "enhancement":
		return "feat", strings.TrimSpace(after)
	}
	return "", title
}

func labelPrefix(labels string) string {
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
// branch. Only keeps lowercase alphanumerics and hyphens, max 60 chars.
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
