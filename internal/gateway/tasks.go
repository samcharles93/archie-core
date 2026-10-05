package gateway

import (
	"context"
	"fmt"

	"github.com/samcharles93/archie-core/internal/domain/taskactions"
	"github.com/samcharles93/archie-core/internal/taskstate"
)

// StoreTaskCreator creates chat-spawned tasks with a synthetic issue number,
// using the default repo when none is given.
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

// chatTaskWriter creates a chat task and returns its database ID.
type chatTaskWriter interface {
	EnqueueChatTask(ctx context.Context, owner, repo, title, body, workflow, identity, origin string, inputs map[string]any) (taskID int64, err error)
}

// NewStoreTaskCreator returns a TaskCreator. repos lists the allowed
// "owner/name" pairs; without a default repo, a spawn must name one.
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
	return c.store.EnqueueChatTask(ctx, owner, repo, req.Title, req.Body, req.Workflow, req.Identity, req.Origin, req.Inputs)
}

func splitOwnerRepo(s string) (owner, repo string, ok bool) {
	for i, c := range s {
		if c == '/' {
			return s[:i], s[i+1:], s[:i] != "" && s[i+1:] != ""
		}
	}
	return "", "", false
}

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
	// ApproveChatTask approves a waiting_human task through the task-action
	// service, posting every offered finding.
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
	// the context cancel delivers it. A task the store says is running but
	// nothing is executing for (a crashed or migrated daemon) is unstuck by the
	// record alone, which is why the delivery is reported, not required.
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
