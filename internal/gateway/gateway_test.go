package gateway

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
)

type fakeStore struct {
	counts map[string]int
	err    error
}

func (f *fakeStore) StatusCounts(ctx context.Context) (map[string]int, error) {
	return f.counts, f.err
}

func fakeLLM(ctx context.Context, msg Message) (string, error) {
	return "llm: " + msg.Text, nil
}

func TestRouteStatusWithTasks(t *testing.T) {
	r := NewRouter(&fakeStore{counts: map[string]int{"queued": 2, "pr_open": 1}}, nil, "test")
	reply, err := r.Route(context.Background(), Message{Text: "/status"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "queued: 2") || !strings.Contains(reply, "pr_open: 1") {
		t.Errorf("reply missing counts: %q", reply)
	}
}

func TestRouteStatusIncludesActiveProviderAndModel(t *testing.T) {
	manager := &fakeProviderModelManager{
		fakeModelManager: fakeModelManager{
			models:      []string{"openai/gpt-5.6", "openrouter/openai/gpt-5.6"},
			activeModel: "openai/gpt-5.6",
		},
	}
	r := NewRouter(&fakeStore{counts: map[string]int{"running": 1}}, nil, "test")
	r.Models = manager

	reply, err := r.Route(context.Background(), Message{Text: "/status"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(reply, "Provider: openai") || !strings.Contains(reply, "Model: openai/gpt-5.6") {
		t.Fatalf("status missing active provider/model:\n%s", reply)
	}
}

func TestRouteStatusAtMention(t *testing.T) {
	r := NewRouter(&fakeStore{counts: map[string]int{"running": 3}}, nil, "archie")
	reply, err := r.Route(context.Background(), Message{Text: "/status@archie"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "running: 3") {
		t.Errorf("reply missing counts: %q", reply)
	}
}

func TestRouteStatusEmpty(t *testing.T) {
	r := NewRouter(&fakeStore{counts: map[string]int{}}, nil, "test")
	reply, err := r.Route(context.Background(), Message{Text: "/status"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if reply != "No tasks yet." {
		t.Errorf("reply = %q, want %q", reply, "No tasks yet.")
	}
}

func TestRouteStatusError(t *testing.T) {
	r := NewRouter(&fakeStore{err: errors.New("db down")}, nil, "test")
	if _, err := r.Route(context.Background(), Message{Text: "/status"}); err == nil {
		t.Error("expected error from Route when StatusCounts fails")
	}
}

func TestRouteNonCommandWithLLM(t *testing.T) {
	r := NewRouter(nil, fakeLLM, "test")
	reply, err := r.Route(context.Background(), Message{Text: "hello"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if reply != "llm: hello" {
		t.Errorf("reply = %q, want %q", reply, "llm: hello")
	}
}

func TestRouteNonCommandWithoutLLM(t *testing.T) {
	r := NewRouter(nil, nil, "test")
	reply, err := r.Route(context.Background(), Message{Text: "hello"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "/status") {
		t.Errorf("reply = %q, want mention of /status", reply)
	}
}

func TestRouterRejectsUnknownCommand(t *testing.T) {
	tests := []string{
		"/frobnicate",
		"/frobnicate with arguments",
		"/frobnicate@test with arguments",
	}
	for _, text := range tests {
		t.Run(text, func(t *testing.T) {
			llmCalls := 0
			r := NewRouter(nil, func(context.Context, Message) (string, error) {
				llmCalls++
				return "fabricated command behavior", nil
			}, "test")
			r.LLMStream = func(context.Context, Message, func(string)) (string, error) {
				llmCalls++
				return "fabricated streaming behavior", nil
			}

			reply, err := r.RouteStream(context.Background(), Message{Text: text}, func(string) {})
			if err != nil {
				t.Fatalf("RouteStream: %v", err)
			}
			if llmCalls != 0 {
				t.Fatalf("unknown command invoked LLM %d times", llmCalls)
			}
			if !strings.Contains(reply, "/frobnicate") || !strings.Contains(reply, "/help") {
				t.Errorf("reply = %q, want unknown command and /help guidance", reply)
			}
		})
	}
}

// -- ModelManager fakes for /model tests --

type fakeModelManager struct {
	models      []string
	activeModel string
	setErr      error
}

type fakeProviderModelManager struct {
	fakeModelManager
}

func (f *fakeProviderModelManager) Providers() []string {
	return []string{"openai", "openrouter"}
}

func (f *fakeProviderModelManager) ActiveProvider() string {
	provider, _, _ := strings.Cut(f.activeModel, "/")
	return provider
}

func (f *fakeProviderModelManager) ModelsForProvider(provider string) []string {
	var models []string
	for _, model := range f.models {
		if strings.HasPrefix(model, provider+"/") {
			models = append(models, model)
		}
	}
	return models
}

func (f *fakeProviderModelManager) SetActiveProvider(_ context.Context, provider string) error {
	models := f.ModelsForProvider(provider)
	if len(models) == 0 {
		return fmt.Errorf("unknown provider: %s", provider)
	}
	f.activeModel = models[0]
	return nil
}

func (f *fakeModelManager) Models() []string {
	return f.models
}

func (f *fakeModelManager) ActiveModel() string {
	return f.activeModel
}

func (f *fakeModelManager) SetActiveModel(_ context.Context, ref string) error {
	if f.setErr != nil {
		return f.setErr
	}
	if slices.Contains(f.models, ref) {
		f.activeModel = ref
		return nil
	}
	return fmt.Errorf("unknown model: %s", ref)
}

// -- /model tests --

func TestRouteModelsIsNoLongerACommand(t *testing.T) {
	r := NewRouter(nil, nil, "test-gw")
	reply, err := r.Route(context.Background(), Message{Text: "/models"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(reply, "Unknown command /models") {
		t.Errorf("reply = %q, want /models to be unavailable", reply)
	}
}

func TestRouteModelSwitch(t *testing.T) {
	mgr := &fakeModelManager{
		models: []string{"a/b", "c/d"},
	}
	r := NewRouter(nil, nil, "test-gw")
	r.Models = mgr
	reply, err := r.Route(context.Background(), Message{Text: "/model a/b"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "a/b") || !strings.Contains(reply, "set to") {
		t.Errorf("reply should confirm model switch: %q", reply)
	}
	if mgr.activeModel != "a/b" {
		t.Errorf("active model = %q, want %q", mgr.activeModel, "a/b")
	}
}

func TestRouteModelAtMention(t *testing.T) {
	mgr := &fakeModelManager{
		models: []string{"a/b"},
	}
	r := NewRouter(nil, nil, "test-gw")
	r.Models = mgr
	reply, err := r.Route(context.Background(), Message{Text: "/model@test-gw a/b"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "a/b") {
		t.Errorf("reply should confirm model switch: %q", reply)
	}
}

func TestRouteModelUnknown(t *testing.T) {
	mgr := &fakeModelManager{
		models: []string{"a/b"},
	}
	r := NewRouter(nil, nil, "test-gw")
	r.Models = mgr
	reply, err := r.Route(context.Background(), Message{Text: "/model unknown/unknown"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "Cannot switch") {
		t.Errorf("reply should mention cannot switch: %q", reply)
	}
}

func TestRouteModelNoArg(t *testing.T) {
	mgr := &fakeModelManager{
		models: []string{"a/b", "c/d"},
	}
	r := NewRouter(nil, nil, "test-gw")
	r.Models = mgr
	reply, err := r.Route(context.Background(), Message{Text: "/model"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "Usage") || !strings.Contains(reply, "a/b") || !strings.Contains(reply, "c/d") {
		t.Errorf("reply should show usage and available models: %q", reply)
	}
}

func TestRouteModelNoManager(t *testing.T) {
	r := NewRouter(nil, nil, "test-gw")
	reply, err := r.Route(context.Background(), Message{Text: "/model a/b"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "not configured") {
		t.Errorf("reply = %q, want 'not configured' message", reply)
	}
}

// -- TaskCreator fakes for /spawn tests --

type fakeTaskCreator struct {
	tasks    []string
	requests []SpawnRequest
	createFn func(ctx context.Context, req SpawnRequest) (int64, error)
}

func (f *fakeTaskCreator) CreateTask(ctx context.Context, req SpawnRequest) (int64, error) {
	f.tasks = append(f.tasks, req.Title)
	f.requests = append(f.requests, req)
	if f.createFn != nil {
		return f.createFn(ctx, req)
	}
	return int64(len(f.tasks)), nil
}

func TestRouteSpawnCreatesTask(t *testing.T) {
	tc := &fakeTaskCreator{}
	r := NewRouter(nil, nil, "test-gw")
	r.Tasks = tc
	reply, err := r.Route(context.Background(), Message{Text: "/spawn Fix the login bug"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "Created task") || !strings.Contains(reply, "1") {
		t.Errorf("reply should confirm task creation: %q", reply)
	}
	if len(tc.tasks) != 1 || tc.tasks[0] != "Fix the login bug" {
		t.Errorf("tasks = %v, want [Fix the login bug]", tc.tasks)
	}
}

func TestRouteSpawnAtMention(t *testing.T) {
	tc := &fakeTaskCreator{}
	r := NewRouter(nil, nil, "archie-bot")
	r.Tasks = tc
	reply, err := r.Route(context.Background(), Message{Text: "/spawn@archie-bot Deploy the release"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "Created task") {
		t.Errorf("reply should confirm creation: %q", reply)
	}
}

func TestRouteSpawnNoTitle(t *testing.T) {
	tc := &fakeTaskCreator{}
	r := NewRouter(nil, nil, "test-gw")
	r.Tasks = tc
	reply, err := r.Route(context.Background(), Message{Text: "/spawn"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "Usage") {
		t.Errorf("reply should show usage: %q", reply)
	}
	if len(tc.tasks) != 0 {
		t.Errorf("no task should be created without a title")
	}
}

func TestRouteSpawnNoManager(t *testing.T) {
	r := NewRouter(nil, nil, "test-gw")
	reply, err := r.Route(context.Background(), Message{Text: "/spawn something"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "not configured") {
		t.Errorf("reply = %q, want 'not configured'", reply)
	}
}

func TestRouteSpawnHandlesCreationError(t *testing.T) {
	tc := &fakeTaskCreator{
		createFn: func(ctx context.Context, req SpawnRequest) (int64, error) {
			return 0, fmt.Errorf("store unavailable")
		},
	}
	r := NewRouter(nil, nil, "test-gw")
	r.Tasks = tc
	reply, err := r.Route(context.Background(), Message{Text: "/spawn Break things"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "Failed") {
		t.Errorf("reply should mention failure: %q", reply)
	}
}

func TestRouteSpawnParsesRepoAndWorkflow(t *testing.T) {
	tc := &fakeTaskCreator{}
	r := NewRouter(nil, nil, "test-gw")
	r.Tasks = tc
	r.Identity = "archie"
	reply, err := r.Route(context.Background(), Message{Text: "/spawn repo=sam/example-service workflow=tdd Fix the flaky test"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "Created task") {
		t.Errorf("reply = %q, want task creation confirmation", reply)
	}
	if len(tc.requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(tc.requests))
	}
	got := tc.requests[0]
	if got.Repo != "sam/example-service" || got.Workflow != "tdd" || got.Title != "Fix the flaky test" || got.Identity != "archie" {
		t.Errorf("request = %+v, want {Repo:sam/example-service Workflow:tdd Title:\"Fix the flaky test\" Identity:archie}", got)
	}
}

func TestRouteSpawnParsesIdentity(t *testing.T) {
	tc := &fakeTaskCreator{}
	r := NewRouter(nil, nil, "test-gw")
	r.Tasks = tc
	r.Identity = "default"

	reply, err := r.Route(context.Background(), Message{
		Text: "/spawn identity=reviewer repo=sam/example-service workflow=tdd Fix the flaky test",
	})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "Created task") {
		t.Errorf("reply = %q, want task creation confirmation", reply)
	}
	got := tc.requests[0]
	if got.Identity != "reviewer" || got.Repo != "sam/example-service" || got.Workflow != "tdd" || got.Title != "Fix the flaky test" {
		t.Errorf("request = %+v, want selected identity, repo, workflow, and full title", got)
	}
}

func TestRouteSpawnWithoutRepoOrWorkflowUsesDefaults(t *testing.T) {
	tc := &fakeTaskCreator{}
	r := NewRouter(nil, nil, "test-gw")
	r.Tasks = tc
	reply, err := r.Route(context.Background(), Message{Text: "/spawn Fix the login bug"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "Created task") {
		t.Errorf("reply = %q", reply)
	}
	got := tc.requests[0]
	if got.Repo != "" || got.Workflow != "" || got.Title != "Fix the login bug" {
		t.Errorf("request = %+v, want empty Repo/Workflow and full title", got)
	}
}

func TestRouteSpawnTitleLooksLikeKeyValueIsPreserved(t *testing.T) {
	// A title that happens to contain "=" after the repo=/workflow=
	// tokens must not be mistaken for another key=value token.
	tc := &fakeTaskCreator{}
	r := NewRouter(nil, nil, "test-gw")
	r.Tasks = tc
	if _, err := r.Route(context.Background(), Message{Text: "/spawn repo=sam/example-service Fix x=y in config"}); err != nil {
		t.Fatalf("Route: %v", err)
	}
	got := tc.requests[0]
	if got.Repo != "sam/example-service" || got.Title != "Fix x=y in config" {
		t.Errorf("request = %+v, want Repo:sam/example-service Title:\"Fix x=y in config\"", got)
	}
}

// -- TaskController fakes for /approve and /cancel tests --

type fakeTaskController struct {
	approveFn  func(ctx context.Context, taskID int64, identity string) error
	cancelFn   func(ctx context.Context, taskID int64, identity string) error
	approved   []int64
	cancelled  []int64
	identities []string
}

func (f *fakeTaskController) Approve(ctx context.Context, taskID int64, identity string) error {
	f.approved = append(f.approved, taskID)
	f.identities = append(f.identities, identity)
	if f.approveFn != nil {
		return f.approveFn(ctx, taskID, identity)
	}
	return nil
}

func (f *fakeTaskController) Cancel(ctx context.Context, taskID int64, identity string) error {
	f.cancelled = append(f.cancelled, taskID)
	f.identities = append(f.identities, identity)
	if f.cancelFn != nil {
		return f.cancelFn(ctx, taskID, identity)
	}
	return nil
}

func TestRouteApproveParsesIdentity(t *testing.T) {
	fc := &fakeTaskController{}
	r := NewRouter(nil, nil, "test-gw")
	r.Controller = fc
	r.Identity = "default"

	reply, err := r.Route(context.Background(), Message{Text: "/approve identity=reviewer 42"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "approved") {
		t.Errorf("reply = %q, want mention of approval", reply)
	}
	if len(fc.approved) != 1 || fc.approved[0] != 42 || fc.identities[0] != "reviewer" {
		t.Errorf("approved/identities = %v/%v, want [42]/[reviewer]", fc.approved, fc.identities)
	}
}

func TestRouteApproveSuccess(t *testing.T) {
	fc := &fakeTaskController{}
	r := NewRouter(nil, nil, "test-gw")
	r.Controller = fc
	reply, err := r.Route(context.Background(), Message{Text: "/approve 42"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "approved") {
		t.Errorf("reply = %q, want mention of approval", reply)
	}
	if len(fc.approved) != 1 || fc.approved[0] != 42 {
		t.Errorf("approved = %v, want [42]", fc.approved)
	}
}

func TestRouteApproveNotConfigured(t *testing.T) {
	r := NewRouter(nil, nil, "test-gw")
	reply, err := r.Route(context.Background(), Message{Text: "/approve 1"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "not configured") {
		t.Errorf("reply = %q, want 'not configured'", reply)
	}
}

func TestRouteApproveBadID(t *testing.T) {
	fc := &fakeTaskController{}
	r := NewRouter(nil, nil, "test-gw")
	r.Controller = fc
	reply, err := r.Route(context.Background(), Message{Text: "/approve notanumber"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "Usage") {
		t.Errorf("reply = %q, want usage message", reply)
	}
	if len(fc.approved) != 0 {
		t.Error("Approve must not be called with an invalid ID")
	}
}

func TestRouteApproveWrongStateSurfacesError(t *testing.T) {
	fc := &fakeTaskController{
		approveFn: func(ctx context.Context, taskID int64, identity string) error {
			return fmt.Errorf("task is queued, not waiting_human")
		},
	}
	r := NewRouter(nil, nil, "test-gw")
	r.Controller = fc
	reply, err := r.Route(context.Background(), Message{Text: "/approve 7"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "Cannot approve") || !strings.Contains(reply, "waiting_human") {
		t.Errorf("reply = %q, want error surfaced to user", reply)
	}
}

func TestRouteCancelSuccess(t *testing.T) {
	fc := &fakeTaskController{}
	r := NewRouter(nil, nil, "test-gw")
	r.Controller = fc
	reply, err := r.Route(context.Background(), Message{Text: "/cancel 5"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "cancelled") {
		t.Errorf("reply = %q, want mention of cancellation", reply)
	}
	if len(fc.cancelled) != 1 || fc.cancelled[0] != 5 {
		t.Errorf("cancelled = %v, want [5]", fc.cancelled)
	}
}

func TestRouteCancelParsesIdentity(t *testing.T) {
	fc := &fakeTaskController{}
	r := NewRouter(nil, nil, "test-gw")
	r.Controller = fc
	r.Identity = "default"

	reply, err := r.Route(context.Background(), Message{Text: "/cancel identity=reviewer 5"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "cancelled") {
		t.Errorf("reply = %q, want mention of cancellation", reply)
	}
	if len(fc.cancelled) != 1 || fc.cancelled[0] != 5 || fc.identities[0] != "reviewer" {
		t.Errorf("cancelled/identities = %v/%v, want [5]/[reviewer]", fc.cancelled, fc.identities)
	}
}

func TestRouteCancelCrossIdentityRejected(t *testing.T) {
	fc := &fakeTaskController{
		cancelFn: func(ctx context.Context, taskID int64, identity string) error {
			if identity != "archie" {
				return fmt.Errorf("task belongs to a different identity")
			}
			return nil
		},
	}
	r := NewRouter(nil, nil, "test-gw")
	r.Controller = fc
	r.Identity = "winter"
	reply, err := r.Route(context.Background(), Message{Text: "/cancel 9"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "Cannot cancel") || !strings.Contains(reply, "different identity") {
		t.Errorf("reply = %q, want cross-identity rejection surfaced", reply)
	}
}

func TestRouteCancelNotConfigured(t *testing.T) {
	r := NewRouter(nil, nil, "test-gw")
	reply, err := r.Route(context.Background(), Message{Text: "/cancel 1"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "not configured") {
		t.Errorf("reply = %q, want 'not configured'", reply)
	}
}

// -- /steer tests --

func routerWithState() *Router {
	r := NewRouter(nil, nil, "test-gw")
	r.State = NewSessionState()
	return r
}

func TestRouteSteerQueuesMessage(t *testing.T) {
	r := routerWithState()
	reply, err := r.Route(context.Background(), Message{Text: "/steer check the logs"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "Steer queued") {
		t.Errorf("reply = %q, want confirmation", reply)
	}
	if text, ok := r.State.PollSteer(); !ok || text != "check the logs" {
		t.Errorf("PollSteer() = (%q, %v), want (check the logs, true)", text, ok)
	}
}

func TestRouteSteerShowsPending(t *testing.T) {
	r := routerWithState()
	r.State.SetSteer("pending steer")
	reply, err := r.Route(context.Background(), Message{Text: "/steer"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "Pending steer") || !strings.Contains(reply, "pending steer") {
		t.Errorf("reply = %q, want pending steer message", reply)
	}
}

func TestRouteSteerEmpty(t *testing.T) {
	r := routerWithState()
	reply, err := r.Route(context.Background(), Message{Text: "/steer"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "No steer pending") {
		t.Errorf("reply = %q, want usage message", reply)
	}
}

func TestRouteSteerNotConfigured(t *testing.T) {
	r := NewRouter(nil, nil, "test-gw")
	reply, err := r.Route(context.Background(), Message{Text: "/steer do x"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "not configured") {
		t.Errorf("reply = %q, want 'not configured'", reply)
	}
}

// -- /goal tests --

func TestRouteGoalSet(t *testing.T) {
	r := routerWithState()
	reply, err := r.Route(context.Background(), Message{Text: "/goal ship the feature"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "Goal set") || !strings.Contains(reply, "ship the feature") {
		t.Errorf("reply = %q, want goal set confirmation", reply)
	}
	goals := r.State.Goals()
	if len(goals) != 1 || goals[0].Text != "ship the feature" {
		t.Errorf("Goals() = %+v, want [ship the feature]", goals)
	}
}

func TestRouteGoalShow(t *testing.T) {
	r := routerWithState()
	r.State.SetGoal("do the thing", false)
	r.State.AddSubgoal("step one")
	r.State.AddSubgoal("step two")

	reply, err := r.Route(context.Background(), Message{Text: "/goal"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "do the thing") {
		t.Error("reply missing goal text")
	}
	if !strings.Contains(reply, "step one") {
		t.Error("reply missing subgoal")
	}
}

func TestRouteGoalShowEmpty(t *testing.T) {
	r := routerWithState()
	reply, err := r.Route(context.Background(), Message{Text: "/goal"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "No goal set") {
		t.Errorf("reply = %q, want 'No goal set'", reply)
	}
}

func TestRouteGoalPauseResume(t *testing.T) {
	r := routerWithState()
	r.State.SetGoal("work", false)

	reply, err := r.Route(context.Background(), Message{Text: "/goal pause"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "Goal paused") {
		t.Errorf("reply = %q, want Goal paused", reply)
	}

	reply, err = r.Route(context.Background(), Message{Text: "/goal resume"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "Goal resumed") {
		t.Errorf("reply = %q, want Goal resumed", reply)
	}
}

func TestRouteGoalPauseWithoutGoal(t *testing.T) {
	r := routerWithState()
	reply, err := r.Route(context.Background(), Message{Text: "/goal pause"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "No goal to pause") {
		t.Errorf("reply = %q, want 'No goal to pause'", reply)
	}
}

func TestRouteGoalClear(t *testing.T) {
	r := routerWithState()
	r.State.SetGoal("x", false)
	r.State.AddSubgoal("y")

	reply, err := r.Route(context.Background(), Message{Text: "/goal clear"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "cleared") {
		t.Errorf("reply = %q, want cleared", reply)
	}
	if r.State.HasGoal() {
		t.Error("HasGoal() = true after /goal clear")
	}
	if len(r.State.Subgoals()) != 0 {
		t.Error("subgoals not cleared")
	}
}

func TestRouteGoalStatus(t *testing.T) {
	r := routerWithState()
	r.State.SetGoal("g", false)
	r.State.AddSubgoal("sg")

	reply, err := r.Route(context.Background(), Message{Text: "/goal status"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "Goal:") || !strings.Contains(reply, "1 subgoal") {
		t.Errorf("reply = %q, want status line", reply)
	}
}

func TestRouteGoalWaitUnwait(t *testing.T) {
	r := routerWithState()
	r.State.SetGoal("g", false)

	reply, err := r.Route(context.Background(), Message{Text: "/goal wait"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "wait flag set") {
		t.Errorf("reply = %q, want wait flag set", reply)
	}
	if !r.State.IsWaiting() {
		t.Error("IsWaiting() = false after /goal wait")
	}

	reply, err = r.Route(context.Background(), Message{Text: "/goal unwait"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "wait flag cleared") {
		t.Errorf("reply = %q, want wait flag cleared", reply)
	}
	if r.State.IsWaiting() {
		t.Error("IsWaiting() = true after /goal unwait")
	}
}

func TestRouteGoalWaitWithoutGoal(t *testing.T) {
	r := routerWithState()
	reply, err := r.Route(context.Background(), Message{Text: "/goal wait"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "No goal to wait") {
		t.Errorf("reply = %q, want 'No goal to wait'", reply)
	}
}

func TestRouteGoalNotConfigured(t *testing.T) {
	r := NewRouter(nil, nil, "test-gw")
	reply, err := r.Route(context.Background(), Message{Text: "/goal show"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "not configured") {
		t.Errorf("reply = %q, want 'not configured'", reply)
	}
}

func TestRouteGoalDraft(t *testing.T) {
	r := routerWithState()
	reply, err := r.Route(context.Background(), Message{Text: "/goal draft"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "I'll suggest one") {
		t.Errorf("reply = %q, want draft prompt", reply)
	}
}

// -- /subgoal tests --

func TestRouteSubgoalAdd(t *testing.T) {
	r := routerWithState()
	r.State.SetGoal("goal", false)

	reply, err := r.Route(context.Background(), Message{Text: "/subgoal write tests"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "Subgoal added") || !strings.Contains(reply, "write tests") {
		t.Errorf("reply = %q, want confirmation", reply)
	}
	if len(r.State.Subgoals()) != 1 || r.State.Subgoals()[0].Text != "write tests" {
		t.Error("subgoal not stored")
	}
}

func TestRouteSubgoalAddWithoutGoal(t *testing.T) {
	r := routerWithState()
	reply, err := r.Route(context.Background(), Message{Text: "/subgoal do x"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "No goal set") {
		t.Errorf("reply = %q, want 'No goal set'", reply)
	}
}

func TestRouteSubgoalShow(t *testing.T) {
	r := routerWithState()
	r.State.SetGoal("g", false)
	r.State.AddSubgoal("a")
	r.State.AddSubgoal("b")

	reply, err := r.Route(context.Background(), Message{Text: "/subgoal"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "a") || !strings.Contains(reply, "b") {
		t.Errorf("reply = %q, want subgoals a and b", reply)
	}
}

func TestRouteSubgoalShowEmpty(t *testing.T) {
	r := routerWithState()
	r.State.SetGoal("g", false)

	reply, err := r.Route(context.Background(), Message{Text: "/subgoal"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "No subgoals") {
		t.Errorf("reply = %q, want 'No subgoals'", reply)
	}
}

func TestRouteSubgoalRemove(t *testing.T) {
	r := routerWithState()
	r.State.SetGoal("g", false)
	r.State.AddSubgoal("a")
	r.State.AddSubgoal("b")
	r.State.AddSubgoal("c")

	reply, err := r.Route(context.Background(), Message{Text: "/subgoal remove 2"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "removed") {
		t.Errorf("reply = %q, want removed", reply)
	}
	sgs := r.State.Subgoals()
	if len(sgs) != 2 || sgs[0].Text != "a" || sgs[1].Text != "c" {
		t.Errorf("Subgoals() = %+v, want [a, c]", sgs)
	}
}

func TestRouteSubgoalRemoveOutOfRange(t *testing.T) {
	r := routerWithState()
	r.State.SetGoal("g", false)

	reply, err := r.Route(context.Background(), Message{Text: "/subgoal remove 5"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "No subgoal at position") {
		t.Errorf("reply = %q, want 'No subgoal at position'", reply)
	}
}

func TestRouteSubgoalClear(t *testing.T) {
	r := routerWithState()
	r.State.SetGoal("g", false)
	r.State.AddSubgoal("a")
	r.State.AddSubgoal("b")

	reply, err := r.Route(context.Background(), Message{Text: "/subgoal clear"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "cleared") {
		t.Errorf("reply = %q, want cleared", reply)
	}
	if len(r.State.Subgoals()) != 0 {
		t.Error("Subgoals not cleared")
	}
}

func TestRouteSubgoalNotConfigured(t *testing.T) {
	r := NewRouter(nil, nil, "test-gw")
	reply, err := r.Route(context.Background(), Message{Text: "/subgoal add x"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "not configured") {
		t.Errorf("reply = %q, want 'not configured'", reply)
	}
}

// -- /queue tests --

func TestRouteQueueAdd(t *testing.T) {
	r := routerWithState()
	reply, err := r.Route(context.Background(), Message{Text: "/queue review the PR"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "Queued") || !strings.Contains(reply, "review the PR") {
		t.Errorf("reply = %q, want confirmation", reply)
	}
	if n := r.State.QueueLen(); n != 1 {
		t.Errorf("QueueLen() = %d, want 1", n)
	}
}

func TestRouteQueueShow(t *testing.T) {
	r := routerWithState()
	r.State.AddToQueue("a")
	r.State.AddToQueue("b")

	reply, err := r.Route(context.Background(), Message{Text: "/queue"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "a") || !strings.Contains(reply, "b") {
		t.Errorf("reply = %q, want queue contents", reply)
	}
}

func TestRouteQueueShowEmpty(t *testing.T) {
	r := routerWithState()
	reply, err := r.Route(context.Background(), Message{Text: "/queue"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "empty") {
		t.Errorf("reply = %q, want 'empty'", reply)
	}
}

func TestRouteQueueRemove(t *testing.T) {
	r := routerWithState()
	r.State.AddToQueue("a")
	r.State.AddToQueue("b")
	r.State.AddToQueue("c")

	reply, err := r.Route(context.Background(), Message{Text: "/queue remove 2"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "removed") {
		t.Errorf("reply = %q, want removed", reply)
	}
	entries := r.State.QueueEntries()
	if len(entries) != 2 || entries[0].Text != "a" || entries[1].Text != "c" {
		t.Errorf("QueueEntries() = %+v, want [a, c]", entries)
	}
}

func TestRouteQueueClear(t *testing.T) {
	r := routerWithState()
	r.State.AddToQueue("a")
	r.State.AddToQueue("b")

	reply, err := r.Route(context.Background(), Message{Text: "/queue clear"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "cleared") {
		t.Errorf("reply = %q, want cleared", reply)
	}
	if n := r.State.QueueLen(); n != 0 {
		t.Errorf("QueueLen() = %d, want 0", n)
	}
}

func TestRouteQueueAliases(t *testing.T) {
	for _, cmd := range []string{"/q add this", "/background add this", "/bg add this", "/btw add this"} {
		t.Run(cmd, func(t *testing.T) {
			r := routerWithState()
			reply, err := r.Route(context.Background(), Message{Text: cmd})
			if err != nil {
				t.Fatalf("Route: %v", err)
			}
			if !strings.Contains(reply, "Queued") {
				t.Errorf("alias %q reply = %q, want Queued", cmd, reply)
			}
			if n := r.State.QueueLen(); n != 1 {
				t.Errorf("QueueLen() = %d, want 1", n)
			}
		})
	}
}

func TestRouteQueueNotConfigured(t *testing.T) {
	r := NewRouter(nil, nil, "test-gw")
	reply, err := r.Route(context.Background(), Message{Text: "/queue show"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "not configured") {
		t.Errorf("reply = %q, want 'not configured'", reply)
	}
}

// -- /goal show includes subgoals and wait status --

func TestRouteGoalShowWithWait(t *testing.T) {
	r := routerWithState()
	r.State.SetGoal("big goal", false)
	r.State.SetWait()

	reply, err := r.Route(context.Background(), Message{Text: "/goal"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "Waiting") {
		t.Errorf("reply = %q, want Waiting indicator", reply)
	}
}

// -- LocalCommands includes the new commands --

func TestLocalCommandsIncludesNewCommands(t *testing.T) {
	cmds := LocalCommands()
	want := []string{"/steer", "/goal", "/subgoal", "/queue", "/q", "/background", "/bg", "/btw"}
	for _, w := range want {
		found := slices.Contains(cmds, w)
		if !found {
			t.Errorf("LocalCommands() missing %q", w)
		}
	}
}

func TestSplitFirst(t *testing.T) {
	tests := []struct {
		input     string
		wantFirst string
		wantRest  string
	}{
		{"show", "show", ""},
		{"remove 2", "remove", "2"},
		{"add do the thing", "add", "do the thing"},
		{"  pause  ", "pause", ""},
		{"", "", ""},
	}
	for _, tt := range tests {
		first, rest := splitFirst(tt.input)
		if first != tt.wantFirst || rest != tt.wantRest {
			t.Errorf("splitFirst(%q) = (%q, %q), want (%q, %q)",
				tt.input, first, rest, tt.wantFirst, tt.wantRest)
		}
	}
}
