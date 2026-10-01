package gateway

import (
	"context"
	"fmt"

	"github.com/samcharles93/archie-core/internal/domain/taskactions"
	"github.com/samcharles93/archie-core/internal/taskstate"
)

// StoreTaskCreator implements TaskCreator backed by a store interface.
// Chat-spawned tasks use a timestamp-based synthetic issue number to
// avoid colliding with Gitea-issued tasks; the created row is native
// (Source "chat", no backing forge issue). The configured default repo
// is used when a spawn request doesn't specify one explicitly.
type StoreTaskCreator struct {
	store        chatTaskWriter
	defaultOwner string
	defaultRepo  string
	// allowed lists "owner/name" repos this identity may spawn tasks
	// against. An explicit SpawnRequest.Repo not in this set is
	// rejected  --  chat must not be able to spawn work against a repo
	// the identity isn't configured for.
	allowed  map[string]bool
	profiles map[string]taskProfile
}

// TaskProfile defines the repositories available to one daemon identity.
// Identity is the stable IdentityConfig.Name stored on chat-originated tasks.
type TaskProfile struct {
	Identity     string
	DefaultOwner string
	DefaultRepo  string
	Repos        []string
}

type taskProfile struct {
	defaultOwner string
	defaultRepo  string
	allowed      map[string]bool
}

// chatTaskWriter is the write surface StoreTaskCreator needs. It
// returns the created task's real database ID, not a *workflow.Task  --
// gateway deliberately has no dependency on the task store or
// internal/domain/workflow; the daemon supplies an adapter closure over
// the store.TaskStore method of the same name.
type chatTaskWriter interface {
	EnqueueChatTask(ctx context.Context, owner, repo, title, body, workflow, identity string, inputs map[string]any) (taskID int64, err error)
}

// NewStoreTaskCreator returns a TaskCreator that enqueues chat-spawned
// tasks via the daemon's store. defaultOwner/defaultRepo are used when
// a spawn request doesn't specify repo=owner/name explicitly. repos
// lists every "owner/name" pair the identity is configured for (the
// default included); an explicit repo= selection outside this set is
// rejected. When defaultOwner/defaultRepo are empty, /spawn without an
// explicit repo= returns a configuration error.
func NewStoreTaskCreator(sw chatTaskWriter, defaultOwner, defaultRepo string, repos []string) *StoreTaskCreator {
	allowed := make(map[string]bool, len(repos))
	for _, r := range repos {
		allowed[r] = true
	}
	return &StoreTaskCreator{store: sw, defaultOwner: defaultOwner, defaultRepo: defaultRepo, allowed: allowed}
}

// NewStoreTaskCreatorForProfiles returns a creator that resolves repository
// defaults and allow-lists from the identity selected in each request.
func NewStoreTaskCreatorForProfiles(sw chatTaskWriter, profiles []TaskProfile) *StoreTaskCreator {
	byIdentity := make(map[string]taskProfile, len(profiles))
	for _, profile := range profiles {
		allowed := make(map[string]bool, len(profile.Repos))
		for _, repo := range profile.Repos {
			allowed[repo] = true
		}
		byIdentity[profile.Identity] = taskProfile{
			defaultOwner: profile.DefaultOwner,
			defaultRepo:  profile.DefaultRepo,
			allowed:      allowed,
		}
	}
	return &StoreTaskCreator{store: sw, profiles: byIdentity}
}

func (c *StoreTaskCreator) CreateTask(ctx context.Context, req SpawnRequest) (int64, error) {
	owner, repo, allowed := c.defaultOwner, c.defaultRepo, c.allowed
	if c.profiles != nil {
		profile, ok := c.profiles[req.Identity]
		if !ok {
			return 0, fmt.Errorf("identity %q is not configured for chat tasks", req.Identity)
		}
		owner, repo, allowed = profile.defaultOwner, profile.defaultRepo, profile.allowed
	}
	if req.Repo != "" {
		if !allowed[req.Repo] {
			return 0, fmt.Errorf("repo %q is not configured for this identity", req.Repo)
		}
		o, r, ok := splitOwnerRepo(req.Repo)
		if !ok {
			return 0, fmt.Errorf("repo %q must be owner/name", req.Repo)
		}
		owner, repo = o, r
	}
	if owner == "" || repo == "" {
		return 0, fmt.Errorf("no repo configured for chat-spawned tasks")
	}
	return c.store.EnqueueChatTask(ctx, owner, repo, req.Title, req.Body, req.Workflow, req.Identity, req.Inputs)
}

func splitOwnerRepo(s string) (owner, repo string, ok bool) {
	for i, c := range s {
		if c == '/' {
			return s[:i], s[i+1:], s[:i] != "" && s[i+1:] != ""
		}
	}
	return "", "", false
}

