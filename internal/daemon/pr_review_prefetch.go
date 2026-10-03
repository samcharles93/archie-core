package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/samcharles93/archie-core/internal/forge"
	"github.com/samcharles93/archie-core/internal/infrastructure/prsource"
)

// prefetchPRReview fetches a pull request's metadata, diff and head snapshot
// with the daemon's forge client into workDir/prReviewPrefetchDir.
func prefetchPRReview(ctx context.Context, f any, owner, repo string, number int, workDir string) error {
	prReader, ok := f.(forge.PullRequestReader)
	if !ok {
		return errors.New("prefetch pr-review: this forge cannot read pull requests")
	}
	diffReader, ok := f.(forge.PullRequestDiffReader)
	if !ok {
		return errors.New("prefetch pr-review: this forge cannot read pull request diffs")
	}
	archiveReader, ok := f.(forge.RepoArchiveReader)
	if !ok {
		return errors.New("prefetch pr-review: this forge cannot read repository archives")
	}

	source := prsource.New(forgeAdapter{pr: prReader, diff: diffReader, archive: archiveReader})

	base := filepath.Join(workDir, prsource.PrefetchDirName)
	if err := os.MkdirAll(base, 0o755); err != nil {
		return fmt.Errorf("prefetch pr-review: create prefetch directory: %w", err)
	}

	meta, err := source.Metadata(ctx, owner, repo, number)
	if err != nil {
		return fmt.Errorf("prefetch pr-review: %w", err)
	}

	snapshotDir := filepath.Join(base, "snapshot")
	if err := os.MkdirAll(snapshotDir, 0o755); err != nil {
		return fmt.Errorf("prefetch pr-review: create snapshot directory: %w", err)
	}
	headSHA, err := source.Snapshot(ctx, owner, repo, number, snapshotDir)
	if err != nil {
		return fmt.Errorf("prefetch pr-review: %w", err)
	}

	diff, err := source.Diff(ctx, owner, repo, number)
	if err != nil {
		return fmt.Errorf("prefetch pr-review: %w", err)
	}
	if err := os.WriteFile(filepath.Join(base, "diff.patch"), []byte(diff), 0o644); err != nil {
		return fmt.Errorf("prefetch pr-review: write diff: %w", err)
	}

	metaJSON, err := json.Marshal(struct {
		Title   string `json:"title"`
		Body    string `json:"body"`
		HeadSHA string `json:"head_sha"`
	}{Title: meta.Title, Body: meta.Body, HeadSHA: headSHA})
	if err != nil {
		return fmt.Errorf("prefetch pr-review: encode metadata: %w", err)
	}
	if err := os.WriteFile(filepath.Join(base, "metadata.json"), metaJSON, 0o644); err != nil {
		return fmt.Errorf("prefetch pr-review: write metadata: %w", err)
	}
	return nil
}

// forgeAdapter narrows a concrete forge client's optional capabilities into
// prsource.Forge, converting forge.PullRequest (the daemon-side type, richer
// than prsource needs) into prsource.PullRequest.
type forgeAdapter struct {
	pr      forge.PullRequestReader
	diff    forge.PullRequestDiffReader
	archive forge.RepoArchiveReader
}

func (a forgeAdapter) GetPullRequest(ctx context.Context, owner, repo string, number int) (prsource.PullRequest, error) {
	pr, err := a.pr.GetPullRequest(ctx, owner, repo, number)
	if err != nil {
		return prsource.PullRequest{}, err
	}
	return prsource.PullRequest{Title: pr.Title, Body: pr.Body, HeadSHA: pr.HeadSHA}, nil
}

func (a forgeAdapter) GetPullRequestDiff(ctx context.Context, owner, repo string, number int) (string, error) {
	return a.diff.GetPullRequestDiff(ctx, owner, repo, number)
}

func (a forgeAdapter) GetRepoArchive(ctx context.Context, owner, repo, ref string) (io.ReadCloser, error) {
	return a.archive.GetRepoArchive(ctx, owner, repo, ref)
}
