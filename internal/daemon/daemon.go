// Package daemon is archied's resident loop: poll GitHub for labelled
// issues, enqueue them, and process tasks through their routed workflows.
// State lives in the store; the daemon is restartable at any point.
package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/container"
	"github.com/samcharles93/archie-core/internal/domain/curator"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/domain/workflow/skillbuild"
	"github.com/samcharles93/archie-core/internal/domain/workintake"
	"github.com/samcharles93/archie-core/internal/eventbus"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/forge"
	agentnats "github.com/samcharles93/archie-core/internal/infrastructure/agenttransport/nats"
	"github.com/samcharles93/archie-core/internal/logging"
	"github.com/samcharles93/archie-core/internal/memory"
	"github.com/samcharles93/archie-core/internal/plugin"
	"github.com/samcharles93/archie-core/internal/storage"
	"github.com/samcharles93/archie-core/internal/store"
	"github.com/samcharles93/archie-core/internal/taskrun"
	"github.com/samcharles93/archie-core/internal/tools"
	"github.com/samcharles93/archie-core/internal/worktree"
)

// TaskBus is the messaging the daemon needs: announce discovered work,
// collect it back, and ask a worker to run a task.
//
// Declared here rather than taking eventbus.Bus because a domain defines the
// smallest interface required to do its work -- the daemon never subscribes
// or creates reply inboxes.
type TaskBus interface {
	PublishUnique(ctx context.Context, subject, idempotencyKey string, payload []byte) error
	Fetch(ctx context.Context) (eventbus.Message, error)
	Request(ctx context.Context, subject string, payload []byte) ([]byte, error)
}

type Daemon struct {
	// Cfg publishes the running configuration. Read through Cfg() so a
	// reload swaps the published snapshot atomically. See config.Holder.
	Cfg *config.Holder
	// ConnectedNATS is the [nats] configuration the daemon's own client
	// connected with at startup (the endpoint it publishes on). Container
	// env is built from this, not from the live config, so a reload of
	// nats.url/token_env cannot point new containers at a server the
	// daemon is not publishing on. Zero value means NATS is not
	// configured.
	ConnectedNATS config.NATSConfig
	Store         store.TaskStore
	Forge         forge.Forge
	Trees         *worktree.Manager
	Agent         agentexec.Runner
	Bus           *events.Bus
	Workflows     workflow.Registry
	Log           *slog.Logger
	// Tasks is the optional message bus for task distribution. Nil means no
	// bus is configured; the existing SQLite ClaimNext flow is used.
	Tasks TaskBus
	// TaskRunReadyTimeout bounds how long runViaAgent retries an initial
	// taskrun request that fails with nats.ErrNoResponders, giving a
	// freshly spawned archie-agent container time to connect to NATS, set
	// up its JetStream stream/consumer, and subscribe before the daemon
	// gives up and parks the task. ContainerPool.Acquire returns as soon
	// as Docker has issued the start syscall, not once that setup
	// finishes, so without this bound the very first request after a
	// container spawn fails deterministically. Zero uses
	// defaultTaskRunReadyTimeout.
	TaskRunReadyTimeout time.Duration
	// TaskRunRetryBackoff is the delay between retry attempts within
	// TaskRunReadyTimeout. Zero uses defaultTaskRunRetryBackoff.
	TaskRunRetryBackoff time.Duration
	// SandboxRequired reports that [containers] asked for sandboxing that
	// could not be brought up. It exists because ContainerPool == nil means
	// two very different things: "sandboxing was never configured, run
	// in-process" and "sandboxing was configured and failed". Treating them
	// alike silently dropped the isolation boundary and ran agent loops on
	// the host with the daemon's own authority.
	SandboxRequired bool

	// ContainerPool manages Docker container lifecycle. Nil when [containers]
	// is not configured. When non-nil, every task gets a fresh container.
	ContainerPool *container.Pool
	// Storage is the pluggable storage backend for container mounts.
	// When nil (no containers configured), mount setup is skipped.
	Storage storage.Backend
	// CapabilityHost owns validated plugin manifests and cross-family
	// lifecycle. Typed capability registries remain in their domain packages;
	// the host never exposes daemon internals or an untyped service locator.
	CapabilityHost *plugin.Host
	// CustomStages discovers a repo's per-repo Yaegi custom stages
	// (.archie/stages/*.go) from its prepared worktree. Set by the
	// composition root (cmd/archied) to wfeval.Discover; nil disables
	// discovery.
	CustomStages func(dir string) ([]workflow.Stage, error)
	// Curators is the curator engine family registry (epic archie-core-yp9).
	// The runtime loop (archie-core-89x) and reference curators (i7i, gs8)
	// are driven through it; nil when the family is not composed.
	Curators *curator.Registry
	// Identities holds per-identity runner state for multi-identity mode.
	// When non-empty, Run() starts one goroutine per identity instead of
	// using the single-identity Forge/Trees/Cfg.Repos path.
	Identities []*IdentityRunner

	// Memory is the pluggable memory manager, wired by the composition root
	// (cmd/archied). When non-nil, the system prompt includes memory context
	// and memory tool calls are routed through the manager. Nil means memory
	// is disabled (backward compatible).
	Memory *memory.Manager

	// Guardrails is the tool-call guardrail engine, wired by the composition
	// root. When non-nil, tool successes and failures are recorded and
	// warnings/hard-stops are issued per the configured thresholds. Nil
	// means guardrails are disabled (backward compatible).
	Guardrails *tools.GuardrailEngine

	// ToolRegistry is the central tool registry, wired by the composition
	// root. MCP-discovered tools and built-in tools are registered here
	// and passed as CaptureTools in agent requests. Nil means no dynamic
	// tool discovery (backward compatible).
	ToolRegistry *tools.Registry

	// TaskLogs persists each task's own log output (including a sandboxed
	// container's, which otherwise disappears at AutoRemove) and mirrors it
	// live onto the dashboard feed, wired by the composition root. Nil
	// disables task logging (backward compatible); every TaskRegistry
	// method is nil-receiver-safe, so process() calls it unconditionally.
	TaskLogs *logging.TaskRegistry

	// running holds a cancel function for every task currently executing,
	// so /stop can reach work already in flight. Its zero value is ready
	// to use.
	running runningTasks
}

