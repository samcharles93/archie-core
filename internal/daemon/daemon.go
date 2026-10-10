// Package daemon is archied's resident loop: poll GitHub for labelled
// issues, enqueue them, and process tasks through their routed workflows.
// State lives in the store; the daemon is restartable at any point.
package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
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
	"github.com/samcharles93/archie-core/internal/domain/access"
	"github.com/samcharles93/archie-core/internal/domain/agentrun"
	"github.com/samcharles93/archie-core/internal/domain/binding"
	"github.com/samcharles93/archie-core/internal/domain/eda/playbook"
	"github.com/samcharles93/archie-core/internal/domain/identity"
	"github.com/samcharles93/archie-core/internal/domain/mapping"
	"github.com/samcharles93/archie-core/internal/domain/org"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	workflowtask "github.com/samcharles93/archie-core/internal/domain/workflow/task"
	"github.com/samcharles93/archie-core/internal/domain/workintake"
	"github.com/samcharles93/archie-core/internal/eventbus"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/forge"
	agentnats "github.com/samcharles93/archie-core/internal/infrastructure/agenttransport/nats"
	"github.com/samcharles93/archie-core/internal/logging"
	"github.com/samcharles93/archie-core/internal/storage"
	"github.com/samcharles93/archie-core/internal/taskrun"
	"github.com/samcharles93/archie-core/internal/taskstate"
	"github.com/samcharles93/archie-core/internal/worktree"
)

// TaskBus publishes discovered work, collects it back, and asks a worker to
// run a task.
type TaskBus interface {
	PublishUnique(ctx context.Context, subject, idempotencyKey string, payload []byte) error
	Fetch(ctx context.Context) (eventbus.Message, error)
	Request(ctx context.Context, subject string, payload []byte) ([]byte, error)
}

// RunCredentialIssuer issues the one credential a task's container holds: it
// authorizes the task's own State Store calls and the push of its branch.
// It lapses at the task's time limit plus a margin if never revoked.
type RunCredentialIssuer interface {
	Issue(task *workflow.Task, limit time.Duration) (token string, revoke func(), err error)
}

// NATSEndpoint is the broker address and credential archied connected with at
// startup. It is runtime state rather than configuration: embedded mode
// generates both values, and external secret references are resolved once.
type NATSEndpoint struct {
	URL   string
	Token string
	// Scoped is true on the embedded broker, where a container logs in with
	// its run credential and reaches only its own task's subjects.
	Scoped bool
}

// ContainerToken is the broker token a container is handed: none on a
// scoped broker. An external broker cannot scope a container, so it still
// gets the broker's token (reported as a health issue by archied).
func (e NATSEndpoint) ContainerToken() string {
	if e.Scoped {
		return ""
	}
	return e.Token
}

// StateStoreEndpoint is the State Store target and token passed to agent
// containers.
type StateStoreEndpoint struct {
	URL   string
	Token string
}

