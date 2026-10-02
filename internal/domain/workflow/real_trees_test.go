package workflow

import (
	"context"

	"github.com/samcharles93/archie-core/internal/worktree"
)

// realTrees adapts a real worktree.Manager to the workflow's Trees contract for
// the tests that drive a stage against a real clone. Manager.Prepare carries
// the daemon's prepare target -- worktree.Fresh for a run that starts new work
// -- which the workflow's Trees contract deliberately does not expose, so the
// manager no longer satisfies Trees directly. Embedding keeps every other
// method; these tests never call Prepare, they only need the manager's git
// reads (Diff, ChangedFiles, ChangedFileStats).
type realTrees struct{ *worktree.Manager }

func (t realTrees) Prepare(ctx context.Context, owner, repo, base string, issue int, title, body, labels string) (string, string, error) {
	return t.Manager.Prepare(ctx, owner, repo, base, issue, title, body, labels, worktree.Fresh)
}
