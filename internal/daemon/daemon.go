// Package daemon is archied's resident loop: poll GitHub for labelled
// issues, enqueue them, and process one task at a time through its
// routed workflow. State lives in the store; the daemon is restartable
// at any point.
package daemon

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/google/go-github/v78/github"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/forge"
	"github.com/samcharles93/archie-core/internal/store"
	"github.com/samcharles93/archie-core/internal/workflow"
	"github.com/samcharles93/archie-core/internal/worktree"
)

type Daemon struct {
	Cfg       config.Config
	Store     *store.Store
	Forge     *forge.Client
	Trees     *worktree.Manager
	Workflows workflow.Registry
	Log       *slog.Logger
}

// Startup runs crash recovery and access verification once.
func (d *Daemon) Startup(ctx context.Context) error {
	n, err := d.Store.RecoverStale(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		d.Log.Info("re-queued tasks left running by a previous daemon", "count", n)
	}
	if err := d.Forge.AcceptInvitations(ctx); err != nil {
		d.Log.Warn("invitation sweep failed", "err", err)
	}
	for _, r := range d.Cfg.Repos {
		if err := d.Forge.VerifyPush(ctx, r.Owner, r.Name); err != nil {
			d.Log.Warn("repo not pushable — tasks from it will fail", "repo", r.FullName(), "err", err)
		}
	}
	return nil
}

// Run polls until the context ends.
func (d *Daemon) Run(ctx context.Context) error {
	ticker := time.NewTicker(d.Cfg.PollInterval.Std())
	defer ticker.Stop()
	for {
		d.Cycle(ctx)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// Cycle is one poll-and-drain pass: enqueue new labelled issues,
// reconcile open PRs, then process every queued task sequentially.
func (d *Daemon) Cycle(ctx context.Context) {
	d.poll(ctx)
	d.reconcilePRs(ctx)
	for {
		if ctx.Err() != nil {
			return
		}
		task, err := d.Store.ClaimNext(ctx)
		if err != nil {
			d.Log.Error("claim failed", "err", err)
			return
		}
		if task == nil {
			return
		}
		d.process(ctx, task)
	}
}

func (d *Daemon) poll(ctx context.Context) {
	for _, repo := range d.Cfg.Repos {
		issues, err := d.Forge.LabelledIssues(ctx, repo.Owner, repo.Name, d.Cfg.Label)
		if err != nil {
			d.Log.Error("poll failed", "repo", repo.FullName(), "err", err)
			continue
		}
		for _, is := range issues {
			inserted, err := d.Store.EnqueueIssue(ctx,
				repo.Owner, repo.Name, is.GetNumber(), is.GetTitle(), is.GetBody(), labelNames(is))
			if err != nil {
				d.Log.Error("enqueue failed", "repo", repo.FullName(), "issue", is.GetNumber(), "err", err)
				continue
			}
			if inserted {
				d.Log.Info("issue queued", "repo", repo.FullName(), "issue", is.GetNumber(), "title", is.GetTitle())
			}
		}
	}
}

// reconcilePRs moves pr_open tasks to merged/rejected from GitHub state
// and cleans up their worktrees.
func (d *Daemon) reconcilePRs(ctx context.Context) {
	tasks, err := d.Store.OpenPRs(ctx)
	if err != nil {
		d.Log.Error("reconcile query failed", "err", err)
		return
	}
	for _, t := range tasks {
		state, err := d.Forge.PRState(ctx, t.Owner, t.Repo, t.PRNumber)
		if err != nil {
			d.Log.Warn("PR state check failed", "pr", t.PRNumber, "err", err)
			continue
		}
		switch state {
		case "merged":
			_ = d.Store.Transition(ctx, t.ID, store.StatusPROpen, store.StatusMerged, "")
			_ = d.Trees.Cleanup(t.Owner, t.Repo, t.IssueNumber)
			d.Log.Info("PR merged", "repo", t.Owner+"/"+t.Repo, "pr", t.PRNumber)
		case "closed":
			_ = d.Store.Transition(ctx, t.ID, store.StatusPROpen, store.StatusRejected, "PR closed without merge")
			_ = d.Trees.Cleanup(t.Owner, t.Repo, t.IssueNumber)
			d.Log.Info("PR rejected", "repo", t.Owner+"/"+t.Repo, "pr", t.PRNumber)
		}
	}
}

func (d *Daemon) process(ctx context.Context, task *store.Task) {
	repo, ok := d.repoFor(task)
	if !ok {
		_ = d.Store.Transition(ctx, task.ID, store.StatusRunning, store.StatusParked, "repo no longer in config")
		return
	}
	wf := workflow.Route(task, d.Workflows)
	d.Log.Info("processing task", "repo", repo.FullName(), "issue", task.IssueNumber, "workflow", wf.Name, "attempt", task.Attempt)
	workflow.Run(ctx, wf, &workflow.TaskContext{
		Task:  task,
		Repo:  repo,
		Cfg:   d.Cfg,
		Forge: d.Forge,
		Store: d.Store,
		Trees: d.Trees,
		Log:   d.Log,
	})
}

func (d *Daemon) repoFor(t *store.Task) (config.Repo, bool) {
	for _, r := range d.Cfg.Repos {
		if r.Owner == t.Owner && r.Name == t.Repo {
			return r, true
		}
	}
	return config.Repo{}, false
}

func labelNames(is *github.Issue) string {
	var names []string
	for _, l := range is.Labels {
		names = append(names, l.GetName())
	}
	return strings.Join(names, ",")
}