type Daemon struct {
	// Cfg publishes the running configuration. Read through Cfg() so a
	// reload swaps the published snapshot atomically. See config.Holder.
	Cfg *config.Holder
	// ConnectedNATS is the endpoint the daemon connected with at startup;
	// container env uses it, never live config.
	ConnectedNATS NATSEndpoint
	// ConnectedStateStore is the State Store target the daemon dialed at
	// startup, passed to containers.
	ConnectedStateStore StateStoreEndpoint
	Store               storecontract.TaskStore
	// Mappings persists payload field mappings.
	// Used by the binding dispatch loop to resolve capture bodies against
	// the mapping a binding names. Optional: nil disables the binding
	// dispatch loop (legacy behaviour).
	Mappings storecontract.MappingStore
	// Bindings persists playbook bindings.
	// Optional: nil disables the binding dispatch loop (legacy behaviour).
	Bindings storecontract.BindingStore
	// BindingDispatcher lists undispatched captures, finds armed bindings and
	// records dispatches. Nil disables binding dispatch.
	BindingDispatcher storecontract.BindingDispatcher
	// MappingMatches counts each event a mapping resolved at dispatch.
	// Optional: nil leaves match counts unrecorded.
	MappingMatches storecontract.MappingMatchRecorder
	// BindingTaskCreator enqueues a task triggered by a binding. Split
	// off TaskLifecycle so the lifecycle surface stays narrow and the
	// dispatch loop does not acquire the full task-creation contract.
	// Optional: nil disables the dispatch loop.
	BindingTaskCreator storecontract.BindingTaskCreator
	// Access evaluates the policy chain at dispatch, the second of the two
	// Authorizer call sites: the workflow's identity may `run` the workflow in
	// its workspace. Optional: nil dispatches without the chain, which is the
	// behaviour of an install that has not built it.
	Access access.Authorizer
	// Principals assembles the dispatch principal for the workflow's
	// identity. Wired with Access or not at all.
	Principals access.PrincipalSource
	// Denials records dispatch refusals. Optional: nil skips the record.
	Denials access.DenialRecorder
	Forge   forge.Forge
	Trees   *worktree.Manager
	Bus     *events.Bus
	Log     *slog.Logger
	// Tasks is the NATS task bus. Always set in production.
	Tasks TaskBus
	// reactionConsumer is built lazily by drainReactions and kept for its
	// lifetime drop counter.
	reactionConsumer *reactionConsumer

	// lastPolled remembers each repo's eligible issues from its previous
	// complete poll, under the dispatch rule that produced them, so only an
	// issue that was eligible and dropped out reads as withdrawn.
	lastPolledMu sync.Mutex
	lastPolled   map[string]polledIssues
	// reactionPublisher is built lazily by scanPRReviews; an indirection
	// only so tests can swap the delivery without the bus.
	reactionPublisher ReactionPublisher
	// Reactions is the review-reaction stream, drained into remediate runs each
	// cycle. Nil disables it.
	Reactions ReactionSource
	// RunCredentials issues each task's run credential (see runCredential).
	// Required whenever ConnectedStateStore.URL is set: the task is parked
	// rather than handed the daemon's own administrative token.
	RunCredentials RunCredentialIssuer
	// KitLauncher starts tasks whose agent profile is a Kit. Nil parks them.
	KitLauncher KitLauncher
	// ModelEgress gives native task containers model access without provider
	// keys. Nil parks a task whose providers need a key.
	ModelEgress ModelEgress
	// TaskRunReadyTimeout bounds how long runViaAgent retries a taskrun request
	// while the new container has not subscribed yet. Zero uses
	// defaultTaskRunReadyTimeout.
	TaskRunReadyTimeout time.Duration
	// TaskRunRetryBackoff is the delay between retry attempts within
	// TaskRunReadyTimeout. Zero uses defaultTaskRunRetryBackoff.
	TaskRunRetryBackoff time.Duration
	// AgentStatus records agent versions for update verification. Nil disables
	// it.
	AgentStatus *AgentStatus

	// ContainerPool manages Docker container lifecycle. Nil means autonomous
	// execution is unavailable and process parks tasks; every runnable task gets
	// a fresh container.
	ContainerPool *container.Pool
	// dispatcher bounds task execution globally across poll passes. Built once,
	// on first use, so ResizeTaskDispatcher can apply a live
	// containers.max_concurrency change without a restart; drainNATS submits
	// each pass through it and leaves its tasks running.
	dispatcherOnce sync.Once
	dispatcher     *taskDispatcher
	// Storage is the pluggable storage backend for container mounts.
	// A runnable task requires it; acquireTaskContainer parks on nil.
	Storage storage.Backend
	// Identities holds per-identity runner state for multi-identity mode.
	// When non-empty, Run() starts one goroutine per identity instead of
	// using the single-identity Forge/Trees/Cfg.Repos path.
	Identities []*IdentityRunner
	// IdentityRepository is the durable lifecycle authority. Polling checks it
	// every cycle so suspend/reactivate commands do not require a restart.
	IdentityRepository identity.Repository
	RootIdentityID     identity.IdentityID

	// KindWorkflows and LabelWorkflows are the routing bindings loaded at
	// startup, sent to the agent. Nil means defaults.
	KindWorkflows  workflow.KindWorkflows
	LabelWorkflows workflow.LabelWorkflows
	// Playbooks are the loaded EDA playbooks, consulted before kind/label
	// bindings when pinning a workflow. Nil means none.
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
	// WithdrawTask declines a task whose issue a complete poll no longer
	// returns. Nil leaves withdrawn issues' work running.
	WithdrawTask func(ctx context.Context, taskID int64, reason string) error

	// WorkflowEnablement supplies which workflows each org has disabled. A
	// binding targeting a workflow its org disabled does not dispatch. Nil
	// means every workflow is enabled.
	WorkflowEnablement interface {
		WorkflowEnablement(context.Context) (workflowtask.WorkflowEnablement, error)
	}

	// TaskLogs persists each task's log output and mirrors it to the dashboard.
	// Nil disables task logging.
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
	// Cfg holds the identity's forge account and repository membership.
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

// sweepAccess accepts pending invitations and verifies push access on the
// root forge and every identity's forge, each in its own goroutine.
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
	// A pass no longer waits for the tasks it submitted, so the run does, once
	// the ctx is cancelled.
	defer d.WaitForTasks()
	d.watchCallWaits(ctx)
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

// runIdentities polls each identity in its own goroutine and runs
// maintenance and draining once in a shared loop, keeping the global
// concurrency cap and per-repo serialization.
func (d *Daemon) runIdentities(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	d.watchCallWaits(ctx)

	var wg sync.WaitGroup

	// One poll loop per identity. Polling is the only identity-scoped
	// work; it never executes tasks.
	for _, id := range d.Identities {
		wg.Add(1)
		go func(id *IdentityRunner) {
			defer wg.Done()
			interval := d.Cfg.Get().PollInterval.Std()
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for {
				d.pollForIdentity(ctx, id)
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					// Pick up the current shared polling interval.
					iv := d.Cfg.Get().PollInterval.Std()
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
	d.WaitForTasks()
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
	for _, repo := range identityRepositories(cfg.Repos, id.Repos) {
		issues, complete := d.pollIssuesWithConfig(ctx, id.Forge, cfg, repo)
		if complete {
			d.withdrawUnpolled(ctx, repo, string(id.ID), dispatchRule(cfg), issues)
		}
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

// pollIssuesWithConfig returns the repo's issues the dispatch rule makes
// work, using the given forge client and dispatch config: single-identity
// mode passes d.Forge/d.Cfg, identity poll loops their own forge and
// configForIdentity, so each identity uses its own bot user with the shared
// dispatch settings. complete is false when any part of the poll failed or was skipped, so
// the result must not be read as the whole eligible set.
func (d *Daemon) pollIssuesWithConfig(ctx context.Context, fg forge.Forge, cfg config.Config, repo config.Repo) (issues []forge.Issue, complete bool) {
	// Webhook intake hears about issues as they change; polling as well would
	// spend forge API quota to find the same work. The receiver runs from the
	// root forge settings, so their mode decides for every identity.
	if d.Cfg.Get().Forge.Intake == config.ForgeIntakeWebhook {
		return nil, false
	}
	labelled := func() ([]forge.Issue, bool) {
		// Empty-label rule shared with workintake.MatchesDispatch: a label
		// trigger needs a non-empty label or it would match every open issue.
		if cfg.Label == "" {
			d.Log.Error("label trigger configured with an empty label; refusing to poll (an empty label matches every open issue)", "repo", repo.FullName())
			return nil, false
		}
		issues, err := fg.IssuesWithLabel(ctx, repo.Owner, repo.Name, cfg.Label)
		if err != nil {
			d.Log.Error("label poll failed", "repo", repo.FullName(), "err", err)
			return nil, false
		}
		return issues, true
	}
	assigned := func() ([]forge.Issue, bool) {
		issues, err := fg.AssignedIssues(ctx, repo.Owner, repo.Name, cfg.BotUser)
		if err != nil {
			d.Log.Error("assigned poll failed", "repo", repo.FullName(), "err", err)
			return nil, false
		}
		return issues, true
	}
	switch cfg.Dispatch.Trigger {
	case "label":
		return labelled()
	case "either":
		byAssignee, assignedOK := assigned()
		byLabel, labelledOK := labelled()
		seen := map[int]bool{}
		for _, is := range byAssignee {
			seen[is.Number] = true
		}
		issues = byAssignee
		for _, is := range byLabel {
			if !seen[is.Number] {
				issues = append(issues, is)
			}
		}
		return issues, assignedOK && labelledOK
	default: // "assignee" (default)
		return assigned()
	}
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

// dispatchBindings offers each identified capture to every armed binding on
// its source. Nil Bindings, BindingDispatcher or BindingTaskCreator disables
// it.
func (d *Daemon) dispatchBindings(ctx context.Context) {
	if d.Bindings == nil || d.BindingDispatcher == nil || d.BindingTaskCreator == nil {
		return
	}
	if d.Principals == nil {
		d.dispatchOrgBindings(ctx)
		return
	}
	identities := []identity.IdentityID{d.RootIdentityID}
	for _, runner := range d.Identities {
		if runner != nil {
			identities = append(identities, runner.ID)
		}
	}
	seen := make(map[org.OrgID]bool)
	for _, id := range identities {
		principal, err := d.Principals.PrincipalFor(ctx, id)
		if err != nil {
			d.Log.Warn("binding dispatch: principal unavailable", "identity", id, "err", err)
			continue
		}
		if seen[principal.Org] {
			continue
		}
		seen[principal.Org] = true
		d.dispatchOrgBindings(org.WithOrg(access.WithPrincipal(ctx, principal), principal.Org))
	}
}

func (d *Daemon) dispatchOrgBindings(ctx context.Context) {
	// Gather armed sources first so the captures query filters down to
	// only relevant rows.
	bindings, err := d.Bindings.ListBindings(ctx)
	if err != nil {
		d.Log.Warn("list bindings failed", "error", err)
		return
	}
	sources := make([]string, 0, len(bindings))
	for _, b := range bindings {
		if b.OrgID == org.OrgFromContext(ctx) && b.Status == binding.StatusArmed && b.Matcher.Source != "" {
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
	enablement, err := d.workflowEnablement(ctx)
	if err != nil {
		d.Log.Warn("binding dispatch: workflow enablement unavailable", "error", err)
		return
	}
	for _, c := range captures {
		d.dispatchCapture(ctx, c, workflows, enablement)
	}
}

// dispatchCapture offers one capture to every armed binding on its source. A
// disabled workflow's binding is skipped like an unarmed one, so the capture
// waits rather than recording a failure every cycle.
func (d *Daemon) dispatchCapture(ctx context.Context, c storecontract.CapturedEvent, workflows workflow.WorkflowDefinitionCollection, enablement workflowtask.WorkflowEnablement) {
	armed, err := d.BindingDispatcher.ArmedBindingsForSource(ctx, c.Source)
	if err != nil {
		d.Log.Warn("armed bindings lookup", "source", c.Source, "error", err)
		return
	}
	for _, b := range armed {
		if b.OrgID == org.OrgFromContext(ctx) && b.Matcher.Matches(c.Source, c.Dispatchable()) && enablement.Enabled(b.OrgID, b.Workflow) {
			d.dispatchOneBinding(ctx, b, c, workflows)
		}
	}
}

// bindingMapping returns b's mapping, or nil after logging why it is
// unavailable. A mapping that no longer exists is a terminal outcome.
func (d *Daemon) bindingMapping(ctx context.Context, b binding.Binding, c storecontract.CapturedEvent) *mapping.Mapping {
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
		d.recordDispatchFailure(ctx, b, c, fmt.Sprintf("mapping %q does not exist", b.MappingID), nil)
	}
	return m
}

// dispatchOneBinding offers one capture to one binding: the mapping must
// match the capture's event type and resolve required fields, the filter
// must admit the values, and the workflow must accept the inputs. The
// dispatch is claimed in the ledger before the task is enqueued, so each
// binding fires at most once per capture.
func (d *Daemon) dispatchOneBinding(ctx context.Context, b binding.Binding, c storecontract.CapturedEvent, workflows workflow.WorkflowDefinitionCollection) {
	m := d.bindingMapping(ctx, b, c)
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
		d.recordEvaluated(ctx, b, c, "filtered")
		return
	}

	target, reason, ok := d.resolveBindingTarget(b, values, workflows)
	if !ok {
		d.recordDispatchFailure(ctx, b, c, reason, nil)
		return
	}
	// The chain decides dispatch: the workflow's identity may `run` this
	// workflow in its workspace, and the event's signature result and address
	// travel with the request. A denial is terminal; an unavailable principal
	// leaves the capture listed for the next cycle.
	if !d.authorizeDispatch(ctx, b, c, target) {
		return
	}
	d.claimAndEnqueue(ctx, b, c, target, values)
}

// claimAndEnqueue claims the dispatch in the at-most-once ledger and enqueues
// the task. A failed enqueue after the claim loses that dispatch, which is
// the at-most-once side of the trade.
func (d *Daemon) claimAndEnqueue(ctx context.Context, b binding.Binding, c storecontract.CapturedEvent, target bindingTarget, values map[string]any) {
	if err := d.BindingDispatcher.RecordDispatch(ctx, b.ID, int64(b.Version), c.ID, 0, ""); err != nil {
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
	taskIdentity := ""
	if principal, ok := access.PrincipalFromContext(ctx); ok && (principal.IdentityID != d.RootIdentityID || principal.Org != org.DefaultOrgID) {
		taskIdentity = string(principal.IdentityID)
	}
	task, err := d.BindingTaskCreator.EnqueueBindingTask(ctx, target.owner, target.repo, title, body, b.Workflow, taskIdentity, b.ID, b.Version, target.inputs)
	if err != nil {
		d.Log.Warn("binding dispatch: enqueue", "binding", b.ID, "capture", c.ID, "error", err)
		return
	}
	if err := d.BindingDispatcher.SetDispatchTask(ctx, b.ID, c.ID, task.ID); err != nil {
		d.Log.Warn("binding dispatch: record task", "binding", b.ID, "capture", c.ID, "task", task.ID, "error", err)
	}
	d.markBindingStart(ctx, task.ID, b, c)
}

// authorizeDispatch checks the identity may run the workflow. A denial is
// recorded and reported as a binding_dispatch_failure.
func (d *Daemon) authorizeDispatch(ctx context.Context, b binding.Binding, c storecontract.CapturedEvent, target bindingTarget) bool {
	if d.Access == nil || d.Principals == nil {
		return true
	}
	principal, ok := access.PrincipalFromContext(ctx)
	var err error
	if !ok {
		principal, err = d.Principals.PrincipalFor(ctx, d.RootIdentityID)
	}
	if err != nil {
		d.Log.Warn("binding dispatch: principal unavailable; dispatch parked for the next cycle",
			"binding", b.ID, "capture", c.ID, "error", err)
		return false
	}
	resource := access.Resource{
		Kind: access.KindWorkflow, ID: b.Workflow, Org: principal.Org,
	}
	contextValue := access.Context{Signature: signatureOf(c), Addr: c.RemoteAddr}
	decision := d.Access.Authorize(principal, access.ActionRun, resource, contextValue)
	if decision.Allowed {
		return true
	}
	if d.Denials != nil {
		denial := access.Denial{
			Principal: principal.IdentityID, Org: principal.Org, Action: access.ActionRun,
			Kind: resource.Kind, ResourceID: resource.ID, Level: decision.Level,
			Policies: decision.Policies,
		}
		if err := d.Denials.RecordDenial(ctx, denial); err != nil {
			d.Log.Warn("binding dispatch: record denial", "binding", b.ID, "error", err)
		}
	}
	d.recordDispatchFailure(ctx, b, c, "access denied", map[string]any{
		"level": string(decision.Level), "policies": decision.Policies,
	})
	return false
}

// signatureOf names the event's signature result as the dispatch context
// carries it: a signed event and an approved unsigned event are the two
// dispatchable shapes (storecontract.CapturedEvent.Dispatchable).
func signatureOf(c storecontract.CapturedEvent) string {
	switch {
	case c.Authenticated:
		return "valid"
	case c.Unsigned:
		return "unsigned"
	}
	return "unverified"
}

// markBindingStart records on a task's timeline the binding and captured
// event that started it.
func (d *Daemon) markBindingStart(ctx context.Context, taskID int64, b binding.Binding, c storecontract.CapturedEvent) {
	if _, err := d.Store.InsertEvent(ctx, events.Event{
		TaskID: taskID,
		Kind:   events.KindBindingStarted,
		Detail: fmt.Sprintf("started by binding %q from source %q", b.Name, c.Source),
		Data: map[string]any{
			"binding_id": b.ID, "binding_name": b.Name, "binding_version": b.Version,
			"capture_id": c.ID, "source": c.Source, "unsigned": c.Unsigned,
		},
	}); err != nil {
		d.Log.Warn("binding dispatch: start marker", "task", taskID, "error", err)
	}
}

// recordDispatchFailure records why a binding did not dispatch a capture and
// marks the pair evaluated, so the capture is not offered to it again.
func (d *Daemon) recordDispatchFailure(ctx context.Context, b binding.Binding, c storecontract.CapturedEvent, reason string, extra map[string]any) {
	d.recordEvaluated(ctx, b, c, reason)
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

// recordEvaluated writes the ledger row for an evaluation that started no
// task. Only terminal outcomes call it; transient ones leave the capture
// listed for the next cycle.
func (d *Daemon) recordEvaluated(ctx context.Context, b binding.Binding, c storecontract.CapturedEvent, reason string) {
	err := d.BindingDispatcher.RecordDispatch(ctx, b.ID, int64(b.Version), c.ID, 0, reason)
	if err != nil && !errors.Is(err, storecontract.ErrAlreadyDispatched) {
		d.Log.Warn("binding dispatch: record evaluation", "binding", b.ID, "capture", c.ID, "error", err)
	}
}

// hasBlockingFailure reports whether any failure is on a required field.
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

// resolveBindingRepo returns the binding's pinned owner/repo, or the only
// configured repo. With zero or several repos and no pin it refuses.
func (d *Daemon) resolveBindingRepo(b binding.Binding) (owner, repo, reason string) {
	if b.Owner != "" && b.Repo != "" {
		return b.Owner, b.Repo, ""
	}
	repos := d.Cfg.Get().Repos
	if len(repos) == 1 {
		return repos[0].Owner, repos[0].Name, ""
	}
	return "", "", fmt.Sprintf("cannot pick a repository: %d are configured; pin the binding to one", len(repos))
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
// through a NATS publish. It returns once the stream and the claim fallback
// are both empty: in-flight tasks keep running, so a task waiting on a callee
// does not hold up the pass that would claim it. Shutdown waits for them
// through WaitForTasks.
func (d *Daemon) drainNATS(ctx context.Context) {
	dispatcher := d.taskDispatcher()
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
}

// taskDispatcher returns the daemon's long-lived global dispatcher, building
// it from the running config on first use. It outlives a single drain pass so
// ResizeTaskDispatcher can resize the running dispatcher.
func (d *Daemon) taskDispatcher() *taskDispatcher {
	d.dispatcherOnce.Do(func() {
		d.dispatcher = newTaskDispatcher(d.Cfg.Get().Containers.MaxConcurrency, d.allowConcurrentForTask)
	})
	return d.dispatcher
}

// WaitForTasks blocks until every submitted task's process has returned. A
// drain pass no longer waits for its own tasks, so shutdown is where in-flight
// work is waited for.
func (d *Daemon) WaitForTasks() {
	d.taskDispatcher().Wait()
}

// ResizeTaskDispatcher applies a new containers.max_concurrency to the
// running global task dispatcher without a restart. Running tasks keep their
// slots; a lowered limit is honoured as they finish, and a raised limit
// admits queued work immediately.
func (d *Daemon) ResizeTaskDispatcher(maxConcurrency int) {
	d.taskDispatcher().SetMaxConcurrency(maxConcurrency)
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
	dispatcher.Submit(ctx, task, func(ctx context.Context, _ *workflow.Task, hold *taskHold) {
		d.processNATSTask(ctx, msg, hold)
	})
}

// taskDispatcher limits concurrent tasks globally and runs one task per repo
// at a time, in submit order, unless the repo allows concurrency.
type taskDispatcher struct {
	allowConcurrent func(task *workflow.Task) bool

	mu        sync.Mutex
	limit     int
	active    int
	slotsFree *sync.Cond
	repoTail  map[string]chan struct{}
	pending   int
	// holds maps a task id to the capacity its run holds, so a run waiting on
	// a callee can give that capacity back and take it again by task id.
	holds map[int64]*taskHold
}

// taskHold is one submitted task's claim on execution capacity: its slot under
// the global limit, and its place at the tail of its repo's chain, which the
// next task for that repo is blocked on. A run that waits on a callee gives
// both back so the callee can run, and takes them again when the wait ends.
type taskHold struct {
	dispatcher *taskDispatcher
	repo       string
	// chain is true when the repo runs one task at a time, so the run holds a
	// place in that repo's chain.
	chain bool
	// baton is the run's place in its repo's chain, closed when it gives that
	// place up; nil while it holds none.
	baton chan struct{}
	// slot is true while the run counts against the global limit.
	slot bool
	// waits counts the callees the run is waiting on: an open call gives capacity
	// back and the last closed call takes it again.
	waits int
	// finished is true once the run's process returned.
	finished bool
	// id is the task this hold belongs to, named by the run once it knows the
	// task's id.
	id      int64
	attempt int
}

// name records which task's capacity this hold is, so a wait on a callee can
// give it back by task id. Zero names nothing: a task arriving on the NATS
// stream has no id until the store claims its row inside the run.
func (h *taskHold) name(id int64, attempt int) {
	if id == 0 {
		return
	}
	h.dispatcher.mu.Lock()
	h.id = id
	h.attempt = attempt
	h.dispatcher.holds[id] = h
	h.dispatcher.mu.Unlock()
}

// SetMaxConcurrency accepts a new global limit without a restart. Running
// tasks keep their slots; a lowered limit is enforced as they finish, and a
// raised limit admits queued work immediately.
func (d *taskDispatcher) SetMaxConcurrency(maxConcurrency int) {
	d.mu.Lock()
	d.limit = maxConcurrency
	d.mu.Unlock()
	d.slotsFree.Broadcast()
}

func newTaskDispatcher(maxConcurrency int, allowConcurrent func(task *workflow.Task) bool) *taskDispatcher {
	if allowConcurrent == nil {
		allowConcurrent = func(*workflow.Task) bool { return false }
	}
	d := &taskDispatcher{
		limit:           maxConcurrency,
		allowConcurrent: allowConcurrent,
		repoTail:        make(map[string]chan struct{}),
		holds:           make(map[int64]*taskHold),
	}
	d.slotsFree = sync.NewCond(&d.mu)
	return d
}

// joinRepoLocked appends baton to the tail of repo's chain and returns the
// baton ahead of it, which the joining task is blocked on. Callers hold d.mu.
func (d *taskDispatcher) joinRepoLocked(repo string, baton chan struct{}) chan struct{} {
	previous := d.repoTail[repo]
	d.repoTail[repo] = baton
	return previous
}

// takeSlotLocked blocks until the global limit has room, then counts the run
// in. limit <= 0 means unlimited. The limit is re-read on every wake, so
// SetMaxConcurrency reaches waiting tasks without recreating the dispatcher.
// Callers hold d.mu.
func (d *taskDispatcher) takeSlotLocked() {
	for d.limit > 0 && d.active >= d.limit {
		d.slotsFree.Wait()
	}
	d.active++
}

// giveBackLocked gives back everything the run holds: its slot under the
// global limit, and its place in its repo's chain, which releases the task
// queued behind it. Callers hold d.mu.
func (d *taskDispatcher) giveBackLocked(hold *taskHold) {
	if hold.slot {
		d.active--
		hold.slot = false
	}
	if hold.baton == nil {
		return
	}
	close(hold.baton)
	if d.repoTail[hold.repo] == hold.baton {
		delete(d.repoTail, hold.repo)
	}
	hold.baton = nil
}

// finishRun retires a returned run: it holds no capacity any more, and no
// Resume may take any for it. Whatever an in-flight resume takes later is
// given back by reacquire, which sees finished.
func (d *taskDispatcher) finishRun(hold *taskHold) {
	d.mu.Lock()
	hold.finished = true
	if hold.id != 0 && d.holds[hold.id] == hold {
		delete(d.holds, hold.id)
	}
	d.giveBackLocked(hold)
	d.pending--
	d.mu.Unlock()
	d.slotsFree.Broadcast()
}

// setCallWaits applies a durable snapshot without accumulating duplicate events.
func (d *taskDispatcher) setCallWaits(hold *taskHold, waits int) {
	d.mu.Lock()
	if d.holds[hold.id] != hold || hold.finished {
		d.mu.Unlock()
		return
	}
	previous := hold.waits
	hold.waits = waits
	if waits > 0 {
		d.giveBackLocked(hold)
	}
	d.mu.Unlock()
	if waits > 0 {
		d.slotsFree.Broadcast()
	} else if previous > 0 {
		// Reacquiring may block behind another task; the caller keeps running.
		go d.reacquire(hold)
	}
}

// reacquire returns a resumed run's capacity: its place in its repo's chain
// and a slot under the global limit. The run keeps executing meanwhile -- a
// wait on a callee is not a pause. A hold records one slot and one place, so a
// re-acquire queued while another is still waiting gives back whichever of the
// two it finds already recorded instead of taking a second.
func (d *taskDispatcher) reacquire(hold *taskHold) {
	d.mu.Lock()
	if hold.finished || hold.waits > 0 {
		// The run returned, or started waiting again, after the reconciliation that
		// queued this: it needs no capacity.
		d.mu.Unlock()
		return
	}
	var previous chan struct{}
	// A place another re-acquire already queued is the run's one place; joining
	// behind it would queue a second, which the task behind it would wait on
	// forever.
	if hold.chain && hold.baton == nil {
		hold.baton = make(chan struct{})
		previous = d.joinRepoLocked(hold.repo, hold.baton)
	}
	d.mu.Unlock()

	if previous != nil {
		<-previous
	}

	d.mu.Lock()
	d.takeSlotLocked()
	if hold.slot {
		// Another re-acquire took the run's one slot while this waited: give
		// back the slot this one just took. The place is the other's, so it is
		// left alone.
		d.active--
		d.mu.Unlock()
		d.slotsFree.Broadcast()
		return
	}
	hold.slot = true
	if hold.finished || hold.waits > 0 {
		// The run ended, or started waiting again, while this was queued for
		// capacity: give back what it took, so no slot or place in the repo
		// chain leaks.
		d.giveBackLocked(hold)
		d.mu.Unlock()
		d.slotsFree.Broadcast()
		return
	}
	d.mu.Unlock()
}

// Submit queues a task's process behind the global limit and, unless the repo
// allows concurrency, behind the task already holding that repo. process is
// handed the run's capacity so it can name it once it knows the task's id and
// give it back while it waits on a callee.
func (d *taskDispatcher) Submit(
	ctx context.Context,
	task *workflow.Task,
	process func(context.Context, *workflow.Task, *taskHold),
) {
	repo := task.Owner + "/" + task.Repo
	hold := &taskHold{dispatcher: d, repo: repo}

	var previous chan struct{}
	if !d.allowConcurrent(task) {
		hold.chain = true
		hold.baton = make(chan struct{})
		d.mu.Lock()
		previous = d.joinRepoLocked(repo, hold.baton)
		d.mu.Unlock()
	}

	d.mu.Lock()
	d.pending++
	d.mu.Unlock()
	go func() {
		defer d.finishRun(hold)
		if previous != nil {
			<-previous
		}
		d.mu.Lock()
		d.takeSlotLocked()
		hold.slot = true
		d.mu.Unlock()
		process(ctx, task, hold)
	}()
}

func (d *taskDispatcher) Wait() {
	d.mu.Lock()
	defer d.mu.Unlock()
	for d.pending > 0 {
		d.slotsFree.Wait()
	}
}

func (d *Daemon) poll(ctx context.Context) {
	if !d.identityActive(ctx, d.RootIdentityID) {
		return
	}
	d.markPoll()
	for _, repo := range d.Cfg.Get().Repos {
		issues, complete := d.pollIssues(ctx, repo)
		if complete {
			d.withdrawUnpolled(ctx, repo, "", dispatchRule(d.Cfg.Get()), issues)
		}
		for _, is := range issues {
			labels := strings.Join(is.Labels, ",")
			if d.Tasks != nil {
				d.pollNATS(ctx, d.Forge, d.Cfg.Get(), repo, is, labels, "")
			}
		}
	}
}

// polledIssues is one repo's eligible issues under one dispatch rule.
type polledIssues struct {
	rule     string
	eligible map[int]bool
}

// rememberPoll records a repo's latest eligible set and returns the previous
// one, reporting whether it was taken under the same rule.
func (d *Daemon) rememberPoll(key string, current polledIssues) (polledIssues, bool) {
	d.lastPolledMu.Lock()
	defer d.lastPolledMu.Unlock()
	previous, seen := d.lastPolled[key]
	if d.lastPolled == nil {
		d.lastPolled = map[string]polledIssues{}
	}
	d.lastPolled[key] = current
	return previous, seen && previous.rule == current.rule
}

// dispatchRule identifies the rule a poll selects issues by.
func dispatchRule(cfg config.Config) string {
	return cfg.Dispatch.Trigger + "\x00" + cfg.Label + "\x00" + cfg.BotUser
}

// withdrawUnpolled declines the queued or running work of an identity's
// issues in repo that a complete poll no longer returns: the issue was
// closed, unlabelled or unassigned.
func (d *Daemon) withdrawUnpolled(ctx context.Context, repo config.Repo, identity, rule string, polled []forge.Issue) {
	if d.WithdrawTask == nil {
		return
	}
	eligible := make(map[int]bool, len(polled))
	for _, is := range polled {
		eligible[is.Number] = true
	}
	// Only an issue the same rule found eligible last time and no longer
	// finds was withdrawn. A changed label, trigger or bot user, or the first
	// poll after a restart, only records the new set: tasks picked up under
	// the old rule were not withdrawn by anyone.
	previous, sameRule := d.rememberPoll(identity+"\x00"+repo.FullName(), polledIssues{rule: rule, eligible: eligible})
	if !sameRule {
		return
	}
	active, err := d.Store.TasksPage(ctx, storecontract.TaskPage{
		Statuses: []string{taskstate.Queued, taskstate.Running}, Limit: 500,
	})
	if err != nil {
		d.Log.Warn("withdrawal check: active tasks unavailable", "repo", repo.FullName(), "err", err)
		return
	}
	for _, t := range active {
		if t.Owner != repo.Owner || t.Repo != repo.Name || t.Identity != identity ||
			!t.IsForgeBacked() || t.PRNumber > 0 || eligible[t.IssueNumber] || !previous.eligible[t.IssueNumber] {
			continue
		}
		if err := d.WithdrawTask(ctx, t.ID, "the issue is no longer assigned, labelled or open"); err != nil {
			d.Log.Warn("withdraw task", "task", t.ID, "err", err)
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

// PublishTask validates a task envelope, stamps the producing identity's
// org and publishes it. The idempotency key dedups rediscovered issues.
func (d *Daemon) PublishTask(ctx context.Context, task workintake.TaskEnvelope) error {
	if d.Tasks == nil {
		return fmt.Errorf("publish task %s: no task bus configured", task.Ref())
	}
	if err := task.Kind.Validate(); err != nil {
		return fmt.Errorf("publish task %s: %w", task.Ref(), err)
	}
	resolved, err := d.identityOrg(ctx, d.publisherIdentity(task.Identity))
	if err != nil {
		return fmt.Errorf("publish task %s: resolve org: %w", task.Ref(), err)
	}
	task.Org = resolved
	payload, err := task.Encode()
	if err != nil {
		return err
	}
	return d.Tasks.PublishUnique(ctx, task.Subject(), task.IdempotencyKey(), payload)
}

// PublishReaction stamps the org on a review reaction, defaulting to the
// root identity's, and publishes it.
func (d *Daemon) PublishReaction(ctx context.Context, reaction workintake.ReviewCommentEnvelope) error {
	if d.Tasks == nil {
		return fmt.Errorf("publish reaction %s: no task bus configured", reaction.Ref())
	}
	if reaction.Org == "" {
		resolved, err := d.identityOrg(ctx, d.RootIdentityID)
		if err != nil {
			return fmt.Errorf("publish reaction %s: resolve org: %w", reaction.Ref(), err)
		}
		reaction.Org = resolved
	}
	payload, err := reaction.Encode()
	if err != nil {
		return err
	}
	return d.Tasks.PublishUnique(ctx, reaction.Subject(), reaction.IdempotencyKey(), payload)
}

// publisherIdentity is the identity a producer publishes as: the envelope's
// identity when it names one (an ID, or a name for a hand-built envelope),
// otherwise the root identity of a single-identity install.
func (d *Daemon) publisherIdentity(ref string) identity.IdentityID {
	if ref == "" {
		return d.RootIdentityID
	}
	for _, runner := range d.Identities {
		if runner == nil {
			continue
		}
		if runner.Name == ref || string(runner.ID) == ref {
			return runner.ID
		}
	}
	return identity.IdentityID(ref)
}

// identityOrg resolves an identity's org. No PrincipalSource means the
// default org; a store error is returned.
func (d *Daemon) identityOrg(ctx context.Context, id identity.IdentityID) (org.OrgID, error) {
	if d.Principals == nil {
		return org.DefaultOrgID, nil
	}
	principal, err := d.Principals.PrincipalFor(ctx, id)
	if err != nil {
		return "", err
	}
	return principal.Org, nil
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
func (d *Daemon) processNATSTask(ctx context.Context, msg eventbus.Message, hold *taskHold) {
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

	d.process(ctx, task, hold)
	if err := msg.Ack(); err != nil {
		d.Log.Warn("ack failed", "err", err)
	}
}

// pollIssues discovers work for one repo according to the root dispatch
// config, using the root forge client. Identity poll loops use
// pollIssuesWithConfig with their own forge and config instead.
func (d *Daemon) pollIssues(ctx context.Context, repo config.Repo) ([]forge.Issue, bool) {
	return d.pollIssuesWithConfig(ctx, d.Forge, d.Cfg.Get(), repo)
}

// closeResolvedIssue closes a finished task's forge issue, skipping chat
// tasks. Failures are logged.
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
		attempt := t.Attempt
		switch state {
		case "merged":
			_ = d.Store.Transition(ctx, t.ID, workflow.StatusPROpen, workflow.StatusMerged, "")
			_ = trees.Cleanup(t.Owner, t.Repo, t.IssueNumber)
			// Close the issue explicitly; nothing else does.
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

func (d *Daemon) process(ctx context.Context, task *workflow.Task, hold *taskHold) {
	// A task off the NATS stream only has an id once the store has claimed its
	// row, inside this call, so its capacity is named here.
	hold.name(task.ID, task.Attempt)
	// The workflow may name the identity the run acts as, which everything
	// below is chosen by, so it is settled before anything else.
	if !d.adoptWorkflowIdentity(ctx, task) {
		return
	}
	// Register first so the task can be stopped during setup.
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
	if profile.IsKit() {
		d.runKitTask(ctx, task, repo, workDir, profile)
		d.teardownStorage(ctx, task, repo, workDir)
		d.cleanupTerminalTaskWorktree(ctx, task, trees)
		return
	}
	ctr, credential, revokeCredential, ok := d.acquireTaskContainer(ctx, task, repo, workDir, profile.Image)
	if !ok {
		return
	}
	defer d.ContainerPool.Release(ctx, ctr)
	defer revokeCredential()

	// Run the whole task in archie-agent, which calls back to the daemon for
	// store, forge and push operations.
	limitCtx, stopLimit := withTaskTimeLimit(ctx, d.configFor(task).Budgets.TaskWallClock.Std())
	runCtx, stopWatch := withContainerExit(limitCtx, ctr.Exited())
	d.runViaAgent(runCtx, task, repo, profile, nil, credential)
	stopWatch()
	stopLimit()

	d.teardownStorage(ctx, task, repo, workDir)
	d.cleanupTerminalTaskWorktree(ctx, task, trees)
}

// teardownStorage runs after the workflow completes. The Docker backend is
// a no-op; future backends (temp volumes, NFS leases) use this hook.
func (d *Daemon) teardownStorage(ctx context.Context, task *workflow.Task, repo config.Repo, workDir string) {
	if d.Storage == nil {
		return
	}
	if err := d.Storage.Teardown(ctx, storage.TaskRef{
		WorktreeDir:       workDir,
		Ecosystem:         repo.Ecosystem,
		PersistentStorage: repo.PersistentStorage,
		Owner:             task.Owner,
		Repo:              task.Repo,
	}); err != nil {
		d.Log.Warn("storage teardown failed", "task", task.ID, "err", err)
	}
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
	// The retry mode on the task decides whether to resume the pushed branch or
	// reset onto base. Prepare clones a missing worktree either way.
	target := worktree.Fresh
	if taskstate.NormalizeRetryMode(task.RetryMode) == taskstate.RetryContinuePushedWork {
		if task.Branch == "" {
			// The action refuses this combination, so an empty branch here is a
			// dispatch bug upstream and no retry can fix it.
			d.Log.Error("task has no persisted branch to continue", "task", task.ID)
			d.parkRunningTask(ctx, task.ID, "worktree resume failed: task has no branch to continue", taskstate.ParkNeedsHuman)
			return "", false
		}
		target = worktree.Target(task.Branch)
	}
	// Every task gets an independent full clone.
	dir, branch, err := trees.Prepare(ctx, task.Owner, task.Repo, repo.BaseBranch(), task.IssueNumber, task.Title, task.Body, task.Labels, target)
	if err != nil {
		reason := "worktree prepare failed: " + err.Error()
		if target != worktree.Fresh {
			reason = "worktree resume failed: " + err.Error()
		}
		d.Log.Error("worktree prepare failed", "task", task.ID, "err", err)
		d.parkRunningTask(ctx, task.ID, reason, taskstate.ParkTransient)
		return "", false
	}
	task.Branch = branch
	if err := d.Store.Update(ctx, task); err != nil {
		d.Log.Warn("task branch not persisted", "task", task.ID, "err", err)
	}
	return dir, true
}

// cleanupTerminalTaskWorktree removes a settled task's worktree, unless it
// holds uncommitted work or its state cannot be read.
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
	// Settled statuses. Keep the worktree while a PR exists.
	switch latest.Status {
	case workflow.StatusMerged, workflow.StatusRejected, workflow.StatusClosedWontDo, workflow.StatusCompleted:
		if latest.PRNumber != 0 {
			return
		}
		if d.worktreeHoldsUncapturedWork(cleanupCtx, trees, task) {
			return
		}
		if err := trees.Cleanup(task.Owner, task.Repo, task.IssueNumber); err != nil {
			d.Log.Warn("terminal worktree cleanup failed", "task", task.ID, "err", err)
		}
	}
}

// worktreeHoldsUncapturedWork reports whether a terminal task's worktree has
// to survive cleanup because it may hold work no commit captured. A worktree
// that is not on disk at all has nothing to keep, so a task whose clone was
// already removed still cleans up whatever leftovers share its path.
func (d *Daemon) worktreeHoldsUncapturedWork(ctx context.Context, trees *worktree.Manager, task *workflow.Task) bool {
	dir := trees.Dir(task.Owner, task.Repo, task.IssueNumber)
	if _, err := os.Stat(dir); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false
		}
		d.Log.Warn("terminal worktree kept: it could not be read; archive the task to remove it", "task", task.ID, "dir", dir, "err", err)
		return true
	}
	uncommitted, err := trees.HasUncommittedChanges(ctx, dir)
	if err != nil {
		d.Log.Warn("terminal worktree kept: uncommitted work could not be read; archive the task to remove it", "task", task.ID, "dir", dir, "err", err)
		return true
	}
	if uncommitted {
		d.Log.Info("terminal worktree kept: it holds uncommitted work; archive the task to remove it", "task", task.ID, "dir", dir)
		return true
	}
	return false
}

// openTaskLog opens the task's log sink for one process() call and returns
// its closer. Failure to open is logged, not fatal.
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
) (*container.Container, string, func(), bool) {
	park := func(reason string, err error) {
		d.Log.Error(reason, "err", err)
		d.parkRunningTask(ctx, task.ID, reason+": "+err.Error(), taskstate.ParkTransient)
	}

	// Resolve the PR number from inputs before the brief is written.
	if task.Workflow == "pr-review" {
		task.PRNumber = task.EffectivePRNumber()
	}

	if err := writeTaskBrief(workDir, task); err != nil {
		park("task.json write failed", err)
		return nil, "", nil, false
	}

	// Fetch the pull request with the daemon's forge credential into workDir,
	// so the container reads it without credentials.
	if task.Workflow == "pr-review" {
		if err := prefetchPRReview(ctx, d.forgeFor(task), task.Owner, task.Repo, task.PRNumber, workDir); err != nil {
			park("pr-review prefetch failed", err)
			return nil, "", nil, false
		}
	}

	// Guard: Storage may be nil if the daemon was wired incorrectly. In
	// normal operation, Storage is always set when ContainerPool is set.
	if d.Storage == nil {
		d.Log.Error("storage backend is nil  --  cannot acquire container")
		d.parkRunningTask(ctx, task.ID, "storage backend not configured", taskstate.ParkTransient)
		return nil, "", nil, false
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
		return nil, "", nil, false
	}

	credential, revokeCredential, err := d.runCredential(task)
	if err != nil {
		d.teardownStorage(ctx, task, repo, workDir)
		park("run credential failed", err)
		return nil, "", nil, false
	}

	model, closeModel, err := d.openModelEgress(task, credential)
	if err != nil {
		revokeCredential()
		d.teardownStorage(ctx, task, repo, workDir)
		park("model egress failed", err)
		return nil, "", nil, false
	}
	revokeRun := revokeCredential
	revokeCredential = func() { closeModel(); revokeRun() }

	ctr, err := d.ContainerPool.Acquire(ctx, image, append(mounts, model.Mounts...), append(d.containerEnv(task, credential), model.Env...))
	if err != nil {
		revokeCredential()
		// Roll back the storage we just set up: the mounts were created
		// for this task but no container will use them. Leaking them until
		// a later TTL sweep is not acceptable on a backend that allocates
		// real resources (volumes, NFS leases).
		d.teardownStorage(ctx, task, repo, workDir)
		park("container acquire failed", err)
		return nil, "", nil, false
	}
	return ctr, credential, revokeCredential, true
}

// writeTaskBrief writes task.json, the container's boot-time brief.
func writeTaskBrief(workDir string, task *workflow.Task) error {
	return container.WriteTaskJSON(workDir, container.TaskPayload{
		ID: task.ID, Owner: task.Owner, Repo: task.Repo,
		Number: task.IssueNumber, Title: task.Title, Body: task.Body,
		Labels:   strings.Split(task.Labels, ","),
		Workflow: task.Workflow, Branch: task.Branch, Plan: task.Plan,
		Inputs: task.Inputs,
	})
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

// parkRunningTask moves a running task to parked with class, using a
// bounded context that ignores cancellation. ErrStaleTransition is ignored;
// other errors are logged.
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

// recordPark writes a park reason to the attempt's log. Dropped when no sink
// is open.
func (d *Daemon) recordPark(ctx context.Context, taskID int64, reason string) {
	d.TaskLogs.Write(ctx, taskID, logging.Entry{
		Time:    time.Now(),
		Level:   slog.LevelError.String(),
		Message: "task parked: " + reason,
		Fields:  map[string]any{"component": "daemon", "task": taskID},
	})
}

// runViaAgent hands a task to archie-agent and waits for its result. It parks
// the task only when the agent never answered or failed before recording an
// outcome.
//
// credential is the task's run credential; the agent presents it to push the
// task's branch.
func (d *Daemon) runViaAgent(ctx context.Context, task *workflow.Task, repo config.Repo, profile config.AgentProfile, harness *agentrun.HarnessSpec, credential string) {
	cfg := d.configFor(task)
	taskCfg := cfg.ForTask()
	d.captureAttemptConfig(ctx, task, taskCfg)
	req := taskrun.Request{
		Task:               task,
		Repo:               repo,
		Cfg:                taskCfg,
		Providers:          agentexec.ProvidersFromConfig(cfg.Providers),
		MCPServers:         cfg.Tools.MCPServers,
		RunCredential:      credential,
		KindWorkflows:      d.KindWorkflows,
		LabelWorkflows:     d.LabelWorkflows,
		WorkflowDefinition: task.WorkflowDefinitionYAML,
		Tools:              profile.Tools,
		Harness:            harness,
	}
	data, err := json.Marshal(req)
	if err != nil {
		d.Log.Error("taskrun encode failed", "task", task.ID, "err", err)
		d.parkRunningTask(ctx, task.ID, "taskrun encode failed: "+err.Error(), taskstate.ParkTransient)
		return
	}

	reply, err := d.requestTaskRun(ctx, task.ID, data)
	if cause := context.Cause(ctx); err != nil && (errors.Is(cause, errContainerExited) || errors.Is(cause, errTaskTimeLimit)) {
		err = cause
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

// pinTaskProfile pins the task's workflow definition and resolves its agent
// profile, parking the task on failure.
func (d *Daemon) pinTaskProfile(ctx context.Context, task *workflow.Task) (config.AgentProfile, bool) {
	if err := d.pinWorkflowDefinition(ctx, task); err != nil {
		d.Log.Error("pin workflow definition failed", "task", task.ID, "err", err)
		d.parkRunningTask(ctx, task.ID, "pin workflow definition: "+err.Error(), pinParkClass(err))
		return config.AgentProfile{}, false
	}
	iface, err := workflowtask.ParseWorkflowInterface(task.WorkflowDefinitionYAML)
	if err == nil {
		// Every producer meets the declared interface here, including the
		// ones that cannot read definitions when they enqueue (chat and
		// dashboard spawns go through the Gateway, which never does).
		err = iface.CheckInputs(task.Inputs)
	}
	if err == nil {
		var profile config.AgentProfile
		if profile, err = d.configFor(task).Containers.Profile(iface.Profile); err == nil {
			if err = validateProfileMeetsNeeds(profile, iface.Needs()); err == nil {
				return profile, true
			}
		}
	}
	d.Log.Error("agent profile unavailable", "task", task.ID, "err", err)
	d.parkRunningTask(ctx, task.ID, "workflow "+task.Workflow+": "+err.Error(), taskstate.ParkNeedsHuman)
	return config.AgentProfile{}, false
}

// validateProfileMeetsNeeds rejects a Kit profile whose harness cannot serve
// the captures the workflow needs. Image profiles always pass.
func validateProfileMeetsNeeds(profile config.AgentProfile, needs workflowtask.WorkflowNeeds) error {
	if !profile.IsKit() || !needs.Captures {
		return nil
	}
	adapter, ok := agentexec.LookupHarnessAdapter(profile.Adapter)
	if !ok || len(adapter.MCPConfig) == 0 {
		return fmt.Errorf("needs.captures is set, but profile adapter %q serves no capture tools", profile.Adapter)
	}
	return nil
}

// pinWorkflowDefinition ensures the task carries a valid definition pin for
// the workflow it names and that its org has the workflow enabled. Every run
// passes through here, whatever started it, so this is the one enablement
// check for issue labels, handoffs, approvals, calls and playbooks alike.
func (d *Daemon) pinWorkflowDefinition(ctx context.Context, task *workflow.Task) error {
	ctx, err := d.workflowContext(ctx, task)
	if err != nil {
		return err
	}
	if err := d.pinDefinition(ctx, task); err != nil {
		return err
	}
	enablement, err := d.workflowEnablement(ctx)
	if err != nil {
		return pinFailure{taskstate.ParkTransient, fmt.Errorf("workflow enablement: %w", err)}
	}
	if !enablement.Enabled(task.Org, task.Workflow) {
		return pinFailure{taskstate.ParkNeedsHuman, fmt.Errorf("workflow %s is disabled", task.Workflow)}
	}
	return nil
}

// pinDefinition keeps a valid pin and resolves a missing or mismatched one
// again from the active definitions.
func (d *Daemon) pinDefinition(ctx context.Context, task *workflow.Task) error {
	pinned, ok, err := pinnedDefinitionID(task)
	if err != nil {
		return pinFailure{taskstate.ParkNeedsHuman, err}
	}
	if ok && pinned == task.Workflow {
		return nil
	}
	d.runActionPlaybooks(ctx, task)
	if d.WorkflowDefinitions == nil {
		return d.pinWorkflowFromCollection(ctx, task, workflow.ShippedDefinitions(), 0)
	}
	collection, version, err := d.WorkflowDefinitions.WorkflowDefinitions(ctx)
	if err != nil {
		return pinFailure{taskstate.ParkTransient, err}
	}
	return d.pinWorkflowFromCollection(ctx, task, collection, version)
}

// pinFailure carries the park class for a pin failure: operator-actionable
// for a bad pin or missing workflow, transient for a store error.
type pinFailure struct {
	class taskstate.ParkClass
	err   error
}

func (e pinFailure) Error() string { return e.err.Error() }
func (e pinFailure) Unwrap() error { return e.err }

// pinParkClass reports the class a pin failure carries, defaulting to
// taskstate.ParkNeedsHuman for a cause that did not classify itself: the safe
// misread is "an operator should look at this", never "a requeue will fix it".
func pinParkClass(err error) taskstate.ParkClass {
	if failure, ok := errors.AsType[pinFailure](err); ok {
		return failure.class
	}
	return taskstate.ParkNeedsHuman
}

// pinnedDefinitionID returns the workflow id a task's pin declares, checking
// its digest. ok is false when there is no pin.
func pinnedDefinitionID(task *workflow.Task) (string, bool, error) {
	if task.WorkflowDefinitionYAML == "" {
		return "", false, nil
	}
	if workflow.DigestDefinition(task.WorkflowDefinitionYAML) != task.WorkflowDefinitionDigest {
		return "", false, fmt.Errorf("stored workflow definition digest mismatch")
	}
	id, err := workflow.DefinitionID(task.WorkflowDefinitionYAML)
	if err != nil {
		return "", false, fmt.Errorf("stored workflow definition: %w", err)
	}
	return id, true, nil
}

// runActionPlaybooks runs the action playbooks matching a task before it is
// pinned. Failures never fail the task; tasks already naming a workflow are
// skipped.
func (d *Daemon) runActionPlaybooks(ctx context.Context, task *workflow.Task) {
	if d.Playbooks == nil || d.PlaybookLedger == nil || task.Workflow != "" {
		return
	}
	if err := d.Playbooks.Run(ctx, d.PlaybookLedger, d.Log, playbookInput(task)); err != nil {
		d.Log.Warn("action playbook run failed", "task", task.ID, "err", err)
	}
}

// resolveWorkflowID picks the definition to pin: a matching playbook, then
// the kind/label bindings. A task already naming a workflow keeps it.
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
		TaskID: workintake.TaskEnvelope{Org: task.Org, Identity: task.Identity, Owner: task.Owner, Repo: task.Repo, Number: task.IssueNumber}.IdempotencyKey(),
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
		return pinFailure{taskstate.ParkNeedsHuman, err}
	}
	definition, ok := collection.DefinitionByID(id)
	if !ok {
		return pinFailure{taskstate.ParkNeedsHuman, fmt.Errorf("workflow definition %q disappeared", id)}
	}
	task.Workflow = id
	task.WorkflowDefinitionVersion = version
	task.WorkflowDefinitionYAML = definition.YAML
	task.WorkflowDefinitionDigest = workflow.DigestDefinition(definition.YAML)
	if err := d.Store.Update(ctx, task); err != nil {
		return pinFailure{taskstate.ParkTransient, fmt.Errorf("persist workflow definition pin: %w", err)}
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

// requestTaskRun publishes the taskrun request, retrying on
// nats.ErrNoResponders until the container subscribes. Other errors return
// at once.
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
	if token := d.ConnectedNATS.ContainerToken(); token != "" {
		env = append(env, "NATS_TOKEN="+token)
	}
	// Pass the State Store target with this task's scoped token, never the
	// daemon's own.
	if d.ConnectedStateStore.URL != "" {
		env = append(env, "STATE_STORE_URL="+d.ConnectedStateStore.URL)
		if stateStoreToken != "" {
			env = append(env, "STATE_STORE_TOKEN="+stateStoreToken)
		}
	}
	// The agent runs as root in the container, so its commits land owned by
	// UID 0 on the host. The daemon's UID/GID let the agent hand the worktree
	// back before the daemon reads it to push.
	env = append(env, fmt.Sprintf("WORKTREE_UID=%d", os.Getuid()))
	env = append(env, fmt.Sprintf("WORKTREE_GID=%d", os.Getgid()))
	return env
}

// runCredential issues the task's run credential, or "" when no State Store
// is configured. It fails when no issuer is wired.
func (d *Daemon) runCredential(task *workflow.Task) (string, func(), error) {
	if d.ConnectedStateStore.URL == "" {
		return "", func() {}, nil
	}
	if d.RunCredentials == nil {
		return "", nil, fmt.Errorf("state store is configured but no run credential issuer is wired")
	}
	return d.RunCredentials.Issue(task, d.configFor(task).Budgets.TaskWallClock.Std())
}

func (d *Daemon) configFor(task *workflow.Task) config.Config {
	if id := d.identityFor(task); id != nil {
		return configForIdentity(d.Cfg.Get(), id.Cfg)
	}
	return d.Cfg.Get()
}

// captureAttemptConfig records the attempt's effective configuration as an
// event. Failures are logged.
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
	root.BotUser, root.BotEmail = identity.BotUser, identity.BotEmail
	root.Org, root.GrantedCredentials = root.CredentialAccess(identity.Name)
	root.Forge = identity.Forge
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

// identityMayAct reports whether a task's identity may act, and the park
// class if not. Empty is the root identity. An unknown or inactive identity
// never falls back to the root forge.
func (d *Daemon) identityMayAct(ctx context.Context, name string) (bool, string, taskstate.ParkClass) {
	if name == "" || name == string(d.RootIdentityID) {
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
		repos = identityRepositories(repos, id.Repos)
	}
	for _, r := range repos {
		if r.Owner == t.Owner && r.Name == t.Repo {
			return r, true
		}
	}
	return config.Repo{}, false
}

// allowConcurrentForTask reports whether the task's repo allows concurrent
// tasks. Unknown repos do not.
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

// markPoll stamps the start of a poll pass, so a hung pass shows as stale.
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

// errTaskTimeLimit is the cancellation cause when a task outruns its overall
// time limit (the execution settings' max task runtime).
var errTaskTimeLimit = errors.New("task exceeded its time limit")

// withTaskTimeLimit bounds a whole task run. A limit of zero or less applies
// no bound.
func withTaskTimeLimit(ctx context.Context, limit time.Duration) (context.Context, func()) {
	if limit <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeoutCause(ctx, limit, fmt.Errorf("%w (%s)", errTaskTimeLimit, limit))
}

// RemoveWorktree deletes a task's clone from the trees of the identity that
// owns it. Archiving calls it: terminal cleanup keeps a worktree that may hold
// uncaptured work, and the operator's archive is the decision to discard it.
func (d *Daemon) RemoveWorktree(owner, repo, identity string, issue int) error {
	trees := d.treesFor(&workflow.Task{Owner: owner, Repo: repo, Identity: identity})
	if trees == nil {
		return nil
	}
	return trees.Cleanup(owner, repo, issue)
}

// InFlight counts the same submitted work that graceful shutdown waits for,
// including tasks waiting for a repository or concurrency slot.
func (d *Daemon) InFlight() int {
	dispatcher := d.taskDispatcher()
	dispatcher.mu.Lock()
	defer dispatcher.mu.Unlock()
	return dispatcher.pending
}

// identityRepositories keeps account membership while taking live settings
// for repositories present in the shared control-plane configuration.
func identityRepositories(shared, owned []config.Repo) []config.Repo {
	repos := append([]config.Repo(nil), owned...)
	for i, repo := range repos {
		for _, current := range shared {
			if current.Owner == repo.Owner && current.Name == repo.Name {
				repos[i] = current
				break
			}
		}
	}
	return repos
}
