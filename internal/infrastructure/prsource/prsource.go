// Package prsource implements workflow.PRSource: fetching an arbitrary
// external pull request's metadata, diff and a read-only snapshot of its
// head, for the pr-review pipeline. It is
// the PR-under-review counterpart to worktree.Manager's use as the task's
// own Trees: that path only ever reaches the task's own worktree, never a
// third-party PR being reviewed.
//
// Every call goes over the forge's HTTP API -- never a git-shell checkout. The
// pipeline's Stage.Run bodies execute inside the sandboxed archie-agent
// process, which holds no forge credential and no git-level credential either;
// a git clone would need one.
package prsource

import (
	"context"
	"fmt"
	"io"

	"github.com/samcharles93/archie-core/internal/domain/workflow"
)

// PullRequest is the pull request fields Metadata, Diff and Snapshot need:
// title, body, and the head commit SHA the diff's line numbers and the
// snapshot are both measured against. It mirrors forge.PullRequest's shape
// so callers can pass that type directly without this package importing
// internal/forge.
type PullRequest struct {
	Title   string
	Body    string
	HeadSHA string
}

// Forge is the narrow forge capability this package needs: reading a pull
// request's metadata, its diff, and an archive of its repository at a ref.
// Matches forge.PullRequestReader, forge.PullRequestDiffReader and
// forge.RepoArchiveReader.
type Forge interface {
	GetPullRequest(ctx context.Context, owner, repo string, number int) (PullRequest, error)
	GetPullRequestDiff(ctx context.Context, owner, repo string, number int) (string, error)
	GetRepoArchive(ctx context.Context, owner, repo, ref string) (io.ReadCloser, error)
}

// Source implements workflow.PRSource against a real forge over HTTP.
type Source struct {
	forge Forge
}

var _ workflow.PRSource = (*Source)(nil)

// New builds a Source. forge may not be nil; a caller that lacks a forge
// PR-reading capability must not construct one (matching the "a capability
// that cannot run is not advertised" rule the existing operator reviewer
// already follows).
func New(forge Forge) *Source {
	return &Source{forge: forge}
}

// Metadata returns the pull request's title and body. Commit messages are left
// empty: no existing Forge capability returns them, and they are only ever one
// of three inputs to the hallucination-check heuristic, not load-bearing on
// their own.
func (s *Source) Metadata(ctx context.Context, owner, repo string, number int) (workflow.PRMetadata, error) {
	pr, err := s.forge.GetPullRequest(ctx, owner, repo, number)
	if err != nil {
		return workflow.PRMetadata{}, fmt.Errorf("prsource: fetch pull request metadata: %w", err)
	}
	return workflow.PRMetadata{Title: pr.Title, Body: pr.Body}, nil
}

// Diff fetches the pull request's unified diff directly over the forge's
// HTTP API.
func (s *Source) Diff(ctx context.Context, owner, repo string, number int) (string, error) {
	diff, err := s.forge.GetPullRequestDiff(ctx, owner, repo, number)
	if err != nil {
		return "", fmt.Errorf("prsource: fetch pull request diff: %w", err)
	}
	return diff, nil
}

// Snapshot fetches a repository archive at the pull request's head commit
// and extracts it into destDir with no .git present, returning the head
// SHA the forge reported -- the same value the diff's line numbers were
// measured against.
func (s *Source) Snapshot(ctx context.Context, owner, repo string, number int, destDir string) (string, error) {
	pr, err := s.forge.GetPullRequest(ctx, owner, repo, number)
	if err != nil {
		return "", fmt.Errorf("prsource: fetch pull request metadata: %w", err)
	}
	archive, err := s.forge.GetRepoArchive(ctx, owner, repo, pr.HeadSHA)
	if err != nil {
		return "", fmt.Errorf("prsource: fetch repository archive at %s: %w", pr.HeadSHA, err)
	}
	defer archive.Close()
	if err := extractTarGz(archive, destDir); err != nil {
		return "", fmt.Errorf("prsource: extract repository archive: %w", err)
	}
	return pr.HeadSHA, nil
}
