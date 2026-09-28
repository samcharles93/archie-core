// Package prsource implements workflow.PRSource: fetching an arbitrary
// external pull request's metadata, diff and a read-only snapshot of its
// head, for the pr-review pipeline (docs/prds/pr-review-agent.md). It is
// the PR-under-review counterpart to worktree.Manager's use as the task's
// own Trees: that path only ever reaches the task's own worktree, never a
// third-party PR being reviewed, so this package checks one out fresh
// through Forge and Trees instead.
package prsource

import (
	"context"
	"fmt"

	"github.com/samcharles93/archie-core/internal/domain/workflow"
)

// PullRequest is the pull request fields Metadata and Diff need: title,
// body, and the head/base refs Trees checks out and diffs against. It
// mirrors forge.PullRequest's shape so callers can pass that type directly
// without this package importing internal/forge.
type PullRequest struct {
	Title   string
	Body    string
	HeadRef string
	BaseRef string
	HeadSHA string
}

// Forge fetches an existing pull request's metadata. Narrow by design: the
// only forge capability this package needs, matching
// forge.PullRequestReader's GetPullRequest.
type Forge interface {
	GetPullRequest(ctx context.Context, owner, repo string, number int) (PullRequest, error)
}

// Trees checks out a pull request's head into a fresh, isolated clone,
// diffs it against its base, and exports its tracked files with no .git
// present. Narrow by design: the subset of worktree.Manager's methods this
// package needs.
type Trees interface {
	CheckoutPR(ctx context.Context, owner, repo, headRef, baseRef string) (dir string, cleanup func(), err error)
	Diff(ctx context.Context, dir, base string) (string, error)
	Snapshot(ctx context.Context, dir, destDir string) error
}

// Source implements workflow.PRSource against a real forge and a real git
// checkout. Each call checks out its own fresh, disposable clone: Diff and
// Snapshot never share one, since the interface gives no session across
// calls to reuse -- correctness over the extra clone.
type Source struct {
	forge Forge
	trees Trees
}

var _ workflow.PRSource = (*Source)(nil)

// New builds a Source. Neither argument may be nil; callers that lack a
// forge PR-reading capability or a worktree manager must not construct one
// (matching the "a capability that cannot run is not advertised" rule the
// existing operator reviewer already follows).
func New(forge Forge, trees Trees) *Source {
	return &Source{forge: forge, trees: trees}
}

// Metadata returns the pull request's title and body. Commit messages are
// left empty: no existing Trees or Forge capability returns them, and they
// are only ever one of three inputs to the hallucination-check heuristic
// (docs/prds/pr-review-agent.md, phase 1), not load-bearing on their own.
func (s *Source) Metadata(ctx context.Context, owner, repo string, number int) (workflow.PRMetadata, error) {
	pr, err := s.forge.GetPullRequest(ctx, owner, repo, number)
	if err != nil {
		return workflow.PRMetadata{}, fmt.Errorf("prsource: fetch pull request metadata: %w", err)
	}
	return workflow.PRMetadata{Title: pr.Title, Body: pr.Body}, nil
}

// Diff checks out the pull request's head into a fresh clone and diffs it
// against its base branch.
func (s *Source) Diff(ctx context.Context, owner, repo string, number int) (string, error) {
	pr, err := s.forge.GetPullRequest(ctx, owner, repo, number)
	if err != nil {
		return "", fmt.Errorf("prsource: fetch pull request metadata: %w", err)
	}
	dir, cleanup, err := s.trees.CheckoutPR(ctx, owner, repo, pr.HeadRef, pr.BaseRef)
	if err != nil {
		return "", fmt.Errorf("prsource: checkout pull request head: %w", err)
	}
	defer cleanup()
	diff, err := s.trees.Diff(ctx, dir, pr.BaseRef)
	if err != nil {
		return "", fmt.Errorf("prsource: diff pull request: %w", err)
	}
	return diff, nil
}

// Snapshot checks out the pull request's head into a fresh clone and
// exports its tracked files into destDir with no .git present, returning
// the head commit SHA the forge reported -- the same value the diff's line
// numbers were measured against.
func (s *Source) Snapshot(ctx context.Context, owner, repo string, number int, destDir string) (string, error) {
	pr, err := s.forge.GetPullRequest(ctx, owner, repo, number)
	if err != nil {
		return "", fmt.Errorf("prsource: fetch pull request metadata: %w", err)
	}
	dir, cleanup, err := s.trees.CheckoutPR(ctx, owner, repo, pr.HeadRef, pr.BaseRef)
	if err != nil {
		return "", fmt.Errorf("prsource: checkout pull request head: %w", err)
	}
	defer cleanup()
	if err := s.trees.Snapshot(ctx, dir, destDir); err != nil {
		return "", fmt.Errorf("prsource: snapshot pull request head: %w", err)
	}
	return pr.HeadSHA, nil
}