// memoryPrompt returns the memory manager's system prompt block, or an empty
// string when memory is disabled. Safe to call from any goroutine.
func (d *Daemon) memoryPrompt() string {
	if d.Memory == nil {
		return ""
	}
	return d.Memory.SystemPromptBlock()
}

// IdentityRunner bundles identity-specific state for a single agent
// identity running within a multi-identity daemon. Each identity gets its
// own forge client, worktree manager, repo list, and config  --  but shares
// the store, NATS connection, container pool, and event bus with siblings.
type IdentityRunner struct {
	Name  string
	Forge forge.Forge
	Trees *worktree.Manager
	Repos []config.Repo
	// Cfg is the identity-scoped config subset (forge, models, dispatch,
	// budgets, etc.).
	Cfg config.IdentityConfig
	// Agent is built from this identity's provider credentials.
	Agent agentexec.Runner
	Log   *slog.Logger
}

// NewIdentityRunner constructs an IdentityRunner from an identity config
// and a pre-built forge client. The caller owns forge and trees lifecycle;
// IdentityRunner just holds references.
func NewIdentityRunner(ctx context.Context, idCfg config.IdentityConfig, fg forge.Forge, trees *worktree.Manager, log *slog.Logger) (*IdentityRunner, error) {
	if idCfg.Name == "" {
		return nil, fmt.Errorf("identity name is required")
	}
	return &IdentityRunner{
		Name:  idCfg.Name,
		Forge: fg,
		Trees: trees,
		Repos: idCfg.Repos,
		Cfg:   idCfg,
		Log:   log.With("identity", idCfg.Name),
	}, nil
}

// emit publishes an observability event; safe on a nil bus.
func (d *Daemon) emit(e events.Event) {
	if d.Bus != nil {
		d.Bus.Publish(e)
	}
}

// Startup runs crash recovery and access verification once.
func (d *Daemon) Startup(ctx context.Context) error {
	d.cleanupExpiredStorage(ctx)
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
	for _, r := range d.Cfg.Get().Repos {
		if err := d.Forge.VerifyPush(ctx, r.Owner, r.Name); err != nil {
			d.Log.Warn("repo not pushable  --  tasks from it will fail", "repo", r.FullName(), "err", err)
		}
	}
	return nil
}

// Run polls until the context ends. When Identities is non-empty, each
// identity gets its own goroutine with its own forge client, repo list,
// and poll interval  --  failure isolation between identities (one identity's
// forge outage doesn't block another's poll cycle).
func (d *Daemon) Run(ctx context.Context) error {
	if len(d.Identities) > 0 {
		return d.runIdentities(ctx)
	}
	interval := d.Cfg.Get().PollInterval.Std()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		d.Cycle(ctx)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			// PollInterval may have been reloaded; reset the ticker so a
			// change takes effect on the next cycle.
			if iv := d.Cfg.Get().PollInterval.Std(); iv != interval {
				interval = iv
				ticker.Reset(iv)
			}
		}
	}
}

// runIdentities starts one goroutine per identity plus one shared
// maintenance-and-drain loop.
//
// Polling is identity-scoped: each identity goroutine polls only its own
// repos via its own forge client and enqueues under its own name. It never
// drains or reconciles. Maintenance and draining are store-wide, not
// identity-scoped, so they run once in a single shared loop  --  that keeps
// the global max_concurrency cap and the per-repo serialization intact
// across identities (N per-identity dispatchers would multiply the cap and
// split the same-repo lock), and it ensures reconcilePRs and storage
// cleanup actually run in multi-identity mode.
func (d *Daemon) runIdentities(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup

	// One poll loop per identity. Polling is the only identity-scoped
	// work; it never executes tasks.
	for _, id := range d.Identities {
		wg.Add(1)
		go func(id *IdentityRunner) {
			defer wg.Done()
			interval := d.Cfg.Get().PollInterval.Std()
			if id.Cfg.PollInterval > 0 {
				interval = id.Cfg.PollInterval.Std()
			}
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for {
				d.pollForIdentity(ctx, id)
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					// The identity's own override is frozen (identity config is
					// startup-built); the root fallback may have been reloaded.
					iv := d.Cfg.Get().PollInterval.Std()
					if id.Cfg.PollInterval > 0 {
						iv = id.Cfg.PollInterval.Std()
					}
					if iv != interval {
						interval = iv
						ticker.Reset(iv)
					}
				}
			}
		}(id)
	}

	// One shared maintenance-and-drain loop.
	wg.Go(func() {
		interval := d.Cfg.Get().PollInterval.Std()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			d.maintainAndDrain(ctx)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// PollInterval may have been reloaded; reset the ticker so a
				// change takes effect on the next cycle.
				if iv := d.Cfg.Get().PollInterval.Std(); iv != interval {
					interval = iv
					ticker.Reset(iv)
				}
			}
		}
	})

	wg.Wait()
	return ctx.Err()
}

