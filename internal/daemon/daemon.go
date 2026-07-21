// Package daemon is archied's resident loop: poll GitHub for labelled
// issues, enqueue them, and process one task at a time through its
// routed workflow. State lives in the store; the daemon is restartable
// at any point.
package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/samcharles93/ai-sdk/core"
	"github.com/samcharles93/ai-sdk/runtime"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/container"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/forge"
	arnats "github.com/samcharles93/archie-core/internal/nats"
	"github.com/samcharles93/archie-core/internal/plugin"
	"github.com/samcharles93/archie-core/internal/storage"
	"github.com/samcharles93/archie-core/internal/store"
	"github.com/samcharles93/archie-core/internal/workflow"
	"github.com/samcharles93/archie-core/internal/workflow/skillbuild"
	"github.com/samcharles93/archie-core/internal/worktree"
)

type Daemon struct {
	Cfg       config.Config
	Store     *store.Store
	Forge     forge.Forge
	Trees     *worktree.Manager
	Runtime   *runtime.Runtime
	Agent     agentexec.Runner
	Bus       *events.Bus
	Workflows workflow.Registry
	Log       *slog.Logger
	// Nats is the optional NATS client for task distribution. Nil means
	// NATS is not configured; the existing SQLite ClaimNext flow is used.
	Nats *arnats.Client
	// ContainerPool manages Docker container lifecycle. Nil when [containers]
	// is not configured. When non-nil, every task gets a fresh container.
	ContainerPool *container.Pool
	// Storage is the pluggable storage backend for container mounts.
	// When nil (no containers configured), mount setup is skipped.
	Storage storage.Backend
	// PluginRegistry holds core daemon plugins loaded from the configured
	// plugin_dir at startup (Layer 2). Nil when no plugin_dir is configured.
	//
	// Reserved for future daemon extension points (forge resolvers, storage
	// backends, notification handlers). Loaded plugins are logged at startup;
	// the registry is available when extension point interfaces are defined.
	PluginRegistry *plugin.Registry
	// CustomStages discovers a repo's per-repo Yaegi custom stages
	// (.archie/stages/*.go) from its prepared worktree. Set by the
	// composition root (cmd/archied) to wfeval.Discover; nil disables
	// discovery.
	CustomStages func(dir string) ([]workflow.Stage, error)
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
	if d.Nats != nil {
		d.drainNATS(ctx)
	} else {
		d.drainSQLite(ctx)
	}
}

