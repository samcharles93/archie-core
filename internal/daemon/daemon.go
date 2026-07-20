// Package daemon is archied's resident loop: poll GitHub for labelled
// issues, enqueue them, and process one task at a time through its
// routed workflow. State lives in the store; the daemon is restartable
// at any point.
package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/go-github/v78/github"
	"github.com/samcharles93/ai-sdk/core"
	"github.com/samcharles93/ai-sdk/runtime"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/events"
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
	Runtime   *runtime.Runtime
	Bus       *events.Bus
	Workflows workflow.Registry
	Log       *slog.Logger
}

// emit publishes an observability event; safe on a nil bus.
func (d *Daemon) emit(e events.Event) {
	if d.Bus != nil {
		d.Bus.Publish(e)
	}
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

// Cycle is one poll-and-drain pass: enqueue newly assigned issues,
// reconcile open PRs, act on human replies to waiting tasks, then
// process every queued task sequentially.
func (d *Daemon) Cycle(ctx context.Context) {
	d.poll(ctx)
	d.reconcilePRs(ctx)
	d.checkWaiting(ctx)
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
		issues, err := d.Forge.AssignedIssues(ctx, repo.Owner, repo.Name, d.Cfg.BotUser)
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
				d.emit(events.Event{
					Kind: events.KindTaskQueued, Repo: repo.FullName(),
					Issue: is.GetNumber(), Detail: is.GetTitle(),
				})
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
			d.emit(events.Event{
				Kind: events.KindPRMerged, TaskID: t.ID,
				Repo: t.Owner + "/" + t.Repo, Issue: t.IssueNumber,
				Data: map[string]any{"pr": t.PRNumber},
			})
		case "closed":
			_ = d.Store.Transition(ctx, t.ID, store.StatusPROpen, store.StatusRejected, "PR closed without merge")
			_ = d.Trees.Cleanup(t.Owner, t.Repo, t.IssueNumber)
			d.Log.Info("PR rejected", "repo", t.Owner+"/"+t.Repo, "pr", t.PRNumber)
			d.emit(events.Event{
				Kind: events.KindPRRejected, TaskID: t.ID,
				Repo: t.Owner + "/" + t.Repo, Issue: t.IssueNumber,
				Data: map[string]any{"pr": t.PRNumber},
			})
		}
	}
}

// checkWaiting looks for human replies on waiting_human tasks and asks
// an LLM — not keyword matching — whether the reply is a go-ahead.
// Approved tasks requeue under the implement workflow; rejections close
// the issue with the human's reasoning acknowledged.
func (d *Daemon) checkWaiting(ctx context.Context) {
	tasks, err := d.Store.WaitingTasks(ctx)
	if err != nil {
		d.Log.Error("waiting query failed", "err", err)
		return
	}
	for _, t := range tasks {
		replies, err := d.Forge.RepliesAfter(ctx, t.Owner, t.Repo, t.IssueNumber, t.WatchCommentID, d.Cfg.BotUser)
		if err != nil {
			d.Log.Warn("reply check failed", "issue", t.IssueNumber, "err", err)
			continue
		}
		if len(replies) == 0 {
			continue
		}
		var sb strings.Builder
		for _, r := range replies {
			fmt.Fprintf(&sb, "%s: %s\n", r.User, r.Body)
		}
		verdict, reason, err := d.judgeReply(ctx, t.Plan, sb.String())
		if err != nil {
			d.Log.Error("reply judge failed", "issue", t.IssueNumber, "err", err)
			continue
		}
		d.Log.Info("human reply judged", "issue", t.IssueNumber, "verdict", verdict)
		switch verdict {
		case "approve":
			if err := d.Store.Requeue(ctx, t.ID, store.StatusWaitingHuman, "implement"); err != nil {
				d.Log.Error("requeue failed", "issue", t.IssueNumber, "err", err)
				continue
			}
			_, _ = d.Forge.Comment(ctx, t.Owner, t.Repo, t.IssueNumber,
				"**Go-ahead received — implementation is queued.** ("+reason+")")
			d.emit(events.Event{
				Kind: "human_approved", TaskID: t.ID,
				Repo: t.Owner + "/" + t.Repo, Issue: t.IssueNumber, Detail: reason,
			})
		case "reject":
			_ = d.Store.Transition(ctx, t.ID, store.StatusWaitingHuman, store.StatusClosedWontDo, reason)
			_ = d.Forge.CloseIssue(ctx, t.Owner, t.Repo, t.IssueNumber,
				"**Understood — closing without implementation.** ("+reason+")")
			d.emit(events.Event{
				Kind: "human_rejected", TaskID: t.ID,
				Repo: t.Owner + "/" + t.Repo, Issue: t.IssueNumber, Detail: reason,
			})
		default:
			// Unclear: keep waiting; the humans can be more explicit.
			d.Log.Info("reply unclear — still waiting", "issue", t.IssueNumber, "reason", reason)
		}
	}
}

// judgeReply classifies the human's response to a PRD as approve,
// reject, or unclear.
func (d *Daemon) judgeReply(ctx context.Context, prd, replies string) (verdict, reason string, err error) {
	if d.Runtime == nil {
		return "", "", fmt.Errorf("no LLM runtime configured")
	}
	model := d.Cfg.Models["triage"]
	if model == "" {
		model = d.Cfg.Models["planner"]
	}
	if model == "" {
		return "", "", fmt.Errorf("no triage or planner model configured")
	}
	res, err := d.Runtime.Chat(ctx, model, core.GenerateOptions{
		System: "You classify a project owner's reply to a proposed PRD. Answer with exactly one line: " +
			"APPROVE, REJECT, or UNCLEAR, followed by ' - ' and a one-sentence justification. " +
			"APPROVE only when the reply expresses a go-ahead (with or without caveats). " +
			"REJECT when it declines or shelves the proposal. Anything ambiguous is UNCLEAR.",
		Prompt: "PRD (abridged):\n" + clip(prd, 3000) + "\n\nOwner's reply:\n" + clip(replies, 2000),
	})
	if err != nil {
		return "", "", err
	}
	line := strings.TrimSpace(res.Text)
	word, rest, _ := strings.Cut(line, "-")
	switch strings.ToUpper(strings.TrimSpace(strings.Trim(word, " :*"))) {
	case "APPROVE":
		return "approve", strings.TrimSpace(rest), nil
	case "REJECT":
		return "reject", strings.TrimSpace(rest), nil
	default:
		return "unclear", line, nil
	}
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
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
		Task:    task,
		Repo:    repo,
		Cfg:     d.Cfg,
		Forge:   d.Forge,
		Store:   d.Store,
		Trees:   d.Trees,
		Runtime: d.Runtime,
		Bus:     d.Bus,
		Log:     d.Log,
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
