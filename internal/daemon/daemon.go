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
	"maps"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/container"
	"github.com/samcharles93/archie-core/internal/domain/binding"
	"github.com/samcharles93/archie-core/internal/domain/curator"
	"github.com/samcharles93/archie-core/internal/domain/eda/playbook"
	"github.com/samcharles93/archie-core/internal/domain/identity"
	"github.com/samcharles93/archie-core/internal/domain/mapping"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	workflowtask "github.com/samcharles93/archie-core/internal/domain/workflow/task"
	"github.com/samcharles93/archie-core/internal/domain/workintake"
	"github.com/samcharles93/archie-core/internal/eventbus"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/forge"
	agentnats "github.com/samcharles93/archie-core/internal/infrastructure/agenttransport/nats"
	"github.com/samcharles93/archie-core/internal/logging"
	"github.com/samcharles93/archie-core/internal/plugin"
	"github.com/samcharles93/archie-core/internal/storage"
	"github.com/samcharles93/archie-core/internal/taskrun"
	"github.com/samcharles93/archie-core/internal/taskstate"
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

// WorktreeGrantIssuer gives one container dispatch the narrow authority to
// publish its already-prepared branch. The daemon owns grant lifetime; the
// transport implementation owns token mechanics.
type WorktreeGrantIssuer interface {
	Issue(task *workflow.Task) (token string, revoke func(), err error)
}

// StateStoreGrantIssuer gives one container dispatch a State Store credential
// scoped to that task's own workflow.Store RPCs (Update, Transition,
// InsertEvent) -- never the daemon's own administrative credential. The
// daemon owns grant lifetime (issue before Acquire, revoke on Release); the
// transport implementation (staterpc.GrantIssuer) owns token mechanics.
type StateStoreGrantIssuer interface {
	Issue(task *workflow.Task) (token string, revoke func(), err error)
}

// NATSEndpoint is the broker address and credential archied connected with at
// startup. It is runtime state rather than configuration: embedded mode
// generates both values, and external secret references are resolved once.
type NATSEndpoint struct {
	URL   string
	Token string
}

// StateStoreEndpoint is the State Store gRPC target (and the bearer token the
// daemon presents, when one is configured) that containerEnv injects as
// STATE_STORE_URL / STATE_STORE_TOKEN so archie-agent authenticates to the
// same remote archie-state-store service the daemon dials
// (docs/prds/state-store-contract.md §6, §9, §10). After the in-process
// serving path is deleted (docs/prds/state-store-contract.md §12 step 7) this
// is always the configured [services.state].target, never a daemon-owned
// listener.
type StateStoreEndpoint struct {
	URL   string
	Token string
}

type Daemon struct {
	// Cfg publishes the running configuration. Read through Cfg() so a
	// reload swaps the published snapshot atomically. See config.Holder.
	Cfg *config.Holder
	// ConnectedNATS is the endpoint and credential the daemon's own client
	// connected with at startup. Container env is built from this, not from
	// live config or environment, so reload cannot point new containers at a
	// broker the daemon is not publishing on. The zero value is invalid in
	// production composition and remains useful only to fail closed in tests.
	ConnectedNATS NATSEndpoint
	// ConnectedStateStore is the State Store gRPC target (and token) the
	// daemon's own client dialed at startup. containerEnv injects these as
	// STATE_STORE_URL / STATE_STORE_TOKEN so archie-agent reaches the same
	// remote archie-state-store service; it is always the configured
	// [services.state].target after the in-process serving path is deleted.
	ConnectedStateStore StateStoreEndpoint
	Store               storecontract.TaskStore
	// Mappings persists payload field mappings (docs/prds/payload-field-mapping.md).
	// Used by the binding dispatch loop to resolve capture bodies against
	// the mapping a binding names. Optional: nil disables the binding
	// dispatch loop (legacy behaviour).
	Mappings storecontract.MappingStore
	// Bindings persists playbook bindings (docs/prds/playbook-binding.md).
	// Optional: nil disables the binding dispatch loop (legacy behaviour).
	Bindings storecontract.BindingStore
	// BindingDispatcher is the dispatch-time helper surface for bindings:
	// list undispatched captures, look up armed bindings by source for the
	// matcher, and write the at-most-once dedup ledger row. Split from
	// BindingStore to keep the CRUD interface under the interfacebloat
	// limit. Optional: nil disables the dispatch loop.
	BindingDispatcher storecontract.BindingDispatcher
	// MappingMatches counts each event a mapping resolved at dispatch.
	// Optional: nil leaves match counts unrecorded.
	MappingMatches storecontract.MappingMatchRecorder
	// BindingTaskCreator enqueues a task triggered by a binding. Split
	// off TaskLifecycle so the lifecycle surface stays narrow and the
	// dispatch loop does not acquire the full task-creation contract.
	// Optional: nil disables the dispatch loop.
	BindingTaskCreator storecontract.BindingTaskCreator
	Forge              forge.Forge
	Trees              *worktree.Manager
	Bus                *events.Bus
	Log                *slog.Logger
	// Tasks is the NATS task distribution bus: the poller publishes
	// discovered work over it and runViaAgent requests execution through it.
	// NATS startup is mandatory (there is no broker-off execution mode), so
	// production composition always sets it. Nil appears only in tests and
	// is handled fail-closed at the publish/run sites.
	Tasks TaskBus
	// reactionConsumer is built lazily by drainReactions and kept for its
	// lifetime drop counter.
	reactionConsumer *reactionConsumer
	// reactionPublisher is built lazily by scanPRReviews; an indirection
	// only so tests can swap the delivery without the bus.
	reactionPublisher ReactionPublisher
	// Reactions pulls the review-reaction fan-out stream
	// (docs/prds/pr-review-remediation.md). Each cycle it is drained into
	// queued remediate runs before the task drain, so a reaction can be
	// claimed in the same pass that carried it. Optional: nil disables the
	// reaction consumer (tests, or a deployment without the reaction
	// stream).
	Reactions      ReactionSource
	WorktreeGrants WorktreeGrantIssuer
	// StateStoreGrants issues per-task State Store credentials for container
	// env (see containerEnv). Required whenever ConnectedStateStore.URL is
	// set -- acquireTaskContainer parks the task rather than fall back to
	// forwarding the daemon's own administrative token.
	StateStoreGrants StateStoreGrantIssuer
	// TaskRunReadyTimeout bounds how long runViaAgent retries an initial
	// taskrun request that fails with nats.ErrNoResponders, giving a
	// freshly spawned archie-agent container time to connect to NATS, set
	// up its task-scoped core-NATS subscription, and become reachable before the daemon
	// gives up and parks the task. ContainerPool.Acquire returns as soon
	// as Docker has issued the start syscall, not once that setup
	// finishes, so without this bound the very first request after a
	// container spawn fails deterministically. Zero uses
	// defaultTaskRunReadyTimeout.
	TaskRunReadyTimeout time.Duration
	// TaskRunRetryBackoff is the delay between retry attempts within
	// TaskRunReadyTimeout. Zero uses defaultTaskRunRetryBackoff.
	TaskRunRetryBackoff time.Duration
	// AgentStatus records the version/install-type the most recently
	// completed archie-agent task response reported about itself, for
	// releaseupdate.Report.Verify to check an update claim against. Nil
	// disables observation -- runViaAgent skips the Observe call rather
	// than reporting the agent as unverifiable via a zero value, which
	// would read as a checked "unknown" rather than "never wired".
	AgentStatus *AgentStatus

	// ContainerPool manages Docker container lifecycle. Nil means autonomous
	// execution is unavailable and process parks tasks; every runnable task gets
	// a fresh container.
	ContainerPool *container.Pool
	// Storage is the pluggable storage backend for container mounts.
	// A runnable task requires it; acquireTaskContainer parks on nil.
	Storage storage.Backend
	// CapabilityHost owns validated plugin manifests and cross-family
	// lifecycle. Typed capability registries remain in their domain packages;
	// the host never exposes daemon internals or an untyped service locator.
	CapabilityHost *plugin.Host
	// Curators is the curator engine family registry (epic archie-core-yp9).
	// The runtime loop (archie-core-89x) and reference curators (i7i, gs8)
	// are driven through it; nil when the family is not composed.
	Curators *curator.Registry
	// Identities holds per-identity runner state for multi-identity mode.
	// When non-empty, Run() starts one goroutine per identity instead of
	// using the single-identity Forge/Trees/Cfg.Repos path.
	Identities []*IdentityRunner
	// IdentityRepository is the durable lifecycle authority. Polling checks it
	// every cycle so suspend/reactivate commands do not require a restart.
	IdentityRepository identity.Repository
	RootIdentityID     identity.IdentityID

	// Guardrails is the tool-call guardrail engine, wired by the composition
	// root. When non-nil, tool successes and failures are recorded and
	// warnings/hard-stops are issued per the configured thresholds. Nil
	// means guardrails are disabled (backward compatible).
	Guardrails *tools.GuardrailEngine

	// KindWorkflows and LabelWorkflows are the resolved kind/label ->
	// workflow-name routing bindings loaded at startup (WorkflowRoutingFile,
	// WorkflowLabelsFile, PlaybookDirs). They travel in taskrun.Request so
	// the archie-agent process that calls workflow.Route applies the same
	// bindings the daemon loaded; nil means built-in defaults.
	KindWorkflows  workflow.KindWorkflows
	LabelWorkflows workflow.LabelWorkflows
	// Playbooks is the loaded EDA playbook set (workflow and action
	// playbooks, internal/domain/eda/playbook). It is consulted before the
	// kind/label bindings when a task's workflow definition is pinned: a
	// workflow playbook is an operator's explicit rule for one trigger, where
	// the bindings are a table of defaults. Nil means no playbooks are loaded
	// and routing is exactly the binding behaviour.
	Playbooks interface {
		Dispatch(playbook.DispatchInput) (playbook.Decision, bool)
		Run(context.Context, storecontract.PlaybookDispatcher, *slog.Logger, playbook.DispatchInput) error
	}
	// PlaybookLedger is the at-most-once gate action playbooks run through.
	// Nil means action playbooks do not run: they never fire unguarded.
	PlaybookLedger storecontract.PlaybookDispatcher

	// WorkflowDefinitions supplies the active database definitions. A task is
	// pinned once before dispatch; retries reuse the task's stored YAML.
	WorkflowDefinitions interface {
		WorkflowDefinitions(context.Context) (workflow.WorkflowDefinitionCollection, int64, error)
	}

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

	// lastPollAt is when the most recent poll pass began, in Unix
	// nanoseconds (zero = no pass has started yet), read by
	// LastPollAt. Atomic because runIdentities runs one poll goroutine per
	// identity and they all stamp this one "is the poller alive" reading.
	lastPollAt atomic.Int64
}