// drainSQLite processes queued tasks from SQLite (existing behaviour).
func (d *Daemon) drainSQLite(ctx context.Context) {
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

// drainNATS processes tasks from NATS, falling back to SQLite ClaimNext for
// requeued tasks (waiting_human approval, retry-parked) that didn't come
// through a NATS publish.
func (d *Daemon) drainNATS(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		msg, err := d.Nats.Fetch(ctx)
		if err != nil {
			d.Log.Error("nats fetch failed", "err", err)
			return
		}
		if msg != nil {
			d.processNATSTask(ctx, msg)
			continue
		}
		// Fall back to SQLite for requeued tasks.
		task, err := d.Store.ClaimNext(ctx)
		if err != nil {
			d.Log.Error("sqlite claim failed", "err", err)
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
		issues := d.pollIssues(ctx, repo)
		for _, is := range issues {
			labels := strings.Join(is.Labels, ",")
			if d.Nats != nil {
				d.pollNATS(ctx, repo, is, labels)
			} else {
				d.pollSQLite(ctx, repo, is, labels)
			}
		}
	}
}

// pollSQLite enqueues discovered issues directly into SQLite (existing flow).
func (d *Daemon) pollSQLite(ctx context.Context, repo config.Repo, is forge.Issue, labels string) {
	inserted, err := d.Store.EnqueueIssue(ctx,
		repo.Owner, repo.Name, is.Number, is.Title, is.Body, labels)
	if err != nil {
		d.Log.Error("enqueue failed", "repo", repo.FullName(), "issue", is.Number, "err", err)
		return
	}
	if inserted {
		d.acknowledge(ctx, repo, is)
		return
	}
	d.maybeRetryParked(ctx, repo, is)
}

// pollNATS publishes discovered issues to NATS (new flow).
func (d *Daemon) pollNATS(ctx context.Context, repo config.Repo, is forge.Issue, labels string) {
	// Read-only existence check — needed for maybeRetryParked.
	existing, err := d.Store.TaskByIssue(ctx, repo.Owner, repo.Name, is.Number)
	if err != nil {
		d.Log.Error("task lookup failed", "repo", repo.FullName(), "issue", is.Number, "err", err)
		return
	}
	if existing != nil {
		d.maybeRetryParked(ctx, repo, is)
		return
	}
	if err := d.Nats.PublishTask(ctx, repo.Owner, repo.Name, is.Number, is.Title, is.Body, labels); err != nil {
		d.Log.Error("nats publish failed", "repo", repo.FullName(), "issue", is.Number, "err", err)
		return
	}
	d.acknowledge(ctx, repo, is)
}

// acknowledge posts the pickup reaction, state label, and queued event.
func (d *Daemon) acknowledge(ctx context.Context, repo config.Repo, is forge.Issue) {
	d.Log.Info("issue queued", "repo", repo.FullName(), "issue", is.Number, "title", is.Title)
	if ack := d.Cfg.Dispatch.AckReaction; ack != "" {
		if err := d.Forge.React(ctx, repo.Owner, repo.Name, is.Number, ack); err != nil {
			d.Log.Warn("ack reaction failed", "issue", is.Number, "err", err)
		}
	}
	d.Forge.SetStateLabel(ctx, repo.Owner, repo.Name, is.Number, d.Cfg.Dispatch.StateLabel("queued"), d.Cfg.Dispatch.LabelValues())
	d.emit(events.Event{
		Kind: events.KindTaskQueued, Repo: repo.FullName(),
		Issue: is.Number, Detail: is.Title,
	})
}

// processNATSTask decodes a NATS message, writes it to SQLite, claims it,
// and runs the workflow. The message is ack'd on terminal (park is a valid
// outcome). Nak on transient errors so NATS redelivers.
func (d *Daemon) processNATSTask(ctx context.Context, msg jetstream.Msg) {
	tm, err := arnats.DecodeTask(msg)
	if err != nil {
		d.Log.Error("nats decode failed", "err", err)
		msg.Ack() // bad message, don't retry
		return
	}

	inserted, err := d.Store.EnqueueIssue(ctx, tm.Owner, tm.Repo, tm.Number, tm.Title, tm.Body, tm.Labels)
	if err != nil {
		d.Log.Error("nats enqueue failed", "err", err)
		msg.Nak()
		return
	}
	if !inserted {
		// Already tracked — dedup. Ack and move on.
		msg.Ack()
		return
	}

	task, err := d.Store.ClaimByIssue(ctx, tm.Owner, tm.Repo, tm.Number)
	if err != nil {
		d.Log.Error("nats claim failed", "err", err)
		msg.Nak()
		return
	}
	if task == nil {
		// Claimed by another consumer, or task is not queued. Ack.
		msg.Ack()
		return
	}

	d.process(ctx, task)
	msg.Ack()
}

// pollIssues discovers work for one repo according to cfg.Dispatch.Trigger.
func (d *Daemon) pollIssues(ctx context.Context, repo config.Repo) []forge.Issue {
	switch d.Cfg.Dispatch.Trigger {
	case "label":
		issues, err := d.Forge.IssuesWithLabel(ctx, repo.Owner, repo.Name, d.Cfg.Label)
		if err != nil {
			d.Log.Error("label poll failed", "repo", repo.FullName(), "err", err)
			return nil
		}
		return issues
	case "either":
		return d.pollEither(ctx, repo)
	default: // "assignee" (default)
		issues, err := d.Forge.AssignedIssues(ctx, repo.Owner, repo.Name, d.Cfg.BotUser)
		if err != nil {
			d.Log.Error("poll failed", "repo", repo.FullName(), "err", err)
			return nil
		}
		return issues
	}
}

// pollEither returns the union of assigned and labelled issues, deduped
// by issue number. An issue both assigned and labelled appears once.
func (d *Daemon) pollEither(ctx context.Context, repo config.Repo) []forge.Issue {
	seen := map[int]bool{}
	var out []forge.Issue

	assigned, err := d.Forge.AssignedIssues(ctx, repo.Owner, repo.Name, d.Cfg.BotUser)
	if err != nil {
		d.Log.Error("assigned poll failed", "repo", repo.FullName(), "err", err)
	} else {
		for _, is := range assigned {
			seen[is.Number] = true
			out = append(out, is)
		}
	}

	labelled, err := d.Forge.IssuesWithLabel(ctx, repo.Owner, repo.Name, d.Cfg.Label)
	if err != nil {
		d.Log.Error("label poll failed", "repo", repo.FullName(), "err", err)
	} else {
		for _, is := range labelled {
			if !seen[is.Number] {
				out = append(out, is)
			}
		}
	}

	return out
}

// maybeRetryParked requeues a parked task whose archie:parked label a
// human has removed — the forge-native retry trigger.
func (d *Daemon) maybeRetryParked(ctx context.Context, repo config.Repo, is forge.Issue) {
	if hasLabel(is.Labels, d.Cfg.Dispatch.StateLabel("parked")) {
		return
	}
	task, err := d.Store.TaskByIssue(ctx, repo.Owner, repo.Name, is.Number)
	if err != nil || task == nil || task.Status != store.StatusParked {
		return
	}
	if err := d.Store.Requeue(ctx, task.ID, store.StatusParked, ""); err != nil {
		d.Log.Error("label-triggered requeue failed", "issue", is.Number, "err", err)
		return
	}
	d.Log.Info("parked task requeued via label removal", "repo", repo.FullName(), "issue", is.Number)
	d.Forge.SetStateLabel(ctx, repo.Owner, repo.Name, is.Number, d.Cfg.Dispatch.StateLabel("queued"), d.Cfg.Dispatch.LabelValues())
	d.emit(events.Event{
		Kind: "task_retried", TaskID: task.ID,
		Repo: repo.FullName(), Issue: is.Number, Detail: "archie:parked label removed",
	})
}

func hasLabel(labels []string, want string) bool {
	return slices.Contains(labels, want)
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
			d.Forge.SetStateLabel(ctx, t.Owner, t.Repo, t.IssueNumber, "", d.Cfg.Dispatch.LabelValues())
			d.Log.Info("PR merged", "repo", t.Owner+"/"+t.Repo, "pr", t.PRNumber)
			d.emit(events.Event{
				Kind: events.KindPRMerged, TaskID: t.ID,
				Repo: t.Owner + "/" + t.Repo, Issue: t.IssueNumber,
				Data: map[string]any{"pr": t.PRNumber},
			})
		case "closed":
			_ = d.Store.Transition(ctx, t.ID, store.StatusPROpen, store.StatusRejected, "PR closed without merge")
			_ = d.Trees.Cleanup(t.Owner, t.Repo, t.IssueNumber)
			d.Forge.SetStateLabel(ctx, t.Owner, t.Repo, t.IssueNumber, "", d.Cfg.Dispatch.LabelValues())
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
			d.Forge.SetStateLabel(ctx, t.Owner, t.Repo, t.IssueNumber, d.Cfg.Dispatch.StateLabel("queued"), d.Cfg.Dispatch.LabelValues())
			d.emit(events.Event{
				Kind: "human_approved", TaskID: t.ID,
				Repo: t.Owner + "/" + t.Repo, Issue: t.IssueNumber, Detail: reason,
			})
		case "reject":
			_ = d.Store.Transition(ctx, t.ID, store.StatusWaitingHuman, store.StatusClosedWontDo, reason)
			_ = d.Forge.CloseIssue(ctx, t.Owner, t.Repo, t.IssueNumber,
				"**Understood — closing without implementation.** ("+reason+")")
			d.Forge.SetStateLabel(ctx, t.Owner, t.Repo, t.IssueNumber, "", d.Cfg.Dispatch.LabelValues())
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
	// Per-worktree registry augmentation: if the worktree already
	// exists (e.g. retry, waiting_human → implement handoff), scan
	// it for .agents/skills/ that declare workflows. Worktree skills
	// override the startup registry for this task only.
	workDir := d.Trees.Dir(task.Owner, task.Repo, task.IssueNumber)
	registry := d.Workflows
	if _, err := os.Stat(workDir); err == nil {
		aug, err := skillbuild.AugmentRegistry(workDir, d.Workflows)
		if err != nil {
			d.Log.Error("worktree registry augmentation failed — using startup registry", "dir", workDir, "err", err)
			d.emit(events.Event{
				Kind: "registry_augment_failed", TaskID: task.ID,
				Repo: task.Owner + "/" + task.Repo, Issue: task.IssueNumber,
				Detail: err.Error(),
			})
		} else {
			registry = aug
		}
	}
	wf := workflow.Route(task, registry)
	d.Log.Info("processing task", "repo", repo.FullName(), "issue", task.IssueNumber, "workflow", wf.Name, "attempt", task.Attempt)

	// Acquire a container for the task when Docker sandboxing is enabled.
	if d.ContainerPool != nil {
		// Write task.json — the container's boot-time brief.
		if err := container.WriteTaskJSON(workDir, container.TaskPayload{
			ID: task.ID, Owner: task.Owner, Repo: task.Repo,
			Number: task.IssueNumber, Title: task.Title, Body: task.Body,
			Labels: strings.Split(task.Labels, ","),
			Workflow: task.Workflow, Branch: task.Branch, Plan: task.Plan,
		}); err != nil {
			d.Log.Error("task.json write failed", "err", err)
			_ = d.Store.Transition(ctx, task.ID, store.StatusRunning, store.StatusParked, "task.json write failed: "+err.Error())
			return
		}

		// Build the mount list from the storage backend.
		// Guard: Storage may be nil if the daemon was wired incorrectly.
		// In normal operation, Storage is always set when ContainerPool is set.
		if d.Storage == nil {
			d.Log.Error("storage backend is nil — cannot acquire container")
			_ = d.Store.Transition(ctx, task.ID, store.StatusRunning, store.StatusParked, "storage backend not configured")
			return
		}
		mounts, err := d.Storage.Setup(ctx, storage.TaskRef{
			WorktreeDir:       workDir,
			Ecosystem:         repo.Ecosystem,
			PersistentStorage: repo.PersistentStorage,
			Owner:             task.Owner,
			Repo:              task.Repo,
		})
		if err != nil {
			d.Log.Error("storage setup failed", "err", err)
			_ = d.Store.Transition(ctx, task.ID, store.StatusRunning, store.StatusParked, "storage setup failed: "+err.Error())
			return
		}
		ctr, err := d.ContainerPool.Acquire(ctx, mounts, d.containerEnv())
		if err != nil {
			d.Log.Error("container acquire failed", "err", err)
			_ = d.Store.Transition(ctx, task.ID, store.StatusRunning, store.StatusParked, "container acquire failed: "+err.Error())
			return
		}
		defer d.ContainerPool.Release(ctr)
	}

	// Set the working label only after successful container setup (or when no
	// container is needed). This prevents label/database divergence on failure.
	d.Forge.SetStateLabel(ctx, task.Owner, task.Repo, task.IssueNumber, d.Cfg.Dispatch.StateLabel("working"), d.Cfg.Dispatch.LabelValues())

	workflow.Run(ctx, wf, &workflow.TaskContext{
		Task:         task,
		Repo:         repo,
		Cfg:          d.Cfg,
		Forge:        d.Forge,
		Store:        d.Store,
		Trees:        d.Trees,
		Agent:        d.Agent,
		Bus:          d.Bus,
		Log:          d.Log,
		CustomStages: d.CustomStages,
	})

	// Teardown storage after workflow completes. The Docker backend is a
	// no-op; future backends (temp volumes, NFS leases) use this hook.
	if d.Storage != nil {
		_ = d.Storage.Teardown(ctx, storage.TaskRef{
			WorktreeDir:       workDir,
			Ecosystem:         repo.Ecosystem,
			PersistentStorage: repo.PersistentStorage,
			Owner:             task.Owner,
			Repo:              task.Repo,
		})
	}
}

// containerEnv returns the environment variables passed to agent containers.
func (d *Daemon) containerEnv() []string {
	var env []string
	env = append(env, "NATS_URL="+d.Cfg.NATS.URL)
	for _, p := range d.Cfg.Providers {
		if p.APIKeyEnv != "" {
			if v := os.Getenv(p.APIKeyEnv); v != "" {
				env = append(env, p.APIKeyEnv+"="+v)
			}
		}
	}
	return env
}

func (d *Daemon) repoFor(t *store.Task) (config.Repo, bool) {
	for _, r := range d.Cfg.Repos {
		if r.Owner == t.Owner && r.Name == t.Repo {
			return r, true
		}
	}
	return config.Repo{}, false
}
