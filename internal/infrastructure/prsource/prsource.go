// Package prsource fetches a pull request's metadata, diff and head snapshot
// over the forge HTTP API.
package prsource

import (
	"context"
	"fmt"
	"io"

	"github.com/samcharles93/archie-core/internal/domain/workflow"
)

// PullRequest is the PR's title, body and head SHA.
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
