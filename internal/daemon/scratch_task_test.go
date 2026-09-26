package daemon

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/worktree"
)

// A task with no repository gets an empty scratch workspace, no publication
// grant, and is not serialized behind other repository-less tasks.
func TestNoRepositoryTaskRunsInScratch(t *testing.T) {
	d := &Daemon{Cfg: config.NewHolder(config.Config{}), Log: slog.New(slog.DiscardHandler)}
	task := &workflow.Task{ID: 12}
	trees := &worktree.Manager{WorkDir: t.TempDir()}

	dir, ok := d.prepareWorkspace(t.Context(), task, trees, config.Repo{})
	if !ok || dir != trees.ScratchDir(12) {
		t.Fatalf("prepareWorkspace() = %q, %v; want the task's scratch dir", dir, ok)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Fatalf("scratch dir not created: %v", err)
	}
	grant, revoke, ok := d.publicationGrant(t.Context(), task)
	if !ok || grant != "" || revoke == nil {
		t.Fatalf("publicationGrant() = %q, %v; want no grant and a no-op revoke", grant, ok)
	}
	revoke()
	if !d.allowConcurrentForTask(task) {
		t.Error("repository-less tasks are serialized on an empty repo key")
	}
}

// runPinnedViaAgent pins the task's workflow and profile, then hands it to the
// agent, the order process uses.
func runPinnedViaAgent(ctx context.Context, d *Daemon, task *workflow.Task, repo config.Repo) {
	profile, ok := d.pinTaskProfile(ctx, task)
	if !ok {
		return
	}
	d.runViaAgent(ctx, task, repo, profile, nil)
}
