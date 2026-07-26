package gateway

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"
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

var lastSyntheticIssueNumber atomic.Int64

// chatTaskWriter is the write surface StoreTaskCreator needs. It
// returns the created task's real database ID, not a *store.Task  --
// gateway deliberately has no dependency on internal/store; the daemon
// supplies an adapter closure over the store.TaskStore method of the
// same name.
type chatTaskWriter interface {
	EnqueueChatTask(ctx context.Context, owner, repo, title, body, workflow, identity string, issueNumber int) (taskID int64, err error)
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
	// Synthetic issue number  --  prevents collisions with real forge
	// issues (which are small sequential ints) while staying within the
	// store's existing (owner, repo, number) uniqueness constraint. The
	// task's real ID (not this number) is what /approve and /cancel use.
	number := nextSyntheticIssueNumber()
	return c.store.EnqueueChatTask(ctx, owner, repo, req.Title, "", req.Workflow, req.Identity, number)
}

func nextSyntheticIssueNumber() int {
	candidate := time.Now().UnixMicro()
	for {
		previous := lastSyntheticIssueNumber.Load()
		if candidate <= previous {
			candidate = previous + 1
		}
		if lastSyntheticIssueNumber.CompareAndSwap(previous, candidate) {
			return int(candidate)
		}
	}
}

func splitOwnerRepo(s string) (owner, repo string, ok bool) {
	for i, c := range s {
		if c == '/' {
			return s[:i], s[i+1:], s[:i] != "" && s[i+1:] != ""
		}
	}
	return "", "", false
}

// Task lifecycle status strings this controller reasons about. Kept as
// local constants (rather than importing internal/store) to preserve
// gateway's deliberate decoupling from the store package; must be kept
// in sync with store.StatusX by hand  --  see StoreTaskController's doc.
const (
	statusRunning      = "running"
	statusWaitingHuman = "waiting_human"
	statusMerged       = "merged"
	statusParked       = "parked"
	statusRejected     = "rejected"
	statusDead         = "dead"
	statusClosedWontDo = "closed_wont_do"
)

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
	// ApproveChatTask requeues a waiting_human task. Callers must have
	// already validated the current status is waiting_human.
	ApproveChatTask(ctx context.Context, taskID int64) error
	// CancelChatTask transitions an active task to a rejected/terminal
	// state. Callers must have already validated the current status is
	// cancellable.
	CancelChatTask(ctx context.Context, taskID int64, reason string) error
}

// StoreTaskController implements TaskController backed by a store
// interface. Authorization requires the caller identity to exactly
// match the task identity, including the empty legacy identity.
type StoreTaskController struct {
	store chatTaskController
}

// NewStoreTaskController returns a TaskController backed by c.
func NewStoreTaskController(c chatTaskController) *StoreTaskController {
	return &StoreTaskController{store: c}
}

func (c *StoreTaskController) Approve(ctx context.Context, taskID int64, identity string) error {
	st, err := c.authorize(ctx, taskID, identity)
	if err != nil {
		return err
	}
	if st.Status != statusWaitingHuman {
		return fmt.Errorf("task is %s, not waiting_human", st.Status)
	}
	return c.store.ApproveChatTask(ctx, taskID)
}

func (c *StoreTaskController) Cancel(ctx context.Context, taskID int64, identity string) error {
	st, err := c.authorize(ctx, taskID, identity)
	if err != nil {
		return err
	}
	switch st.Status {
	case statusRunning:
		return fmt.Errorf("task is actively running; cannot cancel until it reaches a waiting state")
	case statusMerged, statusParked, statusRejected, statusDead, statusClosedWontDo:
		return fmt.Errorf("task is already %s", st.Status)
	}
	return c.store.CancelChatTask(ctx, taskID, "cancelled via chat by "+identity)
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
