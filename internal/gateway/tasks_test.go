package gateway

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// ── StoreTaskCreator ─────────────────────────────────────────────────

type fakeChatTaskWriter struct {
	owner, repo, title, body, workflow, identity string
	issueNumber                                  int
	calls                                        int
	nextID                                       int64
	err                                          error
}

func (f *fakeChatTaskWriter) EnqueueChatTask(ctx context.Context, owner, repo, title, body, workflow, identity string, issueNumber int) (int64, error) {
	f.owner, f.repo, f.title, f.body, f.workflow, f.identity, f.issueNumber = owner, repo, title, body, workflow, identity, issueNumber
	f.calls++
	if f.err != nil {
		return 0, f.err
	}
	f.nextID++
	return f.nextID, nil
}

func TestStoreTaskCreatorCreatesTask(t *testing.T) {
	sw := &fakeChatTaskWriter{}
	tc := NewStoreTaskCreator(sw, "acme", "example-service", []string{"acme/example-service"})
	id, err := tc.CreateTask(context.Background(), SpawnRequest{Title: "Fix the login bug"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if id == 0 {
		t.Error("task id should be non-zero")
	}
	if sw.owner != "acme" || sw.repo != "example-service" {
		t.Errorf("owner/repo = %s/%s, want acme/example-service", sw.owner, sw.repo)
	}
	if sw.title != "Fix the login bug" {
		t.Errorf("title = %q", sw.title)
	}
}

func TestStoreTaskCreatorRejectsEmptyRepo(t *testing.T) {
	tc := NewStoreTaskCreator(&fakeChatTaskWriter{}, "", "", nil)
	_, err := tc.CreateTask(context.Background(), SpawnRequest{Title: "something"})
	if err == nil || !strings.Contains(err.Error(), "no repo configured") {
		t.Errorf("err = %v, want 'no repo configured'", err)
	}
}

func TestStoreTaskCreatorSyntheticNumbersDiffer(t *testing.T) {
	sw := &fakeChatTaskWriter{}
	tc := NewStoreTaskCreator(sw, "acme", "example-service", []string{"acme/example-service"})
	if _, err := tc.CreateTask(context.Background(), SpawnRequest{Title: "first"}); err != nil {
		t.Fatal(err)
	}
	first := sw.issueNumber
	if _, err := tc.CreateTask(context.Background(), SpawnRequest{Title: "second"}); err != nil {
		t.Fatal(err)
	}
	if first == sw.issueNumber {
		t.Errorf("synthetic issue numbers collided: %d", first)
	}
	const maxExactJSONInteger = 1<<53 - 1
	if sw.issueNumber > maxExactJSONInteger {
		t.Errorf("synthetic issue number %d exceeds JSON's exact integer range", sw.issueNumber)
	}
}

func TestStoreTaskCreatorReturnsRealID(t *testing.T) {
	// The task ID returned to chat must be the store's real database
	// ID (whatever EnqueueChatTask returns), never the synthetic issue
	// number used for uniqueness.
	sw := &fakeChatTaskWriter{nextID: 999}
	tc := NewStoreTaskCreator(sw, "acme", "example-service", []string{"acme/example-service"})
	id, err := tc.CreateTask(context.Background(), SpawnRequest{Title: "task"})
	if err != nil {
		t.Fatal(err)
	}
	if id != 1000 {
		t.Errorf("id = %d, want 1000 (fake's nextID+1)", id)
	}
	if id == int64(sw.issueNumber) {
		t.Error("returned ID must not equal the synthetic issue number")
	}
}

func TestStoreTaskCreatorExplicitRepoAllowed(t *testing.T) {
	sw := &fakeChatTaskWriter{}
	tc := NewStoreTaskCreator(sw, "acme", "example-service", []string{"acme/example-service", "acme/archie-core"})
	_, err := tc.CreateTask(context.Background(), SpawnRequest{Title: "x", Repo: "acme/archie-core"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if sw.owner != "acme" || sw.repo != "archie-core" {
		t.Errorf("owner/repo = %s/%s, want acme/archie-core", sw.owner, sw.repo)
	}
}

func TestStoreTaskCreatorExplicitRepoNotAllowedRejected(t *testing.T) {
	sw := &fakeChatTaskWriter{}
	tc := NewStoreTaskCreator(sw, "acme", "example-service", []string{"acme/example-service"})
	_, err := tc.CreateTask(context.Background(), SpawnRequest{Title: "x", Repo: "someone-else/private-repo"})
	if err == nil || !strings.Contains(err.Error(), "not configured for this identity") {
		t.Errorf("err = %v, want repo-not-allowed error", err)
	}
	if sw.calls != 0 {
		t.Error("EnqueueChatTask must not be called for a disallowed repo")
	}
}

func TestStoreTaskCreatorMalformedRepoRejected(t *testing.T) {
	tc := NewStoreTaskCreator(&fakeChatTaskWriter{}, "acme", "example-service", []string{"no-slash"})
	_, err := tc.CreateTask(context.Background(), SpawnRequest{Title: "x", Repo: "no-slash"})
	if err == nil || !strings.Contains(err.Error(), "owner/name") {
		t.Errorf("err = %v, want owner/name format error", err)
	}
}

func TestStoreTaskCreatorPropagatesWorkflowAndIdentity(t *testing.T) {
	sw := &fakeChatTaskWriter{}
	tc := NewStoreTaskCreator(sw, "acme", "example-service", []string{"acme/example-service"})
	_, err := tc.CreateTask(context.Background(), SpawnRequest{Title: "x", Workflow: "tdd", Identity: "archie"})
	if err != nil {
		t.Fatal(err)
	}
	if sw.workflow != "tdd" || sw.identity != "archie" {
		t.Errorf("workflow/identity = %s/%s, want tdd/archie", sw.workflow, sw.identity)
	}
}

func TestStoreTaskCreatorPropagatesStoreError(t *testing.T) {
	sw := &fakeChatTaskWriter{err: fmt.Errorf("store unavailable")}
	tc := NewStoreTaskCreator(sw, "acme", "example-service", []string{"acme/example-service"})
	_, err := tc.CreateTask(context.Background(), SpawnRequest{Title: "x"})
	if err == nil || !strings.Contains(err.Error(), "store unavailable") {
		t.Errorf("err = %v, want store error propagated", err)
	}
}

func TestStoreTaskCreatorSelectsIdentityProfile(t *testing.T) {
	sw := &fakeChatTaskWriter{}
	tc := NewStoreTaskCreatorForProfiles(sw, []TaskProfile{
		{Identity: "builder", DefaultOwner: "acme", DefaultRepo: "archie-core", Repos: []string{"acme/archie-core"}},
		{Identity: "reviewer", DefaultOwner: "acme", DefaultRepo: "example-service", Repos: []string{"acme/example-service"}},
	})

	_, err := tc.CreateTask(context.Background(), SpawnRequest{Title: "review this", Identity: "reviewer"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if sw.owner != "acme" || sw.repo != "example-service" || sw.identity != "reviewer" {
		t.Errorf("created task = %s/%s identity=%s, want acme/example-service identity=reviewer", sw.owner, sw.repo, sw.identity)
	}
}

func TestStoreTaskCreatorRejectsUnknownIdentity(t *testing.T) {
	sw := &fakeChatTaskWriter{}
	tc := NewStoreTaskCreatorForProfiles(sw, []TaskProfile{
		{Identity: "builder", DefaultOwner: "acme", DefaultRepo: "archie-core", Repos: []string{"acme/archie-core"}},
	})

	_, err := tc.CreateTask(context.Background(), SpawnRequest{Title: "x", Identity: "unknown"})
	if err == nil || !strings.Contains(err.Error(), "identity") {
		t.Errorf("err = %v, want unknown identity error", err)
	}
	if sw.calls != 0 {
		t.Error("EnqueueChatTask must not be called for an unknown identity")
	}
}

// ── StoreTaskController ──────────────────────────────────────────────

type fakeChatTaskStore struct {
	statuses  map[int64]ChatTaskStatus
	approved  []int64
	cancelled []int64
	lookupErr error
}

func (f *fakeChatTaskStore) ChatTaskStatus(ctx context.Context, taskID int64) (ChatTaskStatus, bool, error) {
	if f.lookupErr != nil {
		return ChatTaskStatus{}, false, f.lookupErr
	}
	st, ok := f.statuses[taskID]
	return st, ok, nil
}

func (f *fakeChatTaskStore) ApproveChatTask(ctx context.Context, taskID int64) error {
	f.approved = append(f.approved, taskID)
	return nil
}

func (f *fakeChatTaskStore) CancelChatTask(ctx context.Context, taskID int64, reason string) error {
	f.cancelled = append(f.cancelled, taskID)
	return nil
}

func TestStoreTaskControllerApproveWaitingHuman(t *testing.T) {
	store := &fakeChatTaskStore{statuses: map[int64]ChatTaskStatus{1: {Status: statusWaitingHuman}}}
	c := NewStoreTaskController(store)
	if err := c.Approve(context.Background(), 1, ""); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if len(store.approved) != 1 || store.approved[0] != 1 {
		t.Errorf("approved = %v, want [1]", store.approved)
	}
}

func TestStoreTaskControllerApproveRejectsWrongStatus(t *testing.T) {
	for _, status := range []string{"queued", "running", "pr_open", "merged"} {
		store := &fakeChatTaskStore{statuses: map[int64]ChatTaskStatus{1: {Status: status}}}
		c := NewStoreTaskController(store)
		err := c.Approve(context.Background(), 1, "")
		if err == nil {
			t.Errorf("status %s: expected error, got nil", status)
		}
		if len(store.approved) != 0 {
			t.Errorf("status %s: ApproveChatTask must not be called", status)
		}
	}
}

func TestStoreTaskControllerApproveTaskNotFound(t *testing.T) {
	c := NewStoreTaskController(&fakeChatTaskStore{statuses: map[int64]ChatTaskStatus{}})
	err := c.Approve(context.Background(), 99, "")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("err = %v, want not-found error", err)
	}
}

func TestStoreTaskControllerApproveCrossIdentityRejected(t *testing.T) {
	store := &fakeChatTaskStore{statuses: map[int64]ChatTaskStatus{1: {Status: statusWaitingHuman, Identity: "archie"}}}
	c := NewStoreTaskController(store)
	err := c.Approve(context.Background(), 1, "winter")
	if err == nil || !strings.Contains(err.Error(), "different identity") {
		t.Errorf("err = %v, want cross-identity rejection", err)
	}
	if len(store.approved) != 0 {
		t.Error("ApproveChatTask must not be called across identities")
	}
}

func TestStoreTaskControllerApproveSameIdentityAllowed(t *testing.T) {
	store := &fakeChatTaskStore{statuses: map[int64]ChatTaskStatus{1: {Status: statusWaitingHuman, Identity: "archie"}}}
	c := NewStoreTaskController(store)
	if err := c.Approve(context.Background(), 1, "archie"); err != nil {
		t.Fatalf("Approve: %v", err)
	}
}

func TestStoreTaskControllerRejectsCallerIdentityForLegacyTask(t *testing.T) {
	store := &fakeChatTaskStore{statuses: map[int64]ChatTaskStatus{1: {Status: statusWaitingHuman}}}
	c := NewStoreTaskController(store)
	if err := c.Approve(context.Background(), 1, "anyone"); err == nil || !strings.Contains(err.Error(), "different identity") {
		t.Fatalf("Approve error = %v, want cross-identity rejection", err)
	}
}

func TestStoreTaskControllerCancelActiveStates(t *testing.T) {
	for _, status := range []string{"queued", "waiting_human", "pr_open"} {
		store := &fakeChatTaskStore{statuses: map[int64]ChatTaskStatus{1: {Status: status}}}
		c := NewStoreTaskController(store)
		if err := c.Cancel(context.Background(), 1, ""); err != nil {
			t.Errorf("status %s: Cancel: %v", status, err)
		}
		if len(store.cancelled) != 1 {
			t.Errorf("status %s: CancelChatTask should be called", status)
		}
	}
}

// fakeTaskRuntime records the interruptions a controller asks for.
type fakeTaskRuntime struct {
	cancelled []int64
	running   bool
	stopped   []int64
	identity  string
}

func (f *fakeTaskRuntime) CancelTask(taskID int64) bool {
	f.cancelled = append(f.cancelled, taskID)
	return f.running
}

func (f *fakeTaskRuntime) CancelRunningTasks(identity string) []int64 {
	f.identity = identity
	return f.stopped
}

// TestStoreTaskControllerCancelInterruptsRunning covers the case that used
// to be refused. A running task is the only one worth interrupting, so
// cancelling it must reach the work and then record the outcome.
func TestStoreTaskControllerCancelInterruptsRunning(t *testing.T) {
	store := &fakeChatTaskStore{statuses: map[int64]ChatTaskStatus{1: {Status: statusRunning}}}
	rt := &fakeTaskRuntime{running: true}
	c := NewStoreTaskController(store).WithRuntime(rt)

	if err := c.Cancel(context.Background(), 1, ""); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if len(rt.cancelled) != 1 || rt.cancelled[0] != 1 {
		t.Errorf("runtime cancellations = %v, want [1]; the task kept running", rt.cancelled)
	}
	if len(store.cancelled) != 1 {
		t.Error("the cancellation was not recorded in the store")
	}
}

// TestStoreTaskControllerCancelRecordsStaleRunning covers a task the store
// calls running that nothing is executing -- a crashed or replaced daemon.
// Recording the terminal state is what unsticks it.
func TestStoreTaskControllerCancelRecordsStaleRunning(t *testing.T) {
	store := &fakeChatTaskStore{statuses: map[int64]ChatTaskStatus{1: {Status: statusRunning}}}
	rt := &fakeTaskRuntime{running: false}
	c := NewStoreTaskController(store).WithRuntime(rt)

	if err := c.Cancel(context.Background(), 1, ""); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if len(store.cancelled) != 1 {
		t.Error("a stale running task was left stuck")
	}
}

// TestStoreTaskControllerCancelWithoutRuntimeRefusesRunning keeps the
// refusal honest when nothing can interrupt the work: recording the task
// as cancelled while it carries on would be a lie.
func TestStoreTaskControllerCancelWithoutRuntimeRefusesRunning(t *testing.T) {
	store := &fakeChatTaskStore{statuses: map[int64]ChatTaskStatus{1: {Status: statusRunning}}}
	c := NewStoreTaskController(store)

	err := c.Cancel(context.Background(), 1, "")
	if err == nil || !strings.Contains(err.Error(), "no runtime control") {
		t.Errorf("err = %v, want a refusal naming the missing runtime", err)
	}
	if len(store.cancelled) != 0 {
		t.Error("CancelChatTask must not be called when the task cannot be interrupted")
	}
}

// TestStoreTaskControllerStopRunningIsIdentityScoped checks the agent half
// of /stop reaches the runtime and stays within the caller's identity.
func TestStoreTaskControllerStopRunningIsIdentityScoped(t *testing.T) {
	rt := &fakeTaskRuntime{stopped: []int64{4, 7}}
	c := NewStoreTaskController(&fakeChatTaskStore{statuses: map[int64]ChatTaskStatus{}}).WithRuntime(rt)

	stopped, err := c.StopRunning(context.Background(), "archie")
	if err != nil {
		t.Fatalf("StopRunning: %v", err)
	}
	if len(stopped) != 2 || stopped[0] != 4 || stopped[1] != 7 {
		t.Errorf("stopped = %v, want [4 7]", stopped)
	}
	if rt.identity != "archie" {
		t.Errorf("identity = %q, want the caller's identity", rt.identity)
	}
}

func TestStoreTaskControllerStopRunningWithoutRuntime(t *testing.T) {
	c := NewStoreTaskController(&fakeChatTaskStore{statuses: map[int64]ChatTaskStatus{}})
	if _, err := c.StopRunning(context.Background(), ""); err == nil {
		t.Error("StopRunning reported success with no runtime wired")
	}
}

func TestStoreTaskControllerCancelRejectsTerminalStates(t *testing.T) {
	for _, status := range []string{"merged", "parked", "rejected", "dead", "closed_wont_do"} {
		store := &fakeChatTaskStore{statuses: map[int64]ChatTaskStatus{1: {Status: status}}}
		c := NewStoreTaskController(store)
		err := c.Cancel(context.Background(), 1, "")
		if err == nil {
			t.Errorf("status %s: expected error, got nil", status)
		}
		if len(store.cancelled) != 0 {
			t.Errorf("status %s: CancelChatTask must not be called", status)
		}
	}
}

func TestStoreTaskControllerCancelCrossIdentityRejected(t *testing.T) {
	store := &fakeChatTaskStore{statuses: map[int64]ChatTaskStatus{1: {Status: statusQueuedForTest, Identity: "archie"}}}
	c := NewStoreTaskController(store)
	err := c.Cancel(context.Background(), 1, "winter")
	if err == nil || !strings.Contains(err.Error(), "different identity") {
		t.Errorf("err = %v, want cross-identity rejection", err)
	}
}

func TestStoreTaskControllerLookupErrorPropagates(t *testing.T) {
	store := &fakeChatTaskStore{lookupErr: fmt.Errorf("db unavailable")}
	c := NewStoreTaskController(store)
	if err := c.Approve(context.Background(), 1, ""); err == nil || !strings.Contains(err.Error(), "db unavailable") {
		t.Errorf("Approve err = %v, want lookup error propagated", err)
	}
	if err := c.Cancel(context.Background(), 1, ""); err == nil || !strings.Contains(err.Error(), "db unavailable") {
		t.Errorf("Cancel err = %v, want lookup error propagated", err)
	}
}

// statusQueuedForTest avoids a naming collision with the package's own
// unexported statusRunning/statusWaitingHuman constants in test tables.
const statusQueuedForTest = "queued"