// pollForIdentity polls one identity's repos via its own forge client,
// enqueuing any discovered issues under that identity's name. It never
// drains or reconciles  --  those are store-wide and run in the shared
// maintainAndDrain loop.
func (d *Daemon) pollForIdentity(ctx context.Context, id *IdentityRunner) {
	cfg := configForIdentity(d.Cfg.Get(), id.Cfg)
	for _, repo := range id.Repos {
		issues := d.pollIssuesWithConfig(ctx, id.Forge, cfg, repo)
		for _, is := range issues {
			labels := strings.Join(is.Labels, ",")
			if d.Tasks != nil {
				d.pollNATS(ctx, id.Forge, cfg, repo, is, labels, id.Name)
			} else {
				d.pollSQLite(ctx, id.Forge, cfg, repo, is, labels, id.Name)
			}
		}
	}
}

// maintainAndDrain runs the store-wide passes of a multi-identity cycle:
// storage cleanup, PR reconciliation, then task dispatch. A single shared
// loop runs this so the global concurrency cap and per-repo serialization
// hold across identities.
func (d *Daemon) maintainAndDrain(ctx context.Context) {
	d.cleanupExpiredStorage(ctx)
	d.reconcilePRs(ctx)
	if d.Tasks != nil {
		d.drainNATS(ctx)
	} else {
		d.drainSQLite(ctx)
	}
}

// pollIssuesWithConfig discovers work for one repo using the given forge
// client and dispatch config. Single-identity mode passes d.Forge/d.Cfg;
// identity poll loops pass the identity's own forge and configForIdentity,
// so each identity polls with its own bot user, label, and trigger.
func (d *Daemon) pollIssuesWithConfig(ctx context.Context, fg forge.Forge, cfg config.Config, repo config.Repo) []forge.Issue {
	switch cfg.Dispatch.Trigger {
	case "label":
		issues, err := fg.IssuesWithLabel(ctx, repo.Owner, repo.Name, cfg.Label)
		if err != nil {
			d.Log.Error("label poll failed", "repo", repo.FullName(), "err", err)
			return nil
		}
		return issues
	case "either":
		return d.pollEitherWithConfig(ctx, fg, cfg, repo)
	default: // "assignee" (default)
		issues, err := fg.AssignedIssues(ctx, repo.Owner, repo.Name, cfg.BotUser)
		if err != nil {
			d.Log.Error("poll failed", "repo", repo.FullName(), "err", err)
			return nil
		}
		return issues
	}
}

