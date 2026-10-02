package workflow

import (
	"context"

	"github.com/samcharles93/archie-core/internal/worktree"
)

// realTrees adapts a real worktree.Manager to the workflow's Trees contract for
// the tests that drive a stage against a real clone. The workflow's PrepareTarget
// and the manager's worktree.Target are the same choice spelled in two packages
// (workflow cannot import infrastructure), so the adapter maps one onto the
// other. Embedding keeps every other method; these tests never call Prepare,
// they only need the manager's git reads (Diff, ChangedFiles, ChangedFileStats).
type realTrees struct{ *worktree.Manager }

func (t realTrees) Prepare(ctx context.Context, owner, repo, base string, issue int, title, body, labels string, target PrepareTarget) (string, string, error) {
	return t.Manager.Prepare(ctx, owner, repo, base, issue, title, body, labels, worktree.Target(target))
}