// Task lifecycle statuses come from internal/taskstate, a leaf package with
// no dependencies, so gateway stays decoupled from the task store without
// keeping a hand-synced copy of the strings. The copy that used to live here
// is how the dashboard and chat ended up recording different states for the
// same operator decision.

// ChatTaskStatus is the minimal task state StoreTaskController needs to
// authorize and validate /approve and /cancel.
type ChatTaskStatus struct {
	Status   string
	Identity string
}

// chatTaskController is the read/write surface StoreTaskController
// needs. The daemon supplies an adapter over store.TaskStore's
// TaskByID, Requeue, and Transition methods.
type chatTaskController interface {
	// ChatTaskStatus returns the task's status and owning identity, and
	// false if no task with that ID exists.
	ChatTaskStatus(ctx context.Context, taskID int64) (ChatTaskStatus, bool, error)
	// ApproveChatTask releases a waiting_human task through the daemon's one
	// task-action service, so chat and the dashboard cannot record different
	// decisions for one operator intent (docs/prds/pr-review-operator-
	// response.md, Decision 1). The caller has already validated the current
	// status is waiting_human; the actor is the chat-bound identity, never an
	// operator acting across identities. There is no review-gate payload: the
	// chat surface has no instruction or selection syntax, so an approve posts
	// every offered finding.
	ApproveChatTask(ctx context.Context, taskID int64, actor taskactions.Actor) error
	// CancelChatTask transitions an active task to a rejected/terminal
	// state. Callers must have already validated the current status is
	// cancellable.
	CancelChatTask(ctx context.Context, taskID int64, reason string) error
}

// TaskRuntime reaches tasks that are currently executing. The daemon
// implements it; the store cannot, because a row records what a task is
// meant to be doing, not the goroutine doing it. It is only the delivery
// mechanism: the store's CancelExecution record precedes it.
type TaskRuntime interface {
	// CancelTask interrupts one running task, reporting whether it was
	// running.
	CancelTask(taskID int64) bool
}

// StoreTaskController implements TaskController backed by a store
// interface. Authorization requires the caller identity to exactly
// match the task identity, including the empty identity (single-identity deployments).
type StoreTaskController struct {
	store chatTaskController
	// runtime interrupts running work. Nil leaves running tasks
	// uninterruptible, which is the pre-existing behaviour and is
	// reported as such rather than silently succeeding.
	runtime TaskRuntime
}

// NewStoreTaskController returns a TaskController backed by c.
func NewStoreTaskController(c chatTaskController) *StoreTaskController {
	return &StoreTaskController{store: c}
}

// WithRuntime returns a controller that can also interrupt running tasks.
func (c *StoreTaskController) WithRuntime(rt TaskRuntime) *StoreTaskController {
	c.runtime = rt
	return c
}

func (c *StoreTaskController) Approve(ctx context.Context, taskID int64, identity string) error {
	st, err := c.authorize(ctx, taskID, identity)
	if err != nil {
		return err
	}
	if err := taskstate.CheckApprove(st.Status); err != nil {
		return err
	}
	return c.store.ApproveChatTask(ctx, taskID, taskactions.ActorFromScope(identity))
}

func (c *StoreTaskController) Cancel(ctx context.Context, taskID int64, identity string) error {
	st, err := c.authorize(ctx, taskID, identity)
	if err != nil {
		return err
	}
	if err := taskstate.CheckDecline(st.Status); err != nil {
		return err
	}
	// The store records the cancellation first -- the one cancel path -- and
	// the context cancel delivers it. The order is the PRD's: a worker that
	// keeps writing after the record fails ErrStaleTransition on its next
	// step write, so it cannot win the race the old interrupt-first ordering
	// guarded against. A task the store says is running but nothing is
	// executing for (a crashed or migrated daemon) is unstuck by the record
	// alone, which is why the delivery is reported, not required.
	if err := c.store.CancelChatTask(ctx, taskID, "declined by "+identity); err != nil {
		return err
	}
	if st.Status == taskstate.Running && c.runtime != nil {
		c.runtime.CancelTask(taskID)
	}
	return nil
}

func (c *StoreTaskController) authorize(ctx context.Context, taskID int64, identity string) (ChatTaskStatus, error) {
	st, ok, err := c.store.ChatTaskStatus(ctx, taskID)
	if err != nil {
		return ChatTaskStatus{}, fmt.Errorf("lookup failed: %w", err)
	}
	if !ok {
		return ChatTaskStatus{}, fmt.Errorf("task %d not found", taskID)
	}
	if st.Identity != identity {
		return ChatTaskStatus{}, fmt.Errorf("task belongs to a different identity")
	}
	return st, nil
}