func (d *Daemon) pollEitherWithConfig(ctx context.Context, fg forge.Forge, cfg config.Config, repo config.Repo) []forge.Issue {
	seen := map[int]bool{}
	var out []forge.Issue

	assigned, err := fg.AssignedIssues(ctx, repo.Owner, repo.Name, cfg.BotUser)
	if err != nil {
		d.Log.Error("assigned poll failed", "repo", repo.FullName(), "err", err)
	} else {
		for _, is := range assigned {
			seen[is.Number] = true
			out = append(out, is)
		}
	}
	labelled, err := fg.IssuesWithLabel(ctx, repo.Owner, repo.Name, cfg.Label)
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

// Cycle is one poll-and-drain pass: enqueue newly assigned issues,
// reconcile open PRs, act on human replies to waiting tasks, then
// process queued tasks concurrently up to containers.max_concurrency.
func (d *Daemon) Cycle(ctx context.Context) {
	d.cleanupExpiredStorage(ctx)
	d.poll(ctx)
	d.reconcilePRs(ctx)
	if d.Tasks != nil {
		d.drainNATS(ctx)
	} else {
		d.drainSQLite(ctx)
	}
}

func (d *Daemon) cleanupExpiredStorage(ctx context.Context) {
	ttl := d.Cfg.Get().Containers.VolumeTTL.Std()
	if ttl <= 0 {
		return
	}
	if d.Storage != nil {
		n, err := d.Storage.CleanupExpired(ctx, ttl)
		if err != nil {
			d.Log.Warn("persistent volume cleanup failed", "err", err)
		}
		if n > 0 {
			d.Log.Info("expired persistent volumes removed", "count", n)
		}
	}
}

// drainSQLite claims queued tasks from SQLite and dispatches them concurrently.
func (d *Daemon) drainSQLite(ctx context.Context) {
	dispatcher := newTaskDispatcher(d.Cfg.Get().Containers.MaxConcurrency, d.allowConcurrentForTask)
	for ctx.Err() == nil {
		task, err := d.Store.ClaimNext(ctx)
		if err != nil {
			d.Log.Error("claim failed", "err", err)
			break
		}
		if task == nil {
			break
		}
		dispatcher.Submit(ctx, task, d.process)
	}
	dispatcher.Wait()
}

// drainNATS processes tasks from NATS, falling back to SQLite ClaimNext for
// requeued tasks (waiting_human approval, retry-parked) that didn't come
// through a NATS publish.
func (d *Daemon) drainNATS(ctx context.Context) {
	dispatcher := newTaskDispatcher(d.Cfg.Get().Containers.MaxConcurrency, d.allowConcurrentForTask)
	for ctx.Err() == nil {
		msg, err := d.Tasks.Fetch(ctx)
		if err != nil && !errors.Is(err, eventbus.ErrNoMessage) {
			d.Log.Error("nats fetch failed", "err", err)
			break
		}
		if err == nil {
			d.submitNATSTask(ctx, dispatcher, msg)
			continue
		}

		// ErrNoMessage means the stream is drained. That is precisely when
		// the SQLite fallback must run: requeued tasks (waiting_human
		// approval, retry-parked) never went through a NATS publish, so
		// nothing will ever arrive on the stream to trigger them.
		task, claimErr := d.Store.ClaimNext(ctx)
		if claimErr != nil {
			d.Log.Error("sqlite claim failed", "err", claimErr)
			break
		}
		if task == nil {
			break
		}
		dispatcher.Submit(ctx, task, d.process)
	}
	dispatcher.Wait()
}

// submitNATSTask decodes a fetched message and queues it for processing. A
// message that cannot be decoded is acked rather than redelivered: it will
// never decode on a retry, so leaving it unacked would block the queue.
func (d *Daemon) submitNATSTask(ctx context.Context, dispatcher *taskDispatcher, msg eventbus.Message) {
	envelope, err := workintake.DecodeTask(msg.Data())
	if err != nil {
		d.Log.Error("nats decode failed", "subject", msg.Subject(), "err", err)
		_ = msg.Ack()
		return
	}
	task := &store.Task{Owner: envelope.Owner, Repo: envelope.Repo, Identity: envelope.Identity}
	dispatcher.Submit(ctx, task, func(ctx context.Context, _ *store.Task) {
		d.processNATSTask(ctx, msg)
	})
}

// taskDispatcher bounds task execution globally while preserving the default
// one-task-per-repository safety rule. Submit order is retained within a repo;
// tasks for different repos may run concurrently. Repos for which
// allowConcurrent reports true opt out of the same-repo serialization
// entirely  --  the global slot limit is the only bound on their concurrency.
type taskDispatcher struct {
	slots           chan struct{}
	allowConcurrent func(task *store.Task) bool

	mu       sync.Mutex
	repoTail map[string]chan struct{}
	wg       sync.WaitGroup
}

func newTaskDispatcher(maxConcurrency int, allowConcurrent func(task *store.Task) bool) *taskDispatcher {
	var slots chan struct{}
	if maxConcurrency > 0 {
		slots = make(chan struct{}, maxConcurrency)
	}
	if allowConcurrent == nil {
		allowConcurrent = func(*store.Task) bool { return false }
	}
	return &taskDispatcher{
		slots:           slots,
		allowConcurrent: allowConcurrent,
		repoTail:        make(map[string]chan struct{}),
	}
}

func (d *taskDispatcher) Submit(
	ctx context.Context,
	task *store.Task,
	process func(context.Context, *store.Task),
) {
	repo := task.Owner + "/" + task.Repo

	var previous, done chan struct{}
	if !d.allowConcurrent(task) {
		done = make(chan struct{})
		d.mu.Lock()
		previous = d.repoTail[repo]
		d.repoTail[repo] = done
		d.mu.Unlock()
	}

	d.wg.Go(func() {
		if previous != nil {
			<-previous
		}
		if d.slots != nil {
			d.slots <- struct{}{}
			defer func() { <-d.slots }()
		}
		if done != nil {
			defer func() {
				close(done)
				d.mu.Lock()
				if d.repoTail[repo] == done {
					delete(d.repoTail, repo)
				}
				d.mu.Unlock()
			}()
		}
		process(ctx, task)
	})
}

func (d *taskDispatcher) Wait() {
	d.wg.Wait()
}

func (d *Daemon) poll(ctx context.Context) {
	for _, repo := range d.Cfg.Get().Repos {
		issues := d.pollIssues(ctx, repo)
		for _, is := range issues {
			labels := strings.Join(is.Labels, ",")
			if d.Tasks != nil {
				d.pollNATS(ctx, d.Forge, d.Cfg.Get(), repo, is, labels, "")
			} else {
				d.pollSQLite(ctx, d.Forge, d.Cfg.Get(), repo, is, labels, "")
			}
		}
	}
}

// pollSQLite enqueues discovered issues directly into SQLite (existing flow).
// fg is the forge client that discovered is  --  d.Forge for the
// single-identity path, or an identity's own client from pollForIdentity.
// cfg is the dispatch config that owns this poll (root or identity-scoped).
// identity records which identity owns the resulting task; empty for
// single-identity deployments.
func (d *Daemon) pollSQLite(ctx context.Context, fg forge.Forge, cfg config.Config, repo config.Repo, is forge.Issue, labels, identity string) {
	inserted, err := d.Store.EnqueueIssue(ctx,
		repo.Owner, repo.Name, is.Number, is.Title, is.Body, labels, identity)
	if err != nil {
		d.Log.Error("enqueue failed", "repo", repo.FullName(), "issue", is.Number, "err", err)
		return
	}
	if inserted {
		d.acknowledge(ctx, fg, cfg, repo, is)
		return
	}
	// Existing tasks are left untouched. Retry and approval are explicit
	// operator actions in messaging or the Web UI, not forge-side label edits.
}

// pollNATS publishes discovered issues to NATS (new flow).
func (d *Daemon) pollNATS(ctx context.Context, fg forge.Forge, cfg config.Config, repo config.Repo, is forge.Issue, labels, identity string) {
	// Read-only existence check prevents rediscovering an existing task.
	existing, err := d.Store.TaskByIssue(ctx, repo.Owner, repo.Name, is.Number)
	if err != nil {
		d.Log.Error("task lookup failed", "repo", repo.FullName(), "issue", is.Number, "err", err)
		return
	}
	if existing != nil {
		return
	}
	// Classify and encode here: the envelope, its routing kind and its
	// subject belong to the work-intake domain, not to the transport.
	parsed := workintake.SplitLabels(labels)
	task := workintake.TaskEnvelope{
		Owner:    repo.Owner,
		Repo:     repo.Name,
		Number:   is.Number,
		Title:    is.Title,
		Body:     is.Body,
		Labels:   parsed,
		Identity: identity,
		Kind:     workintake.KindForLabels(parsed),
	}
	if err := d.publishTask(ctx, task); err != nil {
		d.Log.Error("task publish failed", "repo", repo.FullName(), "issue", is.Number, "err", err)
		return
	}
	d.acknowledge(ctx, fg, cfg, repo, is)
}

// publishTask validates, encodes and publishes a task envelope. The
// idempotency key means rediscovering the same issue on a later poll does not
// enqueue the work twice.
func (d *Daemon) publishTask(ctx context.Context, task workintake.TaskEnvelope) error {
	if err := task.Kind.Validate(); err != nil {
		return fmt.Errorf("publish task %s: %w", task.Ref(), err)
	}
	payload, err := task.Encode()
	if err != nil {
		return err
	}
	return d.Tasks.PublishUnique(ctx, task.Subject(), task.IdempotencyKey(), payload)
}

// acknowledge posts the pickup reaction and queued event
// using fg  --  the forge client that owns repo (identity-scoped or root) --
// and the ack reaction from cfg, the dispatch config that owns this poll.
func (d *Daemon) acknowledge(ctx context.Context, fg forge.Forge, cfg config.Config, repo config.Repo, is forge.Issue) {
	d.Log.Info("issue queued", "repo", repo.FullName(), "issue", is.Number, "title", is.Title)
	if ack := cfg.Dispatch.AckReaction; ack != "" {
		if err := fg.React(ctx, repo.Owner, repo.Name, is.Number, ack); err != nil {
			d.Log.Warn("ack reaction failed", "issue", is.Number, "err", err)
		}
	}
	d.emit(events.Event{
		Kind: events.KindTaskQueued, Repo: repo.FullName(),
		Issue: is.Number, Detail: is.Title,
	})
}

// processNATSTask decodes a NATS message, writes it to SQLite, claims it,
// and runs the workflow. The message is ack'd on terminal (park is a valid
// outcome). Nak on transient errors so NATS redelivers.
func (d *Daemon) processNATSTask(ctx context.Context, msg eventbus.Message) {
	tm, err := workintake.DecodeTask(msg.Data())
	if err != nil {
		d.Log.Error("nats decode failed", "err", err)
		if err := msg.Ack(); err != nil {
			d.Log.Warn("ack failed", "err", err)
		}
		// bad message, don't retry
		return
	}

	inserted, err := d.Store.EnqueueIssue(ctx, tm.Owner, tm.Repo, tm.Number, tm.Title, tm.Body, strings.Join(tm.Labels, ","), tm.Identity)
	if err != nil {
		d.Log.Error("nats enqueue failed", "err", err)
		if err := msg.Nak(); err != nil {
			d.Log.Warn("nats nak failed", "err", err)
		}
		return
	}
	if !inserted {
		// Already tracked  --  dedup. Ack and move on.
		if err := msg.Ack(); err != nil {
			d.Log.Warn("ack failed", "err", err)
		}
		return
	}

	task, err := d.Store.ClaimByIssue(ctx, tm.Owner, tm.Repo, tm.Number)
	if err != nil {
		d.Log.Error("nats claim failed", "err", err)
		if err := msg.Nak(); err != nil {
			d.Log.Warn("nats nak failed", "err", err)
		}
		return
	}
	if task == nil {
		// Claimed by another consumer, or task is not queued. Ack.
		if err := msg.Ack(); err != nil {
			d.Log.Warn("ack failed", "err", err)
		}
		return
	}

	d.process(ctx, task)
	if err := msg.Ack(); err != nil {
		d.Log.Warn("ack failed", "err", err)
	}
}

// pollIssues discovers work for one repo according to the root dispatch
// config, using the root forge client. Identity poll loops use
// pollIssuesWithConfig with their own forge and config instead.
func (d *Daemon) pollIssues(ctx context.Context, repo config.Repo) []forge.Issue {
	return d.pollIssuesWithConfig(ctx, d.Forge, d.Cfg.Get(), repo)
}

// closeResolvedIssue closes the forge issue behind a finished task.
//
// A chat task's issue number is synthetic and matches no forge issue, so it
// is skipped. Failure is logged rather than returned: the task's own state is
// already correct, and the next reconcile pass will not retry, so a warning
// is the honest outcome -- the issue simply stays open.
func (d *Daemon) closeResolvedIssue(ctx context.Context, fg forge.Forge, task *store.Task, comment string) {
	if !task.IsForgeBacked() {
		return
	}
	if err := fg.CloseIssue(ctx, task.Owner, task.Repo, task.IssueNumber, comment); err != nil {
		d.Log.Warn("closing resolved issue failed; it stays open and will be re-polled",
			"repo", task.Owner+"/"+task.Repo, "issue", task.IssueNumber, "err", err)
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
		fg := d.forgeFor(&t)
		trees := d.treesFor(&t)
		// PRState/PR reconciliation is valid regardless of Source  --  a
		// chat-sourced task can still open a real PR against a real
		// branch. Only the issue-label call (tied to the synthetic
		// issue number) is forge-only.
		state, err := fg.PRState(ctx, t.Owner, t.Repo, t.PRNumber)
		if err != nil {
			d.Log.Warn("PR state check failed", "pr", t.PRNumber, "err", err)
			continue
		}
		switch state {
		case "merged":
			_ = d.Store.Transition(ctx, t.ID, store.StatusPROpen, store.StatusMerged, "")
			_ = trees.Cleanup(t.Owner, t.Repo, t.IssueNumber)
			// Close the issue ourselves rather than relying on the forge
			// noticing a "Closes #N" in the PR body. Nothing else closes it:
			// LinkBranch is sidebar linkage on Gitea and a no-op on GitHub.
			// Left open the issue stays labelled and assigned, the next poll
			// re-enqueues it, and once the dashboard's Clear removes the task
			// row that is a second implementation and a second PR for work
			// already merged.
			d.closeResolvedIssue(ctx, fg, &t, fmt.Sprintf(
				"Closed by #%d, merged by archie.", t.PRNumber))
			d.Log.Info("PR merged", "repo", t.Owner+"/"+t.Repo, "pr", t.PRNumber)
			d.emit(events.Event{
				Kind: events.KindPRMerged, TaskID: t.ID,
				Repo: t.Owner + "/" + t.Repo, Issue: t.IssueNumber,
				Data: map[string]any{"pr": t.PRNumber},
			})
		case "closed":
			_ = d.Store.Transition(ctx, t.ID, store.StatusPROpen, store.StatusRejected, "PR closed without merge")
			_ = trees.Cleanup(t.Owner, t.Repo, t.IssueNumber)
			d.Log.Info("PR rejected", "repo", t.Owner+"/"+t.Repo, "pr", t.PRNumber)
			d.emit(events.Event{
				Kind: events.KindPRRejected, TaskID: t.ID,
				Repo: t.Owner + "/" + t.Repo, Issue: t.IssueNumber,
				Data: map[string]any{"pr": t.PRNumber},
			})
		}
	}
}

func (d *Daemon) process(ctx context.Context, task *store.Task) {
	// Register before any work starts so the task is stoppable for its
	// whole life, including the slow setup -- clone, worktree prepare,
	// image pull -- which is exactly when someone realises they asked for
	// the wrong thing. Every caller reaches execution through here, so
	// this is the one place that has to do it.
	ctx, finished := d.running.begin(ctx, task.ID, task.Identity)
	defer finished()
	defer d.openTaskLog(task)()

	fg := d.forgeFor(task)
	trees := d.treesFor(task)
	executionCfg, executionAgent := d.executionFor(task)
	repo, ok := d.repoFor(task)
	if !ok {
		_ = d.Store.Transition(ctx, task.ID, store.StatusRunning, store.StatusParked, "repo no longer in config")
		return
	}
	// Per-worktree registry augmentation: if the worktree already
	// exists (e.g. retry, waiting_human → implement handoff), scan
	// it for .agents/skills/ that declare workflows. Worktree skills
	// override the startup registry for this task only.
	workDir := trees.Dir(task.Owner, task.Repo, task.IssueNumber)
	registry := d.Workflows
	if _, err := os.Stat(workDir); err == nil {
		aug, err := skillbuild.AugmentRegistry(workDir, d.Workflows)
		if err != nil {
			d.Log.Error("worktree registry augmentation failed  --  using startup registry", "dir", workDir, "err", err)
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

	// Clone the worktree before Docker mount setup. The container
	// binds /data/worktree to workDir on the host  --  the directory
	// must exist before Acquire is called.
	var branch string
	var err error
	// Every task gets an independent full clone. The former
	// PreparePersistent path shared objects with a per-repo bare cache;
	// go-git has no --dissociate, so a shared cache would stay a live
	// dependency of each worktree and expiring one would corrupt running
	// tasks. repo.PersistentStorage still governs the container volume.
	_, branch, err = trees.Prepare(ctx, task.Owner, task.Repo, repo.Base, task.IssueNumber, task.Title, task.Body, task.Labels)
	if err != nil {
		d.Log.Error("worktree prepare failed", "err", err)
		_ = d.Store.Transition(ctx, task.ID, store.StatusRunning, store.StatusParked, "worktree prepare failed: "+err.Error())
		return
	}
	task.Branch = branch
	if err := d.Store.Update(ctx, task); err != nil {
		d.Log.Warn("task branch not persisted", "task", task.ID, "err", err)
	}

	// Refuse to run unsandboxed when sandboxing was asked for. Parking is
	// recoverable -- fix the image, retry -- whereas running the agent loop
	// in-process would hand it the host filesystem and the daemon's own
	// authority, which is the one thing [containers] exists to prevent. The
	// daemon itself stays up: chat and the dashboard are unaffected.
	if d.SandboxRequired && d.ContainerPool == nil {
		const reason = "containers are enabled but the pool is unavailable; " +
			"refusing to run this task unsandboxed on the host"
		d.Log.Error("task parked: sandbox unavailable", "task", task.ID,
			"hint", "run `docker compose pull agent` (or `build agent`), then retry the task")
		_ = d.Store.Transition(ctx, task.ID, store.StatusRunning, store.StatusParked, reason)
		d.emit(events.Event{
			Kind: events.KindParked, TaskID: task.ID,
			Repo: repo.FullName(), Issue: task.IssueNumber, Detail: reason,
		})
		return
	}

	// Acquire a container for the task when Docker sandboxing is enabled.
	if d.ContainerPool != nil {
		ctr, ok := d.acquireTaskContainer(ctx, task, repo, workDir)
		if !ok {
			return
		}
		defer d.ContainerPool.Release(ctx, ctr)
	}

	// Sandboxed path: hand the whole task to archie-agent in one NATS round
	// trip instead of running the stage loop here. archie-agent proxies
	// Store/Forge/worktree-push calls back to archied over storerpc/
	// forgerpc/worktreerpc  --  by the time runViaAgent returns, the task's
	// terminal state already landed via those calls. The task's identity
	// travels in the taskrun request; the agent scopes its forgerpc and
	// worktreerpc clients to that identity, and the daemon registered one
	// server pair per identity (plus the root pair), so a container-mode
	// task is always served by its own forge client and worktree manager.
	if d.ContainerPool != nil {
		d.runViaAgent(ctx, task, repo)
	} else {
		workflow.Run(ctx, wf, &workflow.TaskContext{
			Task:         task,
			Repo:         repo,
			Cfg:          executionCfg,
			Forge:        fg,
			Store:        d.Store,
			Trees:        trees,
			SystemPrompt: d.memoryPrompt,
			Guardrails:   d.Guardrails,
			Agent:        executionAgent,
			Bus:          d.Bus,
			Log:          d.Log,
			CustomStages: d.CustomStages,
		})
	}

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

// openTaskLog opens task's log sink for the lifetime of one process() call
// and returns the function to close it, covering both dispatch paths below
// (sandboxed container and in-process) uniformly regardless of which one
// runs. A failure to open is not fatal to the task -- it means this
// attempt's own output won't be recoverable if something goes wrong, which
// is worse than the pre-existing state, not equal to it, so the run
// proceeds rather than parking over a logging problem.
func (d *Daemon) openTaskLog(task *store.Task) func() {
	if err := d.TaskLogs.Open(task.ID, task.Attempt); err != nil {
		d.Log.Warn("task log sink unavailable", "task", task.ID, "attempt", task.Attempt, "err", err)
	}
	return func() {
		if err := d.TaskLogs.Close(task.ID); err != nil {
			d.Log.Warn("task log sink close failed", "task", task.ID, "err", err)
		}
	}
}

// acquireTaskContainer prepares and acquires the sandbox container for a
// task, reporting false when it could not -- having already parked the
// task and logged why. The caller releases the container it returns.
func (d *Daemon) acquireTaskContainer(
	ctx context.Context,
	task *store.Task,
	repo config.Repo,
	workDir string,
) (*container.Container, bool) {
	park := func(reason string, err error) {
		d.Log.Error(reason, "err", err)
		_ = d.Store.Transition(ctx, task.ID, store.StatusRunning, store.StatusParked, reason+": "+err.Error())
	}

	// Write task.json  --  the container's boot-time brief.
	if err := container.WriteTaskJSON(workDir, container.TaskPayload{
		ID: task.ID, Owner: task.Owner, Repo: task.Repo,
		Number: task.IssueNumber, Title: task.Title, Body: task.Body,
		Labels:   strings.Split(task.Labels, ","),
		Workflow: task.Workflow, Branch: task.Branch, Plan: task.Plan,
	}); err != nil {
		park("task.json write failed", err)
		return nil, false
	}

	// Guard: Storage may be nil if the daemon was wired incorrectly. In
	// normal operation, Storage is always set when ContainerPool is set.
	if d.Storage == nil {
		d.Log.Error("storage backend is nil  --  cannot acquire container")
		_ = d.Store.Transition(ctx, task.ID, store.StatusRunning, store.StatusParked, "storage backend not configured")
		return nil, false
	}

	mounts, err := d.Storage.Setup(ctx, storage.TaskRef{
		WorktreeDir:       workDir,
		Ecosystem:         repo.Ecosystem,
		PersistentStorage: repo.PersistentStorage,
		Owner:             task.Owner,
		Repo:              task.Repo,
	})
	if err != nil {
		park("storage setup failed", err)
		return nil, false
	}

	ctr, err := d.ContainerPool.Acquire(ctx, mounts, d.containerEnv(task))
	if err != nil {
		// Roll back the storage we just set up: the mounts were created
		// for this task but no container will use them. Leaking them until
		// a later TTL sweep is not acceptable on a backend that allocates
		// real resources (volumes, NFS leases).
		if terr := d.Storage.Teardown(ctx, storage.TaskRef{
			WorktreeDir:       workDir,
			Ecosystem:         repo.Ecosystem,
			PersistentStorage: repo.PersistentStorage,
			Owner:             task.Owner,
			Repo:              task.Repo,
		}); terr != nil {
			d.Log.Warn("storage teardown after acquire failure failed", "err", terr)
		}
		park("container acquire failed", err)
		return nil, false
	}
	return ctr, true
}

// containerEnv returns the environment variables passed to agent containers.
// runViaAgent publishes a full-task handoff to archie-agent over the
// infrastructure-owned task subject and waits for its completion report. Every durable
// side effect (Store transitions, Forge calls, git push) happens inside
// archie-agent's workflow.Run, proxied back to this daemon over
// storerpc/forgerpc/worktreerpc  --  those RPC servers are the sole place a
// terminal state gets written on success. runViaAgent only parks the task
// itself when nothing else could have: the request never reached (or was
// never answered by) an archie-agent, or archie-agent failed before its
// own workflow.Run got a chance to record an outcome.
func (d *Daemon) runViaAgent(ctx context.Context, task *store.Task, repo config.Repo) {
	req := taskrun.Request{
		Task:      task,
		Repo:      repo,
		Cfg:       d.configFor(task).ForTask(),
		Providers: agentexec.ProvidersFromConfig(d.configFor(task).Providers),
	}
	data, err := json.Marshal(req)
	if err != nil {
		d.Log.Error("taskrun encode failed", "task", task.ID, "err", err)
		_ = d.Store.Transition(ctx, task.ID, store.StatusRunning, store.StatusParked, "taskrun encode failed: "+err.Error())
		return
	}

	reply, err := d.requestTaskRun(ctx, task.ID, data)
	if err != nil {
		d.Log.Error("taskrun request failed", "task", task.ID, "err", err)
		_ = d.Store.Transition(ctx, task.ID, store.StatusRunning, store.StatusParked, "taskrun request failed: "+err.Error())
		return
	}

	var resp taskrun.Response
	if err := json.Unmarshal(reply, &resp); err != nil {
		d.Log.Error("taskrun decode response failed", "task", task.ID, "err", err)
		_ = d.Store.Transition(ctx, task.ID, store.StatusRunning, store.StatusParked, "taskrun decode response failed: "+err.Error())
		return
	}
	if resp.Error != "" {
		d.Log.Error("taskrun run failed", "task", task.ID, "err", resp.Error)
		_ = d.Store.Transition(ctx, task.ID, store.StatusRunning, store.StatusParked, "taskrun run failed: "+resp.Error)
		return
	}

	d.Log.Info("taskrun complete", "task", task.ID, "status", resp.Status)
}

const (
	// defaultTaskRunReadyTimeout is how long requestTaskRun retries a
	// nats.ErrNoResponders before giving up, when Daemon.TaskRunReadyTimeout
	// is unset.
	defaultTaskRunReadyTimeout = 20 * time.Second
	// defaultTaskRunRetryBackoff is the delay between retry attempts, when
	// Daemon.TaskRunRetryBackoff is unset.
	defaultTaskRunRetryBackoff = 250 * time.Millisecond
)

func (d *Daemon) taskRunReadyTimeout() time.Duration {
	if d.TaskRunReadyTimeout > 0 {
		return d.TaskRunReadyTimeout
	}
	return defaultTaskRunReadyTimeout
}

func (d *Daemon) taskRunRetryBackoff() time.Duration {
	if d.TaskRunRetryBackoff > 0 {
		return d.TaskRunRetryBackoff
	}
	return defaultTaskRunRetryBackoff
}

// requestTaskRun publishes the taskrun request and retries while no
// archie-agent has subscribed yet (nats.ErrNoResponders): the container
// pool's Acquire returns as soon as Docker has issued the start syscall,
// not once the spawned container has connected to NATS, set up its
// JetStream stream/consumer, and subscribed to this per-task subject  --
// a gap of hundreds of milliseconds to a few seconds that would otherwise
// fail the very first request deterministically, every time, on every
// task. Any error other than ErrNoResponders (encode failures, a context
// that's already done, etc.) is returned immediately without retrying,
// since those don't mean "not ready yet".
func (d *Daemon) requestTaskRun(ctx context.Context, taskID int64, data []byte) ([]byte, error) {
	subject := agentnats.SubjectForTask(taskID)
	deadline := time.Now().Add(d.taskRunReadyTimeout())
	backoff := d.taskRunRetryBackoff()
	for {
		reply, err := d.Tasks.Request(ctx, subject, data)
		if err == nil {
			return reply, nil
		}
		if !errors.Is(err, eventbus.ErrNoResponders) || !time.Now().Before(deadline) {
			return nil, err
		}
		wait := min(backoff, time.Until(deadline))
		if wait <= 0 {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}
}

func (d *Daemon) containerEnv(task *store.Task) []string {
	var env []string
	// The endpoint the daemon's own client connected with at startup, not
	// the live config: a reloaded [nats] section must not point new
	// containers at a server the daemon is not publishing on.
	env = append(env, "NATS_URL="+d.ConnectedNATS.URL)
	if tokenEnv := d.ConnectedNATS.TokenEnv; tokenEnv != "" {
		if token := os.Getenv(tokenEnv); token != "" {
			env = append(env, "NATS_TOKEN="+token)
		}
	}
	for _, p := range d.configFor(task).Providers {
		if p.APIKeyEnv != "" {
			if v := os.Getenv(p.APIKeyEnv); v != "" {
				env = append(env, p.APIKeyEnv+"="+v)
			}
		}
	}
	return env
}

func (d *Daemon) executionFor(task *store.Task) (config.Config, agentexec.Runner) {
	if id := d.identityFor(task); id != nil {
		return configForIdentity(d.Cfg.Get(), id.Cfg), id.Agent
	}
	return d.Cfg.Get(), d.Agent
}

func (d *Daemon) configFor(task *store.Task) config.Config {
	cfg, _ := d.executionFor(task)
	return cfg
}

func configForIdentity(root config.Config, identity config.IdentityConfig) config.Config {
	root.BotUser = identity.BotUser
	root.BotEmail = identity.BotEmail
	root.DiffCapLines = identity.DiffCapLines
	root.Forge = identity.Forge
	root.Dispatch = identity.Dispatch
	root.Models = identity.Models
	root.Providers = identity.Providers
	root.Budgets = identity.Budgets
	root.Notify = identity.Notify
	root.Repos = identity.Repos
	return root
}

// identityFor resolves the IdentityRunner that owns task, or nil for
// single-identity deployments and forge-sourced tasks recorded
// before multi-identity routing existed (task.Identity == ""). Callers
// must fall back to the root d.Forge/d.Trees/d.Cfg when this returns nil.
func (d *Daemon) identityFor(task *store.Task) *IdentityRunner {
	if task == nil || task.Identity == "" {
		return nil
	}
	for _, id := range d.Identities {
		if id.Name == task.Identity {
			return id
		}
	}
	return nil
}

// forgeFor returns the forge client that owns task: the identity's own
// client when task.Identity names a configured identity, else the root
// d.Forge. This is the safety boundary that keeps one identity's forge
// token from being used against another identity's repos.
func (d *Daemon) forgeFor(task *store.Task) forge.Forge {
	if id := d.identityFor(task); id != nil {
		return id.Forge
	}
	return d.Forge
}

// treesFor returns the worktree manager that owns task, mirroring forgeFor.
func (d *Daemon) treesFor(task *store.Task) *worktree.Manager {
	if id := d.identityFor(task); id != nil {
		return id.Trees
	}
	return d.Trees
}

// repoFor resolves task's repo config from the owning identity's repo
// list when task.Identity is set, else the root Cfg.Repos.
func (d *Daemon) repoFor(t *store.Task) (config.Repo, bool) {
	repos := d.Cfg.Get().Repos
	if id := d.identityFor(t); id != nil {
		repos = id.Repos
	}
	for _, r := range repos {
		if r.Owner == t.Owner && r.Name == t.Repo {
			return r, true
		}
	}
	return config.Repo{}, false
}

// allowConcurrentForTask reports whether the task's owning repo has opted
// into concurrent task dispatch (config.Repo.AllowConcurrent). The repo is
// resolved through repoFor, which selects the identity-scoped repo list when
// task.Identity names a configured identity -- so multi-identity concurrency
// policy comes from the identity's own config, not the root daemon's.
// Unknown repos default to the safe, serialized behavior.
func (d *Daemon) allowConcurrentForTask(task *store.Task) bool {
	repo, ok := d.repoFor(task)
	return ok && repo.AllowConcurrent
}