// IdentityRunner bundles identity-specific state for a single agent
// identity running within a multi-identity daemon. Each identity gets its
// own forge client, worktree manager, repo list, and config  --  but shares
// the store, NATS connection, container pool, and event bus with siblings.
type IdentityRunner struct {
	ID    identity.IdentityID
	Name  string
	Forge forge.Forge
	Trees *worktree.Manager
	Repos []config.Repo
	// Cfg is the identity-scoped config subset (forge, models, dispatch,
	// budgets, etc.).
	Cfg config.IdentityConfig
	Log *slog.Logger
}

// NewIdentityRunner constructs an IdentityRunner from an identity config
// and a pre-built forge client. The caller owns forge and trees lifecycle;
// IdentityRunner just holds references.
func NewIdentityRunner(ctx context.Context, idCfg config.IdentityConfig, fg forge.Forge, trees *worktree.Manager, log *slog.Logger) (*IdentityRunner, error) {
	if idCfg.Name == "" {
		return nil, fmt.Errorf("identity name is required")
	}
	return &IdentityRunner{
		ID:    identity.StableID(idCfg.Name),
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

	d.sweepAccess(ctx)
	return nil
}

// sweepTimeout bounds one forge's boot sweep. Startup gates the gateways and
// the run loop, and a forge client carries no request deadline of its own, so
// an unreachable host would otherwise hold boot open indefinitely.
const sweepTimeout = 2 * time.Minute

// sweepAccess accepts pending invitations and verifies push access for the
// root forge and for every configured identity's forge.
//
// The root is swept even in multi-identity mode. Run takes runIdentities
// there and never polls the root repo list, but polling is not the only way a
// task reaches a repo: dispatchBinding enqueues with an empty identity and
// resolveBindingRepo falls back to the root repo list, and forgeFor/repoFor
// resolve an empty identity to the root forge and root repos. Root targets
// stay reachable, so they stay worth verifying; a deployment with no root
// forge configured gets the noop client, whose sweep is silent.
//
// Each forge is swept in its own goroutine under its own deadline, mirroring
// Run's goroutine-per-identity isolation. Warnings alone isolate a failing
// sibling; they do not isolate a stalled one.
func (d *Daemon) sweepAccess(ctx context.Context) {
	sweep := func(identity string, fg forge.Forge, repos []config.Repo) {
		log := d.Log
		if identity != "" {
			log = log.With("identity", identity)
		}
		ctx, cancel := context.WithTimeout(ctx, sweepTimeout)
		defer cancel()
		if err := fg.AcceptInvitations(ctx); err != nil {
			log.Warn("invitation sweep failed", "err", err)
		}
		for _, r := range repos {
			if err := fg.VerifyPush(ctx, r.Owner, r.Name); err != nil {
				// A sweep cut short by its own context is not a repo failure: name
				// the budget when it expired, stay quiet on a shutdown.
				switch {
				case errors.Is(ctx.Err(), context.DeadlineExceeded):
					// Every later call would return the same error, so say it once.
					log.Warn("push check abandoned: sweep budget exhausted", "repo", r.FullName(), "err", err)
					return
				case ctx.Err() != nil:
					return
				}
				log.Warn("repo not pushable  --  tasks from it will fail", "repo", r.FullName(), "err", err)
			}
		}
	}

	var wg sync.WaitGroup
	wg.Add(1 + len(d.Identities))
	go func() {
		defer wg.Done()
		sweep("", d.Forge, d.Cfg.Get().Repos)
	}()
	for _, id := range d.Identities {
		go func() {
			defer wg.Done()
			sweep(id.Name, id.Forge, id.Repos)
		}()
	}
	wg.Wait()
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
	if !d.identityActive(ctx, id.ID) {
		return
	}
	d.markPoll()
	cfg := configForIdentity(d.Cfg.Get(), id.Cfg)
	for _, repo := range id.Repos {
		issues := d.pollIssuesWithConfig(ctx, id.Forge, cfg, repo)
		for _, is := range issues {
			labels := strings.Join(is.Labels, ",")
			if d.Tasks != nil {
				d.pollNATS(ctx, id.Forge, cfg, repo, is, labels, string(id.ID))
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
	d.scanPRReviews(ctx)
	d.dispatchBindings(ctx)
	d.drainReactions(ctx)
	if d.Tasks != nil {
		d.drainNATS(ctx)
	}
}

// pollIssuesWithConfig discovers work for one repo using the given forge
// client and dispatch config. Single-identity mode passes d.Forge/d.Cfg;
// identity poll loops pass the identity's own forge and configForIdentity,
// so each identity polls with its own bot user, label, and trigger.
func (d *Daemon) pollIssuesWithConfig(ctx context.Context, fg forge.Forge, cfg config.Config, repo config.Repo) []forge.Issue {
	switch cfg.Dispatch.Trigger {
	case "label":
		// Empty-label rule shared with workintake.MatchesDispatch: a label
		// trigger needs a non-empty label or it would match every open issue.
		if workintake.RequiresLabel(cfg.Dispatch.Trigger) && cfg.Label == "" {
			d.Log.Error("label trigger configured with an empty label; refusing to poll (an empty label matches every open issue)", "repo", repo.FullName())
			return nil
		}
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
	if workintake.RequiresLabel(cfg.Dispatch.Trigger) && cfg.Label == "" {
		d.Log.Error("either trigger configured with an empty label; skipping the label poll (an empty label matches every open issue)", "repo", repo.FullName())
		return out
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
// reconcile open PRs, run the binding dispatch loop, then process
// queued tasks concurrently up to containers.max_concurrency.
func (d *Daemon) Cycle(ctx context.Context) {
	d.cleanupExpiredStorage(ctx)
	d.poll(ctx)
	d.reconcilePRs(ctx)
	d.scanPRReviews(ctx)
	d.dispatchBindings(ctx)
	d.drainReactions(ctx)
	if d.Tasks != nil {
		d.drainNATS(ctx)
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

// bindingDispatchBatchLimit caps how many undispatched captures a single
// dispatchBindings pass will fetch from the store. The cap is defensive
// (the captures table is also pruned by retention/maxEvents) and bounds a
// single cycle's worst-case latency on a backlog.
const bindingDispatchBatchLimit = 100

// dispatchBindings walks identified captures on sources with an armed
// binding and offers each capture to every armed binding on its source.
// Each binding dispatches it at most once (dispatchOneBinding).
//
// Nil Bindings, BindingDispatcher or BindingTaskCreator disables the loop.
func (d *Daemon) dispatchBindings(ctx context.Context) {
	if d.Bindings == nil || d.BindingDispatcher == nil || d.BindingTaskCreator == nil {
		return
	}

	// Gather armed sources first so the captures query filters down to
	// only relevant rows.
	bindings, err := d.Bindings.ListBindings(ctx)
	if err != nil {
		d.Log.Warn("list bindings failed", "error", err)
		return
	}
	sources := make([]string, 0, len(bindings))
	for _, b := range bindings {
		if b.Status == binding.StatusArmed && b.Matcher.Source != "" {
			sources = append(sources, b.Matcher.Source)
		}
	}
	if len(sources) == 0 {
		return
	}

	captures, err := d.BindingDispatcher.ListUndispatchedCaptures(ctx, sources, bindingDispatchBatchLimit)
	if err != nil {
		d.Log.Warn("list undispatched captures failed", "error", err)
		return
	}
	workflows, err := d.activeWorkflows(ctx)
	if err != nil {
		d.Log.Warn("binding dispatch: workflow definitions unavailable", "error", err)
		return
	}
	for _, c := range captures {
		armed, err := d.BindingDispatcher.ArmedBindingsForSource(ctx, c.Source)
		if err != nil {
			d.Log.Warn("armed bindings lookup", "source", c.Source, "error", err)
			continue
		}
		for _, b := range armed {
			if !b.Matcher.Matches(c.Source, c.Dispatchable()) {
				continue
			}
			d.dispatchOneBinding(ctx, b, c, workflows)
		}
	}
}

// bindingMapping returns b's mapping, or nil after logging why it is
// unavailable.
func (d *Daemon) bindingMapping(ctx context.Context, b binding.Binding) *mapping.Mapping {
	if d.Mappings == nil {
		d.Log.Warn("binding dispatch: mapping store unavailable", "binding", b.ID)
		return nil
	}
	m, err := d.Mappings.GetMapping(ctx, b.MappingID)
	if err != nil {
		d.Log.Warn("binding dispatch: get mapping", "binding", b.ID, "mapping", b.MappingID, "error", err)
		return nil
	}
	if m == nil {
		d.Log.Warn("binding dispatch: mapping missing", "binding", b.ID, "mapping", b.MappingID)
	}
	return m
}

// dispatchOneBinding offers one capture to one binding. The binding applies
// when its mapping belongs to the capture's event type. The mapping resolves
// the payload (a required-field failure records binding_dispatch_failure and
// stops), counts the match, and the binding's filter must admit the resolved
// parameters. The workflow it targets must accept the binding's inputs and
// repository (resolveBindingTarget). The dispatch is then claimed in the binding_dispatches ledger
// before the task is enqueued: a capture stays listed until every binding for
// its event type has dispatched it, so the claim is what stops a binding that
// already fired from firing again on a later cycle. A failed enqueue after the
// claim loses that dispatch, which is the at-most-once side of the trade.
func (d *Daemon) dispatchOneBinding(ctx context.Context, b binding.Binding, c storecontract.CapturedEvent, workflows workflow.WorkflowDefinitionCollection) {
	m := d.bindingMapping(ctx, b)
	if m == nil || m.EventTypeID != c.EventType {
		return
	}
	values, failures := mapping.Resolve(m.Fields, []byte(c.Body))
	if hasBlockingFailure(m.Fields, failures) {
		d.recordDispatchFailure(ctx, b, c, "required field failed", map[string]any{"failures": failures})
		return
	}
	if d.MappingMatches != nil {
		if err := d.MappingMatches.RecordMappingMatch(ctx, m.ID, c.ID); err != nil {
			d.Log.Warn("binding dispatch: record mapping match", "mapping", m.ID, "capture", c.ID, "error", err)
		}
	}
	filter, err := binding.CompileFilter(b.Filter, m.Fields)
	if err != nil {
		d.recordDispatchFailure(ctx, b, c, "filter does not compile", map[string]any{"error": err.Error()})
		return
	}
	if admitted, _ := filter.Admits(values); !admitted {
		return
	}

	target, reason, ok := d.resolveBindingTarget(b, values, workflows)
	if reason != "" {
		d.recordDispatchFailure(ctx, b, c, reason, nil)
	}
	if !ok {
		return
	}
	if err := d.BindingDispatcher.RecordDispatch(ctx, b.ID, int64(b.Version), c.ID, 0); err != nil {
		if !errors.Is(err, storecontract.ErrAlreadyDispatched) {
			d.Log.Warn("binding dispatch: record", "binding", b.ID, "capture", c.ID, "error", err)
		}
		return
	}
	title := fmt.Sprintf("binding %s/%d from %s", b.Name, b.Version, c.Source)
	body := renderBindingBody(values, c)
	if target.declared {
		body = fmt.Sprintf("Started by binding %q from capture %q; the workflow's inputs travel with the task.", b.Name, c.ID)
	}
	task, err := d.BindingTaskCreator.EnqueueBindingTask(ctx, target.owner, target.repo, title, body, b.Workflow, "", b.ID, b.Version, target.inputs)
	if err != nil {
		d.Log.Warn("binding dispatch: enqueue", "binding", b.ID, "capture", c.ID, "error", err)
		return
	}
	if c.Unsigned {
		d.markUnsignedStart(ctx, task.ID, b, c)
	}
}

// markUnsignedStart records on a task's timeline that an unsigned event
// started it.
func (d *Daemon) markUnsignedStart(ctx context.Context, taskID int64, b binding.Binding, c storecontract.CapturedEvent) {
	if _, err := d.Store.InsertEvent(ctx, events.Event{
		TaskID: taskID,
		Kind:   events.KindUnsignedEvent,
		Detail: fmt.Sprintf("started by an unsigned event from source %q", c.Source),
		Data:   map[string]any{"source": c.Source, "capture_id": c.ID, "binding_id": b.ID},
	}); err != nil {
		d.Log.Warn("binding dispatch: unsigned marker", "task", taskID, "error", err)
	}
}

// recordDispatchFailure records why a binding did not dispatch a capture.
func (d *Daemon) recordDispatchFailure(ctx context.Context, b binding.Binding, c storecontract.CapturedEvent, reason string, extra map[string]any) {
	data := map[string]any{
		"binding_id":      b.ID,
		"binding_version": b.Version,
		"capture_id":      c.ID,
	}
	maps.Copy(data, extra)
	_, _ = d.Store.InsertEvent(ctx, events.Event{
		Kind:   "binding_dispatch_failure",
		Detail: fmt.Sprintf("binding %q (capture %q): %s", b.ID, c.ID, reason),
		Data:   data,
	})
}

// hasBlockingFailure reports whether any of the failures belongs to a
// required field. The mapping package reports failures only by field
// name and reason -- the Field's Required flag is on the Field
// declaration, so the dispatch loop re-derives it from the full
// Field list. Failures on optional fields are warnings, not blocks:
// the workflow still receives a partial value map.
func hasBlockingFailure(fields []mapping.Field, failures []mapping.Failure) bool {
	if len(failures) == 0 {
		return false
	}
	required := make(map[string]bool, len(fields))
	for _, f := range fields {
		if f.Required {
			required[f.Name] = true
		}
	}
	for _, fail := range failures {
		if required[fail.FieldName] {
			return true
		}
	}
	return false
}

// renderBindingBody builds the task body from the values a mapping
// resolved, one "key=value" pair per line. Empty maps still produce
// a body that records the source capture id so the workflow has
// provenance to hand back to the operator.
func renderBindingBody(values map[string]any, c storecontract.CapturedEvent) string {
	if len(values) == 0 {
		return fmt.Sprintf("(no fields resolved from capture %q)", c.ID)
	}
	parts := make([]string, 0, len(values))
	for k, v := range values {
		parts = append(parts, fmt.Sprintf("%s=%v", k, v))
	}
	return strings.Join(parts, "\n")
}

// resolveBindingRepo picks the owner/repo a binding-spawned task
// operates against. A binding that pins Owner/Repo (binding.Binding's
// Owner/Repo fields, validated as a complete pair or not set at all)
// dispatches to that target unconditionally -- multi-repo deployments
// use this to disambiguate. An unpinned binding falls back to the
// single-configured-repo behaviour that predates the pin: with exactly
// one configured repo the choice is unambiguous; with zero or more than
// one the loop refuses to dispatch (logged below) so a binding cannot
// silently fire against the wrong target.
func (d *Daemon) resolveBindingRepo(b binding.Binding) (string, string, bool) {
	if b.Owner != "" && b.Repo != "" {
		return b.Owner, b.Repo, true
	}
	repos := d.Cfg.Get().Repos
	if len(repos) == 1 {
		return repos[0].Owner, repos[0].Name, true
	}
	d.Log.Warn("binding dispatch: cannot resolve target repo",
		"hint", "configure exactly one [[repos]] entry, or pin the binding to a specific owner/repo",
		"configured_repos", len(repos))
	return "", "", false
}

// drainReactions turns available review reactions into queued remediate
// runs. It runs before the task drain so a reaction queued in this pass can
// be claimed by it; the consumer is built lazily once, so its drop counter
// spans the process lifetime.
func (d *Daemon) drainReactions(ctx context.Context) {
	if d.Reactions == nil || d.Store == nil {
		return
	}
	if d.reactionConsumer == nil {
		d.reactionConsumer = newReactionConsumer(
			// The store's owned-PR lookup is the authorization boundary: a
			// reaction only ever causes work against a PR archie opened and
			// still owns (pr-review-remediation.md decision 3).
			d.Store,
			d.Store,
			d.botUserForTask,
			d.Log,
		)
	}
	if _, err := d.reactionConsumer.drain(ctx, d.Reactions, maxReactionsPerCycle); err != nil {
		d.Log.Error("reaction drain failed", "err", err)
	}
}

// botUserForTask resolves the bot login that owns task, so the reaction
// consumer can exclude Archie's own comments. Identity-derived for
// multi-identity deployments, the global forge bot user otherwise.
func (d *Daemon) botUserForTask(task *workflowtask.Task) string {
	if runner := d.identityFor(task); runner != nil {
		return runner.Cfg.BotUser
	}
	return d.Cfg.Get().BotUser
}

// drainNATS processes tasks from NATS, falling back to the task store's ClaimNext for
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
	task := &workflow.Task{Owner: envelope.Owner, Repo: envelope.Repo, Identity: envelope.Identity}
	dispatcher.Submit(ctx, task, func(ctx context.Context, _ *workflow.Task) {
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
	allowConcurrent func(task *workflow.Task) bool

	mu       sync.Mutex
	repoTail map[string]chan struct{}
	wg       sync.WaitGroup
}

func newTaskDispatcher(maxConcurrency int, allowConcurrent func(task *workflow.Task) bool) *taskDispatcher {
	var slots chan struct{}
	if maxConcurrency > 0 {
		slots = make(chan struct{}, maxConcurrency)
	}
	if allowConcurrent == nil {
		allowConcurrent = func(*workflow.Task) bool { return false }
	}
	return &taskDispatcher{
		slots:           slots,
		allowConcurrent: allowConcurrent,
		repoTail:        make(map[string]chan struct{}),
	}
}

func (d *taskDispatcher) Submit(
	ctx context.Context,
	task *workflow.Task,
	process func(context.Context, *workflow.Task),
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
	if !d.identityActive(ctx, d.RootIdentityID) {
		return
	}
	d.markPoll()
	for _, repo := range d.Cfg.Get().Repos {
		issues := d.pollIssues(ctx, repo)
		for _, is := range issues {
			labels := strings.Join(is.Labels, ",")
			if d.Tasks != nil {
				d.pollNATS(ctx, d.Forge, d.Cfg.Get(), repo, is, labels, "")
			}
		}
	}
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
	if err := d.PublishTask(ctx, task); err != nil {
		d.Log.Error("task publish failed", "repo", repo.FullName(), "issue", is.Number, "err", err)
		return
	}
	d.acknowledge(ctx, fg, cfg, repo, is)
}

// PublishTask validates, encodes and publishes a task envelope. The
// idempotency key means rediscovering the same issue on a later poll does not
// enqueue the work twice. It is the single enqueue path shared by the poller
// and the forge webhook intake, so neither can drift in how a discovered issue
// becomes a task.
func (d *Daemon) PublishTask(ctx context.Context, task workintake.TaskEnvelope) error {
	if d.Tasks == nil {
		return fmt.Errorf("publish task %s: no task bus configured", task.Ref())
	}
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

	if ok, reason, _ := d.identityMayAct(ctx, tm.Identity); !ok {
		d.Log.Error("nats intake rejected: "+reason, "subject", msg.Subject())
		if err := msg.Ack(); err != nil {
			d.Log.Warn("ack failed", "err", err)
		}
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
func (d *Daemon) closeResolvedIssue(ctx context.Context, fg forge.Forge, task *workflow.Task, comment string) {
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
		// OpenPRs returns a deliberately narrow projection of a pr_open task
		// (id/owner/repo/issue/pr/status/source/identity) that does not carry
		// the attempt, so the outcome event below reads it from the task's own
		// row. A failed lookup costs attribution and never the merge handling:
		// the status transition and the worktree cleanup still run. OpenPRs
		// carries attempt, so these events are attributed without a second
		// read per open PR on every reconcile tick.
		attempt := t.Attempt
		switch state {
		case "merged":
			_ = d.Store.Transition(ctx, t.ID, workflow.StatusPROpen, workflow.StatusMerged, "")
			_ = trees.Cleanup(t.Owner, t.Repo, t.IssueNumber)
			// Close the issue ourselves rather than relying on the forge
			// noticing a "Closes #N" in the PR body. Nothing else closes it:
			// LinkBranch is sidebar linkage on Gitea and a no-op on GitHub.
			// Left open the issue stays labelled and assigned, the next poll
			// re-enqueues it, and once the dashboard's Clear removes the task
			// row that is a second implementation and a second PR for work
			// already merged.
			d.closeResolvedIssue(ctx, fg, &t, fmt.Sprintf(
				"Closed by #%d, merged by archie.", t.PRNumber,
			))
			d.Log.Info("PR merged", "repo", t.Owner+"/"+t.Repo, "pr", t.PRNumber)
			d.emit(events.Event{
				Kind: events.KindPRMerged, TaskID: t.ID, Attempt: attempt,
				Repo: t.Owner + "/" + t.Repo, Issue: t.IssueNumber,
				Data: map[string]any{"pr": t.PRNumber},
			})
		case "closed":
			_ = d.Store.Transition(ctx, t.ID, workflow.StatusPROpen, workflow.StatusRejected, "PR closed without merge")
			_ = trees.Cleanup(t.Owner, t.Repo, t.IssueNumber)
			d.Log.Info("PR rejected", "repo", t.Owner+"/"+t.Repo, "pr", t.PRNumber)
			d.emit(events.Event{
				Kind: events.KindPRRejected, TaskID: t.ID, Attempt: attempt,
				Repo: t.Owner + "/" + t.Repo, Issue: t.IssueNumber,
				Data: map[string]any{"pr": t.PRNumber},
			})
		}
	}
}

func (d *Daemon) process(ctx context.Context, task *workflow.Task) {
	// Register before any work starts so the task is stoppable for its
	// whole life, including the slow setup -- clone, worktree prepare,
	// image pull -- which is exactly when someone realises they asked for
	// the wrong thing. Every caller reaches execution through here, so
	// this is the one place that has to do it.
	ctx, finished := d.running.begin(ctx, task.ID, task.Identity)
	defer finished()
	defer d.openTaskLog(task)()

	if d.parkIfIdentityUnresolvable(ctx, task) {
		return
	}

	trees := d.treesFor(task)
	repo, ok := d.repoFor(task)
	if !ok && task.HasRepository() {
		d.parkRunningTask(ctx, task.ID, "repo no longer in config", taskstate.ParkTerminal)
		return
	}
	if d.ContainerPool == nil || d.Tasks == nil {
		d.parkCapabilityUnavailable(ctx, task, repo)
		return
	}

	d.Log.Info("processing task", "repo", repo.FullName(), "issue", task.IssueNumber, "attempt", task.Attempt)
	workDir, ok := d.prepareWorkspace(ctx, task, trees, repo)
	if !ok {
		return
	}
	if !task.HasRepository() {
		defer func() {
			if err := trees.RemoveScratch(task.ID); err != nil {
				d.Log.Warn("scratch workspace cleanup failed", "task", task.ID, "err", err)
			}
		}()
	}

	// The workflow is pinned before the container starts, because the
	// profile it names decides the container's image.
	profile, ok := d.pinTaskProfile(ctx, task)
	if !ok {
		return
	}
	ctr, revokeStateStoreGrant, ok := d.acquireTaskContainer(ctx, task, repo, workDir, profile.Image)
	if !ok {
		return
	}
	defer d.ContainerPool.Release(ctx, ctr)
	defer revokeStateStoreGrant()

	// Hand the whole task to archie-agent in one NATS round trip. archie-agent
	// proxies Store/Forge/worktree-push calls back to archied over storerpc/
	// forgerpc/worktreerpc  --  by the time runViaAgent returns, the task's
	// terminal state already landed via those calls. The task's identity
	// travels in the taskrun request; the agent scopes its forgerpc and
	// worktreerpc clients to that identity, and the daemon registered one
	// server pair per identity (plus the root pair), so a container-mode
	// task is always served by its own forge client and worktree manager.
	runCtx, stopWatch := withContainerExit(ctx, ctr.Exited())
	d.runViaAgent(runCtx, task, repo, profile)
	stopWatch()

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

	d.cleanupTerminalTaskWorktree(ctx, task, trees)
}

// prepareWorkspace makes the directory the container binds as its
// workspace, which must exist before Acquire: a fresh clone of the task's
// repository, or an empty scratch directory for a task with none. It parks
// the task and reports false when it cannot.
func (d *Daemon) prepareWorkspace(ctx context.Context, task *workflow.Task, trees *worktree.Manager, repo config.Repo) (string, bool) {
	if !task.HasRepository() {
		dir, err := trees.PrepareScratch(task.ID)
		if err != nil {
			d.Log.Error("scratch workspace prepare failed", "err", err)
			d.parkRunningTask(ctx, task.ID, "scratch workspace prepare failed: "+err.Error(), taskstate.ParkTransient)
			return "", false
		}
		return dir, true
	}
	// Every task gets an independent full clone. The former
	// PreparePersistent path shared objects with a per-repo bare cache;
	// go-git has no --dissociate, so a shared cache would stay a live
	// dependency of each worktree and expiring one would corrupt running
	// tasks. repo.PersistentStorage still governs the container volume.
	dir, branch, err := trees.Prepare(ctx, task.Owner, task.Repo, repo.BaseBranch(), task.IssueNumber, task.Title, task.Body, task.Labels)
	if err != nil {
		d.Log.Error("worktree prepare failed", "err", err)
		d.parkRunningTask(ctx, task.ID, "worktree prepare failed: "+err.Error(), taskstate.ParkTransient)
		return "", false
	}
	task.Branch = branch
	if err := d.Store.Update(ctx, task); err != nil {
		d.Log.Warn("task branch not persisted", "task", task.ID, "err", err)
	}
	return dir, true
}

func (d *Daemon) cleanupTerminalTaskWorktree(ctx context.Context, task *workflow.Task, trees *worktree.Manager) {
	if d.Store == nil || trees == nil || task == nil || !task.HasRepository() {
		return
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()

	latest, err := d.Store.TaskByID(cleanupCtx, task.ID)
	if err != nil {
		d.Log.Warn("terminal worktree cleanup task lookup failed", "task", task.ID, "err", err)
		return
	}
	if latest == nil {
		return
	}
	switch latest.Status {
	case workflow.StatusMerged, workflow.StatusRejected, workflow.StatusClosedWontDo:
		if latest.PRNumber == 0 {
			if err := trees.Cleanup(task.Owner, task.Repo, task.IssueNumber); err != nil {
				d.Log.Warn("terminal worktree cleanup failed", "task", task.ID, "err", err)
			}
		}
	}
}

// openTaskLog opens task's log sink for the lifetime of one process() call
// and returns the function to close it. That lifetime covers preparation,
// the full-task container handoff, and teardown. A failure to open is not
// fatal to the task -- it means this
// attempt's own output won't be recoverable if something goes wrong, which
// is worse than the pre-existing state, not equal to it, so the run
// proceeds rather than parking over a logging problem.
func (d *Daemon) openTaskLog(task *workflow.Task) func() {
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
	task *workflow.Task,
	repo config.Repo,
	workDir string,
	image string,
) (*container.Container, func(), bool) {
	park := func(reason string, err error) {
		d.Log.Error(reason, "err", err)
		d.parkRunningTask(ctx, task.ID, reason+": "+err.Error(), taskstate.ParkTransient)
	}

	// Write task.json  --  the container's boot-time brief.
	if err := container.WriteTaskJSON(workDir, container.TaskPayload{
		ID: task.ID, Owner: task.Owner, Repo: task.Repo,
		Number: task.IssueNumber, Title: task.Title, Body: task.Body,
		Labels:   strings.Split(task.Labels, ","),
		Workflow: task.Workflow, Branch: task.Branch, Plan: task.Plan,
		Inputs: task.Inputs,
	}); err != nil {
		park("task.json write failed", err)
		return nil, nil, false
	}

	// Guard: Storage may be nil if the daemon was wired incorrectly. In
	// normal operation, Storage is always set when ContainerPool is set.
	if d.Storage == nil {
		d.Log.Error("storage backend is nil  --  cannot acquire container")
		d.parkRunningTask(ctx, task.ID, "storage backend not configured", taskstate.ParkTransient)
		return nil, nil, false
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
		return nil, nil, false
	}

	stateStoreToken, revokeStateStoreGrant, err := d.stateStoreGrantToken(task)
	if err != nil {
		if terr := d.Storage.Teardown(ctx, storage.TaskRef{
			WorktreeDir:       workDir,
			Ecosystem:         repo.Ecosystem,
			PersistentStorage: repo.PersistentStorage,
			Owner:             task.Owner,
			Repo:              task.Repo,
		}); terr != nil {
			d.Log.Warn("storage teardown after state store grant failure failed", "err", terr)
		}
		park("state store grant failed", err)
		return nil, nil, false
	}

	ctr, err := d.ContainerPool.Acquire(ctx, image, mounts, d.containerEnv(task, stateStoreToken))
	if err != nil {
		revokeStateStoreGrant()
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
		return nil, nil, false
	}
	return ctr, revokeStateStoreGrant, true
}

// parkCapabilityUnavailable parks a task whose daemon-side capability is
// missing (container pool or task transport): both are environmental, so
// the park class is transient and the KindParked event carries the class
// for the dashboard. The reason distinguishes the two in logs and events.
func (d *Daemon) parkCapabilityUnavailable(ctx context.Context, task *workflow.Task, repo config.Repo) {
	var reason string
	if d.ContainerPool == nil {
		reason = "managed agent container pool is unavailable; refusing to run this task on the host"
		d.Log.Error("task parked: managed worker unavailable", "task", task.ID,
			"hint", "enable containers and make the archie-agent image available, then retry the task")
	} else {
		reason = "agent task transport is unavailable; refusing to run this task on the host"
		d.Log.Error("task parked: agent task transport unavailable", "task", task.ID)
	}
	if err := d.Store.ParkTask(ctx, task.ID, workflow.StatusRunning, reason, taskstate.ParkTransient); err != nil {
		d.Log.Warn("capability park transition failed", "task", task.ID, "err", err)
		return
	}
	d.recordPark(ctx, task.ID, reason)
	d.emit(events.Event{
		Kind: events.KindParked, TaskID: task.ID, Attempt: task.Attempt,
		Repo: repo.FullName(), Issue: task.IssueNumber, Detail: reason,
		Data: map[string]any{"park_class": taskstate.ParkTransient},
	})
}

// parkRunningTask transitions a task from running to parked using a context
// detached from caller cancellation, with a bounded timeout.
//
// The park carries the caller's taskstate.ParkClass: every call site states
// what kind of intervention its park needs, and the store normalizes the
// class, so an unclassified caller cannot silently mislabel a park.
// When a task fails or is cancelled (e.g. via /stop or dashboard Stop), ctx is
// already cancelled. Writing terminal state using the cancelled ctx fails
// immediately in SQLite transactions and silently leaves the task row 'running'
// indefinitely. Like container teardown in Pool.Release, terminal state
// recording is cleanup on the way out of a task and must use a bounded context
// that survives cancellation while preserving values.
//
// The transition remains guarded from StatusRunning: if the worker already
// recorded a terminal state over storerpc, storecontract.ErrStaleTransition is returned
// and ignored. Unexpected store errors are logged as warnings.
func (d *Daemon) parkRunningTask(ctx context.Context, taskID int64, reason string, class taskstate.ParkClass) {
	if d.Store == nil {
		return
	}
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()

	if err := d.Store.ParkTask(writeCtx, taskID, workflow.StatusRunning, reason, class); err != nil {
		if !errors.Is(err, storecontract.ErrStaleTransition) {
			d.Log.Warn("terminal park transition failed", "task", taskID, "reason", reason, "err", err)
		}
		return
	}
	// Recorded only once the transition landed. An entry claiming the task
	// parked would be a lie when another actor moved it out of running first
	// -- the same race the parked event is withheld for.
	d.recordPark(ctx, taskID, reason)
}

// recordPark writes one park reason into the task attempt's own log. Every
// place that parks a task calls it, including the two capability parks in
// process() that write their transition directly instead of going through
// parkRunningTask -- a park an operator cannot read the reason for is the same
// defect wherever it happens. Nothing daemon-side used to write to a task log
// at all: the container's system-log subscription was the only writer, so an
// attempt that parked before any container produced output left an empty log
// file and an unexplained park reason.
//
// A task with no open sink (a retry this instance never dispatched, or logging
// unwired) silently drops the entry, which is TaskRegistry.Write's own
// contract.
func (d *Daemon) recordPark(ctx context.Context, taskID int64, reason string) {
	d.TaskLogs.Write(ctx, taskID, logging.Entry{
		Time:    time.Now(),
		Level:   slog.LevelError.String(),
		Message: "task parked: " + reason,
		Fields:  map[string]any{"component": "daemon", "task": taskID},
	})
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
func (d *Daemon) runViaAgent(ctx context.Context, task *workflow.Task, repo config.Repo, profile config.AgentProfile) {
	grant, revoke, ok := d.publicationGrant(ctx, task)
	if !ok {
		return
	}
	defer revoke()
	cfg := d.configFor(task)
	taskCfg := cfg.ForTask()
	d.captureAttemptConfig(ctx, task, taskCfg)
	req := taskrun.Request{
		Task:               task,
		Repo:               repo,
		Cfg:                taskCfg,
		Providers:          agentexec.ProvidersFromConfig(cfg.Providers),
		MCPServers:         cfg.Tools.MCPServers,
		WorktreeGrant:      grant,
		KindWorkflows:      d.KindWorkflows,
		LabelWorkflows:     d.LabelWorkflows,
		WorkflowDefinition: task.WorkflowDefinitionYAML,
		Tools:              profile.Tools,
	}
	data, err := json.Marshal(req)
	if err != nil {
		d.Log.Error("taskrun encode failed", "task", task.ID, "err", err)
		d.parkRunningTask(ctx, task.ID, "taskrun encode failed: "+err.Error(), taskstate.ParkTransient)
		return
	}

	reply, err := d.requestTaskRun(ctx, task.ID, data)
	if err != nil && errors.Is(context.Cause(ctx), errContainerExited) {
		err = errContainerExited
	}
	if err != nil {
		d.Log.Error("taskrun request failed", "task", task.ID, "err", err)
		d.parkRunningTask(ctx, task.ID, "taskrun request failed: "+err.Error(), taskstate.ParkTransient)
		return
	}

	var resp taskrun.Response
	if err := json.Unmarshal(reply, &resp); err != nil {
		d.Log.Error("taskrun decode response failed", "task", task.ID, "err", err)
		d.parkRunningTask(ctx, task.ID, "taskrun decode response failed: "+err.Error(), taskstate.ParkTransient)
		return
	}
	if resp.Error != "" {
		d.Log.Error("taskrun run failed", "task", task.ID, "err", resp.Error)
		d.parkRunningTask(ctx, task.ID, "taskrun run failed: "+resp.Error, taskstate.ParkTransient)
		return
	}

	// Reached only past every failure return above, so this is always a
	// fully decoded, error-free response -- a version is never recorded off
	// a request that never reached an agent, or a response describing a
	// parked/errored task.
	if d.AgentStatus != nil && resp.AgentVersion != "" {
		d.AgentStatus.Observe(resp.AgentVersion, resp.AgentInstallType)
	}

	d.Log.Info("taskrun complete", "task", task.ID, "status", resp.Status)
}

// pinTaskProfile pins the task's workflow definition and resolves the agent
// profile it names, parking the task when either fails. An unconfigured
// profile needs an operator: the fix is configuration, then a retry.
func (d *Daemon) pinTaskProfile(ctx context.Context, task *workflow.Task) (config.AgentProfile, bool) {
	if err := d.pinWorkflowDefinition(ctx, task); err != nil {
		d.Log.Error("pin workflow definition failed", "task", task.ID, "err", err)
		d.parkRunningTask(ctx, task.ID, "pin workflow definition: "+err.Error(), taskstate.ParkTransient)
		return config.AgentProfile{}, false
	}
	iface, err := workflowtask.ParseWorkflowInterface(task.WorkflowDefinitionYAML)
	if err == nil {
		var profile config.AgentProfile
		if profile, err = d.configFor(task).Containers.Profile(iface.Profile); err == nil {
			return profile, true
		}
	}
	d.Log.Error("agent profile unavailable", "task", task.ID, "err", err)
	d.parkRunningTask(ctx, task.ID, "workflow "+task.Workflow+": "+err.Error(), taskstate.ParkNeedsHuman)
	return config.AgentProfile{}, false
}

// publicationGrant issues the capability to publish the task's branch. A task
// with no repository has nothing to publish and gets none.
func (d *Daemon) publicationGrant(ctx context.Context, task *workflow.Task) (string, func(), bool) {
	if !task.HasRepository() {
		return "", func() {}, true
	}
	if d.WorktreeGrants == nil {
		const reason = "worktree publication grants are unavailable"
		d.Log.Error(reason, "task", task.ID)
		d.parkRunningTask(ctx, task.ID, reason, taskstate.ParkTransient)
		return "", nil, false
	}
	grant, revoke, err := d.WorktreeGrants.Issue(task)
	if err != nil {
		d.Log.Error("worktree publication grant failed", "task", task.ID, "err", err)
		d.parkRunningTask(ctx, task.ID, "worktree publication grant failed: "+err.Error(), taskstate.ParkTransient)
		return "", nil, false
	}
	return grant, revoke, true
}

func (d *Daemon) pinWorkflowDefinition(ctx context.Context, task *workflow.Task) error {
	if task.WorkflowDefinitionYAML != "" {
		if workflow.DigestDefinition(task.WorkflowDefinitionYAML) != task.WorkflowDefinitionDigest {
			return fmt.Errorf("stored workflow definition digest mismatch")
		}
		return nil
	}
	d.runActionPlaybooks(ctx, task)
	if d.WorkflowDefinitions == nil {
		return d.pinWorkflowFromCollection(ctx, task, workflow.ShippedDefinitions(), 0)
	}
	collection, version, err := d.WorkflowDefinitions.WorkflowDefinitions(ctx)
	if err != nil {
		return err
	}
	return d.pinWorkflowFromCollection(ctx, task, collection, version)
}

// runActionPlaybooks runs the action playbooks matching a task that is about
// to be pinned, which happens once per task (docs/prds/action-playbook-run.md).
// A run never affects routing and its failure never fails the task. A task
// that already names a workflow is the approval requeue and is skipped, as
// it is for workflow playbooks.
func (d *Daemon) runActionPlaybooks(ctx context.Context, task *workflow.Task) {
	if d.Playbooks == nil || d.PlaybookLedger == nil || task.Workflow != "" {
		return
	}
	if err := d.Playbooks.Run(ctx, d.PlaybookLedger, d.Log, playbookInput(task)); err != nil {
		d.Log.Warn("action playbook run failed", "task", task.ID, "err", err)
	}
}

// resolveWorkflowID picks the definition id to pin. A playbook whose trigger
// matches the task decides, ahead of the kind/label bindings; anything else
// falls through to workflow.ResolveWorkflowID unchanged.
//
// A task that already names a workflow is skipped entirely: that assignment is
// the waiting_human -> approved requeue handoff, a decision already made about
// this specific task, and a trigger that still matches its labels must not
// overturn it.
func (d *Daemon) resolveWorkflowID(task *workflow.Task, available map[string]struct{}) (string, error) {
	if d.Playbooks == nil || task.Workflow != "" {
		return workflow.ResolveWorkflowID(task, available, d.KindWorkflows, d.LabelWorkflows)
	}
	decision, matched := d.Playbooks.Dispatch(playbookInput(task))
	if !matched {
		return workflow.ResolveWorkflowID(task, available, d.KindWorkflows, d.LabelWorkflows)
	}
	if _, defined := available[decision.Workflow]; !defined {
		// Reported, not silently downgraded to the binding's choice: the
		// operator bound this trigger to a named workflow, and running a
		// different one under their rule is worse than parking the task.
		return "", fmt.Errorf("playbook %q binds this trigger to workflow %q, which is not defined", decision.PlaybookID, decision.Workflow)
	}
	d.Log.Info("playbook selected workflow",
		"task", task.ID, "playbook", decision.PlaybookID,
		"playbook_version", decision.Version, "workflow", decision.Workflow)
	return decision.Workflow, nil
}

// playbookInput builds the coordinator's dispatch input from a task row. The
// event surface is what the intake point actually knows about the originating
// issue -- the fields a `when` condition can read; it is deliberately not
// padded with values the daemon would have to invent.
func playbookInput(task *workflow.Task) playbook.DispatchInput {
	labels := workintake.SplitLabels(task.Labels)
	kind := string(workintake.KindForLabels(labels))
	return playbook.DispatchInput{
		Labels: labels,
		Kind:   kind,
		TaskID: workintake.TaskEnvelope{Owner: task.Owner, Repo: task.Repo, Number: task.IssueNumber}.IdempotencyKey(),
		Event: map[string]any{
			"kind":   kind,
			"labels": labels,
			"owner":  task.Owner,
			"repo":   task.Repo,
			"number": task.IssueNumber,
			"title":  task.Title,
			"body":   task.Body,
			"source": task.Source,
		},
	}
}

func (d *Daemon) pinWorkflowFromCollection(ctx context.Context, task *workflow.Task, collection workflow.WorkflowDefinitionCollection, version int64) error {
	available := make(map[string]struct{}, len(collection.Definitions))
	for _, definition := range collection.Definitions {
		available[definition.ID] = struct{}{}
	}
	id, err := d.resolveWorkflowID(task, available)
	if err != nil {
		return err
	}
	definition, ok := collection.DefinitionByID(id)
	if !ok {
		return fmt.Errorf("workflow definition %q disappeared", id)
	}
	task.Workflow = id
	task.WorkflowDefinitionVersion = version
	task.WorkflowDefinitionYAML = definition.YAML
	task.WorkflowDefinitionDigest = workflow.DigestDefinition(definition.YAML)
	if err := d.Store.Update(ctx, task); err != nil {
		return fmt.Errorf("persist workflow definition pin: %w", err)
	}
	return nil
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
// not once the spawned container has connected to core NATS and subscribed
// to this per-task subject --
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

func (d *Daemon) containerEnv(task *workflow.Task, stateStoreToken string) []string {
	var env []string
	// The endpoint the daemon's own client connected with at startup, not
	// the live config: a reloaded [nats] section must not point new
	// containers at a server the daemon is not publishing on.
	env = append(env, "NATS_URL="+d.ConnectedNATS.URL)
	if token := d.ConnectedNATS.Token; token != "" {
		env = append(env, "NATS_TOKEN="+token)
	}
	// The State Store gRPC target follows the same seam as NATS_URL/
	// NATS_TOKEN above (docs/prds/state-store-contract.md §6). It is always
	// the configured [services.state].target after the in-process serving
	// path is deleted. stateStoreToken is this task's own scoped credential
	// (see stateStoreGrantToken) -- never d.ConnectedStateStore.Token, which
	// is the daemon's administrative credential and must never reach a
	// container.
	if d.ConnectedStateStore.URL != "" {
		env = append(env, "STATE_STORE_URL="+d.ConnectedStateStore.URL)
		if stateStoreToken != "" {
			env = append(env, "STATE_STORE_TOKEN="+stateStoreToken)
		}
	}
	// The agent process runs as root inside the container (no USER in the
	// Dockerfile, no userns-remap), so a commit it writes to the bind-mounted
	// worktree lands owned by UID 0 on the host. The daemon then reads those
	// same loose objects to push, running as its own non-root host user --
	// archie-core#520's "permission denied" on .git/objects. Passing the
	// daemon's own UID/GID lets the agent chown the worktree back to it
	// before pushing.
	env = append(env, fmt.Sprintf("WORKTREE_UID=%d", os.Getuid()))
	env = append(env, fmt.Sprintf("WORKTREE_GID=%d", os.Getgid()))
	for _, p := range d.configFor(task).Providers {
		if p.APIKeyEnv != "" {
			if v := os.Getenv(p.APIKeyEnv); v != "" {
				env = append(env, p.APIKeyEnv+"="+v)
			}
		}
	}
	return env
}

// stateStoreGrantToken issues this task's scoped State Store credential, or
// ("", no-op revoke, nil) when no State Store is configured. It fails closed
// -- rather than falling back to the daemon's administrative token -- when a
// State Store is configured but no StateStoreGrantIssuer is wired, which is
// a composition bug, not a runtime condition to paper over.
func (d *Daemon) stateStoreGrantToken(task *workflow.Task) (string, func(), error) {
	if d.ConnectedStateStore.URL == "" {
		return "", func() {}, nil
	}
	if d.StateStoreGrants == nil {
		return "", nil, fmt.Errorf("state store is configured but no task-scoped grant issuer is wired")
	}
	return d.StateStoreGrants.Issue(task)
}

func (d *Daemon) configFor(task *workflow.Task) config.Config {
	if id := d.identityFor(task); id != nil {
		return configForIdentity(d.Cfg.Get(), id.Cfg)
	}
	return d.Cfg.Get()
}

// captureAttemptConfig persists the effective task configuration this attempt
// runs under, as a durable event on the attempt's own key.
//
// It is written where the configuration is materialised for the dispatch, so
// the document is exactly what the run received. The published configuration
// snapshot cannot answer this: it is the CURRENT configuration, replaced on
// every publish, so a run that finished last week has no record anywhere else.
//
// Fail-open by construction: a configuration that could not be recorded is
// worth a log line and nothing more. Parking a task over a reporting failure
// would trade the run for the receipt.
func (d *Daemon) captureAttemptConfig(ctx context.Context, task *workflow.Task, cfg config.TaskConfig) {
	if d.Store == nil {
		return
	}
	document, err := json.Marshal(cfg)
	if err != nil {
		d.Log.Warn("attempt configuration not captured", "task", task.ID, "attempt", task.Attempt, "err", err)
		return
	}
	event := events.Event{
		At:       time.Now().UTC(),
		Kind:     events.KindConfigCaptured,
		TaskID:   task.ID,
		Repo:     task.Owner + "/" + task.Repo,
		Issue:    task.IssueNumber,
		Workflow: task.Workflow,
		Attempt:  task.Attempt,
		Data: map[string]any{
			"schema": events.ConfigCapturedSchema,
			// Raw so the document is embedded once, as it was encoded, rather
			// than as a string a reader would have to decode again.
			"document": json.RawMessage(document),
		},
	}
	id, err := d.Store.InsertEvent(ctx, event)
	if err != nil {
		d.Log.Warn("attempt configuration not captured", "task", task.ID, "attempt", task.Attempt, "err", err)
		return
	}
	// The assigned ID tells the event sink to broadcast without inserting a
	// duplicate row.
	event.ID = id
	d.emit(event)
}

func configForIdentity(root config.Config, identity config.IdentityConfig) config.Config {
	root.BotUser = identity.BotUser
	root.BotEmail = identity.BotEmail
	// Only an identity that set the cap overrides the shared one. Assigning
	// unconditionally made an identity that omits the key inherit a nil cap,
	// which reads as "no cap" and silently disabled the safety rail.
	if identity.DiffCapLines != nil {
		root.DiffCapLines = identity.DiffCapLines
	}
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
func (d *Daemon) identityFor(task *workflow.Task) *IdentityRunner {
	if task == nil || task.Identity == "" {
		return nil
	}
	for _, id := range d.Identities {
		if id.Name == task.Identity || string(id.ID) == task.Identity {
			return id
		}
	}
	return nil
}

// identityMayAct reports whether the identity a task carries may be acted on.
// A denial also carries the park class the task would need: a name that no
// longer resolves is fixable by re-adding the identity (transient), while a
// retired or deactivated identity is a deliberate operator state (terminal).
// An empty identity is the single-identity root and always may act. A
// non-empty identity must resolve to a configured runner and pass the
// lifecycle check; otherwise the returned reason says why it may not. This is
// the single task-side fail-closed point, mirroring identity.Resolve's
// credential-side behaviour: a task naming an identity archie no longer knows
// -- renamed, retired, or deleted in the control plane -- must park, never
// fall back to the root forge's credential.
func (d *Daemon) identityMayAct(ctx context.Context, name string) (bool, string, taskstate.ParkClass) {
	if name == "" {
		return true, "", ""
	}
	runner := d.identityFor(&workflow.Task{Identity: name})
	if runner == nil {
		return false, "identity " + name + " no longer resolves", taskstate.ParkTransient
	}
	if !d.identityActive(ctx, runner.ID) {
		return false, "identity " + runner.Name + " may not act", taskstate.ParkTerminal
	}
	return true, "", ""
}

// parkIfIdentityUnresolvable parks task when its non-empty identity cannot be
// acted on, reporting whether it did. An empty identity is the root
// single-identity path and never parks here.
func (d *Daemon) parkIfIdentityUnresolvable(ctx context.Context, task *workflow.Task) bool {
	ok, reason, class := d.identityMayAct(ctx, task.Identity)
	if ok {
		return false
	}
	d.parkRunningTask(ctx, task.ID, reason, class)
	return true
}

func (d *Daemon) identityActive(ctx context.Context, id identity.IdentityID) bool {
	if d.IdentityRepository == nil {
		return true
	}
	if id == "" {
		d.Log.Error("identity lifecycle check failed", "err", "identity ID is empty")
		return false
	}
	value, err := d.IdentityRepository.Get(ctx, id)
	if err != nil {
		d.Log.Error("identity lifecycle check failed", "identity", id, "err", err)
		return false
	}
	return value.Lifecycle == identity.LifecycleActive
}

// forgeFor returns the forge client that owns task: the identity's own
// client when task.Identity names a configured identity, else the root
// d.Forge. This is the safety boundary that keeps one identity's forge
// token from being used against another identity's repos.
func (d *Daemon) forgeFor(task *workflow.Task) forge.Forge {
	if id := d.identityFor(task); id != nil {
		return id.Forge
	}
	return d.Forge
}

// treesFor returns the worktree manager that owns task, mirroring forgeFor.
func (d *Daemon) treesFor(task *workflow.Task) *worktree.Manager {
	if id := d.identityFor(task); id != nil {
		return id.Trees
	}
	return d.Trees
}

// repoFor resolves task's repo config from the owning identity's repo
// list when task.Identity is set, else the root Cfg.Repos.
func (d *Daemon) repoFor(t *workflow.Task) (config.Repo, bool) {
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
func (d *Daemon) allowConcurrentForTask(task *workflow.Task) bool {
	if !task.HasRepository() {
		return true
	}
	repo, ok := d.repoFor(task)
	return ok && repo.AllowConcurrent
}

// LastPollAt reports when the daemon last began a poll pass, or the zero
// time when no pass has started yet.
func (d *Daemon) LastPollAt() time.Time {
	ns := d.lastPollAt.Load()
	if ns == 0 {
		return time.Time{}
	}
	return time.Unix(0, ns).UTC()
}

// markPoll stamps the start of a poll pass. It is called from both poll
// paths: Run's single-identity loop (via poll) and runIdentities' per-identity
// goroutines (via pollForIdentity).
//
// The stamp is the pass's START, not its completion: a pass that hangs on an
// unreachable forge leaves the stamp stale, which is exactly the signal an
// operator needs. Stamping the completion instead would leave the last
// successful pass's time in place, so a wedged poller would look healthy.
func (d *Daemon) markPoll() {
	d.lastPollAt.Store(time.Now().UnixNano())
}

// errContainerExited is the cancellation cause when a task's agent container
// stops before answering its taskrun request.
var errContainerExited = errors.New("agent container exited before the task finished")

// withContainerExit derives a context cancelled when exited closes. A dead
// container never answers its taskrun request, so without this the request
// blocks forever and the task row stays running.
func withContainerExit(ctx context.Context, exited <-chan struct{}) (context.Context, func()) {
	runCtx, cancel := context.WithCancelCause(ctx)
	go func() {
		select {
		case <-exited:
			cancel(errContainerExited)
		case <-runCtx.Done():
		}
	}()
	return runCtx, func() { cancel(nil) }
}
